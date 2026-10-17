import { ArrowsRotateRight } from "@gravity-ui/icons";
import { Button, cn, Link, Meter, Table } from "@heroui/react";

import { isSuspended, level, percentLabel, windowLabel } from "../-lib/quota";
import type { QuotaAccount, QuotaProvider, QuotaWindow } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { getOAuthProviderLabel } from "../../../../lib/providers";
import { formatRelative } from "../../../../lib/time";
import { ProviderMark } from "../../providers/-components/ProviderMark";
import { accountHealth } from "../../providers/-lib/accounts";

export type RefreshTarget = { provider: string } | { account_id: string };

const TEXT = { ok: "text-foreground", warn: "text-warning", critical: "text-danger", unknown: "text-muted" } as const;
const METER = { ok: "success", warn: "warning", critical: "danger", unknown: "default" } as const;
const key = (w: QuotaWindow) => `${w.scope}/${w.window}`;

/** The quota windows any account of the provider reported, in the order they first appear: one column each. */
function columns(rows: QuotaAccount[]): QuotaWindow[] {
  const seen = new Map<string, QuotaWindow>();
  for (const r of rows) for (const w of r.quota?.windows ?? []) if (!seen.has(key(w))) seen.set(key(w), w);
  return [...seen.values()];
}

/** One window of one account: the figure first, a short bar under it, the reset beside it. */
function WindowCell({ w, now }: { w: QuotaWindow | undefined; now: Date }) {
  if (!w) return <span className="text-muted">—</span>;
  const lvl = level(w.remaining);
  return (
    <span className="flex w-36 flex-col gap-1.5">
      <span className="flex items-baseline justify-between gap-2">
        <span className={cn("font-mono text-[13px] tabular-nums", TEXT[lvl])}>
          {lvl === "unknown" ? "—" : percentLabel(w.remaining)}
        </span>
        {w.resets_at ? (
          <span className="text-muted text-xs whitespace-nowrap">{formatRelative(w.resets_at, now)}</span>
        ) : null}
      </span>
      <Meter
        aria-label={`${windowLabel(w)} · ${percentLabel(w.remaining)}`}
        value={Math.round((w.remaining ?? 0) * 100)}
        color={METER[lvl]}
        size="sm"
      >
        <Meter.Track>
          <Meter.Fill />
        </Meter.Track>
      </Meter>
    </span>
  );
}

/** One provider's accounts: counts, the next reset and Refresh all on top, one table row per account below. */
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
  const windows = columns(rows);
  const counts = [
    [provider.exhausted, "exhausted", "text-danger"],
    [provider.cooling, "cooling", "text-warning"],
    [provider.suspended, "suspended", "text-danger"],
    [provider.sign_in, "need sign-in", "text-warning"],
  ] as const;

  return (
    <section aria-label={provider.provider} className="border-border bg-surface overflow-hidden rounded-sm border">
      <header className="border-border flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-3">
        <Link
          href={`/providers/${provider.provider}`}
          className="text-foreground flex min-w-0 items-center gap-2.5 text-sm font-medium no-underline"
        >
          <ProviderMark type={provider.provider} name={label} />
          {label}
        </Link>
        <p className="text-muted flex flex-wrap items-center gap-x-3 font-mono text-xs tabular-nums">
          <span className="text-foreground">{`${provider.available}/${provider.accounts.length} available`}</span>
          {counts
            .filter(([n]) => n > 0)
            .map(([n, name, tone]) => (
              <span key={name} className={tone}>{`${n} ${name}`}</span>
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
      <Table variant="secondary" className="rounded-none [&_td:first-child]:pl-4 [&_th:first-child]:pl-4">
        <Table.ScrollContainer>
          <Table.Content aria-label={`${label} accounts`}>
            <Table.Header>
              <Table.Column isRowHeader>Account</Table.Column>
              <Table.Column>Status</Table.Column>
              {windows.map((w) => (
                <Table.Column key={key(w)} id={key(w)}>
                  {windowLabel(w)}
                </Table.Column>
              ))}
              <Table.Column>Updated</Table.Column>
              <Table.Column className="w-12">
                <span className="sr-only">Actions</span>
              </Table.Column>
            </Table.Header>
            <Table.Body>
              {rows.map(({ account, quota }) => {
                const name = account.email ?? account.id;
                return (
                  <Table.Row key={account.id} id={account.id} className="group/row">
                    <Table.Cell>
                      <span className="flex max-w-64 min-w-0 flex-col gap-0.5">
                        <span className="truncate text-[13px] font-medium">{name}</span>
                        {quota?.error ? (
                          <span className="text-danger truncate text-xs" title={quota.error}>
                            {quota.error}
                          </span>
                        ) : quota?.plan ? (
                          <span className="text-muted text-xs">{quota.plan}</span>
                        ) : null}
                      </span>
                    </Table.Cell>
                    <Table.Cell>
                      <StatusChip
                        status={
                          account.enabled && isSuspended(quota?.error)
                            ? "suspended"
                            : accountHealth(account, now.getTime())
                        }
                        until={account.cooldown_until}
                      />
                    </Table.Cell>
                    {windows.map((w) => (
                      <Table.Cell key={key(w)}>
                        <WindowCell w={quota?.windows.find((x) => key(x) === key(w))} now={now} />
                      </Table.Cell>
                    ))}
                    <Table.Cell className="text-muted text-xs whitespace-nowrap">
                      {quota?.fetched_at ? formatRelative(quota.fetched_at, now) : provider.supported ? "never" : "—"}
                    </Table.Cell>
                    <Table.Cell>
                      {provider.supported ? (
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          aria-label={`Refresh quota of ${name}`}
                          className="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100"
                          isDisabled={pending}
                          onPress={() => onRefresh({ account_id: account.id })}
                        >
                          <ArrowsRotateRight aria-hidden="true" />
                        </Button>
                      ) : null}
                    </Table.Cell>
                  </Table.Row>
                );
              })}
            </Table.Body>
          </Table.Content>
        </Table.ScrollContainer>
      </Table>
    </section>
  );
}
