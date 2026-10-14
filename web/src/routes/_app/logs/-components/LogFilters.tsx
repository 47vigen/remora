import {
  ComboBox,
  DateField,
  DateRangePicker,
  Input,
  Label,
  ListBox,
  RangeCalendar,
  Select,
  TextField,
} from "@heroui/react";
import { type Key, useState } from "react";

import { type LogSearch, RANGES } from "../-lib/filters";

const RANGE_LABELS: Record<(typeof RANGES)[number], string> = {
  "15m": "Last 15 minutes",
  "1h": "Last hour",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  custom: "Custom",
};

const ALL = "__all__";

type Option = { id: string; label: string };

function FilterSelect({
  label,
  value,
  options,
  allLabel,
  onChange,
}: {
  label: string;
  value: string | undefined;
  options: Option[];
  allLabel?: string;
  onChange(v: string | undefined): void;
}) {
  const items = allLabel ? [{ id: ALL, label: allLabel }, ...options] : options;
  return (
    <Select
      className="w-44"
      value={value ?? ALL}
      onChange={(k: Key | null) => onChange(k === null || k === ALL ? undefined : String(k))}
    >
      <Label>{label}</Label>
      <Select.Trigger>
        <Select.Value />
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover>
        <ListBox aria-label={label}>
          {items.map((o) => (
            <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
              {o.label}
              <ListBox.ItemIndicator />
            </ListBox.Item>
          ))}
        </ListBox>
      </Select.Popover>
    </Select>
  );
}

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
      className="w-48"
      selectedKey={value ?? null}
      onSelectionChange={(k) => onChange(k === null ? undefined : String(k))}
      onInputChange={(q) => {
        onQuery?.(q);
        if (q === "" && value !== undefined) onChange(undefined);
      }}
      allowsEmptyCollection
    >
      <Label>{label}</Label>
      <ComboBox.InputGroup>
        <Input placeholder="Any" />
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
    <TextField className="w-48" value={text} onChange={setText} onBlur={commit}>
      <Label>Served model</Label>
      <Input
        placeholder="provider/model"
        className="font-mono"
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
      className="w-72"
      onChange={(range) => {
        // Whole days in the viewer's zone; the end day is included.
        if (range)
          onChange(range.start.toDate(zone).toISOString(), range.end.add({ days: 1 }).toDate(zone).toISOString());
      }}
    >
      <Label>Dates</Label>
      <DateField.Group>
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
      <FilterSelect
        label="Provider"
        value={value.provider}
        allLabel="All providers"
        options={providers.map((p) => ({ id: p, label: p }))}
        onChange={(provider) => onChange({ provider })}
      />
      <FilterSelect
        label="Status"
        value={value.status}
        allLabel="Any status"
        options={[
          { id: "success", label: "Success" },
          { id: "error", label: "Error" },
        ]}
        onChange={(s) => onChange({ status: s === "success" || s === "error" ? s : undefined })}
      />
      <ServedModel value={value.servedModel} onChange={(servedModel) => onChange({ servedModel })} />
      <FilterSelect
        label="Range"
        value={value.range}
        options={RANGES.map((r) => ({ id: r, label: RANGE_LABELS[r] }))}
        onChange={(r) => {
          const range = RANGES.find((x) => x === r) ?? "24h";
          onChange(range === "custom" ? { range } : { range, from: undefined, to: undefined });
        }}
      />
      {value.range === "custom" ? <CustomRange onChange={(from, to) => onChange({ from, to })} /> : null}
    </>
  );
}
