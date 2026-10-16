import type { QuotaProvider } from "../../../../client/types.gen";
import { level, percentLabel, quotaRows, refreshSettled, windowLabel } from "./quota";

const account = (id: string, email: string) => ({
  id,
  provider: "x",
  email,
  enabled: true,
  priority: 0,
  status: "active",
  quota: [],
  models: [],
});
const snapshot = (plan: string, checked_at: string) => ({
  plan,
  windows: [],
  limit_reached: false,
  source: "poll" as const,
  checked_at,
  error: "",
});
const providers: QuotaProvider[] = [
  {
    provider: "antigravity",
    supported: true,
    available: 1,
    exhausted: 0,
    cooling: 0,
    sign_in: 0,
    accounts: [{ account: account("1", "a@example.test"), quota: snapshot("Pro", "2026-10-16T10:00:00Z") }],
  },
  {
    provider: "codex",
    supported: true,
    available: 1,
    exhausted: 0,
    cooling: 0,
    sign_in: 0,
    accounts: [{ account: account("2", "b@example.test"), quota: snapshot("plus", "2026-10-16T12:00:00Z") }],
  },
];

test("level", () => {
  expect([0.51, 0.5, 0.2, 0.19, 0, null, undefined].map(level)).toEqual([
    "ok",
    "warn",
    "warn",
    "critical",
    "critical",
    "unknown",
    "unknown",
  ]);
});

test("percentLabel never turns unknown into a number", () => {
  expect(percentLabel(null)).toBe("Unknown");
  expect(percentLabel(0)).toBe("0%");
  expect(percentLabel(0.804)).toBe("80%");
});

test("windowLabel", () => {
  const w = (scope: string, window: string) => windowLabel({ scope, window, remaining: null, resets_at: null });
  expect(w("gemini", "5h")).toBe("Gemini · 5h");
  expect(w("claude-gpt", "weekly")).toBe("Claude & GPT · weekly");
  expect(w("", "weekly")).toBe("Weekly");
  expect(w("", "1440")).toBe("1440 min");
});

test("quotaRows filters by plan and provider", () => {
  expect(quotaRows(providers, "pro").map((r) => r.item.account.id)).toEqual(["1"]);
  expect(quotaRows(providers, "", "codex").map((r) => r.provider)).toEqual(["codex"]);
  expect(quotaRows(providers).length).toBe(2);
});

test("refreshSettled waits for every queued account", () => {
  expect(refreshSettled(providers, ["1", "2"], new Date("2026-10-16T11:00:00Z"))).toBe(false);
  expect(refreshSettled(providers, ["2"], new Date("2026-10-16T11:00:00Z"))).toBe(true);
});
