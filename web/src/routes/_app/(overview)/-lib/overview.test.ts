import type { Provider, StatsGroup, StatsSummary } from "../../../../client/types.gen";
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
    fromTraffic: 0,
  });
});

const provider = (name: string, status: Provider["status"] = "unknown"): Provider => ({
  name,
  status,
  kind: "openai-compatible",
  key_count: 0,
});

const traffic = (name: string, requests: number, errors: number): StatsGroup => ({
  name,
  summary: { ...base, requests, errors, error_rate: requests > 0 ? errors / requests : 0 },
});

test("providers with low-error traffic count as healthy even without probes", () => {
  expect(
    healthCounts(
      [provider("alpha"), provider("beta"), provider("gamma")],
      [traffic("alpha", 25, 1), traffic("beta", 1, 0), traffic("gamma", 8, 0)],
    ),
  ).toEqual({ ok: 3, degraded: 0, down: 0, unknown: 0, fromTraffic: 3 });
});

test("traffic without evidence of health leaves untested providers unknown", () => {
  expect(
    healthCounts(
      [provider("idle"), provider("missing"), provider("threshold"), provider("failing")],
      [traffic("unlisted", 2, 0), traffic("idle", 0, 0), traffic("threshold", 20, 1), traffic("failing", 3, 1)],
    ),
  ).toEqual({ ok: 0, degraded: 0, down: 0, unknown: 4, fromTraffic: 0 });
});

test("traffic does not override a known provider status", () => {
  expect(
    healthCounts(
      [provider("down", "down"), provider("degraded", "degraded"), provider("ok", "ok")],
      [traffic("down", 4, 0), traffic("degraded", 4, 0), traffic("ok", 4, 4)],
    ),
  ).toEqual({ ok: 1, degraded: 1, down: 1, unknown: 0, fromTraffic: 0 });
});
