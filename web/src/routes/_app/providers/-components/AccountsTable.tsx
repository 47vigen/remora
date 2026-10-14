import { Button, Chip, Label, Meter, Switch, Table } from "@heroui/react";

import { accountHealth, isCooling } from "../-lib/accounts";
import type { Account } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";

const MODEL_CHIPS = 3;

function Quota({ quota }: { quota: Account["quota"] }) {
  const shown = quota.filter((q) => typeof q.remaining_fraction === "number");
  if (shown.length === 0) return <span className="text-muted">—</span>;
  return (
    <div className="flex min-w-40 flex-col gap-2">
      {shown.map((q, i) => {
        const left = Math.round(Math.min(1, Math.max(0, q.remaining_fraction ?? 0)) * 100);
        const label = `${q.model ?? "All models"}: ${left}% left`;
        return (
          <Meter key={q.model ?? i} value={left} valueLabel={label} size="sm">
            <Label className="text-xs">{label}</Label>
            <Meter.Track>
              <Meter.Fill />
            </Meter.Track>
          </Meter>
        );
      })}
    </div>
  );
}

function Models({ models }: { models: string[] }) {
  if (models.length === 0) return <span className="text-muted">—</span>;
  const rest = models.length - MODEL_CHIPS;
  return (
    <span className="flex flex-wrap gap-1">
      {models.slice(0, MODEL_CHIPS).map((m) => (
        <Chip key={m} size="sm" variant="soft">
          <Chip.Label>{m}</Chip.Label>
        </Chip>
      ))}
      {rest > 0 ? (
        <Chip size="sm" variant="soft">
          <Chip.Label>+{rest}</Chip.Label>
        </Chip>
      ) : null}
    </span>
  );
}

export function AccountsTable({
  items,
  onToggle,
  onResetCooldown,
  onDelete,
}: {
  items: Account[];
  onToggle(a: Account, enabled: boolean): void;
  onResetCooldown(a: Account): void;
  onDelete(a: Account): void;
}) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="Accounts">
          <Table.Header>
            <Table.Column isRowHeader>Account</Table.Column>
            <Table.Column>Status</Table.Column>
            <Table.Column>Quota</Table.Column>
            <Table.Column>Models</Table.Column>
            <Table.Column>Enabled</Table.Column>
            <Table.Column>
              <span className="sr-only">Actions</span>
            </Table.Column>
          </Table.Header>
          <Table.Body>
            {items.map((a) => {
              const name = a.email ?? a.id;
              return (
                <Table.Row key={a.id} id={a.id}>
                  <Table.Cell>
                    <span className="font-medium">{name}</span>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="flex flex-col items-start gap-1">
                      <StatusChip status={accountHealth(a)} until={a.cooldown_until} />
                      {a.message ? (
                        <span className="text-muted max-w-xs text-xs" dir="auto">
                          {a.message}
                        </span>
                      ) : null}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <Quota quota={a.quota} />
                  </Table.Cell>
                  <Table.Cell>
                    <Models models={a.models} />
                  </Table.Cell>
                  <Table.Cell>
                    <Switch isSelected={a.enabled} onChange={(enabled) => onToggle(a, enabled)}>
                      <Switch.Content aria-label={`Enable ${name}`}>
                        <Switch.Control>
                          <Switch.Thumb />
                        </Switch.Control>
                      </Switch.Content>
                    </Switch>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="flex justify-end gap-2">
                      <Button
                        size="sm"
                        variant="secondary"
                        isDisabled={!isCooling(a)}
                        onPress={() => onResetCooldown(a)}
                      >
                        Reset cooldown
                      </Button>
                      <Button size="sm" variant="danger-soft" onPress={() => onDelete(a)}>
                        Delete
                      </Button>
                    </span>
                  </Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
