import { cn, Link } from "@heroui/react";
import type { ReactNode } from "react";

import type { HealthCounts } from "../-lib/overview";

const PARTS = [
  { status: "ok", label: "healthy", bar: "bg-success" },
  { status: "degraded", label: "degraded", bar: "bg-warning" },
  { status: "down", label: "down", bar: "bg-danger" },
  { status: "unknown", label: "not tested", bar: "bg-surface-tertiary" },
] as const;

/** How many providers answer, as one proportional bar and a line per state that needs a look. */
export function HealthSummary({ counts, action }: { counts: HealthCounts; action?: ReactNode }) {
  const total = counts.ok + counts.degraded + counts.down + counts.unknown;
  return (
    <section className="bg-surface border-border flex flex-col gap-4 rounded-sm border p-4">
      <header className="flex items-center justify-between gap-3">
        <h3 className="label-caps text-muted">Provider health</h3>
        {action}
      </header>
      <p className="text-xl leading-none font-medium tracking-[-0.02em] tabular-nums">
        {counts.ok}
        <span className="text-muted"> / {total}</span>
      </p>
      {/* One block per provider, grouped by state. */}
      <div className="flex h-2 gap-0.5" aria-hidden="true">
        {total === 0 ? <span className="bg-surface-tertiary flex-1" /> : null}
        {PARTS.flatMap(({ status, bar }) =>
          Array.from({ length: counts[status] }, (_, i) => (
            <span key={`${status}-${i}`} className={cn("flex-1", bar)} />
          )),
        )}
      </div>
      <ul className="flex flex-col gap-2 text-sm">
        <li className="flex items-center gap-2">
          <span aria-hidden="true" className="bg-success size-1.5 rounded-full" />
          <span>{counts.ok} healthy</span>
        </li>
        {PARTS.slice(1)
          .filter(({ status }) => counts[status] > 0)
          .map(({ status, label, bar }) => (
            <li key={status} className="flex items-center gap-2">
              <span
                aria-hidden="true"
                className={cn("size-1.5 rounded-full", bar === "bg-surface-tertiary" ? "bg-muted" : bar)}
              />
              <Link href={`/providers?status=${status}`} className="text-sm">
                {counts[status]} {label}
              </Link>
            </li>
          ))}
      </ul>
    </section>
  );
}
