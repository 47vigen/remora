import { cloneElement, type ReactElement, type ReactNode } from "react";
import { ReferenceLine } from "recharts";

import { Panel } from "../Panel";

/** A titled tile whose plot keeps its axes even when there is no data. */
export function ChartCard({
  title,
  value,
  legend,
  isEmpty,
  emptyText = "No data in this window.",
  className,
  children,
}: {
  title: string;
  /** The headline figure for the chart; a dash keeps the row height when there is none. */
  value?: ReactNode;
  legend?: ReactNode;
  isEmpty: boolean;
  emptyText?: string;
  className?: string;
  /** A ResponsiveContainer wrapping one Cartesian chart. */
  children: ReactElement<{
    children: ReactElement<{ children: ReactNode; data: readonly { index: number }[]; accessibilityLayer?: boolean }>;
  }>;
}) {
  const chart = children.props.children;
  const plot = isEmpty
    ? cloneElement(children, {
        children: cloneElement(
          chart,
          { accessibilityLayer: false, data: chart.props.data.length ? chart.props.data : [{ index: 0 }] },
          chart.props.children,
          <ReferenceLine y={0} ifOverflow="extendDomain" stroke="var(--muted)" strokeOpacity={0.4} strokeWidth={2} />,
        ),
      })
    : children;
  return (
    <Panel
      title={title}
      aside={legend ? <div className="text-muted flex flex-wrap items-center gap-3 text-xs">{legend}</div> : null}
      className={className}
    >
      <div className="flex flex-1 flex-col gap-3 p-4">
        <p className="text-xl leading-none font-medium tracking-[-0.02em] tabular-nums">{value ?? "—"}</p>
        <div className="relative min-h-48 flex-1">
          <div className="absolute inset-0" aria-hidden={isEmpty || undefined} inert={isEmpty}>
            {plot}
          </div>
          {isEmpty ? (
            <p className="text-muted pointer-events-none absolute inset-x-0 top-1/2 text-center text-sm">{emptyText}</p>
          ) : null}
        </div>
      </div>
    </Panel>
  );
}
/** One legend entry: a square swatch and its name. */
export function LegendItem({ className, children }: { className: string; children: ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      <span aria-hidden="true" className={`size-2 ${className}`} />
      {children}
    </span>
  );
}
