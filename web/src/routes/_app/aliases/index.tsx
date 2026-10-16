import { Plus } from "@gravity-ui/icons";
import { buttonVariants, cn, Link } from "@heroui/react";
import { useMutation, useQueries, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState } from "react";

import {
  deleteAliasMutation,
  getAliasChainOptions,
  listAliasesOptions,
  listCoolingOptions,
} from "../../../client/@tanstack/react-query.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, parseEnum, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
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
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [deleting, setDeleting] = useState<string>();
  const remove = useMutation({
    ...deleteAliasMutation(),
    onSuccess: async () => {
      setDeleting(undefined);
      await invalidateOps(queryClient, "listAliases", "getAlias", "getAliasChain", "listCooling");
    },
  });
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

  const newAlias = (
    <Link
      href="/aliases/new"
      className={cn(buttonVariants({ variant: "primary", size: "sm" }), "gap-1.5 no-underline")}
    >
      <Plus aria-hidden="true" className="size-4" />
      New alias
    </Link>
  );
  const filtering = Boolean(q || kind || cooling);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Aliases"
        description="A named model that clients call. Its strategy picks the first member, and requests fall back past any that fail or are cooling."
        actions={newAlias}
      />
      <DataList.Root>
        <DataList.Toolbar>
          <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search aliases" />
          <DataList.Filters>
            <DataList.Filter
              label="Kind"
              value={kind}
              options={ALIAS_KINDS.map((k) => ({ id: k, label: k }))}
              onChange={(k) => update({ kind: k })}
            />
            <DataList.Filter
              label="Status"
              allLabel="Any"
              value={cooling ? "cooling" : undefined}
              options={[{ id: "cooling", label: "Cooling" }]}
              onChange={(v) => update({ cooling: v ? true : undefined })}
            />
          </DataList.Filters>
        </DataList.Toolbar>
        <DataList.State
          isLoading={list.isPending}
          error={list.error ?? undefined}
          isEmpty={items.length === 0}
          emptyText={
            filtering
              ? "No aliases match these filters. Clear them to see every alias."
              : "No aliases yet. Create one to give clients a stable model name with fallbacks."
          }
          emptyAction={filtering ? undefined : newAlias}
        >
          <AliasesTable
            items={items}
            chainLength={chainLength}
            cooling={coolingByAlias}
            onOpen={(name) => void navigate({ to: "/aliases/$name", params: { name } })}
            onDelete={setDeleting}
          />
        </DataList.State>
        <DataList.Pagination
          page={page}
          pageSize={pageSize}
          total={total}
          onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
          onPageSizeChange={(n) => update({ pageSize: n as ListSearch["pageSize"] })}
        />
      </DataList.Root>
      <ConfirmDelete
        title={`Delete ${deleting ?? ""}?`}
        isOpen={deleting !== undefined}
        isPending={remove.isPending}
        onCancel={() => {
          setDeleting(undefined);
          remove.reset();
        }}
        onConfirm={() => deleting && remove.mutate({ path: { name: deleting } })}
        body={
          remove.error ? (
            <p role="alert" className="text-danger">
              {errorMessage(remove.error)}
            </p>
          ) : (
            <p>Requests for this alias will fail. Keys and aliases that use it must be changed first.</p>
          )
        }
      />
    </div>
  );
}
