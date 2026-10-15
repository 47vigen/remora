import { Link, Skeleton } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";

import { getStatsOptions } from "../../../../client/@tanstack/react-query.gen";
import { SectionLabel } from "../../../../components/page-header/PageHeader";
import { formatCost, formatLatency } from "../../../../lib/chart";
import { formatCompactNumber } from "../../../../lib/numbers";
import { SpecSheet } from "./SpecSheet";

/** What this provider carried over the last day, with a way into its logs. */
export function ProviderTraffic({ name }: { name: string }) {
  const stats = useQuery(getStatsOptions({ query: { provider: name } }));
  const s = stats.data?.summary;
  return (
    <section className="flex flex-col gap-3">
      <SectionLabel
        aside={
          <Link href={`/logs?provider=${encodeURIComponent(name)}`} className="text-xs">
            View logs
          </Link>
        }
      >
        Last 24 hours
      </SectionLabel>
      {s ? (
        <SpecSheet>
          <SpecSheet.Item label="Requests" mono>
            {formatCompactNumber(s.requests, 1)}
          </SpecSheet.Item>
          <SpecSheet.Item label="Error rate" mono>
            {s.requests > 0 ? `${((s.errors / s.requests) * 100).toFixed(1)}%` : "—"}
          </SpecSheet.Item>
          <SpecSheet.Item label="p95 latency" mono>
            {s.latency_p95_ms == null ? "—" : formatLatency(s.latency_p95_ms)}
          </SpecSheet.Item>
          <SpecSheet.Item label="Tokens" mono>
            {formatCompactNumber(s.total_tokens, 1)}
          </SpecSheet.Item>
          <SpecSheet.Item label="Cost" mono>
            {formatCost(s.cost_usd)}
          </SpecSheet.Item>
        </SpecSheet>
      ) : stats.isError ? (
        <p className="text-muted text-sm">Traffic is not available right now.</p>
      ) : (
        <Skeleton className="h-20 w-full" />
      )}
    </section>
  );
}
