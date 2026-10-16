import { ArrowsRotateRight } from "@gravity-ui/icons";
import { Button, Chip } from "@heroui/react";

import type { QuotaAccount, QuotaProvider } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { formatRelative } from "../../../../lib/time";
import { accountHealth } from "../../providers/-lib/accounts";
import { QuotaBar } from "./QuotaBar";

export type RefreshTarget = { provider: string } | { account_id: string };

function Count({ label, value }: { label: string; value: number }) {
  return (
    <Chip size="sm" variant="soft">
      <Chip.Label>{`${label} ${value}`}</Chip.Label>
    </Chip>
  );
}

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
  return (
    <section aria-label={provider.provider} className="border-border bg-surface rounded-sm border">
      <header className="border-border flex flex-wrap items-center gap-2 border-b px-4 py-3">
        <h2 className="mr-2 text-sm font-medium">{provider.provider}</h2>
        <Count label="Available" value={provider.available} />
        <Count label="Exhausted" value={provider.exhausted} />
        <Count label="Cooling" value={provider.cooling} />
        <Count label="Sign in" value={provider.sign_in} />
        {provider.next_reset ? (
          <span className="text-muted text-xs">Next reset {formatRelative(provider.next_reset, now)}</span>
        ) : null}
        {provider.supported ? (
          <Button
            size="sm"
            variant="secondary"
            className="ml-auto"
            isPending={pending}
            onPress={() => onRefresh({ provider: provider.provider })}
          >
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
