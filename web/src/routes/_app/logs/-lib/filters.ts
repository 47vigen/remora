import type { LogEntry } from "../../../../client";
import { type ListSearch, parseEnum, parseListSearch } from "../../../../lib/list-search";

export const RANGES = ["15m", "1h", "24h", "7d", "custom"] as const;
export type Range = (typeof RANGES)[number];
const STATUSES = ["success", "error"] as const;

export type LogSearch = ListSearch & {
  keyId?: string;
  alias?: string;
  servedModel?: string;
  provider?: string;
  status?: "success" | "error";
  range: Range;
  from?: string;
  to?: string;
  log?: string;
  live?: boolean;
};

/** One row of the logs list, built from a LogEntry. */
export type LogSummary = {
  id: string;
  ts: string;
  status: LogEntry["status"];
  provider: string;
  model: string;
  alias?: string;
  keyId?: string;
  keyName?: string;
  latencyMs?: number;
  tokens: LogEntry["tokens"];
  costUsd?: number;
  stream: boolean;
  /** True when this attempt is a fallback of an earlier one. */
  fallback: boolean;
  error?: string;
};

const RANGE_MS: Record<Exclude<Range, "custom">, number> = {
  "15m": 15 * 60_000,
  "1h": 3_600_000,
  "24h": 86_400_000,
  "7d": 7 * 86_400_000,
};

const text = (v: unknown) => (typeof v === "string" && v.trim() !== "" ? v.trim() : undefined);
const time = (v: unknown) => {
  const s = text(v);
  return s !== undefined && !Number.isNaN(new Date(s).getTime()) ? s : undefined;
};

export function parseLogSearch(raw: Record<string, unknown>): LogSearch {
  return {
    ...parseListSearch(raw),
    keyId: text(raw.keyId),
    alias: text(raw.alias),
    servedModel: text(raw.servedModel),
    provider: text(raw.provider),
    status: parseEnum(raw.status, STATUSES),
    range: parseEnum(raw.range, RANGES) ?? "24h",
    from: time(raw.from),
    to: time(raw.to),
    log: text(raw.log),
    live: raw.live === true || raw.live === "true" ? true : undefined,
  };
}

/** The time window a search asks for; a custom range missing an end falls back to the last 24 hours. */
export function rangeToWindow(s: LogSearch, now: Date): { from: string; to: string } {
  if (s.range === "custom" && s.from && s.to)
    return { from: new Date(s.from).toISOString(), to: new Date(s.to).toISOString() };
  const span = RANGE_MS[s.range === "custom" ? "24h" : s.range];
  return { from: new Date(now.getTime() - span).toISOString(), to: now.toISOString() };
}

/** Whether a live entry passes the list's filters; the search text is matched case-insensitively. */
export function matchesLogFilters(l: LogSummary, s: LogSearch): boolean {
  if (s.keyId && l.keyId !== s.keyId) return false;
  if (s.alias && l.alias !== s.alias) return false;
  if (s.provider && l.provider !== s.provider) return false;
  if (s.status && l.status !== s.status) return false;
  if (s.servedModel && `${l.provider}/${l.model}` !== s.servedModel) return false;
  if (s.q) {
    const q = s.q.toLowerCase();
    const fields = [l.alias, l.model, l.provider, l.keyName, `${l.provider}/${l.model}`];
    if (!fields.some((f) => f?.toLowerCase().includes(q))) return false;
  }
  return true;
}

/** Puts `incoming` first, drops an older copy of the same id, and keeps at most `cap` rows. */
export function prependLive(list: LogSummary[], incoming: LogSummary, cap: number): LogSummary[] {
  return [incoming, ...list.filter((l) => l.id !== incoming.id)].slice(0, cap);
}

export function toLogSummary(e: LogEntry, keyNames?: Map<string, string>): LogSummary {
  return {
    id: e.id,
    ts: e.time,
    status: e.status,
    provider: e.provider,
    model: e.model,
    alias: e.alias,
    keyId: e.key,
    keyName: e.key !== undefined ? keyNames?.get(e.key) : undefined,
    latencyMs: e.latency_ms,
    tokens: e.tokens,
    costUsd: e.cost_usd,
    stream: e.stream,
    fallback: e.attempt > 1 || e.parent_id !== undefined,
    error: e.error,
  };
}
