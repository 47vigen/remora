import type { ReactNode } from "react";

/** A titled panel with a fixed-height plot area; shows `emptyText` instead of the chart when there is no data. */
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
  /** The headline figure for the chart, shown under the title. */
  value?: ReactNode;
  legend?: ReactNode;
  isEmpty: boolean;
  emptyText?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={`bg-surface border-border flex flex-col gap-4 rounded-sm border p-4 ${className ?? ""}`}>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <h3 className="label-caps text-muted">{title}</h3>
          {value ? <p className="text-xl leading-none font-medium tracking-[-0.02em] tabular-nums">{value}</p> : null}
        </div>
        {legend ? <div className="text-muted flex flex-wrap items-center gap-3 text-xs">{legend}</div> : null}
      </header>
      <div className="h-52 flex-none">
        {isEmpty ? (
          <p className="text-muted border-border flex h-full items-center justify-center rounded-sm border border-dashed text-sm">
            {emptyText}
          </p>
        ) : (
          children
        )}
      </div>
    </section>
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
