import { ArrowsRotateRight } from "@gravity-ui/icons";
import { Button, Chip, Link } from "@heroui/react";

import type { QuotaAccount, QuotaProvider } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { getOAuthProviderLabel } from "../../../../lib/providers";
import { formatRelative } from "../../../../lib/time";
import { ProviderMark } from "../../providers/-components/ProviderMark";
import { accountHealth } from "../../providers/-lib/accounts";
import { QuotaBar } from "./QuotaBar";

export type RefreshTarget = { provider: string } | { account_id: string };

function Row({
  item,
  supported,
  now,
  pending,
  onRefresh,
}: {
  item: QuotaAccount;
  supported: boolean;
  now: Date;
  pending: boolean;
  onRefresh(target: RefreshTarget): void;
}) {
  const { account, quota } = item;
  const label = account.email ?? account.id;
  return (
    <li className="border-border flex flex-col gap-2 border-t px-4 py-3 first:border-t-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 truncate text-sm font-medium">{label}</span>
        {quota?.plan ? (
          <Chip size="sm" variant="soft">
            <Chip.Label>{quota.plan}</Chip.Label>
          </Chip>
        ) : null}
        <StatusChip status={accountHealth(account, now.getTime())} until={account.cooldown_until} />
        <span className="text-muted ml-auto text-xs">
          {quota?.fetched_at
            ? `updated ${formatRelative(quota.fetched_at, now)} · ${quota.source}`
            : supported
              ? "never checked"
              : null}
        </span>
        {supported ? (
          <Button
            isIconOnly
            size="sm"
            variant="ghost"
            aria-label={`Refresh quota of ${label}`}
            isDisabled={pending}
            onPress={() => onRefresh({ account_id: account.id })}
          >
            <ArrowsRotateRight aria-hidden="true" />
          </Button>
        ) : null}
      </div>
      {quota && quota.windows.length > 0 ? (
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          {quota.windows.map((w) => (
            <QuotaBar key={`${w.scope}/${w.window}`} window={w} now={now} />
          ))}
        </div>
      ) : null}
      {quota?.error ? <p className="text-danger text-xs">{quota.error}</p> : null}
    </li>
  );
}

/** One provider's accounts: counts, the next reset and Refresh all on top, one row per account below. */
export function ProviderQuota({
  provider,
  rows,
  now,
  pending,
  onRefresh,
}: {
  provider: QuotaProvider;
  rows: QuotaAccount[];
  now: Date;
  pending: boolean;
  onRefresh(target: RefreshTarget): void;
}) {
  const label = getOAuthProviderLabel(provider.provider);
  const counts: [string, number, string][] = [
    ["available", provider.available, "text-foreground"],
    ["exhausted", provider.exhausted, "text-danger"],
    ["cooling", provider.cooling, "text-warning"],
    ["need sign-in", provider.sign_in, "text-warning"],
  ];
  return (
    <section aria-label={provider.provider} className="border-border bg-surface rounded-sm border">
      <header className="border-border flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-3">
        <Link
          href={`/providers/${provider.provider}`}
          className="text-foreground flex min-w-0 items-center gap-2.5 text-sm font-medium no-underline"
        >
          <ProviderMark type={provider.provider} name={label} />
          {label}
        </Link>
        <p className="text-muted flex flex-wrap items-center gap-x-3 font-mono text-xs tabular-nums">
          {counts.map(([name, n, tone]) => (
            <span key={name} className={n > 0 ? tone : undefined}>
              {`${n} ${name}`}
            </span>
          ))}
          {provider.next_reset ? <span>{`next reset ${formatRelative(provider.next_reset, now)}`}</span> : null}
        </p>
        {provider.supported ? (
          <Button
            size="sm"
            variant="secondary"
            className="ml-auto"
            isPending={pending}
            onPress={() => onRefresh({ provider: provider.provider })}
          >
            <ArrowsRotateRight aria-hidden="true" />
            Refresh all
          </Button>
        ) : null}
      </header>
      {provider.supported ? null : (
        <p className="text-muted border-border border-b px-4 py-2 text-xs">Quota is not reported for this provider.</p>
      )}
      <ul>
        {rows.map((item) => (
          <Row
            key={item.account.id}
            item={item}
            supported={provider.supported}
            now={now}
            pending={pending}
            onRefresh={onRefresh}
          />
        ))}
      </ul>
    </section>
  );
}
