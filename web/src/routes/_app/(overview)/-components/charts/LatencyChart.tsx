// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/latencyChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { SeriesRow } from "../../-lib/overview";
import { AXIS_TICK, CHART_COLORS, formatFullTimestamp, formatLatency } from "../../../../../lib/chart";
import { ChartCard, LegendItem } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

// p50 and p95 latency per bucket over the window; gaps where a bucket had no successful request.
function LatencyChartImpl({ rows, total }: { rows: SeriesRow[]; total?: string }) {
  return (
    <ChartCard
      title="Latency"
      value={total}
      legend={
        <>
          <LegendItem className="bg-accent">p50</LegendItem>
          <LegendItem className="bg-ink-strong">p95</LegendItem>
        </>
      }
      isEmpty={!rows.some((r) => r.p50 !== undefined || r.p95 !== undefined)}
    >
      <ResponsiveContainer width="100%" height="100%" initialDimension={{ width: 1, height: 1 }}>
        <LineChart data={rows} margin={{ top: 6, right: 6, left: 4, bottom: 0 }}>
          <CartesianGrid vertical={false} stroke={CHART_COLORS.grid} />
          <XAxis
            dataKey="index"
            type="number"
            domain={[0, Math.max(rows.length - 1, 1)]}
            tick={{ ...AXIS_TICK, dy: 5 }}
            tickLine={false}
            axisLine={false}
            tickFormatter={(idx: number) => rows[Math.round(idx)]?.label ?? ""}
            interval="preserveStartEnd"
          />
          <YAxis
            tick={AXIS_TICK}
            tickLine={false}
            axisLine={false}
            width={52}
            tickFormatter={formatLatency}
            domain={[0, (dataMax: number) => Math.max(dataMax, 1)]}
          />
          <Tooltip
            cursor={{ stroke: "var(--border)" }}
            content={({ active, payload }) => {
              const row = active ? (payload?.[0]?.payload as SeriesRow | undefined) : undefined;
              if (!row) return null;
              return (
                <ChartTooltip title={formatFullTimestamp(row.time)}>
                  <ChartTooltip.Row
                    tone="accent"
                    label="p50"
                    value={row.p50 === undefined ? "—" : formatLatency(row.p50)}
                  />
                  <ChartTooltip.Row
                    tone="ink"
                    label="p95"
                    value={row.p95 === undefined ? "—" : formatLatency(row.p95)}
                  />
                </ChartTooltip>
              );
            }}
          />
          <Line
            isAnimationActive={false}
            type="monotone"
            dataKey="p50"
            stroke={CHART_COLORS.p50}
            strokeWidth={1.75}
            dot={{ r: 2, strokeWidth: 0, fill: CHART_COLORS.p50 }}
            connectNulls={false}
          />
          <Line
            isAnimationActive={false}
            type="monotone"
            dataKey="p95"
            stroke={CHART_COLORS.p95}
            strokeWidth={1.75}
            dot={{ r: 2, strokeWidth: 0, fill: CHART_COLORS.p95 }}
            connectNulls={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const LatencyChart = memo(LatencyChartImpl);
