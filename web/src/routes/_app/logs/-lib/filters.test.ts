import type { LogEntry } from "../../../../client";
import {
  type LogSummary,
  matchesLogFilters,
  parseLogSearch,
  prependLive,
  rangeToWindow,
  toLogSummary,
} from "./filters";

test("parseLogSearch defaults and drops unknown values", () => {
  expect(parseLogSearch({})).toMatchObject({ page: 1, pageSize: 25, q: "", range: "24h" });
  expect(parseLogSearch({ status: "weird", range: "1y" })).toMatchObject({ status: undefined, range: "24h" });
  expect(parseLogSearch({ keyId: "k1", live: "true", log: "l1", from: "nope" })).toMatchObject({
    keyId: "k1",
    live: true,
    log: "l1",
    from: undefined,
  });
});

test("rangeToWindow", () => {
  const now = new Date("2026-10-14T10:00:00Z");
  expect(rangeToWindow(parseLogSearch({ range: "1h" }), now)).toEqual({
    from: "2026-10-14T09:00:00.000Z",
    to: "2026-10-14T10:00:00.000Z",
  });
  expect(
    rangeToWindow(parseLogSearch({ range: "custom", from: "2026-10-01T00:00:00Z", to: "2026-10-02T00:00:00Z" }), now),
  ).toEqual({ from: "2026-10-01T00:00:00.000Z", to: "2026-10-02T00:00:00.000Z" });
  // A custom range without its ends falls back to the last day.
  expect(rangeToWindow(parseLogSearch({ range: "custom" }), now).from).toBe("2026-10-13T10:00:00.000Z");
});

test("matchesLogFilters applies every set filter", () => {
  const l = {
    status: "error",
    provider: "or",
    alias: "fast",
    model: "x",
    keyId: "k1",
    ts: "2026-10-14T10:00:00Z",
  } as LogSummary;
  expect(matchesLogFilters(l, parseLogSearch({ status: "error", provider: "or" }))).toBe(true);
  expect(matchesLogFilters(l, parseLogSearch({ alias: "slow" }))).toBe(false);
  expect(matchesLogFilters(l, parseLogSearch({ keyId: "k2" }))).toBe(false);
  expect(matchesLogFilters(l, parseLogSearch({ servedModel: "or/x" }))).toBe(true);
  expect(matchesLogFilters(l, parseLogSearch({ servedModel: "or/y" }))).toBe(false);
  expect(matchesLogFilters(l, parseLogSearch({ q: "FAST" }))).toBe(true); // case-insensitive over alias, model, provider, key name
  expect(matchesLogFilters({ ...l, keyName: "Laptop" }, parseLogSearch({ q: "lap" }))).toBe(true);
  expect(matchesLogFilters(l, parseLogSearch({ q: "nothing" }))).toBe(false);
});

test("prependLive caps and de-duplicates", () => {
  const a = { id: "a" } as LogSummary,
    b = { id: "b" } as LogSummary;
  expect(prependLive([a], b, 1).map((l) => l.id)).toEqual(["b"]);
  expect(prependLive([a], a, 5).map((l) => l.id)).toEqual(["a"]);
  expect(prependLive([a], b, 5).map((l) => l.id)).toEqual(["b", "a"]);
});

test("toLogSummary maps an entry and marks fallbacks", () => {
  const e: LogEntry = {
    id: "l2",
    parent_id: "l1",
    attempt: 2,
    time: "2026-10-14T10:00:00Z",
    key: "k1",
    alias: "fast",
    provider: "or",
    model: "x",
    status: "success",
    latency_ms: 120,
    tokens: { prompt: 10, completion: 5, total: 15 },
    cost_usd: 0.001,
    stream: true,
  };
  expect(toLogSummary(e, new Map([["k1", "ci"]]))).toEqual({
    id: "l2",
    ts: "2026-10-14T10:00:00Z",
    status: "success",
    provider: "or",
    model: "x",
    alias: "fast",
    keyId: "k1",
    keyName: "ci",
    latencyMs: 120,
    tokens: { prompt: 10, completion: 5, total: 15 },
    costUsd: 0.001,
    stream: true,
    fallback: true,
    error: undefined,
  });
  expect(toLogSummary({ ...e, parent_id: undefined, attempt: 1 }).fallback).toBe(false);
});
