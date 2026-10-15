import { Chip, cn, Table } from "@heroui/react";

import type { LogSummary } from "../-lib/filters";
import { formatTime, formatTokens } from "../-lib/format";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { formatCost, formatLatency } from "../../../../lib/chart";

const statusOf = (s: LogSummary["status"]) => (s === "success" ? "success" : s === "error" ? "error" : "unknown");

/** Latency at or above this reads as slow and is emphasised. */
const SLOW_MS = 5_000;

const NUM = "text-right font-mono text-[13px] tabular-nums";

/**
 * One page of log entries, newest first. Pressing a row opens its detail. With `highlightNew`, a row that enters the
 * table (a live arrival) starts tinted and fades to plain.
 */
export function LogsTable({
  items,
  now,
  onOpen,
  highlightNew = false,
}: {
  items: LogSummary[];
  now: Date;
  onOpen(id: string): void;
  highlightNew?: boolean;
}) {
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label="Logs" onRowAction={(key) => onOpen(String(key))}>
          <Table.Header>
            <Table.Column isRowHeader>Time</Table.Column>
            <Table.Column>Status</Table.Column>
            <Table.Column>Model</Table.Column>
            <Table.Column>Key</Table.Column>
            <Table.Column className="text-right">Tokens</Table.Column>
            <Table.Column className="text-right">Latency</Table.Column>
            <Table.Column className="text-right">Cost</Table.Column>
          </Table.Header>
          <Table.Body items={items}>
            {(l) => (
              <Table.Row
                id={l.id}
                className={cn(
                  highlightNew &&
                    "transition-colors duration-[1500ms] ease-out motion-reduce:transition-none starting:bg-accent/20",
                )}
              >
                <Table.Cell className="whitespace-nowrap">
                  <time
                    dateTime={l.ts}
                    title={new Date(l.ts).toLocaleString()}
                    className="text-muted font-mono text-[13px] tabular-nums"
                  >
                    {formatTime(l.ts, now)}
                  </time>
                </Table.Cell>
                <Table.Cell>
                  <StatusChip status={statusOf(l.status)} />
                </Table.Cell>
                <Table.Cell>
                  <span className="flex items-center gap-2 font-mono text-[13px] whitespace-nowrap">
                    {l.alias ? (
                      <>
                        <span>{l.alias}</span>
                        <span aria-hidden="true" className="text-muted">
                          →
                        </span>
                      </>
                    ) : null}
                    <ProviderIcon type={l.provider} size="xs" />
                    <span className={l.alias ? "text-muted" : undefined}>
                      {l.provider}/{l.model}
                    </span>
                    {l.fallback ? (
                      <Chip size="sm" variant="soft" color="warning">
                        <Chip.Label className="text-[11px]">fallback</Chip.Label>
                      </Chip>
                    ) : null}
                  </span>
                </Table.Cell>
                <Table.Cell className="text-[13px] whitespace-nowrap">
                  {l.keyName ?? <span className="font-mono">{l.keyId ?? "–"}</span>}
                </Table.Cell>
                <Table.Cell className={cn(NUM, "whitespace-nowrap")}>
                  {formatTokens(l.tokens.prompt, l.tokens.completion)}
                </Table.Cell>
                <Table.Cell
                  className={cn(NUM, l.latencyMs !== undefined && l.latencyMs >= SLOW_MS && "text-warning font-medium")}
                >
                  {l.latencyMs !== undefined ? formatLatency(l.latencyMs) : "–"}
                </Table.Cell>
                <Table.Cell className={NUM}>{l.costUsd !== undefined ? formatCost(l.costUsd) : "–"}</Table.Cell>
              </Table.Row>
            )}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}
