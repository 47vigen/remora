import { ArrowDownToLine } from "@gravity-ui/icons";
import { Alert, Button, Spinner } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { getStatsOptions, listCoolingOptions, listProvidersOptions } from "../../../client/@tanstack/react-query.gen";
import type { Stats } from "../../../client/types.gen";
import { errorMessage } from "../../../lib/api";
import { buildCSV, downloadFile } from "../../../lib/download";
import { CostChart } from "./-components/charts/CostChart";
import { LatencyChart } from "./-components/charts/LatencyChart";
import { TokenChart } from "./-components/charts/TokenChart";
import { VolumeChart } from "./-components/charts/VolumeChart";
import { COOLING_LIMIT, CoolingList } from "./-components/CoolingList";
import { HealthSummary } from "./-components/HealthSummary";
import { StatCards } from "./-components/StatCards";
import { overviewCostToCSV, overviewLatencyToCSV, overviewTokensToCSV, overviewVolumeToCSV } from "./-lib/export";
import { healthCounts, providerRows, summarize, volumeRows } from "./-lib/overview";

export const Route = createFileRoute("/_app/(overview)/")({
  component: OverviewPage,
});

const STATS_REFRESH_MS = 60_000;
const WINDOW_HINT = "last 24 hours";

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

function OverviewPage() {
  // No from/to: the server's default window is the last 24 hours ending now, so each refetch moves with the clock.
  const stats = useQuery({
    ...getStatsOptions({ query: { group_by: "provider" } }),
    refetchInterval: STATS_REFRESH_MS,
  });
  const providers = useQuery(listProvidersOptions({ query: { limit: 100 } }));
  const cooling = useQuery(listCoolingOptions({ query: { limit: COOLING_LIMIT } }));

  const summary = stats.data ? summarize(stats.data.summary) : undefined;
  const byProvider = stats.data ? providerRows(stats.data.groups) : [];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-xl font-semibold">Overview</h1>
        <Button variant="secondary" isDisabled={!stats.data} onPress={() => stats.data && exportOverview(stats.data)}>
          <ArrowDownToLine aria-hidden="true" />
          Export CSV
        </Button>
      </div>

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
          <StatCards.Item label="Requests" value={summary.requests} hint={WINDOW_HINT} />
          <StatCards.Item label="Error rate" value={summary.errorRate} />
          <StatCards.Item label="p50" value={summary.p50} />
          <StatCards.Item label="p95" value={summary.p95} />
          <StatCards.Item label="Tokens" value={summary.tokens} />
          <StatCards.Item label="Cost" value={summary.cost} />
        </StatCards>
      ) : stats.isLoading ? (
        <div className="flex justify-center py-6">
          <Spinner />
        </div>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        {providers.data ? <HealthSummary counts={healthCounts(providers.data.items)} /> : null}
        {cooling.data ? <CoolingList items={cooling.data.items} total={cooling.data.total} /> : null}
      </div>

      {stats.data ? (
        <div className="grid gap-4 lg:grid-cols-2">
          <VolumeChart rows={volumeRows(stats.data.series)} />
          <TokenChart rows={byProvider} />
          <CostChart rows={byProvider} />
          <LatencyChart rows={byProvider} />
        </div>
      ) : null}
    </div>
  );
}
