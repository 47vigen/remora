import { Plus } from "@gravity-ui/icons";
import { buttonVariants, Label, Link, ListBox, Select, Switch } from "@heroui/react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo } from "react";

import {
  getAliasChainOptions,
  listAliasesOptions,
  listCoolingOptions,
} from "../../../client/@tanstack/react-query.gen";
import { DataList } from "../../../components/data-list/DataList";
import { type ListSearch, parseEnum, parseListSearch } from "../../../lib/list-search";
import { AliasesTable } from "./-components/AliasesTable";
import { ALIAS_KINDS, type AliasDraft } from "./-lib/alias-form";

type Kind = AliasDraft["kind"];
type AliasesSearch = ListSearch & { kind?: Kind; cooling?: boolean };

/** The API caps a page at 200; the cooling filter has no server side, so it filters one 200-row page here. */
const MAX_LIMIT = 200;

export const Route = createFileRoute("/_app/aliases/")({
  validateSearch: (raw: Record<string, unknown>): AliasesSearch => ({
    ...parseListSearch(raw),
    kind: parseEnum(raw.kind, ALIAS_KINDS),
    cooling: raw.cooling === true || raw.cooling === "true" ? true : undefined,
  }),
  component: AliasesPage,
});

function AliasesPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { page, pageSize, q, kind, cooling } = search;
  const update = (next: Partial<AliasesSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...next }), replace: true });

  const coolingList = useQuery(listCoolingOptions({ query: { limit: MAX_LIMIT } }));
  const coolingByAlias = useMemo(() => {
    const counts = new Map<string, number>();
    for (const c of coolingList.data?.items ?? []) for (const a of c.aliases) counts.set(a, (counts.get(a) ?? 0) + 1);
    return counts;
  }, [coolingList.data]);

  const list = useQuery(
    listAliasesOptions({
      query: cooling
        ? { limit: MAX_LIMIT, q: q || undefined, kind }
        : { limit: pageSize, offset: (page - 1) * pageSize, q: q || undefined, kind },
    }),
  );
  const filtered = cooling ? (list.data?.items ?? []).filter((a) => coolingByAlias.has(a.name)) : undefined;
  const items = filtered ? filtered.slice((page - 1) * pageSize, page * pageSize) : (list.data?.items ?? []);
  const total = filtered ? filtered.length : (list.data?.total ?? 0);

  const chains = useQueries({
    queries: items.map((a) => ({ ...getAliasChainOptions({ path: { name: a.name } }), staleTime: 30_000 })),
  });
  const chainLength = new Map<string, number>();
  items.forEach((a, i) => {
    const data = chains[i]?.data;
    if (data) chainLength.set(a.name, data.targets.length);
  });

  return (
    <DataList.Root>
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold">Aliases</h1>
        <Link href="/aliases/new" className={buttonVariants({ variant: "primary" })}>
          <Plus aria-hidden="true" />
          New alias
        </Link>
      </div>
      <DataList.Toolbar>
        <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search aliases" />
        <DataList.Filters>
          <Select
            className="w-44"
            value={kind ?? "all"}
            onChange={(key) => update({ kind: parseEnum(key, ALIAS_KINDS) })}
          >
            <Label>Kind</Label>
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox aria-label="Kind">
                {["all", ...ALIAS_KINDS].map((k) => (
                  <ListBox.Item key={k} id={k} textValue={k === "all" ? "All kinds" : k}>
                    {k === "all" ? "All kinds" : k}
                    <ListBox.ItemIndicator />
                  </ListBox.Item>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
          <Switch isSelected={cooling === true} onChange={(on) => update({ cooling: on ? true : undefined })}>
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Label>Cooling only</Label>
            </Switch.Content>
          </Switch>
        </DataList.Filters>
      </DataList.Toolbar>
      <DataList.State
        isLoading={list.isPending}
        error={list.error ?? undefined}
        isEmpty={items.length === 0}
        emptyText={q || kind || cooling ? "No aliases match these filters." : "No aliases yet."}
      >
        <AliasesTable items={items} chainLength={chainLength} cooling={coolingByAlias} />
      </DataList.State>
      <DataList.Pagination
        page={page}
        pageSize={pageSize}
        total={total}
        onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
        onPageSizeChange={(n) => update({ pageSize: n as ListSearch["pageSize"] })}
      />
    </DataList.Root>
  );
}
