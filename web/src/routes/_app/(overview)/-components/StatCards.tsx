import type { ReactNode } from "react";

/** One sheet of figures split by hairlines, the way a spec table reads. */
function Root({ children }: { children: ReactNode }) {
  return (
    <dl className="bg-border border-border grid grid-cols-2 gap-px overflow-hidden rounded-sm border md:grid-cols-3 xl:grid-cols-6">
      {children}
    </dl>
  );
}

function Item({ label, value, hint, trend }: { label: string; value: string; hint?: string; trend?: ReactNode }) {
  return (
    <div className="bg-surface relative flex min-h-28 flex-col gap-2 p-4">
      <dt className="label-caps text-muted">{label}</dt>
      <dd className="text-[28px] leading-none font-medium tracking-[-0.03em] tabular-nums">{value}</dd>
      {hint ? <dd className="text-muted mt-auto text-xs">{hint}</dd> : null}
      {trend ? <dd className="mt-auto">{trend}</dd> : null}
    </div>
  );
}

export const StatCards = Object.assign(Root, { Root, Item });
