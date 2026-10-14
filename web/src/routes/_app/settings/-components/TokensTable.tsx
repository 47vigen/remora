import { TrashBin } from "@gravity-ui/icons";
import { Button, Table } from "@heroui/react";

import type { ApiToken } from "../../../../client";
import { formatRelative } from "../../../../lib/time";

/** One page of management API tokens. Values are never listed, only their first characters. */
export function TokensTable({ items, now, onDelete }: { items: ApiToken[]; now: Date; onDelete(t: ApiToken): void }) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="API tokens">
          <Table.Header>
            <Table.Column isRowHeader>Name</Table.Column>
            <Table.Column>Token</Table.Column>
            <Table.Column>Created</Table.Column>
            <Table.Column>Last used</Table.Column>
            <Table.Column>Actions</Table.Column>
          </Table.Header>
          <Table.Body items={items}>
            {(t) => (
              <Table.Row id={t.id}>
                <Table.Cell>{t.name}</Table.Cell>
                <Table.Cell className="font-mono text-sm">{t.prefix}…</Table.Cell>
                <Table.Cell>
                  <time dateTime={t.created_at}>{formatRelative(t.created_at, now)}</time>
                </Table.Cell>
                <Table.Cell>
                  {t.last_used_at ? (
                    <time dateTime={t.last_used_at}>{formatRelative(t.last_used_at, now)}</time>
                  ) : (
                    "Never"
                  )}
                </Table.Cell>
                <Table.Cell>
                  <Button
                    isIconOnly
                    size="sm"
                    variant="ghost"
                    aria-label={`Delete ${t.name}`}
                    onPress={() => onDelete(t)}
                  >
                    <TrashBin aria-hidden="true" />
                  </Button>
                </Table.Cell>
              </Table.Row>
            )}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
