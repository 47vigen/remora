import type { Account } from "../../../../client/types.gen";
import type { Health } from "../../../../components/status-chip/StatusChip";

const ACTIVE = new Set(["active", "ok", "ready"]);
const FAILED = new Set(["error", "missing", "unavailable", "invalid", "expired"]);

/** Whether the account's cooldown ends in the future. */
export function isCooling(a: Account, now = Date.now()): boolean {
  if (!a.cooldown_until) return false;
  const until = Date.parse(a.cooldown_until);
  return Number.isFinite(until) && until > now;
}

/** The chip status of an account: disabled first, then cooling, then the engine's status. */
export function accountHealth(a: Account, now = Date.now()): Health {
  if (!a.enabled) return "disabled";
  if (isCooling(a, now)) return "cooling";
  const s = a.status.toLowerCase();
  if (ACTIVE.has(s)) return "active";
  if (FAILED.has(s)) return "error";
  return "unknown";
}
