// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/costChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { ProviderRow } from "../../-lib/overview";
import { CHART_COLORS, formatCost } from "../../../../../lib/chart";
import { formatCurrencyNumber } from "../../../../../lib/numbers";
import { barShape } from "./barShape";
import { ChartCard } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

// Total cost per provider over the window.
function CostChartImpl({ rows }: { rows: ProviderRow[] }) {
  return (
    <ChartCard title="Cost by provider" isEmpty={rows.length === 0}>
      <ResponsiveContainer width="100%" height="100%" initialDimension={{ width: 1, height: 1 }}>
        <BarChart data={rows} margin={{ top: 6, right: 4, left: 4, bottom: 0 }}>
          <CartesianGrid strokeDasharray="3 3" vertical={false} stroke={CHART_COLORS.grid} />
          <XAxis
            dataKey="name"
            tick={{ fontSize: 11, fill: CHART_COLORS.axis, dy: 5 }}
            tickLine={false}
            axisLine={false}
            interval="preserveStartEnd"
          />
          <YAxis
            tick={{ fontSize: 11, fill: CHART_COLORS.axis }}
            tickLine={false}
            axisLine={false}
            width={55}
            tickFormatter={(v: number) => formatCurrencyNumber(v)}
            domain={[0, (dataMax: number) => Math.max(dataMax, 0.01)]}
            allowDataOverflow={false}
          />
          <Tooltip
            cursor={{ fill: CHART_COLORS.axis, fillOpacity: 0.15 }}
            content={({ active, payload }) => {
              const row = active ? (payload?.[0]?.payload as ProviderRow | undefined) : undefined;
              if (!row) return null;
              return (
                <ChartTooltip title={row.name}>
                  <ChartTooltip.Row tone="accent" label="Cost" value={formatCost(row.cost)} />
                </ChartTooltip>
              );
            }}
          />
          <Bar
            isAnimationActive={false}
            dataKey="cost"
            fill={CHART_COLORS.cost}
            fillOpacity={0.9}
            barSize={30}
            shape={barShape}
          />
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const CostChart = memo(CostChartImpl);
