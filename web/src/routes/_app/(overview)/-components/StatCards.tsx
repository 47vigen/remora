import type { ReactNode } from "react";

/** One sheet of figures split by hairlines, the way a spec table reads. */
function Root({ children }: { children: ReactNode }) {
  return (
    <dl className="bg-border border-border grid grid-cols-2 gap-px overflow-hidden rounded-sm border md:grid-cols-3 xl:grid-cols-5">
      {children}
    </dl>
  );
}

function Item({ label, value, hint, trend }: { label: string; value: string; hint?: string; trend?: ReactNode }) {
  return (
    <div className="bg-surface relative flex min-h-28 flex-col gap-2 p-4">
      <dt className="label-caps text-muted">{label}</dt>
      <dd className="text-[28px] leading-none font-medium tracking-[-0.03em] tabular-nums">{value}</dd>
      {hint || trend ? (
        <dd className="mt-auto flex flex-col gap-1">
          {hint ? <span className="text-muted text-xs">{hint}</span> : null}
          {trend}
        </dd>
      ) : null}
    </div>
  );
}

export const StatCards = Object.assign(Root, { Root, Item });
