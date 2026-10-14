import { Plus } from "@gravity-ui/icons";
import { Alert, Button, Drawer, Label, ListBox, Select, toast } from "@heroui/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, stripSearchParams, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import {
  deleteProviderMutation,
  listAccountsOptions,
  listProvidersOptions,
  testProviderMutation,
} from "../../../client/@tanstack/react-query.gen";
import type { Provider } from "../../../client/types.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, parseEnum, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { ProviderForm } from "./-components/ProviderForm";
import { ProvidersTable } from "./-components/ProvidersTable";
import { useProviderEditor } from "./-hooks/useProviderEditor";
import {
  DISPLAY_GROUPS,
  type DisplayGroup,
  emptyDraft,
  fromProvider,
  GROUP_LABELS,
  PROVIDER_STATUSES,
} from "./-lib/provider-form";
import { FETCH_LIMIT, providerListRows } from "./-lib/providers-list";

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

const STATUS_LABELS: Record<Provider["status"], string> = {
  ok: "Healthy",
  degraded: "Degraded",
  down: "Down",
  unknown: "Not tested",
};

function FilterSelect<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T | undefined;
  options: readonly { id: T; label: string }[];
  onChange(v: T | undefined): void;
}) {
  return (
    <Select className="w-44" value={value ?? "all"} onChange={(key) => onChange(options.find((o) => o.id === key)?.id)}>
      <Label>{label}</Label>
      <Select.Trigger>
        <Select.Value />
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover>
        <ListBox aria-label={label}>
          {[{ id: "all", label: "All" }, ...options].map((o) => (
            <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
              {o.label}
              <ListBox.ItemIndicator />
            </ListBox.Item>
          ))}
        </ListBox>
      </Select.Popover>
    </Select>
  );
}

function ProviderEditor({ provider, onClose }: { provider?: Provider; onClose(): void }) {
  const isEdit = provider !== undefined;
  const editor = useProviderEditor({
    initial: provider ? fromProvider(provider) : emptyDraft("builtin"),
    isEdit,
    onSaved: (p) => {
      toast.success(isEdit ? `Saved ${p.name}` : `Added ${p.name}`);
      onClose();
    },
  });
  return (
    <ProviderForm
      draft={editor.draft}
      errors={editor.errors}
      isEdit={isEdit}
      keyHints={provider?.keys?.map((k) => k.name) ?? []}
      isPending={editor.isPending}
      error={editor.error}
      onChange={editor.setDraft}
      onSubmit={editor.submit}
      onCancel={onClose}
    />
  );
}

function ProvidersPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const go = useNavigate();
  const qc = useQueryClient();
  const [deleting, setDeleting] = useState<Provider>();
  const [testing, setTesting] = useState<string>();

  const providers = useQuery(listProvidersOptions({ query: { limit: FETCH_LIMIT, q: search.q || undefined } }));
  const accounts = useQuery(listAccountsOptions({ query: { limit: FETCH_LIMIT } }));
  const rows = providerListRows(providers.data?.items ?? [], accounts.data?.items ?? [], search);

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

  const editing =
    search.edit && search.edit !== "new" ? providers.data?.items.find((p) => p.name === search.edit) : undefined;
  const editorOpen = search.edit === "new" || editing !== undefined;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-xl font-semibold">Providers</h1>
        <Button variant="primary" onPress={() => setSearch({ edit: "new" })}>
          <Plus aria-hidden="true" />
          Add provider
        </Button>
      </div>

      <DataList>
        <DataList.Toolbar>
          <DataList.Search value={search.q} onChange={(q) => setSearch({ q })} placeholder="Search providers" />
          <DataList.Filters>
            <FilterSelect
              label="Group"
              value={search.group}
              options={DISPLAY_GROUPS.map((id) => ({ id, label: GROUP_LABELS[id] }))}
              onChange={(group) => setSearch({ group })}
            />
            <FilterSelect
              label="Status"
              value={search.status}
              options={PROVIDER_STATUSES.map((id) => ({ id, label: STATUS_LABELS[id] }))}
              onChange={(status) => setSearch({ status })}
            />
          </DataList.Filters>
        </DataList.Toolbar>
        {providers.data && providers.data.total > FETCH_LIMIT ? (
          <Alert status="warning">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Description>
                Showing the first {FETCH_LIMIT} of {providers.data.total} providers. Search to narrow the list.
              </Alert.Description>
            </Alert.Content>
          </Alert>
        ) : null}
        <DataList.State
          isLoading={providers.isLoading}
          error={providers.error ?? accounts.error}
          isEmpty={rows.total === 0}
          emptyText={search.q || search.group || search.status ? "No provider matches." : "No providers yet."}
        >
          <ProvidersTable
            items={rows.items}
            subscriptions={rows.subscriptions}
            testing={testing}
            onOpen={(name) =>
              void go({
                to: "/providers/$providerId",
                params: { providerId: name },
                search: { page: 1, pageSize: 25, q: "" },
              })
            }
            onTest={(p) => test.mutate({ path: { name: p.name } })}
            onDelete={(p) => {
              remove.reset();
              setDeleting(p);
            }}
          />
        </DataList.State>
        <DataList.Pagination
          page={search.page}
          pageSize={search.pageSize}
          total={rows.total}
          onPageChange={(page) => void navigate({ search: (prev) => ({ ...prev, page }) })}
          onPageSizeChange={(n) => setSearch({ pageSize: n === 50 || n === 100 ? n : 25 })}
        />
      </DataList>

      <Drawer.Backdrop
        isOpen={editorOpen}
        onOpenChange={(open) => {
          if (!open) closeEditor();
        }}
      >
        <Drawer.Content placement="right">
          <Drawer.Dialog>
            <Drawer.Header>
              <Drawer.Heading>{editing ? `Edit ${editing.name}` : "Add provider"}</Drawer.Heading>
            </Drawer.Header>
            <Drawer.Body>
              {editorOpen ? <ProviderEditor key={search.edit} provider={editing} onClose={closeEditor} /> : null}
            </Drawer.Body>
          </Drawer.Dialog>
        </Drawer.Content>
      </Drawer.Backdrop>

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
