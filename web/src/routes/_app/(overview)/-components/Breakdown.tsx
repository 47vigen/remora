import { cn, Skeleton, Table, Tabs } from "@heroui/react";

import type { BreakdownRow } from "../-lib/overview";
import { formatCost, formatLatency } from "../../../../lib/chart";
import { formatCompactNumber } from "../../../../lib/numbers";

function Rows({ label, rows, error }: { label: string; rows?: BreakdownRow[]; error?: string }) {
  if (error) return <p className="text-danger py-10 text-center text-sm">Could not load this breakdown: {error}</p>;
  if (!rows)
    return (
      <div className="flex flex-col gap-2 p-4">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
      </div>
    );
  if (rows.length === 0) return <p className="text-muted py-10 text-center text-sm">No traffic in this window.</p>;
  const peak = Math.max(...rows.map((r) => r.requests), 1);
  return (
    <Table
      variant="secondary"
      className="rounded-none [&_td]:px-3 [&_td:first-child]:pl-4 [&_th]:px-3 [&_th:first-child]:pl-4"
    >
      <Table.ScrollContainer>
        <Table.Content aria-label={label}>
          <Table.Header>
            <Table.Column isRowHeader>{label}</Table.Column>
            <Table.Column className="text-end">Requests</Table.Column>
            <Table.Column className="text-end">Errors</Table.Column>
            <Table.Column className="text-end">Tokens</Table.Column>
            <Table.Column className="text-end">Cost</Table.Column>
            <Table.Column className="text-end">p95</Table.Column>
          </Table.Header>
          <Table.Body items={rows}>
            {(r) => (
              <Table.Row id={r.name}>
                <Table.Cell>
                  <span className="flex max-w-64 min-w-0 flex-col gap-1.5">
                    <span className="truncate font-mono text-[13px]">{r.name}</span>
                    {/* Share of the busiest row; one block scale instead of an inline width. */}
                    <span aria-hidden="true" className="flex h-1 w-32 gap-px">
                      {Array.from({ length: 20 }, (_, i) => (
                        <span
                          key={i}
                          className={cn(
                            "flex-1",
                            i < Math.ceil((r.requests / peak) * 20) ? "bg-accent" : "bg-surface-tertiary",
                          )}
                        />
                      ))}
                    </span>
                  </span>
                </Table.Cell>
                <Table.Cell className="text-end font-mono text-[13px] tabular-nums">
                  {formatCompactNumber(r.requests)}
                </Table.Cell>
                <Table.Cell
                  className={cn("text-end font-mono text-[13px] tabular-nums", r.errorRate >= 0.05 && "text-danger")}
                >
                  {(r.errorRate * 100).toFixed(1)}%
                </Table.Cell>
                <Table.Cell className="text-end font-mono text-[13px] tabular-nums">
                  {formatCompactNumber(r.totalTokens)}
                </Table.Cell>
                <Table.Cell className="text-end font-mono text-[13px] tabular-nums">{formatCost(r.cost)}</Table.Cell>
                <Table.Cell className="text-muted text-end font-mono text-[13px] tabular-nums">
                  {r.p95 === undefined ? "—" : formatLatency(r.p95)}
                </Table.Cell>
              </Table.Row>
            )}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}

/** Who carried the traffic: the busiest models and providers in the window, side by side in tabs. */
export function Breakdown({
  models,
  modelsError,
  providers,
}: {
  /** Undefined while the separate model query loads. */
  models?: BreakdownRow[];
  modelsError?: string;
  providers: BreakdownRow[];
}) {
  return (
    <section className="bg-surface border-border overflow-hidden rounded-sm border">
      <Tabs defaultSelectedKey="models">
        <div className="border-border flex items-center justify-between gap-3 border-b px-4 py-2">
          <h3 className="label-caps text-muted">Breakdown</h3>
          <Tabs.ListContainer>
            <Tabs.List aria-label="Breakdown" className="p-0.5">
              <Tabs.Tab id="models" className="h-7 px-3 text-[13px]">
                Models
                <Tabs.Indicator />
              </Tabs.Tab>
              <Tabs.Tab id="providers" className="h-7 px-3 text-[13px]">
                Providers
                <Tabs.Indicator />
              </Tabs.Tab>
            </Tabs.List>
          </Tabs.ListContainer>
        </div>
        <Tabs.Panel id="models">
          <Rows label="Model" rows={models} error={modelsError} />
        </Tabs.Panel>
        <Tabs.Panel id="providers">
          <Rows label="Provider" rows={providers} />
        </Tabs.Panel>
      </Tabs>
    </section>
  );
}
