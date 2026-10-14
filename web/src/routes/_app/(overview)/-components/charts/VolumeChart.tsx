// Derived from maximhq/bifrost ui/app/workspace/dashboard/components/charts/logVolumeChart.tsx (Apache-2.0); modified.
import { memo } from "react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { VolumeRow } from "../../-lib/overview";
import { CHART_COLORS, formatFullTimestamp } from "../../../../../lib/chart";
import { formatCompactNumber } from "../../../../../lib/numbers";
import { CappedBarStack } from "./barShape";
import { ChartCard } from "./ChartCard";
import { ChartTooltip } from "./ChartTooltip";

function VolumeChartImpl({ rows }: { rows: VolumeRow[] }) {
  return (
    <ChartCard title="Requests" isEmpty={rows.length === 0}>
      <ResponsiveContainer width="100%" height="100%" initialDimension={{ width: 1, height: 1 }}>
        <BarChart data={rows} margin={{ top: 6, right: 4, left: 12, bottom: 0 }} barCategoryGap={1}>
          <CartesianGrid strokeDasharray="3 3" vertical={false} stroke={CHART_COLORS.grid} />
          <XAxis
            dataKey="index"
            type="number"
            domain={[-0.5, rows.length - 0.5]}
            tick={{ fontSize: 11, fill: CHART_COLORS.axis, dy: 5 }}
            tickLine={false}
            axisLine={false}
            tickFormatter={(idx: number) => rows[Math.round(idx)]?.label ?? ""}
            interval="preserveStartEnd"
          />
          <YAxis
            tick={{ fontSize: 11, fill: CHART_COLORS.axis }}
            tickLine={false}
            axisLine={false}
            width={44}
            tickFormatter={(v: number) => formatCompactNumber(v)}
            domain={[0, (dataMax: number) => Math.max(dataMax, 1)]}
            allowDataOverflow={false}
          />
          <Tooltip
            cursor={{ fill: CHART_COLORS.axis, fillOpacity: 0.15 }}
            content={({ active, payload }) => {
              const row = active ? (payload?.[0]?.payload as VolumeRow | undefined) : undefined;
              if (!row) return null;
              return (
                <ChartTooltip title={formatFullTimestamp(row.time)}>
                  <ChartTooltip.Row tone="success" label="Success" value={row.success.toLocaleString()} />
                  <ChartTooltip.Row tone="danger" label="Error" value={row.errors.toLocaleString()} />
                </ChartTooltip>
              );
            }}
          />
          <CappedBarStack buckets={rows.length}>
            <Bar
              isAnimationActive={false}
              dataKey="success"
              fill={CHART_COLORS.success}
              fillOpacity={0.9}
              barSize={30}
            />
            <Bar isAnimationActive={false} dataKey="errors" fill={CHART_COLORS.error} fillOpacity={0.9} barSize={30} />
          </CappedBarStack>
        </BarChart>
      </ResponsiveContainer>
    </ChartCard>
  );
}

export const VolumeChart = memo(VolumeChartImpl);
