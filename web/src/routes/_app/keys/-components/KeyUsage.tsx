import { cn, Link, Skeleton } from "@heroui/react";

import type { StatsSummary } from "../../../../client";
import { formatCost } from "../../../../lib/chart";
import { formatCompactNumber } from "../../../../lib/numbers";

/** One key's traffic over the last 24 hours, as a small sheet of figures split by hairlines. */
export function KeyUsage({
  totals,
  isLoading,
  logsHref,
}: {
  totals: StatsSummary | undefined;
  isLoading: boolean;
  logsHref: string;
}) {
  const isEmpty = !totals?.requests;
  const figures: [string, string, string?][] = [
    ["Requests", formatCompactNumber(totals?.requests ?? 0)],
    ["Errors", formatCompactNumber(totals?.errors ?? 0), `${((totals?.error_rate ?? 0) * 100).toFixed(1)}%`],
    ["Tokens", formatCompactNumber(totals?.total_tokens ?? 0)],
    ["Cost", formatCost(totals?.cost_usd ?? 0)],
  ];
  return (
    <section aria-label="Usage, last 24 hours" className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <h3 className="label-caps text-muted">Last 24 hours</h3>
        <Link href={logsHref} className="text-xs">
          View this key's logs
        </Link>
      </div>
      {isLoading ? (
        <Skeleton className="h-36 w-full" />
      ) : (
        <>
          <dl
            aria-hidden={isEmpty || undefined}
            className={cn(
              "bg-border border-border grid grid-cols-2 gap-px overflow-hidden rounded-sm border",
              isEmpty && "opacity-40",
            )}
          >
            {figures.map(([label, value, hint]) => (
              <div key={label} className="bg-surface flex flex-col gap-1.5 p-3">
                <dt className="label-caps text-muted">{label}</dt>
                <dd className="flex items-baseline gap-2">
                  <span className="text-xl font-medium tracking-[-0.02em] tabular-nums">{value}</span>
                  {hint ? <span className="text-muted font-mono text-xs tabular-nums">{hint}</span> : null}
                </dd>
              </div>
            ))}
          </dl>
          {isEmpty ? <p className="text-muted text-sm">No requests in the last 24 hours.</p> : null}
        </>
      )}
    </section>
  );
}
