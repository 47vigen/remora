import { ChevronLeft, PencilToSquare, PlugConnection, Plus, TrashBin } from "@gravity-ui/icons";
import { Alert, AlertDialog, Button, Description, Label, Link, ListBox, Select, Skeleton, toast } from "@heroui/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, stripSearchParams, useNavigate } from "@tanstack/react-router";
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";

import {
  deleteAccountMutation,
  deleteProviderMutation,
  getProviderOptions,
  listAccountStrategiesOptions,
  listAccountsOptions,
  resetAccountCooldownMutation,
  setAccountStrategyMutation,
  testProviderMutation,
  updateAccountMutation,
} from "../../../client/@tanstack/react-query.gen";
import type { Account } from "../../../client/types.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader, SectionLabel } from "../../../components/page-header/PageHeader";
import { StatusChip } from "../../../components/status-chip/StatusChip";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, parseListSearch } from "../../../lib/list-search";
import { getOAuthProviderLabel, getProviderLabel, isOAuthProvider } from "../../../lib/providers";
import { invalidateOps } from "../../../lib/query";
import { StrategyIcon, StrategyValue } from "../../../lib/routing";
import { AccountLoginDialog } from "./-components/AccountLoginDialog";
import { AccountsTable } from "./-components/AccountsTable";
import { ProviderEditorDrawer } from "./-components/ProviderEditorDrawer";
import { ProviderMark } from "./-components/ProviderMark";
import { ProviderTraffic } from "./-components/ProviderTraffic";
import { SpecSheet } from "./-components/SpecSheet";
import { useAccountLogin } from "./-hooks/useAccountLogin";
import { ACCOUNT_STRATEGIES, usesPriority } from "./-lib/accounts";
import { displayGroup, GROUP_LABELS, KIND_LABELS } from "./-lib/provider-form";

type DetailSearch = ListSearch & { login?: "1"; edit?: "1" };

export const Route = createFileRoute("/_app/providers/$providerId")({
  validateSearch: (raw: Record<string, unknown>): DetailSearch => ({
    ...parseListSearch(raw),
    login: raw.login === "1" || raw.login === 1 ? "1" : undefined,
    edit: raw.edit === "1" || raw.edit === 1 ? "1" : undefined,
  }),
  search: { middlewares: [stripSearchParams({ page: 1, pageSize: 25, q: "" })] },
  component: ProviderDetailPage,
});

const backLink = (
  <Link
    href="/providers"
    className="text-muted hover:text-foreground flex items-center gap-1 self-start text-sm no-underline transition-colors duration-150"
  >
    <ChevronLeft aria-hidden="true" className="size-4" />
    Providers
  </Link>
);

function ProviderDetailPage() {
  const { providerId } = Route.useParams();
  return (
    <div className="flex flex-col gap-6">
      {isOAuthProvider(providerId) ? (
        <SubscriptionProvider providerId={providerId} />
      ) : (
        <ApiKeyProvider providerId={providerId} />
      )}
    </div>
  );
}

function Title({ icon, name, children }: { icon: string; name: string; children: ReactNode }) {
  return (
    <span className="flex min-w-0 items-center gap-3">
      <ProviderMark type={icon} name={name} size="md" />
      {children}
    </span>
  );
}

function LoginFlow({ providerId, onFinished }: { providerId: string; onFinished(signedIn: boolean): void }) {
  const { login, start, submitRedirect, cancel } = useAccountLogin(providerId);
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    start();
  }, [start]);
  useEffect(() => {
    if (login.phase === "done") onFinished(true);
  }, [login.phase, onFinished]);
  const close = () => {
    cancel();
    onFinished(false);
  };
  return (
    <AccountLoginDialog
      login={login}
      onSubmitRedirect={submitRedirect}
      onCancel={close}
      onClose={close}
      onRestart={() => {
        cancel();
        start();
      }}
    />
  );
}

