import { Chip, Link, Table } from "@heroui/react";

import type { Alias } from "../../../../client";

/** One page of aliases. Chain length and cooling counts come from the container; a missing chain shows "–". */
export function AliasesTable({
  items,
  chainLength,
  cooling,
}: {
  items: Alias[];
  chainLength: Map<string, number>;
  cooling: Map<string, number>;
}) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="Aliases">
          <Table.Header>
            <Table.Column isRowHeader>Name</Table.Column>
            <Table.Column>Kind</Table.Column>
            <Table.Column>Members</Table.Column>
            <Table.Column>Chain</Table.Column>
            <Table.Column>Cooling</Table.Column>
          </Table.Header>
          <Table.Body items={items.map((a) => ({ ...a, id: a.name }))}>
            {(a) => {
              const coolingCount = cooling.get(a.name) ?? 0;
              return (
                <Table.Row id={a.name}>
                  <Table.Cell>
                    <Link href={`/aliases/${encodeURIComponent(a.name)}`} className="font-mono">
                      {a.name}
                    </Link>
                  </Table.Cell>
                  <Table.Cell>{a.kind}</Table.Cell>
                  <Table.Cell className="tabular-nums">{a.members.length}</Table.Cell>
                  <Table.Cell className="tabular-nums">{chainLength.get(a.name) ?? "–"}</Table.Cell>
                  <Table.Cell>
                    {coolingCount > 0 ? (
                      <Chip size="sm" variant="soft" color="warning">
                        <Chip.Label>{coolingCount} cooling</Chip.Label>
                      </Chip>
                    ) : null}
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
