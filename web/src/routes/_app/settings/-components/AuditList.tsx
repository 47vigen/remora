import { Table } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { listAuditOptions } from "../../../../client/@tanstack/react-query.gen";
import { DataList } from "../../../../components/data-list/DataList";
import { PAGE_SIZES } from "../../../../lib/list-search";

export function AuditList() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES[0]);
  const [operation, setOperation] = useState("");
  const list = useQuery(
    listAuditOptions({ query: { limit: pageSize, offset: (page - 1) * pageSize, operation: operation || undefined } }),
  );
  const items = list.data?.items ?? [];

  return (
    <DataList.Root>
      <DataList.Toolbar>
        <DataList.Search
          value={operation}
          onChange={(next) => {
            setOperation(next);
            setPage(1);
          }}
          placeholder="Filter by operation ID"
        />
      </DataList.Toolbar>
      <DataList.State
        isLoading={list.isPending}
        error={list.error ?? undefined}
        isEmpty={items.length === 0}
        emptyText={operation ? "No audit entries match this operation." : "No management writes recorded yet."}
      >
        <Table>
          <Table.ScrollContainer>
            <Table.Content aria-label="Audit log">
              <Table.Header>
                <Table.Column>Time</Table.Column>
                <Table.Column isRowHeader>Principal</Table.Column>
                <Table.Column isRowHeader>Operation</Table.Column>
                <Table.Column>Changes</Table.Column>
              </Table.Header>
              <Table.Body items={items}>
                {(entry) => (
                  <Table.Row id={entry.id}>
                    <Table.Cell className="text-muted text-[13px] whitespace-nowrap">
                      <time dateTime={entry.at}>{new Date(entry.at).toLocaleString()}</time>
                    </Table.Cell>
                    <Table.Cell>
                      {entry.principal === "session"
                        ? "Session"
                        : `Token: ${entry.token_name || entry.token_id || "unknown"}`}
                    </Table.Cell>
                    <Table.Cell className="font-mono text-[13px]">{entry.operation_id}</Table.Cell>
                    <Table.Cell className="text-muted font-mono text-xs wrap-anywhere whitespace-pre-wrap">
                      {Object.keys(entry.diff).length ? JSON.stringify(entry.diff, null, 2) : "–"}
                    </Table.Cell>
                  </Table.Row>
                )}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table>
      </DataList.State>
      <DataList.Pagination
        page={page}
        pageSize={pageSize}
        total={list.data?.total ?? 0}
        onPageChange={setPage}
        onPageSizeChange={(next) => {
          setPageSize(next);
          setPage(1);
        }}
      />
    </DataList.Root>
  );
}
