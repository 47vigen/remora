// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/latencyChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { ProviderRow } from "../../-lib/overview";
import { CHART_COLORS, formatLatency } from "../../../../../lib/chart";
import { barShape } from "./barShape";
import { ChartCard } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

// p50 and p95 latency per provider over the window.
function LatencyChartImpl({ rows }: { rows: ProviderRow[] }) {
  const measured = rows.filter((r) => r.p50 !== undefined || r.p95 !== undefined);
  return (
    <ChartCard title="Latency by provider" isEmpty={measured.length === 0}>
      <ResponsiveContainer width="100%" height="100%" initialDimension={{ width: 1, height: 1 }}>
        <BarChart data={measured} margin={{ top: 6, right: 4, left: 4, bottom: 0 }}>
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
            tickFormatter={formatLatency}
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
                  <ChartTooltip.Row
                    tone="accent"
                    label="p50"
                    value={row.p50 === undefined ? "—" : formatLatency(row.p50)}
                  />
                  <ChartTooltip.Row
                    tone="warning"
                    label="p95"
                    value={row.p95 === undefined ? "—" : formatLatency(row.p95)}
                  />
                </ChartTooltip>
              );
            }}
          />
          <Bar
            isAnimationActive={false}
            dataKey="p50"
            fill={CHART_COLORS.p50}
            fillOpacity={0.9}
            barSize={12}
            shape={barShape}
          />
          <Bar
            isAnimationActive={false}
            dataKey="p95"
            fill={CHART_COLORS.p95}
            fillOpacity={0.9}
            barSize={12}
            shape={barShape}
          />
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const LatencyChart = memo(LatencyChartImpl);
