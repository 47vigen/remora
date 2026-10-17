import type { QuotaAccount, QuotaProvider, QuotaWindow } from "../../../../client/types.gen";

export type Level = "ok" | "warn" | "critical" | "unknown";

/** Whether a quota error says the provider suspended the account; signing in again does not help. */
export function isSuspended(error: string | null | undefined): boolean {
  return !!error?.startsWith("the provider suspended this account");
}

/** Above half left is ok, a fifth to a half is warn, below a fifth critical; unknown stays unknown. */
export function level(remaining: number | null | undefined): Level {
  if (remaining == null) return "unknown";
  if (remaining > 0.5) return "ok";
  if (remaining >= 0.2) return "warn";
  return "critical";
}

/** "80%", or "Unknown" when the provider did not say; never 0% for unknown. */
export function percentLabel(remaining: number | null | undefined): string {
  return remaining == null ? "Unknown" : `${Math.round(remaining * 100)}%`;
}

const SCOPES: Record<string, string> = { gemini: "Gemini", "claude-gpt": "Claude & GPT" };

/** "Gemini · 5h", "Claude & GPT · weekly", "Weekly", "1440 min". */
export function windowLabel(w: QuotaWindow): string {
  const length = w.window === "5h" || w.window === "weekly" ? w.window : `${w.window} min`;
  if (w.scope === "") return length.charAt(0).toUpperCase() + length.slice(1);
  return `${SCOPES[w.scope] ?? w.scope} · ${length}`;
}

export type QuotaRow = { provider: string; item: QuotaAccount };

/** Every account as one row, in provider order, matched by label or plan and kept to one provider when given. */
export function quotaRows(providers: QuotaProvider[], q?: string, provider?: string): QuotaRow[] {
  const needle = q?.trim().toLowerCase() ?? "";
  return providers
    .filter((p) => !provider || p.provider === provider)
    .flatMap((p) => p.accounts.map((item) => ({ provider: p.provider, item })))
    .filter(
      ({ item }) =>
        needle === "" ||
        (item.account.email ?? item.account.id).toLowerCase().includes(needle) ||
        (item.quota?.plan ?? "").toLowerCase().includes(needle),
    );
}

/** Whether every queued account was checked at or after since. */
export function refreshSettled(providers: QuotaProvider[], queued: string[], since: Date): boolean {
  const checked = new Map(providers.flatMap((p) => p.accounts.map((a) => [a.account.id, a.quota?.checked_at])));
  return queued.every((id) => {
    const at = checked.get(id);
    return at !== undefined && Date.parse(at) >= since.getTime();
  });
}
