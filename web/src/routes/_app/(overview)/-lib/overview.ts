import type { Provider, StatsGroup, StatsPoint, StatsSummary } from "../../../../client/types.gen";
import { formatCost, formatLatency, formatTimestamp } from "../../../../lib/chart";
import { formatCompactNumber } from "../../../../lib/numbers";

export type Summary = { requests: string; errorRate: string; p50: string; p95: string; tokens: string; cost: string };

const DASH = "—";

function latency(ms: number | null | undefined): string {
  return typeof ms === "number" && Number.isFinite(ms) ? formatLatency(ms) : DASH;
}

/** Formats the window's totals for the stat cards. With no requests, rates and latencies show a dash. */
export function summarize(t: StatsSummary): Summary {
  const traffic = t.requests > 0;
  return {
    requests: formatCompactNumber(t.requests, 1),
    errorRate: traffic ? `${((t.errors / t.requests) * 100).toFixed(1)}%` : DASH,
    p50: traffic ? latency(t.latency_p50_ms) : DASH,
    p95: traffic ? latency(t.latency_p95_ms) : DASH,
    tokens: formatCompactNumber(t.total_tokens, 1),
    cost: formatCost(t.cost_usd),
  };
}

export type HealthCounts = Record<Provider["status"], number>;

/** Counts providers by status. */
export function healthCounts(items: Provider[]): HealthCounts {
  const counts: HealthCounts = { ok: 0, degraded: 0, down: 0, unknown: 0 };
  for (const p of items) counts[p.status] = (counts[p.status] ?? 0) + 1;
  return counts;
}

export type VolumeRow = { index: number; time: string; label: string; success: number; errors: number };

/** The bucket size of a series in seconds, from the gap between its first two points; an hour when unknown. */
export function bucketSeconds(series: StatsPoint[]): number {
  const [a, b] = series;
  if (!a || !b) return 3600;
  const gap = (Date.parse(b.time) - Date.parse(a.time)) / 1000;
  return Number.isFinite(gap) && gap > 0 ? gap : 3600;
}

/** Splits each bucket's requests into successes and errors for the stacked volume chart. */
export function volumeRows(series: StatsPoint[]): VolumeRow[] {
  const size = bucketSeconds(series);
  return series.map((p, index) => ({
    index,
    time: p.time,
    label: formatTimestamp(p.time, size),
    success: Math.max(p.requests - p.errors, 0),
    errors: p.errors,
  }));
}

export type ProviderRow = {
  name: string;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  cost: number;
  p50?: number;
  p95?: number;
};

/** One row per provider group, busiest first, for the per-provider charts. */
export function providerRows(groups: StatsGroup[]): ProviderRow[] {
  return groups
    .map(({ name, summary: s }) => ({
      name,
      promptTokens: s.prompt_tokens,
      completionTokens: s.completion_tokens,
      totalTokens: s.total_tokens,
      cost: s.cost_usd,
      p50: s.latency_p50_ms ?? undefined,
      p95: s.latency_p95_ms ?? undefined,
    }))
    .sort((a, b) => b.totalTokens - a.totalTokens || a.name.localeCompare(b.name));
}

export type SeriesRow = {
  index: number;
  time: string;
  label: string;
  promptTokens: number;
  completionTokens: number;
  cost: number;
  p50?: number;
  p95?: number;
};

/** One row per bucket with tokens, cost and latency, for the charts over time. */
export function seriesRows(series: StatsPoint[]): SeriesRow[] {
  const size = bucketSeconds(series);
  return series.map((p, index) => ({
    index,
    time: p.time,
    label: formatTimestamp(p.time, size),
    promptTokens: p.prompt_tokens ?? 0,
    completionTokens: p.completion_tokens ?? 0,
    cost: p.cost_usd ?? 0,
    p50: p.latency_p50_ms ?? undefined,
    p95: p.latency_p95_ms ?? undefined,
  }));
}

export type BreakdownRow = {
  name: string;
  requests: number;
  errorRate: number;
  totalTokens: number;
  cost: number;
  p95?: number;
};

/** One row per group, busiest first, for the breakdown table. */
export function breakdownRows(groups: StatsGroup[]): BreakdownRow[] {
  return groups
    .map(({ name, summary: s }) => ({
      name,
      requests: s.requests,
      errorRate: s.requests > 0 ? s.errors / s.requests : 0,
      totalTokens: s.total_tokens,
      cost: s.cost_usd,
      p95: s.latency_p95_ms ?? undefined,
    }))
    .sort((a, b) => b.requests - a.requests || a.name.localeCompare(b.name));
}
