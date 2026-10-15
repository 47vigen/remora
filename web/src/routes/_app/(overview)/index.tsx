import { ArrowDownToLine } from "@gravity-ui/icons";
import { Alert, Button, Skeleton, ToggleButton, ToggleButtonGroup } from "@heroui/react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import {
  getStatsOptions,
  listCoolingOptions,
  listProvidersOptions,
  testProviderMutation,
} from "../../../client/@tanstack/react-query.gen";
import { listProviders } from "../../../client/sdk.gen";
import type { Stats } from "../../../client/types.gen";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { errorMessage } from "../../../lib/api";
import { buildCSV, downloadFile } from "../../../lib/download";
import { parseEnum } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { Breakdown } from "./-components/Breakdown";
import { CostChart } from "./-components/charts/CostChart";
import { LatencyChart } from "./-components/charts/LatencyChart";
import { TokenChart } from "./-components/charts/TokenChart";
import { VolumeChart } from "./-components/charts/VolumeChart";
import { COOLING_LIMIT, CoolingList } from "./-components/CoolingList";
import { HealthSummary } from "./-components/HealthSummary";
import { Sparkline } from "./-components/Sparkline";
import { StatCards } from "./-components/StatCards";
import { overviewCostToCSV, overviewLatencyToCSV, overviewTokensToCSV, overviewVolumeToCSV } from "./-lib/export";
import { breakdownRows, healthCounts, seriesRows, summarize, volumeRows } from "./-lib/overview";

const RANGES = ["1h", "24h", "7d"] as const;
type Range = (typeof RANGES)[number];
const RANGE_MS: Record<Range, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000 };
const RANGE_HINT: Record<Range, string> = { "1h": "last hour", "24h": "last 24 hours", "7d": "last 7 days" };

export const Route = createFileRoute("/_app/(overview)/")({
  validateSearch: (raw: Record<string, unknown>): { range?: Range } => ({
    range: parseEnum(raw.range, RANGES),
  }),
  component: OverviewPage,
});

const STATS_REFRESH_MS = 60_000;

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
  const from = range === "24h" ? undefined : new Date(now - RANGE_MS[range]).toISOString();

  const stats = useQuery({
    ...getStatsOptions({ query: { group_by: "provider", from } }),
    refetchInterval: STATS_REFRESH_MS,
    placeholderData: keepPreviousData,
  });
  const byModel = useQuery({
    ...getStatsOptions({ query: { group_by: "model", from } }),
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

      {stats.error ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>Could not load the statistics</Alert.Title>
            <Alert.Description>{errorMessage(stats.error)}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      {summary ? (
        <StatCards>
          <StatCards.Item
            label="Requests"
            value={summary.requests}
            trend={<Sparkline values={series.map((p) => p.requests)} />}
          />
          <StatCards.Item
            label="Error rate"
            value={summary.errorRate}
            trend={<Sparkline values={series.map((p) => p.errors)} />}
          />
          <StatCards.Item label="p50 latency" value={summary.p50} hint="half of requests answer faster" />
          <StatCards.Item label="p95 latency" value={summary.p95} hint="the slow tail" />
          <StatCards.Item label="Tokens" value={summary.tokens} hint={RANGE_HINT[range]} />
          <StatCards.Item label="Cost" value={summary.cost} hint={RANGE_HINT[range]} />
        </StatCards>
      ) : stats.isLoading ? (
        <Skeleton className="h-28 w-full" />
      ) : null}

      <div className="grid gap-4 lg:grid-cols-3">
        {stats.data ? (
          <VolumeChart rows={volumeRows(series)} total={summary?.requests} className="lg:col-span-2" />
        ) : (
          <Skeleton className="h-72 lg:col-span-2" />
        )}
        {providers.data ? (
          <HealthSummary
            counts={healthCounts(providers.data.items)}
            action={
              providers.data.items.length > 0 ? (
                <Button size="sm" variant="secondary" isPending={testAll.isPending} onPress={() => testAll.mutate()}>
                  Test all
                </Button>
              ) : null
            }
          />
        ) : (
          <Skeleton className="h-72" />
        )}
      </div>

      {stats.data ? (
        <div className="grid gap-4 lg:grid-cols-3">
          <TokenChart rows={overTime} total={summary?.tokens} />
          <CostChart rows={overTime} total={summary?.cost} />
          <LatencyChart rows={overTime} total={summary?.p95 === "—" ? undefined : `p95 ${summary?.p95}`} />
        </div>
      ) : null}

      {stats.data ? (
        <Breakdown
          models={byModel.data ? breakdownRows(byModel.data.groups) : undefined}
          modelsError={byModel.error && !byModel.data ? errorMessage(byModel.error) : undefined}
          providers={breakdownRows(stats.data.groups)}
        />
      ) : null}

      {cooling.data ? <CoolingList items={cooling.data.items} total={cooling.data.total} /> : null}
    </div>
  );
}
