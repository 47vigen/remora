// Derived from maximhq/bifrost ui/app/workspace/dashboard/utils/exportUtils.ts (Apache-2.0); modified.
import type { StatsGroup, StatsPoint } from "../../../../client/types.gen";

type CSVData = { headers: string[]; rows: unknown[][] };

export function overviewVolumeToCSV(series: StatsPoint[]): CSVData {
  const headers = ["Time", "Requests", "Errors"];
  const rows = series.map((b) => [b.time, b.requests, b.errors]);
  return { headers, rows };
}

export function overviewTokensToCSV(groups: StatsGroup[]): CSVData {
  const headers = ["Provider", "Prompt tokens", "Completion tokens", "Total tokens"];
  const rows = groups.map((g) => [
    g.name,
    g.summary.prompt_tokens,
    g.summary.completion_tokens,
    g.summary.total_tokens,
  ]);
  return { headers, rows };
}

export function overviewCostToCSV(groups: StatsGroup[]): CSVData {
  const headers = ["Provider", "Cost (USD)"];
  const rows = groups.map((g) => [g.name, g.summary.cost_usd]);
  return { headers, rows };
}

export function overviewLatencyToCSV(groups: StatsGroup[]): CSVData {
  const headers = ["Provider", "p50 (ms)", "p95 (ms)"];
  const rows = groups.map((g) => [g.name, g.summary.latency_p50_ms ?? "", g.summary.latency_p95_ms ?? ""]);
  return { headers, rows };
}
