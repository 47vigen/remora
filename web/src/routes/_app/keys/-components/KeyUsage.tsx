import { Link, Spinner } from "@heroui/react";

import type { StatsSummary } from "../../../../client";
import { formatCost } from "../../../../lib/chart";
import { formatCompactNumber } from "../../../../lib/numbers";

/** One key's traffic over the last 24 hours. */
export function KeyUsage({
  totals,
  isLoading,
  logsHref,
}: {
  totals: StatsSummary | undefined;
  isLoading: boolean;
  logsHref: string;
}) {
  const figures = totals
    ? [
        ["Requests", formatCompactNumber(totals.requests)],
        ["Errors", `${formatCompactNumber(totals.errors)} (${(totals.error_rate * 100).toFixed(1)}%)`],
        ["Tokens", formatCompactNumber(totals.total_tokens)],
        ["Cost", formatCost(totals.cost_usd)],
      ]
    : [];
  return (
    <section aria-label="Usage, last 24 hours" className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Last 24 hours</h3>
      {isLoading ? (
        <Spinner size="sm" />
      ) : !totals || totals.requests === 0 ? (
        <p className="text-muted text-sm">No requests in the last 24 hours.</p>
      ) : (
        <dl className="grid grid-cols-2 gap-3">
          {figures.map(([label, value]) => (
            <div key={label} className="flex flex-col">
              <dt className="text-muted text-xs">{label}</dt>
              <dd className="text-lg font-semibold tabular-nums">{value}</dd>
            </div>
          ))}
        </dl>
      )}
      <Link href={logsHref} className="text-sm">
        View this key's logs
      </Link>
    </section>
  );
}
