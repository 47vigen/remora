import {
  Button,
  cn,
  ComboBox,
  DateField,
  DateRangePicker,
  Input,
  Label,
  ListBox,
  RangeCalendar,
  TextField,
} from "@heroui/react";
import { useState } from "react";

import { type LogSearch, RANGES } from "../-lib/filters";
import { DataList } from "../../../../components/data-list/DataList";

const RANGE_LABELS: Record<(typeof RANGES)[number], string> = {
  "15m": "Last 15 minutes",
  "1h": "Last hour",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  custom: "Custom",
};

const COMPACT = "h-8 min-h-8 text-[13px]";
type Option = { id: string; label: string };

/** A pick-one box over `options`; typing filters (and reports the text, so the caller can search the server). */
function FilterCombo({
  label,
  value,
  options,
  onChange,
  onQuery,
}: {
  label: string;
  value: string | undefined;
  options: Option[];
  onChange(v: string | undefined): void;
  onQuery?(q: string): void;
}) {
  return (
    <ComboBox
      aria-label={label}
      className="w-40"
      selectedKey={value ?? null}
      onSelectionChange={(k) => onChange(k === null ? undefined : String(k))}
      onInputChange={(q) => {
        onQuery?.(q);
        if (q === "" && value !== undefined) onChange(undefined);
      }}
      allowsEmptyCollection
    >
      <Label className="sr-only">{label}</Label>
      <ComboBox.InputGroup className={cn(COMPACT, value !== undefined && "border-ink-faint")}>
        <Input placeholder={label} className={cn(COMPACT, "py-0")} />
        <ComboBox.Trigger />
      </ComboBox.InputGroup>
      <ComboBox.Popover>
        <ListBox renderEmptyState={() => <p className="text-muted p-2 text-sm">No matches.</p>}>
          {options.map((o) => (
            <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
              {o.label}
            </ListBox.Item>
          ))}
        </ListBox>
      </ComboBox.Popover>
    </ComboBox>
  );
}

/** The served-model box commits on Enter or when it loses focus, not on every keystroke. */
function ServedModel({ value, onChange }: { value: string | undefined; onChange(v: string | undefined): void }) {
  const [text, setText] = useState(value ?? "");
  const [synced, setSynced] = useState(value);
  if (value !== synced) {
    setSynced(value);
    setText(value ?? "");
  }
  const commit = () => {
    const v = text.trim() || undefined;
    if (v !== value) onChange(v);
  };
  return (
    <TextField className="w-44" value={text} onChange={setText} onBlur={commit}>
      <Label className="sr-only">Served model</Label>
      <Input
        placeholder="provider/model"
        className={cn(COMPACT, "font-mono", value !== undefined && "border-ink-faint")}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit();
        }}
      />
    </TextField>
  );
}

function CustomRange({ onChange }: { onChange(from: string, to: string): void }) {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return (
    <DateRangePicker
      className="w-64"
      onChange={(range) => {
        // Whole days in the viewer's zone; the end day is included.
        if (range)
          onChange(range.start.toDate(zone).toISOString(), range.end.add({ days: 1 }).toDate(zone).toISOString());
      }}
    >
      <Label className="sr-only">Dates</Label>
      <DateField.Group className={COMPACT}>
        <DateField.Input slot="start">{(segment) => <DateField.Segment segment={segment} />}</DateField.Input>
        <DateRangePicker.RangeSeparator />
        <DateField.Input slot="end">{(segment) => <DateField.Segment segment={segment} />}</DateField.Input>
        <DateField.Suffix>
          <DateRangePicker.Trigger>
            <DateRangePicker.TriggerIndicator />
          </DateRangePicker.Trigger>
        </DateField.Suffix>
      </DateField.Group>
      <DateRangePicker.Popover>
        <RangeCalendar aria-label="Dates">
          <RangeCalendar.Header>
            <RangeCalendar.NavButton slot="previous" />
            <RangeCalendar.Heading />
            <RangeCalendar.NavButton slot="next" />
          </RangeCalendar.Header>
          <RangeCalendar.Grid>
            <RangeCalendar.GridHeader>
              {(day) => <RangeCalendar.HeaderCell>{day}</RangeCalendar.HeaderCell>}
            </RangeCalendar.GridHeader>
            <RangeCalendar.GridBody>{(date) => <RangeCalendar.Cell date={date} />}</RangeCalendar.GridBody>
          </RangeCalendar.Grid>
        </RangeCalendar>
      </DateRangePicker.Popover>
    </DateRangePicker>
  );
}

export function LogFilters({
  value,
  keys,
  aliases,
  providers,
  onKeyQuery,
  onChange,
}: {
  value: LogSearch;
  keys: Option[];
  aliases: string[];
  providers: string[];
  onKeyQuery(q: string): void;
  onChange(patch: Partial<LogSearch>): void;
}) {
  const isFiltered =
    [value.q, value.keyId, value.alias, value.provider, value.status, value.servedModel].some((v) => v) ||
    value.range !== "24h";
  return (
    <>
      <FilterCombo
        label="Key"
        value={value.keyId}
        options={keys}
        onQuery={onKeyQuery}
        onChange={(keyId) => onChange({ keyId })}
      />
      <FilterCombo
        label="Alias"
        value={value.alias}
        options={aliases.map((a) => ({ id: a, label: a }))}
        onChange={(alias) => onChange({ alias })}
      />
      <DataList.Filter
        label="Provider"
        value={value.provider}
        options={providers.map((p) => ({ id: p, label: p }))}
        onChange={(provider) => onChange({ provider })}
      />
      <DataList.Filter
        label="Status"
        value={value.status}
        allLabel="Any"
        options={[
          { id: "success", label: "Success" },
          { id: "error", label: "Error" },
        ]}
        onChange={(status) => onChange({ status })}
      />
      <ServedModel value={value.servedModel} onChange={(servedModel) => onChange({ servedModel })} />
      {/* The default range stands in for "all", so it is the unfiltered choice of the compact filter. */}
      <DataList.Filter
        label="Range"
        value={value.range === "24h" ? undefined : value.range}
        allLabel={RANGE_LABELS["24h"]}
        options={RANGES.filter((r) => r !== "24h").map((r) => ({ id: r, label: RANGE_LABELS[r] }))}
        onChange={(r) =>
          onChange(r === "custom" ? { range: r } : { range: r ?? "24h", from: undefined, to: undefined })
        }
      />
      {value.range === "custom" ? <CustomRange onChange={(from, to) => onChange({ from, to })} /> : null}
      {isFiltered ? (
        <Button
          size="sm"
          variant="ghost"
          className="text-link"
          onPress={() =>
            onChange({
              q: "",
              keyId: undefined,
              alias: undefined,
              provider: undefined,
              status: undefined,
              servedModel: undefined,
              range: "24h",
              from: undefined,
              to: undefined,
            })
          }
        >
          Clear
        </Button>
      ) : null}
    </>
  );
}
