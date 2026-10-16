import type { ReactNode } from "react";

import { Panel } from "../Panel";

/** A titled tile whose plot fills the rest of its height; shows `emptyText` instead of the chart when there is no data. */
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
  children: ReactNode;
}) {
  return (
    <Panel
      title={title}
      aside={legend ? <div className="text-muted flex flex-wrap items-center gap-3 text-xs">{legend}</div> : null}
      className={className}
    >
      <div className="flex flex-1 flex-col gap-3 p-4">
        <p className="text-xl leading-none font-medium tracking-[-0.02em] tabular-nums">{value ?? "—"}</p>
        <div className="min-h-48 flex-1">
          {isEmpty ? (
            <p className="text-muted border-border flex h-full items-center justify-center rounded-sm border border-dashed text-sm">
              {emptyText}
            </p>
          ) : (
            children
          )}
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
