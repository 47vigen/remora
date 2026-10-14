import { Chip, Table } from "@heroui/react";

import type { LogSummary } from "../-lib/filters";
import { formatTime, formatTokens } from "../-lib/format";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { formatCost, formatLatency } from "../../../../lib/chart";

const statusOf = (s: LogSummary["status"]) => (s === "success" ? "success" : s === "error" ? "error" : "unknown");

/** One page of log entries, newest first. Pressing a row opens its detail. */
export function LogsTable({ items, now, onOpen }: { items: LogSummary[]; now: Date; onOpen(id: string): void }) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="Logs" onRowAction={(key) => onOpen(String(key))}>
          <Table.Header>
            <Table.Column isRowHeader>Time</Table.Column>
            <Table.Column>Status</Table.Column>
            <Table.Column>Alias</Table.Column>
            <Table.Column>Served by</Table.Column>
            <Table.Column>Key</Table.Column>
            <Table.Column>Tokens</Table.Column>
            <Table.Column>Latency</Table.Column>
            <Table.Column>Cost</Table.Column>
          </Table.Header>
          <Table.Body items={items}>
            {(l) => (
              <Table.Row id={l.id} className="cursor-pointer">
                <Table.Cell>
                  <time dateTime={l.ts} className="tabular-nums">
                    {formatTime(l.ts, now)}
                  </time>
                </Table.Cell>
                <Table.Cell>
                  <StatusChip status={statusOf(l.status)} />
                </Table.Cell>
                <Table.Cell>{l.alias ?? "–"}</Table.Cell>
                <Table.Cell>
                  <span className="flex items-center gap-2">
                    <ProviderIcon type={l.provider} size="xs" />
                    <span className="font-mono text-sm">
                      {l.provider}/{l.model}
                    </span>
                    {l.fallback ? (
                      <Chip size="sm" variant="soft" color="warning">
                        <Chip.Label>fallback</Chip.Label>
                      </Chip>
                    ) : null}
                  </span>
                </Table.Cell>
                <Table.Cell>{l.keyName ?? l.keyId ?? "–"}</Table.Cell>
                <Table.Cell className="tabular-nums">{formatTokens(l.tokens.prompt, l.tokens.completion)}</Table.Cell>
                <Table.Cell className="tabular-nums">
                  {l.latencyMs !== undefined ? formatLatency(l.latencyMs) : "–"}
                </Table.Cell>
                <Table.Cell className="tabular-nums">
                  {l.costUsd !== undefined ? formatCost(l.costUsd) : "–"}
                </Table.Cell>
              </Table.Row>
            )}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
