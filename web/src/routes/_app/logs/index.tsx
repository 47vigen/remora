import { Chip, Label, Switch } from "@heroui/react";
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
import type { ListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { LogDetail } from "./-components/LogDetail";
import { LogFilters } from "./-components/LogFilters";
import { LogsTable } from "./-components/LogsTable";
import { useLiveLogs } from "./-hooks/useLiveLogs";
import {
  type LogSearch,
  type LogSummary,
  matchesLogFilters,
  parseLogSearch,
  prependLive,
  rangeToWindow,
  toLogSummary,
} from "./-lib/filters";

/** The API caps a page at 200; filter pickers offer what fits in one page. */
const MAX_LIMIT = 200;

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
  const list = useQuery(
    listLogsOptions({
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
    }),
  );
  const pageRows = useMemo(() => (list.data?.items ?? []).map((e) => toLogSummary(e, keyNames)), [list.data, keyNames]);

  // Live mode starts from page 1 and prepends what arrives, within the same filters.
  const [liveRows, setLiveRows] = useState<LogSummary[] | null>(null);
  // New filters restart live mode from the new first page; rows that matched the old filters must not linger.
  const [liveFilters, setLiveFilters] = useState(filterKey);
  if (liveFilters !== filterKey) {
    setLiveFilters(filterKey);
    setLiveRows(null);
  }
  if (live && liveRows === null && list.data && !list.isPlaceholderData && liveFilters === filterKey)
    setLiveRows(pageRows);
  if (!live && liveRows !== null) setLiveRows(null);
  const { isConnected } = useLiveLogs(live === true, (l) => {
    const row = l.keyId !== undefined ? { ...l, keyName: keyNames.get(l.keyId) } : l;
    if (matchesLogFilters(row, search)) setLiveRows((rows) => prependLive(rows ?? [], row, pageSize));
  });
  const rows = live ? (liveRows ?? pageRows) : pageRows;

  const detail = useQuery({ ...getLogOptions({ path: { id: search.log ?? "" } }), enabled: search.log !== undefined });

  return (
    <DataList.Root>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold">Logs</h1>
        <div className="flex items-center gap-3">
          {live ? (
            <Chip size="sm" variant="soft" color={isConnected ? "success" : "warning"}>
              <Chip.Label>{isConnected ? "Connected" : "Reconnecting…"}</Chip.Label>
            </Chip>
          ) : null}
          <Switch
            isSelected={live === true}
            onChange={(on) => {
              if (!on) void invalidateOps(queryClient, "listLogs");
              update({ live: on ? true : undefined });
            }}
          >
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Label>Live</Label>
            </Switch.Content>
          </Switch>
        </div>
      </div>
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
          now={clock.now}
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
