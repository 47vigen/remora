// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/tokenUsageChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { SeriesRow } from "../../-lib/overview";
import { AXIS_TICK, CHART_COLORS, formatFullTimestamp } from "../../../../../lib/chart";
import { formatCompactNumber } from "../../../../../lib/numbers";
import { CappedBarStack } from "./barShape";
import { ChartCard, LegendItem } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

// Tokens per bucket over the window: input and output stacked.
function TokenChartImpl({ rows, total }: { rows: SeriesRow[]; total?: string }) {
  const used = rows.some((r) => r.promptTokens + r.completionTokens > 0);
  return (
    <ChartCard
      title="Tokens"
      value={total}
      legend={
        <>
          <LegendItem className="bg-accent">Input</LegendItem>
          <LegendItem className="bg-ink-strong">Output</LegendItem>
        </>
      }
      isEmpty={!used}
    >
      <ResponsiveContainer width="100%" height="100%" initialDimension={{ width: 1, height: 1 }}>
        <BarChart data={rows} margin={{ top: 6, right: 4, left: 4, bottom: 0 }} barCategoryGap={1}>
          <CartesianGrid vertical={false} stroke={CHART_COLORS.grid} />
          <XAxis
            dataKey="index"
            type="number"
            domain={[-0.5, rows.length - 0.5]}
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
            width={48}
            tickFormatter={(v: number) => formatCompactNumber(v, 1)}
            domain={[0, (dataMax: number) => Math.max(dataMax, 1)]}
          />
          <Tooltip
            cursor={{ fill: "var(--surface-tertiary)", fillOpacity: 0.7 }}
            content={({ active, payload }) => {
              const row = active ? (payload?.[0]?.payload as SeriesRow | undefined) : undefined;
              if (!row) return null;
              return (
                <ChartTooltip title={formatFullTimestamp(row.time)}>
                  <ChartTooltip.Row tone="accent" label="Input" value={row.promptTokens.toLocaleString()} />
                  <ChartTooltip.Row tone="ink" label="Output" value={row.completionTokens.toLocaleString()} />
                  <ChartTooltip.Row label="Total" value={(row.promptTokens + row.completionTokens).toLocaleString()} />
                </ChartTooltip>
              );
            }}
          />
          <CappedBarStack buckets={rows.length}>
            <Bar isAnimationActive={false} dataKey="promptTokens" fill={CHART_COLORS.promptTokens} barSize={30} />
            <Bar
              isAnimationActive={false}
              dataKey="completionTokens"
              fill={CHART_COLORS.completionTokens}
              barSize={30}
            />
          </CappedBarStack>
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const TokenChart = memo(TokenChartImpl);
