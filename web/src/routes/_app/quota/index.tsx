import { Alert, buttonVariants, cn, Link, Table } from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { getQuotaOptions, refreshQuotaMutation } from "../../../client/@tanstack/react-query.gen";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, PAGE_SIZES, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { ProviderQuota, type RefreshTarget } from "./-components/ProviderQuota";
import { refreshSettled } from "./-lib/quota";

type QuotaSearch = ListSearch & { provider?: string };

const FAST_POLL_MS = 2_000;
const SLOW_POLL_MS = 60_000;
const REFRESH_WAIT_MS = 60_000;

export const Route = createFileRoute("/_app/quota/")({
  validateSearch: (raw: Record<string, unknown>): QuotaSearch => ({
    ...parseListSearch(raw),
    provider: typeof raw.provider === "string" && raw.provider !== "" ? raw.provider : undefined,
  }),
  component: QuotaPage,
});

function QuotaPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const { page, pageSize, q, provider } = search;
  const update = (next: Partial<QuotaSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...next }), replace: true });

  const [pending, setPending] = useState<{ queued: string[]; since: Date } | null>(null);
  const [skipped, setSkipped] = useState(0);
  useEffect(() => {
    if (pending === null) return;
    const timer = setTimeout(() => setPending(null), REFRESH_WAIT_MS);
    return () => clearTimeout(timer);
  }, [pending]);
  const waiting = (items: Parameters<typeof refreshSettled>[0]) =>
    pending !== null && !refreshSettled(items, pending.queued, pending.since);
  const quota = useQuery({
    ...getQuotaOptions({ query: { limit: pageSize, offset: (page - 1) * pageSize, q: q || undefined, provider } }),
    refetchInterval: (query) => (waiting(query.state.data?.items ?? []) ? FAST_POLL_MS : SLOW_POLL_MS),
  });
  const providers = quota.data?.items ?? [];
  const providerOptions = useQuery({ ...getQuotaOptions({ query: { limit: 1 } }), enabled: provider !== undefined });
  const now = new Date(quota.dataUpdatedAt);

  const refresh = useMutation({ ...refreshQuotaMutation() });
  const onRefresh = (body: RefreshTarget) => {
    const since = new Date();
    refresh.mutate(
      { body },
      {
        onSuccess: async (res) => {
          setPending({ queued: res.queued, since });
          setSkipped(res.skipped.length);
          await invalidateOps(queryClient, "getQuota");
        },
      },
    );
  };
  const busy = waiting(providers);
  const groups = providers.filter((p) => p.accounts.length > 0);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Quota"
        description="How much of each subscription account's quota is left, and when it resets."
      />
      <DataList.Root>
        <DataList.Toolbar>
          <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search accounts or plans" />
          <DataList.Filters>
            <DataList.Filter
              label="Provider"
              value={provider}
              options={(providerOptions.data?.items ?? providers).map((p) => ({ id: p.provider, label: p.provider }))}
              onChange={(v) => update({ provider: v })}
            />
          </DataList.Filters>
        </DataList.Toolbar>
        {skipped > 0 ? (
          <Alert status="default">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Description>{`${skipped} account(s) were checked less than a minute ago.`}</Alert.Description>
            </Alert.Content>
          </Alert>
        ) : null}
        {refresh.error ? (
          <Alert status="danger" role="alert">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>Could not refresh quota</Alert.Title>
              <Alert.Description>{errorMessage(refresh.error)}</Alert.Description>
            </Alert.Content>
          </Alert>
        ) : null}
        <DataList.State
          isLoading={quota.isPending}
          error={quota.error ?? undefined}
          isEmpty={groups.length === 0}
          emptyText={
            q || provider
              ? "No accounts match these filters."
              : "No subscription accounts yet. Sign in on the provider page."
          }
          emptyAction={
            q || provider ? undefined : (
              <Link
                href="/providers"
                className={cn(buttonVariants({ variant: "primary", size: "sm" }), "no-underline")}
              >
                Open providers
              </Link>
            )
          }
        >
          {groups.length === 0 ? (
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label="Subscription accounts">
                  <Table.Header>
                    <Table.Column isRowHeader>Account</Table.Column>
                    <Table.Column>Status</Table.Column>
                    <Table.Column>Updated</Table.Column>
                  </Table.Header>
                  <Table.Body />
                </Table.Content>
              </Table.ScrollContainer>
            </Table>
          ) : (
            <div className="flex flex-col gap-4">
              {groups.map((g) => (
                <ProviderQuota
                  key={g.provider}
                  provider={g}
                  rows={g.accounts}
                  now={now}
                  pending={busy || refresh.isPending}
                  onRefresh={onRefresh}
                />
              ))}
            </div>
          )}
        </DataList.State>
        <DataList.Pagination
          page={page}
          pageSize={pageSize}
          total={quota.data?.total ?? 0}
          onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
          onPageSizeChange={(n) => update({ pageSize: PAGE_SIZES.find((size) => size === n) ?? PAGE_SIZES[0] })}
        />
      </DataList.Root>
    </div>
  );
}
