import { cn, ToggleButton } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState } from "react";

import {
  getLogOptions,
  listAccountsOptions,
  listAliasesOptions,
  listKeysOptions,
  listLogsOptions,
  listProvidersOptions,
} from "../../../client/@tanstack/react-query.gen";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader } from "../../../components/page-header/PageHeader";
import type { ListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { useNow } from "../../../lib/use-now";
import { LogDetail } from "./-components/LogDetail";
import { LogFilters } from "./-components/LogFilters";
import { LogsTable } from "./-components/LogsTable";
import { useLiveLogs } from "./-hooks/useLiveLogs";
import {
  type LogSearch,
  type LiveState,
  type LogSummary,
  matchesLogFilters,
  mergeCatchUp,
  parseLogSearch,
  prependLive,
  rangeToWindow,
  toLogSummary,
} from "./-lib/filters";

/** The API caps a page at 200; filter pickers offer what fits in one page. */
const MAX_LIMIT = 200;

/** Live mode keeps this many rows at most, so a busy stream stays cheap to render. */
const LIVE_ROWS = 50;
/** How often the relative times in the table move on. */
const CLOCK_MS = 15_000;

export const Route = createFileRoute("/_app/logs/")({
  validateSearch: parseLogSearch,
  component: LogsPage,
});

function LogsPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const { page, pageSize, q, live } = search;
  const update = (next: Partial<LogSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...next }), replace: true });

  // The window is fixed for one set of filters, so the query key stays stable until a filter changes.
  const filterKey = JSON.stringify({ ...search, log: undefined, live: undefined, page: undefined });
  const [clock, setClock] = useState(() => ({ key: filterKey, now: new Date() }));
  // Reading the clock once per filter change is the point here.
  // oxlint-disable-next-line react/purity
  if (clock.key !== filterKey) setClock({ key: filterKey, now: new Date() });
  const span = rangeToWindow(search, clock.now);
  const now = useNow(CLOCK_MS);

  const [keyQuery, setKeyQuery] = useState("");
  const keys = useQuery(listKeysOptions({ query: { limit: MAX_LIMIT } }));
  const keySearch = useQuery({
    ...listKeysOptions({ query: { limit: MAX_LIMIT, q: keyQuery } }),
    enabled: keyQuery !== "",
  });
  const aliases = useQuery(listAliasesOptions({ query: { limit: MAX_LIMIT } }));
  const providers = useQuery(listProvidersOptions({ query: { limit: MAX_LIMIT } }));
  const accounts = useQuery(listAccountsOptions({ query: { limit: MAX_LIMIT } }));

  const keyNames = useMemo(() => new Map((keys.data?.items ?? []).map((k) => [k.id, k.name])), [keys.data]);
  const keyOptions = ((keyQuery !== "" ? keySearch.data : keys.data)?.items ?? []).map((k) => ({
    id: k.id,
    label: k.name,
  }));
  const providerNames = useMemo(
    () => [
      ...new Set([
        ...(providers.data?.items ?? []).map((p) => p.name),
        ...(accounts.data?.items ?? []).map((a) => a.provider),
      ]),
    ],
    [providers.data, accounts.data],
  );

  const effectivePage = live ? 1 : page;
  const listOptions = listLogsOptions({
    query: {
      limit: pageSize,
      offset: (effectivePage - 1) * pageSize,
      q: q || undefined,
      key: search.keyId,
      alias: search.alias,
      provider: search.provider,
      model: search.servedModel,
      status: search.status,
      ...span,
    },
  });
  const list = useQuery(listOptions);
  const pageRows = useMemo(() => (list.data?.items ?? []).map((e) => toLogSummary(e, keyNames)), [list.data, keyNames]);

  // Live mode starts from page 1 and prepends what arrives, within the same filters. The rows carry the filters they
  // belong to, so a read that finishes after the filters changed cannot add its page to the new rows.
  const liveCap = Math.min(pageSize, LIVE_ROWS);
  const [liveState, setLive] = useState<LiveState | null>(null);
  const liveRows = liveState?.key === filterKey ? liveState.rows : null;
  const edit = (key: string, change: (rows: LogSummary[]) => LogSummary[]) =>
    setLive((s) => (s === null ? { key, rows: change([]) } : s.key === key ? { key, rows: change(s.rows) } : s));
  // New filters restart live mode from the new first page; rows that matched the old filters must not linger.
  if (liveState !== null && liveState.key !== filterKey) setLive(null);
  if (live && liveState === null && list.data && !list.isPlaceholderData)
    setLive({ key: filterKey, rows: pageRows.slice(0, liveCap) });
  if (!live && liveState !== null) setLive(null);
  // The page Live starts from may be old and the stream only carries what completes after it connects, so each
  // connection reads page 1 again and merges in the rows the stream did not bring.
  const { isConnected } = useLiveLogs(
    live === true,
    (batch) => {
      const fresh = batch
        .map((l) => (l.keyId !== undefined ? { ...l, keyName: keyNames.get(l.keyId) } : l))
        .filter((l) => matchesLogFilters(l, search));
      if (fresh.length > 0) edit(filterKey, (rows) => prependLive(rows, fresh, liveCap));
    },
    async () => {
      const key = filterKey;
      try {
        const data = await queryClient.fetchQuery({ ...listOptions, staleTime: 0 });
        const page = data.items.map((e) => toLogSummary(e, keyNames));
        setLive((s) => mergeCatchUp(s, key, page, liveCap));
      } catch {
        // Filters changed under the read, or it failed: the stream carries on and the next connection reads again.
      }
    },
  );
  const rows = live ? (liveRows ?? pageRows) : pageRows;

  const detail = useQuery({ ...getLogOptions({ path: { id: search.log ?? "" } }), enabled: search.log !== undefined });

  return (
    <DataList.Root>
      <PageHeader
        title="Logs"
        actions={
          <>
            {live ? (
              <output className="text-muted font-mono text-xs">{isConnected ? "Connected" : "Reconnecting…"}</output>
            ) : null}
            <ToggleButton
              size="sm"
              isSelected={live === true}
              onChange={(on) => {
                if (!on) void invalidateOps(queryClient, "listLogs");
                update({ live: on ? true : undefined });
              }}
            >
              <span
                aria-hidden="true"
                className={cn(
                  "size-1.5 rounded-full",
                  !live ? "bg-muted" : isConnected ? "bg-accent motion-safe:animate-pulse" : "bg-warning",
                )}
              />
              Live
            </ToggleButton>
          </>
        }
      />
      <DataList.Toolbar>
        <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search logs" />
        <DataList.Filters>
          <LogFilters
            value={search}
            keys={keyOptions}
            aliases={(aliases.data?.items ?? []).map((a) => a.name)}
            providers={providerNames}
            onKeyQuery={setKeyQuery}
            onChange={update}
          />
        </DataList.Filters>
      </DataList.Toolbar>
      <DataList.State
        isLoading={list.isPending}
        error={list.error ?? undefined}
        isEmpty={rows.length === 0}
        emptyText={live ? "Waiting for requests…" : "No requests in this range."}
      >
        <LogsTable
          items={rows}
          now={now}
          highlightNew={live === true}
          onOpen={(id) => void navigate({ search: (prev) => ({ ...prev, log: id }) })}
        />
      </DataList.State>
      {live ? null : (
        <DataList.Pagination
          page={page}
          pageSize={pageSize}
          total={list.data?.total ?? 0}
          onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
          onPageSizeChange={(n) => update({ pageSize: n as ListSearch["pageSize"] })}
        />
      )}
      {search.log !== undefined ? (
        <LogDetail
          log={detail.data}
          keyName={detail.data?.key !== undefined ? keyNames.get(detail.data.key) : undefined}
          isLoading={detail.isPending}
          onClose={() => void navigate({ search: (prev) => ({ ...prev, log: undefined }) })}
        />
      ) : null}
    </DataList.Root>
  );
}
