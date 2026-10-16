// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/costChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { SeriesRow } from "../../-lib/overview";
import { AXIS_TICK, CHART_COLORS, formatCost, formatFullTimestamp } from "../../../../../lib/chart";
import { formatCurrencyNumber } from "../../../../../lib/numbers";
import { barShape } from "./barShape";
import { ChartCard } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

// Cost per bucket over the window.
function CostChartImpl({ rows, total, className }: { rows: SeriesRow[]; total?: string; className?: string }) {
  return (
    <ChartCard title="Cost" value={total} className={className} isEmpty={!rows.some((r) => r.cost > 0)}>
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
            width={52}
            tickFormatter={(v: number) => formatCurrencyNumber(v)}
            domain={[0, (dataMax: number) => Math.max(dataMax, 0.01)]}
          />
          <Tooltip
            cursor={{ fill: "var(--surface-tertiary)", fillOpacity: 0.7 }}
            content={({ active, payload }) => {
              const row = active ? (payload?.[0]?.payload as SeriesRow | undefined) : undefined;
              if (!row) return null;
              return (
                <ChartTooltip title={formatFullTimestamp(row.time)}>
                  <ChartTooltip.Row tone="accent" label="Cost" value={formatCost(row.cost)} />
                </ChartTooltip>
              );
            }}
          />
          <Bar isAnimationActive={false} dataKey="cost" fill={CHART_COLORS.cost} barSize={30} shape={barShape} />
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const CostChart = memo(CostChartImpl);
