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
        <Table.Content aria-label="Keys">
          <Table.Header>
            <Table.Column isRowHeader>Name</Table.Column>
            <Table.Column>Allowed</Table.Column>
            <Table.Column>Enabled</Table.Column>
            <Table.Column>Created</Table.Column>
            <Table.Column>Requests 24h</Table.Column>
            <Table.Column>Actions</Table.Column>
          </Table.Header>
          <Table.Body items={items} dependencies={[usage, now]}>
            {(k) => {
              const hidden = k.allow.length - SHOWN_PATTERNS;
              return (
                <Table.Row id={k.id}>
                  <Table.Cell>
                    <div className="flex flex-col items-start">
                      <Button variant="ghost" size="sm" onPress={() => onOpen(k)}>
                        {k.name}
                      </Button>
                      <span className="text-muted px-3 font-mono text-xs">{k.prefix}…</span>
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    <div className="flex flex-wrap gap-1">
                      {k.allow.slice(0, SHOWN_PATTERNS).map((p) => (
                        <Chip key={p} size="sm" variant="secondary">
                          <Chip.Label className="font-mono">{p}</Chip.Label>
                        </Chip>
                      ))}
                      {hidden > 0 ? (
                        <Chip size="sm" variant="soft">
                          <Chip.Label>+{hidden}</Chip.Label>
                        </Chip>
                      ) : null}
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    <Switch isSelected={k.enabled} onChange={(on) => onToggle(k, on)}>
                      <Switch.Content>
                        <Switch.Control>
                          <Switch.Thumb />
                        </Switch.Control>
                        <Label className="sr-only">Enabled</Label>
                      </Switch.Content>
                    </Switch>
                  </Table.Cell>
                  <Table.Cell>
                    <time dateTime={k.created_at}>{formatRelative(k.created_at, now)}</time>
                  </Table.Cell>
                  <Table.Cell className="tabular-nums">
                    {usage ? formatCompactNumber(usage.get(k.id)?.summary.requests ?? 0) : "–"}
                  </Table.Cell>
                  <Table.Cell>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
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
