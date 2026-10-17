import type { Account, RoutingStrategy } from "../../../../client/types.gen";
import type { Health } from "../../../../components/status-chip/StatusChip";

const ACTIVE = new Set(["active", "ok", "ready"]);
const FAILED = new Set(["error", "missing", "unavailable", "invalid", "expired", "refused"]);

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

/** The routing strategies of an account pool, in the order the provider page offers them. */
export const ACCOUNT_STRATEGIES: { id: RoutingStrategy; label: string; description: string }[] = [
  { id: "priority", label: "Priority", description: "Highest priority first; equal priorities take turns." },
  { id: "fill-first", label: "Fill first", description: "One account at a time: highest priority, then oldest." },
  { id: "round-robin", label: "Round robin", description: "Accounts take turns." },
  { id: "random", label: "Random", description: "A random account." },
  {
    id: "p2c",
    label: "Power of two",
    description: "Two random accounts; the one with fewer requests in the last minute serves.",
  },
  { id: "least-used", label: "Least used", description: "The account with the fewest requests in the last minute." },
];

/** Whether a pool's strategy reads the accounts' priority numbers. */
export function usesPriority(st: RoutingStrategy): boolean {
  return st === "priority" || st === "fill-first";
}
