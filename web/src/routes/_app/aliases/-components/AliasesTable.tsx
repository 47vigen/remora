import { Ellipsis, Pencil, TrashBin } from "@gravity-ui/icons";
import { Button, Chip, Dropdown, Label, Link, Table } from "@heroui/react";

import type { Alias } from "../../../../client";

const SHOWN_MEMBERS = 3;

/**
 * One page of aliases; a row opens its editor. Compiled target counts and cooling counts come from the container;
 * a target count not loaded yet shows "–".
 */
export function AliasesTable({
  items,
  chainLength,
  cooling,
  onOpen,
  onDelete,
}: {
  items: Alias[];
  chainLength: Map<string, number>;
  cooling: Map<string, number>;
  onOpen(name: string): void;
  onDelete(name: string): void;
}) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="Aliases" onRowAction={(key) => onOpen(String(key))}>
          <Table.Header>
            <Table.Column isRowHeader>Name</Table.Column>
            <Table.Column>Kind</Table.Column>
            <Table.Column>Chain</Table.Column>
            <Table.Column className="text-right">Targets</Table.Column>
            <Table.Column>Cooling</Table.Column>
            <Table.Column className="w-12">
              <span className="sr-only">Actions</span>
            </Table.Column>
          </Table.Header>
          <Table.Body items={items.map((a) => ({ ...a, id: a.name }))}>
            {(a) => {
              const coolingCount = cooling.get(a.name) ?? 0;
              const more = a.members.length - SHOWN_MEMBERS;
              return (
                <Table.Row id={a.name} className="cursor-pointer">
                  <Table.Cell>
                    <Link
                      href={`/aliases/${encodeURIComponent(a.name)}`}
                      className="font-mono text-[13px] no-underline"
                    >
                      {a.name}
                    </Link>
                  </Table.Cell>
                  <Table.Cell>
                    <Chip size="sm" variant="soft">
                      <Chip.Label className="text-[11px]">{a.kind}</Chip.Label>
                    </Chip>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="flex max-w-md min-w-0 items-center gap-1.5 font-mono text-[13px]">
                      {a.members.slice(0, SHOWN_MEMBERS).map((m, i) => (
                        <span key={m} className="flex min-w-0 items-center gap-1.5">
                          {i > 0 ? (
                            <span aria-hidden="true" className="text-muted/60">
                              →
                            </span>
                          ) : null}
                          <span className="truncate">{m}</span>
                        </span>
                      ))}
                      {more > 0 ? <span className="text-muted shrink-0 text-xs">+{more}</span> : null}
                    </span>
                  </Table.Cell>
                  <Table.Cell className="text-right font-mono text-[13px] tabular-nums">
                    {chainLength.get(a.name) ?? "–"}
                  </Table.Cell>
                  <Table.Cell>{coolingCount > 0 ? <CoolingCount count={coolingCount} /> : null}</Table.Cell>
                  <Table.Cell>
                    <Dropdown>
                      <Button isIconOnly size="sm" variant="ghost" aria-label={`Actions for ${a.name}`}>
                        <Ellipsis aria-hidden="true" className="size-4" />
                      </Button>
                      <Dropdown.Popover placement="bottom end">
                        <Dropdown.Menu
                          aria-label={`Actions for ${a.name}`}
                          onAction={(key) => (key === "delete" ? onDelete(a.name) : onOpen(a.name))}
                        >
                          <Dropdown.Item id="edit" textValue="Edit">
                            <Pencil aria-hidden="true" className="size-4" />
                            <Label>Edit</Label>
                          </Dropdown.Item>
                          <Dropdown.Item id="delete" textValue="Delete" variant="danger">
                            <TrashBin aria-hidden="true" className="size-4" />
                            <Label>Delete</Label>
                          </Dropdown.Item>
                        </Dropdown.Menu>
                      </Dropdown.Popover>
                    </Dropdown>
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

function CoolingCount({ count }: { count: number }) {
  return (
    <Chip color="warning" variant="soft" size="sm" className="gap-1.5 pl-1.5">
      <span aria-hidden="true" className="bg-warning size-1.5 shrink-0 rounded-full" />
      <Chip.Label className="text-[11px] tabular-nums">{count} cooling</Chip.Label>
    </Chip>
  );
}
