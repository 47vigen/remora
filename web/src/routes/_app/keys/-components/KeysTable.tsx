import { TrashBin } from "@gravity-ui/icons";
import { Button, Chip, Label, Switch, Table } from "@heroui/react";

import type { Key, StatsGroup } from "../../../../client";
import { formatCompactNumber } from "../../../../lib/numbers";
import { formatRelative } from "../../../../lib/time";

/** How many allowlist patterns a row shows before "+N". */
const SHOWN_PATTERNS = 3;

export function KeysTable({
  items,
  usage,
  onOpen,
  onToggle,
  onDelete,
  now,
}: {
  items: Key[];
  /** Last-24-hour stats per key id. */
  usage?: Map<string, StatsGroup>; // undefined while loading
  onOpen(k: Key): void;
  onToggle(k: Key, enabled: boolean): void;
  onDelete(k: Key): void;
  now: Date;
}) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content
          aria-label="Keys"
          onRowAction={(id) => {
            const k = items.find((x) => x.id === id);
            if (k) onOpen(k);
          }}
        >
          <Table.Header>
            <Table.Column isRowHeader>Name</Table.Column>
            <Table.Column>Allowed</Table.Column>
            <Table.Column>Enabled</Table.Column>
            <Table.Column>Created</Table.Column>
            <Table.Column className="text-end">Requests 24h</Table.Column>
            <Table.Column>
              <span className="sr-only">Actions</span>
            </Table.Column>
          </Table.Header>
          <Table.Body items={items} dependencies={[usage, now]}>
            {(k) => {
              const hidden = k.allow.length - SHOWN_PATTERNS;
              return (
                <Table.Row id={k.id}>
                  <Table.Cell>
                    <div className="flex min-w-0 flex-col items-start gap-0.5">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-auto min-h-0 max-w-full truncate rounded-none bg-transparent p-0 font-medium hover:underline"
                        onPress={() => onOpen(k)}
                      >
                        {k.name}
                      </Button>
                      <span className="text-muted font-mono text-xs whitespace-nowrap">{k.prefix}…</span>
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    {k.allow.length === 0 ? (
                      <span className="text-muted text-xs">Nothing</span>
                    ) : (
                      <div className="flex flex-wrap gap-1">
                        {k.allow.slice(0, SHOWN_PATTERNS).map((p) => (
                          <Chip key={p} size="sm" variant="soft">
                            <Chip.Label className="text-xs">{p}</Chip.Label>
                          </Chip>
                        ))}
                        {hidden > 0 ? (
                          <span className="text-muted self-center font-mono text-xs">+{hidden}</span>
                        ) : null}
                      </div>
                    )}
                  </Table.Cell>
                  <Table.Cell>
                    <Switch size="sm" className="w-fit" isSelected={k.enabled} onChange={(on) => onToggle(k, on)}>
                      <Switch.Content>
                        <Switch.Control>
                          <Switch.Thumb />
                        </Switch.Control>
                        <Label className="sr-only">Enabled</Label>
                      </Switch.Content>
                    </Switch>
                  </Table.Cell>
                  <Table.Cell className="text-muted text-[13px] whitespace-nowrap">
                    <time dateTime={k.created_at}>{formatRelative(k.created_at, now)}</time>
                  </Table.Cell>
                  <Table.Cell className="text-end font-mono text-[13px] tabular-nums">
                    {usage ? formatCompactNumber(usage.get(k.id)?.summary.requests ?? 0) : "–"}
                  </Table.Cell>
                  <Table.Cell className="w-0 text-end">
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      className="text-muted hover:text-danger"
                      aria-label={`Delete ${k.name}`}
                      onPress={() => onDelete(k)}
                    >
                      <TrashBin aria-hidden="true" />
                    </Button>
                  </Table.Cell>
                </Table.Row>
              );
            }}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
