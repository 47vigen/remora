import { Plus } from "@gravity-ui/icons";
import { Alert, Button, toast } from "@heroui/react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, stripSearchParams, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import {
  deleteProviderMutation,
  getProviderOptions,
  listAccountsOptions,
  listProvidersOptions,
  testProviderMutation,
} from "../../../client/@tanstack/react-query.gen";
import type { Provider } from "../../../client/types.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { STATUS_LABELS } from "../../../components/status-chip/StatusChip";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, PAGE_SIZES, parseEnum, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { ProviderCards } from "./-components/ProviderCards";
import { ProviderEditorDrawer } from "./-components/ProviderEditorDrawer";
import { DISPLAY_GROUPS, type DisplayGroup, GROUP_LABELS, PROVIDER_STATUSES } from "./-lib/provider-form";
import { providerKinds, subscriptionRows } from "./-lib/providers-list";

type ProvidersSearch = ListSearch & { group?: DisplayGroup; status?: Provider["status"]; edit?: string };

export const Route = createFileRoute("/_app/providers/")({
  validateSearch: (raw: Record<string, unknown>): ProvidersSearch => ({
    ...parseListSearch(raw),
    group: parseEnum(raw.group, DISPLAY_GROUPS),
    status: parseEnum(raw.status, PROVIDER_STATUSES),
    edit: typeof raw.edit === "string" && raw.edit !== "" ? raw.edit : undefined,
  }),
  search: { middlewares: [stripSearchParams({ page: 1, pageSize: 25, q: "" })] },
  component: ProvidersPage,
});

function ProvidersPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const qc = useQueryClient();
  const [deleting, setDeleting] = useState<Provider>();
  const [testing, setTesting] = useState<string>();

  const providers = useQuery({
    ...listProvidersOptions({
      query: {
        limit: search.pageSize,
        offset: (search.page - 1) * search.pageSize,
        q: search.q || undefined,
        kinds: providerKinds(search.group),
        status: search.status,
      },
    }),
    enabled: search.group !== "subscription",
  });
  const subscriptions = subscriptionRows(search);
  const accounts = useQueries({
    queries: subscriptions.map((provider) => listAccountsOptions({ query: { provider, limit: 1 } })),
  });
  const rows = subscriptions.map((provider, i) => ({ provider, accounts: accounts[i]?.data?.total ?? 0 }));
  const items = search.group === "subscription" ? [] : (providers.data?.items ?? []);

  const setSearch = (patch: Partial<ProvidersSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...patch }) });
  const closeEditor = () => void navigate({ search: (prev) => ({ ...prev, edit: undefined }) });

  const test = useMutation({
    ...testProviderMutation(),
    onMutate: (v) => setTesting(v.path.name),
    onSuccess: (r) => {
      if (r.ok) toast.success(`Reachable in ${r.latency_ms} ms`);
      else toast.danger(r.message || "The provider did not answer.");
    },
    onError: (e) => toast.danger(errorMessage(e)),
    onSettled: async () => {
      setTesting(undefined);
      await invalidateOps(qc, "listProviders", "getProvider");
    },
  });
  const remove = useMutation({
    ...deleteProviderMutation(),
    onSuccess: async () => {
      setDeleting(undefined);
      await invalidateOps(qc, "listProviders");
    },
  });

  const editName = search.edit && search.edit !== "new" ? search.edit : undefined;
  const listed = items.find((p) => p.name === editName);
  const detail = useQuery({
    ...getProviderOptions({ path: { name: editName ?? "" } }),
    enabled: editName !== undefined && listed === undefined,
  });
  const editing = listed ?? detail.data;
  const editorOpen = search.edit === "new" || editing !== undefined;
  const filtered = Boolean(search.q || search.group || search.status);
  const addButton = (
    <Button variant="primary" onPress={() => setSearch({ edit: "new" })}>
      <Plus aria-hidden="true" />
      Add provider
    </Button>
  );

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Providers"
        description="The upstreams this gateway forwards to: an API with its keys, or a subscription with signed-in accounts."
        actions={addButton}
      />

      <DataList>
        <DataList.Toolbar>
          <DataList.Search value={search.q} onChange={(q) => setSearch({ q })} placeholder="Search providers" />
          <DataList.Filters>
            <DataList.Filter
              label="Group"
              value={search.group}
              options={DISPLAY_GROUPS.map((id) => ({ id, label: GROUP_LABELS[id] }))}
              onChange={(group) => setSearch({ group })}
            />
            <DataList.Filter
              label="Status"
              value={search.status}
              options={PROVIDER_STATUSES.map((id) => ({ id, label: STATUS_LABELS[id] }))}
              onChange={(status) => setSearch({ status })}
            />
          </DataList.Filters>
        </DataList.Toolbar>
        <DataList.State
          isLoading={providers.isLoading || accounts.some((a) => a.isLoading)}
          error={providers.error ?? accounts.find((a) => a.error)?.error}
          isEmpty={items.length === 0 && rows.length === 0}
          emptyText={
            filtered
              ? "No provider matches. Clear the search or the filters."
              : "No providers yet. Add one to start routing requests."
          }
          emptyAction={filtered ? undefined : addButton}
        >
          <ProviderCards
            items={items}
            subscriptions={rows}
            testing={testing}
            onTest={(p) => test.mutate({ path: { name: p.name } })}
            onEdit={(p) => void navigate({ search: (prev) => ({ ...prev, edit: p.name }) })}
            onDelete={(p) => {
              remove.reset();
              setDeleting(p);
            }}
          />
        </DataList.State>
        <DataList.Pagination
          page={search.page}
          pageSize={search.pageSize}
          total={search.group === "subscription" ? 0 : (providers.data?.total ?? 0)}
          onPageChange={(page) => void navigate({ search: (prev) => ({ ...prev, page }) })}
          onPageSizeChange={(n) => setSearch({ pageSize: PAGE_SIZES.find((size) => size === n) ?? PAGE_SIZES[0] })}
        />
      </DataList>

      <ProviderEditorDrawer
        isOpen={editorOpen}
        provider={editing}
        editorKey={search.edit ?? ""}
        onClose={closeEditor}
      />

      <ConfirmDelete
        title={`Delete ${deleting?.name ?? "provider"}?`}
        body={
          <div className="flex flex-col gap-3">
            <p>Clients can no longer reach its models. Aliases that use it must be changed first.</p>
            {remove.error ? (
              <Alert status="danger" role="alert">
                <Alert.Indicator />
                <Alert.Content>
                  <Alert.Description>{errorMessage(remove.error)}</Alert.Description>
                </Alert.Content>
              </Alert>
            ) : null}
          </div>
        }
        isOpen={deleting !== undefined}
        isPending={remove.isPending}
        onConfirm={() => deleting && remove.mutate({ path: { name: deleting.name } })}
        onCancel={() => setDeleting(undefined)}
      />
    </div>
  );
}
