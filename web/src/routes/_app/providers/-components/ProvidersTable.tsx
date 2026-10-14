import { Button, Table } from "@heroui/react";

import { displayGroup, GROUP_LABELS } from "../-lib/provider-form";
import type { Provider } from "../../../../client/types.gen";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { getOAuthProviderLabel, getProviderLabel } from "../../../../lib/providers";

export type SubscriptionRow = { provider: string; accounts: number };

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

function target(p: Provider): string {
  if (p.kind === "builtin") return p.type ? getProviderLabel(p.type) : "";
  return p.base_url ?? "";
}

export function ProvidersTable({
  items,
  subscriptions,
  onOpen,
  onTest,
  onDelete,
  testing,
}: {
  items: Provider[];
  subscriptions: SubscriptionRow[];
  /** Called with a provider name, or a subscription provider name. */
  onOpen(name: string): void;
  onTest(p: Provider): void;
  onDelete(p: Provider): void;
  /** The name of the provider whose test is running. */
  testing?: string;
}) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="Providers" onRowAction={(key) => onOpen(String(key))}>
          <Table.Header>
            <Table.Column isRowHeader>Name</Table.Column>
            <Table.Column>Group</Table.Column>
            <Table.Column>Target</Table.Column>
            <Table.Column>Credentials</Table.Column>
            <Table.Column>Status</Table.Column>
            <Table.Column>
              <span className="sr-only">Actions</span>
            </Table.Column>
          </Table.Header>
          <Table.Body>
            {subscriptions.map((s) => (
              <Table.Row key={`sub:${s.provider}`} id={s.provider}>
                <Table.Cell>
                  <span className="flex items-center gap-2">
                    <ProviderIcon type={s.provider} size="xs" />
                    {getOAuthProviderLabel(s.provider)}
                  </span>
                </Table.Cell>
                <Table.Cell>{GROUP_LABELS.subscription}</Table.Cell>
                <Table.Cell>
                  <span className="text-muted">Signed-in accounts</span>
                </Table.Cell>
                <Table.Cell>{plural(s.accounts, "account")}</Table.Cell>
                <Table.Cell>{null}</Table.Cell>
                <Table.Cell>{null}</Table.Cell>
              </Table.Row>
            ))}
            {items.map((p) => (
              <Table.Row key={p.name} id={p.name}>
                <Table.Cell>
                  <span className="flex items-center gap-2">
                    <ProviderIcon type={p.type ?? p.name} size="xs" />
                    <span className="font-mono">{p.name}</span>
                  </span>
                </Table.Cell>
                <Table.Cell>{GROUP_LABELS[displayGroup(p.kind)]}</Table.Cell>
                <Table.Cell>
                  <span className="text-muted block max-w-xs truncate">{target(p)}</span>
                </Table.Cell>
                <Table.Cell>{plural(p.key_count, "key")}</Table.Cell>
                <Table.Cell>
                  <StatusChip status={p.status} />
                </Table.Cell>
                <Table.Cell>
                  <span className="flex justify-end gap-2">
                    <Button size="sm" variant="secondary" isPending={testing === p.name} onPress={() => onTest(p)}>
                      Test
                    </Button>
                    <Button size="sm" variant="danger-soft" onPress={() => onDelete(p)}>
                      Delete
                    </Button>
                  </span>
                </Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
