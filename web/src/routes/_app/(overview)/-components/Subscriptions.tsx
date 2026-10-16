import { buttonVariants, cn, Link } from "@heroui/react";

import { capacityWindows } from "../-lib/overview";
import type { QuotaProvider } from "../../../../client/types.gen";
import { getOAuthProviderLabel } from "../../../../lib/providers";
import { formatRelative } from "../../../../lib/time";
import { ProviderMark } from "../../providers/-components/ProviderMark";
import { QuotaBar } from "../../quota/-components/QuotaBar";

const SHOWN_WINDOWS = 2;

/** Subscription capacity at a glance: per provider, the accounts that can serve and how much quota they have left. */
export function Subscriptions({ providers, now }: { providers: QuotaProvider[]; now: Date }) {
  const active = providers.filter((p) => p.accounts.length > 0);
  return (
    <section className="bg-surface border-border flex flex-col rounded-sm border">
      <header className="border-border flex items-center justify-between gap-3 border-b px-4 py-3">
        <h3 className="label-caps text-muted">Subscriptions</h3>
        {active.length > 0 ? (
          <Link href="/quota" className="text-xs">
            Open quota
          </Link>
        ) : null}
      </header>
      {active.length === 0 ? (
        <div className="flex flex-1 flex-col items-start justify-center gap-3 p-4">
          <p className="text-muted text-sm">
            No subscription accounts yet. Sign in to Antigravity, Codex or Claude to route through them.
          </p>
          <Link
            href="/providers?group=subscription"
            className={cn(buttonVariants({ size: "sm", variant: "secondary" }), "no-underline")}
          >
            Connect an account
          </Link>
        </div>
      ) : (
        <ul className="divide-separator flex flex-col divide-y">
          {active.map((p) => {
            const label = getOAuthProviderLabel(p.provider);
            const windows = capacityWindows(p).slice(0, SHOWN_WINDOWS);
            const total = p.accounts.length;
            return (
              <li key={p.provider} className="flex flex-col gap-3 px-4 py-3">
                <div className="flex items-center gap-2.5">
                  <ProviderMark type={p.provider} name={label} />
                  <Link
                    href={`/providers/${p.provider}`}
                    className="text-foreground min-w-0 truncate text-sm font-medium no-underline"
                  >
                    {label}
                  </Link>
                  <span className="text-muted ml-auto font-mono text-xs tabular-nums">
                    <span className={p.available < total ? "text-warning" : "text-foreground"}>{p.available}</span>/
                    {total} ready
                  </span>
                </div>
                {windows.length > 0 ? (
                  <div className="flex flex-col gap-2">
                    {windows.map((w) => (
                      <QuotaBar key={`${w.scope}/${w.window}`} window={w} now={now} />
                    ))}
                  </div>
                ) : (
                  <p className="text-muted text-xs">
                    {p.supported ? "Quota not checked yet." : "Quota is not reported."}
                  </p>
                )}
                {p.next_reset ? (
                  <p className="text-muted text-xs">Next reset {formatRelative(p.next_reset, now)}</p>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
