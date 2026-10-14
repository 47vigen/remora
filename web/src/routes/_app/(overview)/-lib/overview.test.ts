import type { Provider, StatsSummary } from "../../../../client/types.gen";
import { healthCounts, summarize } from "./overview";

const base: StatsSummary = {
  requests: 8000,
  errors: 80,
  error_rate: 0.01,
  latency_p50_ms: 420,
  latency_p95_ms: 2600,
  prompt_tokens: 1_200_000,
  completion_tokens: 300_000,
  total_tokens: 1_500_000,
  cost_usd: 1.234,
};

test("summarize", () => {
  expect(summarize(base)).toEqual({
    requests: "8K",
    errorRate: "1.0%",
    p50: "420ms",
    p95: "2.60s",
    tokens: "1.5M",
    cost: "$1.23",
  });
});

test("no traffic shows dashes, never NaN", () => {
  const s = summarize({
    ...base,
    requests: 0,
    errors: 0,
    error_rate: 0,
    latency_p50_ms: null,
    latency_p95_ms: null,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    cost_usd: 0,
  });
  expect(s.errorRate).toBe("—");
  expect(s.p50).toBe("—");
  expect(s.p95).toBe("—");
  expect(JSON.stringify(s)).not.toContain("NaN");
});

test("healthCounts", () => {
  expect(healthCounts([{ status: "ok" }, { status: "down" }, { status: "ok" }] as Provider[])).toEqual({
    ok: 2,
    degraded: 0,
    down: 1,
    unknown: 0,
  });
});
