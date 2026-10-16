import { Alert } from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";

import { getQuotaOptions, refreshQuotaMutation } from "../../../client/@tanstack/react-query.gen";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { ProviderQuota, type RefreshTarget } from "./-components/ProviderQuota";
import { quotaRows, refreshSettled } from "./-lib/quota";

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
  const quota = useQuery({
    ...getQuotaOptions(),
    refetchInterval: (query) => {
      const waiting =
        pending !== null &&
        Date.now() - pending.since.getTime() < REFRESH_WAIT_MS &&
        !refreshSettled(query.state.data ?? [], pending.queued, pending.since);
      return waiting ? FAST_POLL_MS : SLOW_POLL_MS;
    },
  });
  const providers = quota.data ?? [];
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
  const busy = pending !== null && !refreshSettled(providers, pending.queued, pending.since);

  const rows = quotaRows(providers, q, provider);
  const shown = rows.slice((page - 1) * pageSize, page * pageSize);
  const groups = providers
    .map((p) => ({ provider: p, rows: shown.filter((r) => r.provider === p.provider).map((r) => r.item) }))
    .filter((g) => g.rows.length > 0);

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
              options={providers.map((p) => ({ id: p.provider, label: p.provider }))}
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
          emptyText={q || provider ? "No accounts match these filters." : "No subscription accounts yet."}
        >
          <div className="flex flex-col gap-4">
            {groups.map((g) => (
              <ProviderQuota
                key={g.provider.provider}
                provider={g.provider}
                rows={g.rows}
                now={now}
                pending={busy || refresh.isPending}
                onRefresh={onRefresh}
              />
            ))}
          </div>
        </DataList.State>
        <DataList.Pagination
          page={page}
          pageSize={pageSize}
          total={rows.length}
          onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
          onPageSizeChange={(n) => update({ pageSize: n as ListSearch["pageSize"] })}
        />
      </DataList.Root>
    </div>
  );
}