function SubscriptionProvider({ providerId }: { providerId: string }) {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const qc = useQueryClient();
  const [deleting, setDeleting] = useState<Account>();
  const [resetting, setResetting] = useState<Account>();

  const accounts = useQuery(
    listAccountsOptions({
      query: {
        provider: providerId,
        limit: search.pageSize,
        offset: (search.page - 1) * search.pageSize,
        q: search.q || undefined,
      },
    }),
  );
  const strategies = useQuery(listAccountStrategiesOptions({ query: { limit: 200 } }));
  const strategy = strategies.data?.items.find((s) => s.provider === providerId)?.strategy ?? "round-robin";
  const current = ACCOUNT_STRATEGIES.find((s) => s.id === strategy);
  const refresh = () => invalidateOps(qc, "listAccounts", "listProviders", "listAccountStrategies");
  const setSearch = (patch: Partial<DetailSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...patch }) });

  const toggle = useMutation({
    ...updateAccountMutation(),
    onError: (e) => toast.danger(errorMessage(e)),
    onSettled: refresh,
  });
  const setStrategy = useMutation({
    ...setAccountStrategyMutation(),
    onError: (e) => toast.danger(errorMessage(e)),
    onSettled: refresh,
  });
  const reset = useMutation({
    ...resetAccountCooldownMutation(),
    onSuccess: async () => {
      setResetting(undefined);
      await refresh();
    },
    onError: (e) => toast.danger(errorMessage(e)),
  });
  const remove = useMutation({
    ...deleteAccountMutation(),
    onSuccess: async () => {
      setDeleting(undefined);
      await refresh();
    },
  });
  const onFinished = useCallback(
    (signedIn: boolean) => {
      if (signedIn) toast.success("Account signed in");
      void navigate({ search: (prev) => ({ ...prev, login: undefined }) });
    },
    [navigate],
  );

  const label = getOAuthProviderLabel(providerId);
  const total = accounts.data?.total ?? 0;
  const addAccount = (
    <Button variant="primary" onPress={() => setSearch({ login: "1" })}>
      <Plus aria-hidden="true" />
      Add account
    </Button>
  );

  return (
    <>
      <PageHeader
        before={backLink}
        title={
          <Title icon={providerId} name={label}>
            {label}
          </Title>
        }
        description={`Accounts signed in to ${label}. The strategy picks which enabled account serves a request.`}
        actions={addAccount}
      />

      <SpecSheet>
        <SpecSheet.Item label="Group">{GROUP_LABELS.subscription}</SpecSheet.Item>
        <SpecSheet.Item label="Accounts" mono>
          {accounts.data ? total : "—"}
        </SpecSheet.Item>
        <SpecSheet.Item label="Call as" mono wide>
          {providerId}/model
        </SpecSheet.Item>
      </SpecSheet>

      <section className="bg-surface border-border flex items-center justify-between gap-6 rounded-sm border p-4 max-sm:flex-col max-sm:items-stretch">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="label-caps text-muted">Routing</h2>
          <p className="text-sm">
            {current?.description}
            {usesPriority(strategy) ? null : " Account priorities do not apply."}
          </p>
        </div>
        <Select
          aria-label="Strategy"
          className="w-56 shrink-0 max-sm:w-full"
          value={strategy}
          isDisabled={!strategies.data}
          onChange={(v) => {
            const next = ACCOUNT_STRATEGIES.find((x) => x.id === v)?.id;
            if (next && next !== strategy)
              setStrategy.mutate({ path: { provider: providerId }, body: { strategy: next } });
          }}
        >
          <Select.Trigger>
            <Select.Value>
              {({ selectedText }) => <StrategyValue id={strategy} text={selectedText ?? ""} />}
            </Select.Value>
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover placement="bottom end">
            <ListBox aria-label="Strategy">
              {ACCOUNT_STRATEGIES.map((s) => (
                <ListBox.Item key={s.id} id={s.id} textValue={s.label}>
                  <StrategyIcon id={s.id} />
                  <div className="flex min-w-0 flex-1 flex-col">
                    <Label>{s.label}</Label>
                    <Description>{s.description}</Description>
                  </div>
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
      </section>

      <SectionLabel>Accounts</SectionLabel>
      <DataList>
        <DataList.Toolbar>
          <DataList.Search value={search.q} onChange={(q) => setSearch({ q })} placeholder="Search accounts" />
        </DataList.Toolbar>
        <DataList.State
          isLoading={accounts.isLoading}
          error={accounts.error}
          isEmpty={(accounts.data?.items.length ?? 0) === 0}
          emptyText={search.q ? "No account matches. Clear the search." : "No accounts yet. Add one to sign in."}
          emptyAction={search.q ? undefined : addAccount}
        >
          <AccountsTable
            items={accounts.data?.items ?? []}
            onToggle={(a, enabled) => toggle.mutate({ path: { id: a.id }, body: { enabled } })}
            onPriority={(a, priority) => toggle.mutate({ path: { id: a.id }, body: { priority } })}
            priorityApplies={usesPriority(strategy)}
            onResetCooldown={setResetting}
            onDelete={(a) => {
              remove.reset();
              setDeleting(a);
            }}
          />
        </DataList.State>
        <DataList.Pagination
          page={search.page}
          pageSize={search.pageSize}
          total={total}
          onPageChange={(page) => void navigate({ search: (prev) => ({ ...prev, page }) })}
          onPageSizeChange={(n) => setSearch({ pageSize: n === 50 || n === 100 ? n : 25 })}
        />
      </DataList>

      {search.login === "1" ? <LoginFlow providerId={providerId} onFinished={onFinished} /> : null}

      <AlertDialog.Backdrop
        isOpen={resetting !== undefined}
        onOpenChange={(open) => {
          if (!open && !reset.isPending) setResetting(undefined);
        }}
      >
        <AlertDialog.Container>
          <AlertDialog.Dialog>
            <AlertDialog.Header>
              <AlertDialog.Icon status="warning" />
              <AlertDialog.Heading>Reset the cooldown?</AlertDialog.Heading>
            </AlertDialog.Header>
            <AlertDialog.Body>
              {resetting?.email ?? resetting?.id} takes requests again at once, even if the provider still limits it.
            </AlertDialog.Body>
            <AlertDialog.Footer>
              <Button variant="tertiary" isDisabled={reset.isPending} onPress={() => setResetting(undefined)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                isPending={reset.isPending}
                onPress={() => resetting && reset.mutate({ path: { id: resetting.id } })}
              >
                Reset cooldown
              </Button>
            </AlertDialog.Footer>
          </AlertDialog.Dialog>
        </AlertDialog.Container>
      </AlertDialog.Backdrop>

      <ConfirmDelete
        title="Delete this account?"
        body={
          <div className="flex flex-col gap-3">
            <p>{deleting?.email ?? deleting?.id} is signed out and removed. Sign in again to add it back.</p>
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
        onConfirm={() => deleting && remove.mutate({ path: { id: deleting.id } })}
        onCancel={() => setDeleting(undefined)}
      />
    </>
  );
}

function ApiKeyProvider({ providerId }: { providerId: string }) {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const go = useNavigate();
  const qc = useQueryClient();
  const [deleting, setDeleting] = useState(false);
  const provider = useQuery(getProviderOptions({ path: { name: providerId } }));
  const test = useMutation({
    ...testProviderMutation(),
    onSuccess: (r) => {
      if (r.ok) toast.success(`Reachable in ${r.latency_ms} ms`);
      else toast.danger(r.message || "The provider did not answer.");
    },
    onError: (e) => toast.danger(errorMessage(e)),
    onSettled: () => invalidateOps(qc, "listProviders", "getProvider"),
  });
  const remove = useMutation({
    ...deleteProviderMutation(),
    onSuccess: async () => {
      setDeleting(false);
      toast.success(`Deleted ${providerId}`);
      await invalidateOps(qc, "listProviders");
      void go({ to: "/providers", search: { page: 1, pageSize: 25, q: "" } });
    },
  });
  const setEdit = (open: boolean) => void navigate({ search: (prev) => ({ ...prev, edit: open ? "1" : undefined }) });

  if (provider.isLoading)
    return (
      <>
        <PageHeader before={backLink} title={<span className="font-mono">{providerId}</span>} />
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-40 w-full" />
      </>
    );
  if (provider.error || !provider.data)
    return (
      <>
        <PageHeader before={backLink} title={<span className="font-mono">{providerId}</span>} />
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>Could not load {providerId}</Alert.Title>
            <Alert.Description>{errorMessage(provider.error)}</Alert.Description>
          </Alert.Content>
        </Alert>
      </>
    );

  const p = provider.data;
  const keys = p.keys ?? [];
  const lastTest = test.data
    ? test.data.ok
      ? `Reachable · ${test.data.latency_ms} ms`
      : "Did not answer"
    : "Not run yet";

  return (
    <>
      <PageHeader
        before={backLink}
        title={
          <Title icon={p.type ?? p.name} name={p.name}>
            <span className="truncate font-mono">{p.name}</span>
            <StatusChip status={p.status} />
          </Title>
        }
        description={`${GROUP_LABELS[displayGroup(p.kind)]} provider. Clients call its models as ${p.name}/model.`}
        actions={
          <>
            <Button
              size="sm"
              variant="secondary"
              isPending={test.isPending}
              onPress={() => test.mutate({ path: { name: p.name } })}
            >
              <PlugConnection aria-hidden="true" />
              Test
            </Button>
            <Button size="sm" variant="secondary" onPress={() => setEdit(true)}>
              <PencilToSquare aria-hidden="true" />
              Edit
            </Button>
            <Button
              size="sm"
              variant="danger-soft"
              onPress={() => {
                remove.reset();
                setDeleting(true);
              }}
            >
              <TrashBin aria-hidden="true" />
              Delete
            </Button>
          </>
        }
      />

      <SpecSheet>
        <SpecSheet.Item label="Kind">{KIND_LABELS[p.kind]}</SpecSheet.Item>
        <SpecSheet.Item label="Group">{GROUP_LABELS[displayGroup(p.kind)]}</SpecSheet.Item>
        {p.kind === "builtin" ? (
          <SpecSheet.Item label="Type">{p.type ? getProviderLabel(p.type) : "—"}</SpecSheet.Item>
        ) : null}
        {p.base_url ? (
          <SpecSheet.Item label="Base URL" mono wide>
            <span title={p.base_url}>{p.base_url}</span>
          </SpecSheet.Item>
        ) : null}
        <SpecSheet.Item label="Keys" mono>
          {p.key_count}
        </SpecSheet.Item>
        <SpecSheet.Item label="Last test" mono>
          {lastTest}
        </SpecSheet.Item>
        <SpecSheet.Item label="Call as" mono>
          {p.name}/model
        </SpecSheet.Item>
      </SpecSheet>

      <ProviderTraffic name={p.name} />

      <section className="bg-surface border-border flex flex-col gap-3 rounded-sm border p-4">
        <SectionLabel
          aside={
            <Button size="sm" variant="ghost" onPress={() => setEdit(true)}>
              Manage keys
            </Button>
          }
        >
          Keys
        </SectionLabel>
        {keys.length === 0 ? (
          <p className="text-muted text-sm">No key is stored. Add one to authenticate requests to this provider.</p>
        ) : (
          <ul className="divide-separator flex flex-col divide-y">
            {keys.map((k) => (
              <li key={k.name} className="flex items-center justify-between gap-3 py-2">
                <span className="truncate font-mono text-[13px]">{k.name}</span>
                <span className="text-muted font-mono text-xs tracking-widest">
                  <span aria-hidden="true">••••••••</span>
                  <span className="sr-only">Value hidden</span>
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <ProviderEditorDrawer
        isOpen={search.edit === "1"}
        provider={p}
        editorKey={`${p.name}:${keys.map((k) => k.name).join(",")}`}
        onClose={() => setEdit(false)}
      />

      <ConfirmDelete
        title={`Delete ${p.name}?`}
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
        isOpen={deleting}
        isPending={remove.isPending}
        onConfirm={() => remove.mutate({ path: { name: p.name } })}
        onCancel={() => setDeleting(false)}
      />
    </>
  );
}
