import type { StatsGroup, StatsPoint } from "../../../../client/types.gen";
import { overviewCostToCSV, overviewLatencyToCSV, overviewTokensToCSV, overviewVolumeToCSV } from "./export";

const group: StatsGroup = {
  name: "openrouter",
  summary: {
    requests: 10,
    errors: 1,
    error_rate: 0.1,
    latency_p50_ms: 400,
    latency_p95_ms: null,
    prompt_tokens: 100,
    completion_tokens: 20,
    total_tokens: 120,
    cost_usd: 0.5,
  },
};

test("overviewVolumeToCSV", () => {
  expect(overviewVolumeToCSV([{ time: "2026-10-14T10:00:00Z", requests: 10, errors: 1 } as StatsPoint])).toEqual({
    headers: ["Time", "Requests", "Errors"],
    rows: [["2026-10-14T10:00:00Z", 10, 1]],
  });
});

test("overviewTokensToCSV, overviewCostToCSV and overviewLatencyToCSV are per provider", () => {
  expect(overviewTokensToCSV([group])).toEqual({
    headers: ["Provider", "Prompt tokens", "Completion tokens", "Total tokens"],
    rows: [["openrouter", 100, 20, 120]],
  });
  expect(overviewCostToCSV([group])).toEqual({ headers: ["Provider", "Cost (USD)"], rows: [["openrouter", 0.5]] });
  expect(overviewLatencyToCSV([group])).toEqual({
    headers: ["Provider", "p50 (ms)", "p95 (ms)"],
    rows: [["openrouter", 400, ""]],
  });
});
