// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/tokenUsageChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { ProviderRow } from "../../-lib/overview";
import { CHART_COLORS } from "../../../../../lib/chart";
import { formatCompactNumber } from "../../../../../lib/numbers";
import { CappedBarStack } from "./barShape";
import { ChartCard } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

// Tokens per provider over the window: prompt and completion stacked.
function TokenChartImpl({ rows }: { rows: ProviderRow[] }) {
  return (
    <ChartCard title="Tokens by provider" isEmpty={rows.length === 0}>
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
            width={50}
            tickFormatter={(v: number) => formatCompactNumber(v)}
            domain={[0, (dataMax: number) => Math.max(dataMax, 1)]}
            allowDataOverflow={false}
          />
          <Tooltip
            cursor={{ fill: CHART_COLORS.axis, fillOpacity: 0.15 }}
            content={({ active, payload }) => {
              const row = active ? (payload?.[0]?.payload as ProviderRow | undefined) : undefined;
              if (!row) return null;
              return (
                <ChartTooltip title={row.name}>
                  <ChartTooltip.Row tone="accent" label="Input" value={row.promptTokens.toLocaleString()} />
                  <ChartTooltip.Row tone="warning" label="Output" value={row.completionTokens.toLocaleString()} />
                  <ChartTooltip.Row label="Total" value={row.totalTokens.toLocaleString()} />
                </ChartTooltip>
              );
            }}
          />
          <CappedBarStack buckets={rows.length}>
            <Bar
              isAnimationActive={false}
              dataKey="promptTokens"
              fill={CHART_COLORS.promptTokens}
              fillOpacity={0.9}
              barSize={30}
            />
            <Bar
              isAnimationActive={false}
              dataKey="completionTokens"
              fill={CHART_COLORS.completionTokens}
              fillOpacity={0.9}
              barSize={30}
            />
          </CappedBarStack>
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const TokenChart = memo(TokenChartImpl);
