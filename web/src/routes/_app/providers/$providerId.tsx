import { ChevronLeft, Plus } from "@gravity-ui/icons";
import { Alert, AlertDialog, Button, Card, Link, Spinner, toast } from "@heroui/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, stripSearchParams, useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  deleteAccountMutation,
  getProviderOptions,
  listAccountsOptions,
  resetAccountCooldownMutation,
  testProviderMutation,
  updateAccountMutation,
} from "../../../client/@tanstack/react-query.gen";
import type { Account, Provider } from "../../../client/types.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { ProviderIcon } from "../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../components/status-chip/StatusChip";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, parseListSearch } from "../../../lib/list-search";
import { getOAuthProviderLabel, isOAuthProvider } from "../../../lib/providers";
import { invalidateOps } from "../../../lib/query";
import { AccountLoginDialog } from "./-components/AccountLoginDialog";
import { AccountsTable } from "./-components/AccountsTable";
import { ProviderForm } from "./-components/ProviderForm";
import { useAccountLogin } from "./-hooks/useAccountLogin";
import { useProviderEditor } from "./-hooks/useProviderEditor";
import { displayGroup, fromProvider, GROUP_LABELS } from "./-lib/provider-form";

type DetailSearch = ListSearch & { login?: "1" };

export const Route = createFileRoute("/_app/providers/$providerId")({
  validateSearch: (raw: Record<string, unknown>): DetailSearch => ({
    ...parseListSearch(raw),
    login: raw.login === "1" || raw.login === 1 ? "1" : undefined,
  }),
  search: { middlewares: [stripSearchParams({ page: 1, pageSize: 25, q: "" })] },
  component: ProviderDetailPage,
});

function ProviderDetailPage() {
  const { providerId } = Route.useParams();
  return (
    <div className="flex flex-col gap-6">
      <Link href="/providers" className="text-muted flex items-center gap-1 self-start text-sm no-underline">
        <ChevronLeft aria-hidden="true" className="size-4" />
        Providers
      </Link>
      {isOAuthProvider(providerId) ? (
        <SubscriptionProvider providerId={providerId} />
      ) : (
        <ApiKeyProvider providerId={providerId} />
      )}
    </div>
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
  const refresh = () => invalidateOps(qc, "listAccounts", "listProviders");
  const setSearch = (patch: Partial<DetailSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...patch }) });

  const toggle = useMutation({
    ...updateAccountMutation(),
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

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          <ProviderIcon type={providerId} size="md" />
          {label}
        </h1>
        <Button variant="primary" onPress={() => setSearch({ login: "1" })}>
          <Plus aria-hidden="true" />
          Add account
        </Button>
      </div>
      <p className="text-muted text-sm">
        {GROUP_LABELS.subscription}: accounts signed in to {label}. Requests rotate across the enabled ones.
      </p>

      <DataList>
        <DataList.Toolbar>
          <DataList.Search value={search.q} onChange={(q) => setSearch({ q })} placeholder="Search accounts" />
        </DataList.Toolbar>
        <DataList.State
          isLoading={accounts.isLoading}
          error={accounts.error}
          isEmpty={(accounts.data?.items.length ?? 0) === 0}
          emptyText={search.q ? "No account matches." : "No accounts yet. Add one to sign in."}
        >
          <AccountsTable
            items={accounts.data?.items ?? []}
            onToggle={(a, enabled) => toggle.mutate({ path: { id: a.id }, body: { enabled } })}
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

function ProviderEditCard({ provider }: { provider: Provider }) {
  const editor = useProviderEditor({
    initial: fromProvider(provider),
    isEdit: true,
    onSaved: (p) => toast.success(`Saved ${p.name}`),
  });
  return (
    <Card>
      <Card.Header>
        <Card.Title>Settings</Card.Title>
      </Card.Header>
      <Card.Content>
        <ProviderForm
          draft={editor.draft}
          errors={editor.errors}
          isEdit
          keyHints={provider.keys?.map((k) => k.name) ?? []}
          isPending={editor.isPending}
          error={editor.error}
          onChange={editor.setDraft}
          onSubmit={editor.submit}
          onCancel={() => editor.setDraft(fromProvider(provider))}
        />
      </Card.Content>
    </Card>
  );
}

function ApiKeyProvider({ providerId }: { providerId: string }) {
  const qc = useQueryClient();
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

  if (provider.isLoading)
    return (
      <div className="flex justify-center py-12">
        <Spinner />
      </div>
    );
  if (provider.error || !provider.data)
    return (
      <Alert status="danger" role="alert">
        <Alert.Indicator />
        <Alert.Content>
          <Alert.Title>Could not load {providerId}</Alert.Title>
          <Alert.Description>{errorMessage(provider.error)}</Alert.Description>
        </Alert.Content>
      </Alert>
    );

  const p = provider.data;
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          <ProviderIcon type={p.type ?? p.name} size="md" />
          <span className="font-mono">{p.name}</span>
          <StatusChip status={p.status} />
        </h1>
        <Button variant="secondary" isPending={test.isPending} onPress={() => test.mutate({ path: { name: p.name } })}>
          Test
        </Button>
      </div>
      <p className="text-muted text-sm">
        {GROUP_LABELS[displayGroup(p.kind)]} provider. Clients call its models as {p.name}/model.
      </p>
      <ProviderEditCard key={`${p.name}:${p.keys?.map((k) => k.name).join(",") ?? ""}`} provider={p} />
    </>
  );
}
