import { Ellipsis, PencilToSquare, PlugConnection, TrashBin } from "@gravity-ui/icons";
import { Button, cn, Dropdown, Label, Table } from "@heroui/react";

import { displayGroup, GROUP_LABELS } from "../-lib/provider-form";
import type { Provider } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { getOAuthProviderLabel, getProviderLabel } from "../../../../lib/providers";
import { ProviderMark } from "./ProviderMark";

export type SubscriptionRow = { provider: string; accounts: number };

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

function target(p: Provider): string {
  if (p.kind === "builtin") return p.type ? getProviderLabel(p.type) : "";
  return p.base_url ?? "";
}

function NameCell({ icon, name, sub, mono }: { icon: string; name: string; sub: string; mono?: boolean }) {
  return (
    <span className="flex min-w-0 items-center gap-3">
      <ProviderMark type={icon} name={name} />
      <span className="flex min-w-0 flex-col">
        <span className={mono ? "truncate font-mono text-[13px]" : "truncate text-[13px] font-medium"}>{name}</span>
        <span className="text-muted max-w-xs truncate text-xs">{sub}</span>
      </span>
    </span>
  );
}

export function ProvidersTable({
  items,
  subscriptions,
  onOpen,
  onTest,
  onEdit,
  onDelete,
  testing,
}: {
  items: Provider[];
  subscriptions: SubscriptionRow[];
  /** Called with a provider name, or a subscription provider name. */
  onOpen(name: string): void;
  onTest(p: Provider): void;
  onEdit(p: Provider): void;
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
            <Table.Column>Credentials</Table.Column>
            <Table.Column>Status</Table.Column>
            <Table.Column>
              <span className="sr-only">Actions</span>
            </Table.Column>
          </Table.Header>
          <Table.Body>
            {items.map((p) => (
              <Table.Row key={p.name} id={p.name} className="cursor-pointer">
                <Table.Cell>
                  <NameCell icon={p.type ?? p.name} name={p.name} sub={target(p)} mono />
                </Table.Cell>
                <Table.Cell>{GROUP_LABELS[displayGroup(p.kind)]}</Table.Cell>
                <Table.Cell>
                  <span className="font-mono text-[13px] whitespace-nowrap tabular-nums">
                    {plural(p.key_count, "key")}
                  </span>
                </Table.Cell>
                <Table.Cell>
                  <StatusChip status={p.status} />
                </Table.Cell>
                <Table.Cell>
                  <span className="flex justify-end">
                    <Dropdown>
                      <Button
                        isIconOnly
                        size="sm"
                        variant="ghost"
                        aria-label={`Actions for ${p.name}`}
                        isPending={testing === p.name}
                      >
                        <Ellipsis aria-hidden="true" className="size-4" />
                      </Button>
                      <Dropdown.Popover placement="bottom end">
                        <Dropdown.Menu
                          aria-label={`Actions for ${p.name}`}
                          onAction={(key) => {
                            if (key === "test") onTest(p);
                            else if (key === "edit") onEdit(p);
                            else if (key === "delete") onDelete(p);
                          }}
                        >
                          <Dropdown.Item id="test" textValue="Test">
                            <PlugConnection aria-hidden="true" className="text-muted size-4 shrink-0" />
                            <Label>Test</Label>
                          </Dropdown.Item>
                          <Dropdown.Item id="edit" textValue="Edit">
                            <PencilToSquare aria-hidden="true" className="text-muted size-4 shrink-0" />
                            <Label>Edit</Label>
                          </Dropdown.Item>
                          <Dropdown.Item id="delete" textValue="Delete" variant="danger">
                            <TrashBin aria-hidden="true" className="text-danger size-4 shrink-0" />
                            <Label>Delete</Label>
                          </Dropdown.Item>
                        </Dropdown.Menu>
                      </Dropdown.Popover>
                    </Dropdown>
                  </span>
                </Table.Cell>
              </Table.Row>
            ))}
            {[...subscriptions]
              .sort((x, y) => y.accounts - x.accounts)
              .map((s) => (
                <Table.Row
                  key={`sub:${s.provider}`}
                  id={s.provider}
                  className={cn("cursor-pointer", s.accounts === 0 && "opacity-60")}
                >
                  <Table.Cell>
                    <NameCell
                      icon={s.provider}
                      name={getOAuthProviderLabel(s.provider)}
                      sub={s.accounts > 0 ? "Signed-in accounts" : "Not connected"}
                    />
                  </Table.Cell>
                  <Table.Cell>{GROUP_LABELS.subscription}</Table.Cell>
                  <Table.Cell>
                    <span className="font-mono text-[13px] whitespace-nowrap tabular-nums">
                      {plural(s.accounts, "account")}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="text-muted">—</span>
                  </Table.Cell>
                  <Table.Cell>{null}</Table.Cell>
                </Table.Row>
              ))}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
