import { ArrowDownToLine } from "@gravity-ui/icons";
import { Button, Skeleton, ToggleButton, ToggleButtonGroup } from "@heroui/react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import {
  getAccountPoolOptions,
  getQuotaOptions,
  getStatsOptions,
  listCoolingOptions,
  listProvidersOptions,
  testProviderMutation,
} from "../../../client/@tanstack/react-query.gen";
import { getQuota, listProviders } from "../../../client/sdk.gen";
import type { QuotaProvider, Stats } from "../../../client/types.gen";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { errorMessage } from "../../../lib/api";
import { buildCSV, downloadFile } from "../../../lib/download";
import { PAGE_SIZES, parseEnum } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { Attention, COOLING_LIMIT } from "./-components/Attention";
import { Breakdown } from "./-components/Breakdown";
import { CostChart } from "./-components/charts/CostChart";
import { LatencyChart } from "./-components/charts/LatencyChart";
import { TokenChart } from "./-components/charts/TokenChart";
import { VolumeChart } from "./-components/charts/VolumeChart";
import { Panel } from "./-components/Panel";
import { Sparkline } from "./-components/Sparkline";
import { StatCards } from "./-components/StatCards";
import { Subscriptions } from "./-components/Subscriptions";
import { overviewCostToCSV, overviewLatencyToCSV, overviewTokensToCSV, overviewVolumeToCSV } from "./-lib/export";
import { breakdownRows, healthCounts, seriesRows, summarize, volumeRows } from "./-lib/overview";

const RANGES = ["1h", "24h", "7d"] as const;
type Range = (typeof RANGES)[number];
const RANGE_MS: Record<Range, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000 };
const RANGE_HINT: Record<Range, string> = { "1h": "last hour", "24h": "last 24 hours", "7d": "last 7 days" };

export function statsFrom(range: Range, now: number): string | undefined {
  if (range === "24h") return undefined;
  const bucket = range === "1h" ? 60_000 : 86_400_000;
  return new Date(Math.floor((now - RANGE_MS[range]) / bucket) * bucket).toISOString();
}

export const Route = createFileRoute("/_app/(overview)/")({
  validateSearch: (raw: Record<string, unknown>): { range?: Range } => ({
    range: parseEnum(raw.range, RANGES),
  }),
  component: OverviewPage,
});

const STATS_REFRESH_MS = 60_000;
const POOL_REFRESH_MS = 30_000;

function exportOverview(stats: Stats) {
  const sections = [
    ["Requests", overviewVolumeToCSV(stats.series)],
    ["Tokens", overviewTokensToCSV(stats.groups)],
    ["Cost", overviewCostToCSV(stats.groups)],
    ["Latency", overviewLatencyToCSV(stats.groups)],
  ] as const;
  const content = sections.map(([name, csv]) => `${name}\n${buildCSV(csv.headers, csv.rows)}`).join("\n\n");
  downloadFile(content, "remora-overview.csv", "text/csv");
}

/** The current minute, ticking once a minute, so a window that ends "now" moves with the clock. */
function useNow() {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), STATS_REFRESH_MS);
    return () => clearInterval(t);
  }, []);
  return now;
}

function OverviewPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const range: Range = search.range ?? "24h";
  const now = useNow();
  // The default window is the server's own (the last 24 hours ending now), so its refetch moves with the clock.
  const from = statsFrom(range, now);

  const stats = useQuery({
    ...getStatsOptions({ query: { group_by: "provider", from } }),
    refetchInterval: STATS_REFRESH_MS,
    placeholderData: keepPreviousData,
  });
  const byModel = useQuery({
    ...getStatsOptions({ query: { group_by: "model", groups_only: true, from } }),
    refetchInterval: STATS_REFRESH_MS,
    placeholderData: keepPreviousData,
  });
  const providers = useQuery(listProvidersOptions({ query: { limit: 100 } }));
  const qc = useQueryClient();
  const test = useMutation(testProviderMutation());
  const testAll = useMutation({
    // One at a time: a burst of probes against every upstream at once helps nobody.
    mutationFn: async () => {
      // The overview loads one page of providers; testing all of them means walking every page.
      const names: string[] = [];
      for (let offset = 0; ;) {
        const { data } = await listProviders({ query: { limit: 100, offset }, throwOnError: true });
        names.push(...data.items.map((p) => p.name));
        offset += data.items.length;
        if (data.items.length === 0 || offset >= data.total) break;
      }
      for (const name of names) await test.mutateAsync({ path: { name } }).catch(() => undefined);
    },
    onSettled: () => invalidateOps(qc, "listProviders", "getProvider"),
  });
  const cooling = useQuery(listCoolingOptions({ query: { limit: COOLING_LIMIT } }));
  const pool = useQuery({ ...getAccountPoolOptions(), refetchInterval: POOL_REFRESH_MS });
  const quota = useQuery({
    queryKey: getQuotaOptions().queryKey,
    refetchInterval: STATS_REFRESH_MS,
    queryFn: async ({ signal }) => {
      const providers = new Map<string, QuotaProvider>();
      for (let offset = 0; ;) {
        const { data } = await getQuota({ query: { limit: PAGE_SIZES[2], offset }, signal, throwOnError: true });
        let count = 0;
        for (const group of data.items) {
          const prev = providers.get(group.provider);
          if (prev) prev.accounts.push(...group.accounts);
          else providers.set(group.provider, group);
          count += group.accounts.length;
        }
        offset += count;
        if (count === 0 || offset >= data.total) break;
      }
      return [...providers.values()];
    },
  });

  const statsError = stats.error ? errorMessage(stats.error) : undefined;
  const summary = stats.data ? summarize(stats.data.summary) : undefined;
  const series = stats.data?.series ?? [];
  const overTime = seriesRows(series);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Overview"
        description={`Traffic through this gateway over the ${RANGE_HINT[range]}.`}
        actions={
          <>
            <ToggleButtonGroup
              aria-label="Time range"
              selectionMode="single"
              disallowEmptySelection
              selectedKeys={[range]}
              onSelectionChange={(keys) => {
                const next = parseEnum([...keys][0], RANGES);
                if (next) void navigate({ search: { range: next === "24h" ? undefined : next }, replace: true });
              }}
              size="sm"
            >
              {RANGES.map((r) => (
                <ToggleButton key={r} id={r} className="font-mono text-xs">
                  {r}
                </ToggleButton>
              ))}
            </ToggleButtonGroup>
            <Button
              size="sm"
              variant="secondary"
              isDisabled={!stats.data}
              onPress={() => stats.data && exportOverview(stats.data)}
            >
              <ArrowDownToLine aria-hidden="true" />
              Export CSV
            </Button>
          </>
        }
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-12">
        {stats.error ? (
          <StatCards className="lg:col-span-12" error={stats.error} />
        ) : summary ? (
          <StatCards className="lg:col-span-12">
            <StatCards.Item
              label="Requests"
              value={summary.requests}
              trend={<Sparkline values={series.map((p) => p.requests)} />}
            />
            <StatCards.Item
              label="Error rate"
              value={summary.errorRate}
              trend={<Sparkline values={series.map((p) => (p.requests > 0 ? p.errors / p.requests : 0))} />}
            />
            <StatCards.Item
              label="p95 latency"
              value={summary.p95}
              hint={`p50 ${summary.p50}`}
              trend={<Sparkline values={series.map((p) => p.latency_p95_ms ?? 0)} />}
            />
            <StatCards.Item
              label="Tokens"
              value={summary.tokens}
              trend={<Sparkline values={series.map((p) => p.total_tokens)} />}
            />
            <StatCards.Item
              label="Cost"
              value={summary.cost}
              trend={<Sparkline values={series.map((p) => p.cost_usd)} />}
            />
          </StatCards>
        ) : stats.isLoading ? (
          <Skeleton className="h-28 w-full lg:col-span-12" />
        ) : null}

        {stats.data || statsError ? (
          <VolumeChart
            rows={volumeRows(series)}
            total={summary?.requests}
            className="lg:col-span-8"
            error={statsError}
          />
        ) : (
          <Skeleton className="h-80 lg:col-span-8" />
        )}
        {quota.data ? (
          <Subscriptions providers={quota.data} now={new Date(now)} className="lg:col-span-4" />
        ) : quota.error ? (
          <Panel title="Subscriptions" className="lg:col-span-4">
            <p className="text-danger p-4 text-sm">Could not load the quota: {errorMessage(quota.error)}</p>
          </Panel>
        ) : (
          <Skeleton className="h-80 lg:col-span-4" />
        )}

        {stats.data || statsError ? (
          <>
            <TokenChart rows={overTime} total={summary?.tokens} className="lg:col-span-4" error={statsError} />
            <CostChart rows={overTime} total={summary?.cost} className="lg:col-span-4" error={statsError} />
            <LatencyChart rows={overTime} className="lg:col-span-4" error={statsError} />
          </>
        ) : null}

        {stats.data ? (
          <Breakdown
            className="lg:col-span-8"
            models={byModel.data ? breakdownRows(byModel.data.groups) : undefined}
            modelsError={byModel.error && !byModel.data ? errorMessage(byModel.error) : undefined}
            providers={breakdownRows(stats.data.groups)}
          />
        ) : !statsError ? (
          <Skeleton className="h-72 lg:col-span-8" />
        ) : null}
        {providers.data ? (
          <Attention
            className="lg:col-span-4"
            health={healthCounts(providers.data.items, stats.isPlaceholderData ? undefined : stats.data?.groups)}
            pool={pool.data ?? []}
            quota={quota.data ?? []}
            quotaError={quota.error && !quota.data ? errorMessage(quota.error) : undefined}
            cooling={cooling.data?.items ?? []}
            coolingTotal={cooling.data?.total ?? 0}
            now={new Date(now)}
            action={
              providers.data.items.length > 0 ? (
                <Button size="sm" variant="secondary" isPending={testAll.isPending} onPress={() => testAll.mutate()}>
                  Test all
                </Button>
              ) : null
            }
          />
        ) : (
          <Skeleton className="h-72 lg:col-span-4" />
        )}
      </div>
    </div>
  );
}
