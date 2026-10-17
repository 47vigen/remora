import { cn, Link } from "@heroui/react";
import type { ReactNode } from "react";

import type { HealthCounts } from "../-lib/overview";
import type { AccountPool, Cooling, QuotaProvider } from "../../../../client/types.gen";
import { getOAuthProviderLabel } from "../../../../lib/providers";
import { formatRelative } from "../../../../lib/time";

export const COOLING_LIMIT = 5;

type Tone = "danger" | "warning" | "muted";
type Item = { key: string; tone: Tone; title: ReactNode; detail?: ReactNode; href?: string };

const DOT: Record<Tone, string> = { danger: "bg-danger", warning: "bg-warning", muted: "bg-muted" };
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** Everything an operator should look at, most urgent first, with provider health as one bar on top. */
export function Attention({
  health,
  pool,
  quota,
  quotaError,
  cooling,
  coolingTotal,
  now,
  action,
  className,
}: {
  className?: string;
  health: HealthCounts;
  pool: AccountPool[];
  quota: QuotaProvider[];
  /** Set when the quota could not be read, so its silence is not taken for health. */
  quotaError?: string;
  cooling: Cooling[];
  coolingTotal: number;
  now: Date;
  action?: ReactNode;
}) {
  const total = health.ok + health.degraded + health.down + health.unknown;
  const items: Item[] = [];
  if (health.down > 0)
    items.push({
      key: "down",
      tone: "danger",
      title: `${plural(health.down, "provider")} down`,
      href: "/providers?status=down",
    });
  if (health.degraded > 0)
    items.push({
      key: "degraded",
      tone: "warning",
      title: `${plural(health.degraded, "provider")} degraded`,
      href: "/providers?status=degraded",
    });
  for (const p of pool.filter((x) => x.out.length > 0))
    items.push({
      key: `pool:${p.provider}`,
      tone: "warning",
      title: `${p.out.length} of ${p.enabled} ${getOAuthProviderLabel(p.provider)} accounts out of the pool`,
      detail: p.out.map((a) => `${a.label} (${a.reason})`).join(", "),
      href: `/providers/${p.provider}`,
    });
  if (quotaError)
    items.push({ key: "quota-error", tone: "danger", title: "Could not read subscription quota", detail: quotaError });
  for (const q of quota) {
    const label = getOAuthProviderLabel(q.provider);
    if (q.exhausted > 0)
      items.push({
        key: `exhausted:${q.provider}`,
        tone: "warning",
        title: `${plural(q.exhausted, `${label} account`)} out of quota`,
        detail: q.next_reset ? `next reset ${formatRelative(q.next_reset, now)}` : undefined,
        href: `/quota?provider=${q.provider}`,
      });
    if (q.suspended > 0)
      items.push({
        key: `suspended:${q.provider}`,
        tone: "danger",
        title: `${plural(q.suspended, `${label} account`)} suspended by the provider`,
        detail: "signing in again does not help; appeal to the provider or replace the account",
        href: `/quota?provider=${q.provider}`,
      });
    if (q.sign_in > 0)
      items.push({
        key: `signin:${q.provider}`,
        tone: "danger",
        title: `${plural(q.sign_in, `${label} account`)} must sign in again`,
        href: `/providers/${q.provider}`,
      });
  }
  for (const c of cooling.slice(0, COOLING_LIMIT))
    items.push({
      key: `cool:${c.provider}/${c.model}`,
      tone: "muted",
      title: <span className="font-mono text-[13px]">{`${c.provider}/${c.model}`}</span>,
      detail: `cooling ${formatRelative(c.until, now).replace(/^in /, "for ")} · ${c.reason}`,
      href: c.aliases[0] ? `/aliases/${encodeURIComponent(c.aliases[0])}` : undefined,
    });
  if (coolingTotal > COOLING_LIMIT)
    items.push({
      key: "cool:more",
      tone: "muted",
      title: `${coolingTotal - COOLING_LIMIT} more cooling`,
      href: "/aliases?cooling=true",
    });
  if (health.unknown > 0)
    items.push({ key: "untested", tone: "muted", title: `${plural(health.unknown, "provider")} not tested yet` });

  return (
    <section className={cn("bg-surface border-border flex min-w-0 flex-col rounded-sm border", className)}>
      <header className="border-border flex h-12 shrink-0 items-center justify-between gap-3 border-b px-4">
        <h3 className="label-caps text-muted">Needs attention</h3>
        {action}
      </header>
      <div className="border-border flex flex-col gap-2 border-b px-4 py-3">
        <p className="flex items-baseline justify-between text-sm">
          <span className="text-muted">Providers healthy</span>
          <span className="font-mono text-xs tabular-nums">
            {health.ok}
            <span className="text-muted">/{total}</span>
          </span>
        </p>
        <div className="flex h-1.5 gap-0.5" aria-hidden="true">
          {total === 0 ? <span className="bg-surface-tertiary flex-1" /> : null}
          {(["ok", "degraded", "down", "unknown"] as const).flatMap((s) =>
            Array.from({ length: health[s] }, (_, i) => (
              <span
                key={`${s}-${i}`}
                className={cn(
                  "flex-1",
                  s === "ok"
                    ? "bg-success"
                    : s === "degraded"
                      ? "bg-warning"
                      : s === "down"
                        ? "bg-danger"
                        : "bg-surface-tertiary",
                )}
              />
            )),
          )}
        </div>
      </div>
      {items.length === 0 ? (
        <p className="text-muted p-4 text-sm">
          Nothing needs attention. Every provider, account and fallback is serving.
        </p>
      ) : (
        <ul className="divide-separator flex flex-col divide-y">
          {items.map((it) => (
            <li key={it.key} className="flex items-start gap-2.5 px-4 py-2.5">
              <span aria-hidden="true" className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", DOT[it.tone])} />
              <span className="flex min-w-0 flex-col gap-0.5">
                {it.href ? (
                  <Link href={it.href} className="text-foreground text-sm no-underline hover:underline">
                    {it.title}
                  </Link>
                ) : (
                  <span className="text-sm">{it.title}</span>
                )}
                {it.detail ? (
                  <span className="text-muted truncate text-xs" dir="auto">
                    {it.detail}
                  </span>
                ) : null}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
