import { ArrowDownToLine, ArrowUpFromLine, Plus } from "@gravity-ui/icons";
import { Alert, Button, FieldError, Form, Input, Label, Modal, Skeleton, TextField } from "@heroui/react";
import { type MutationFunctionContext, useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { FileTrigger } from "react-aria-components";

import {
  type ApiToken,
  type ApplyConfigData,
  type CreateApiTokenData,
  exportConfig,
  type Options,
} from "../../../client";
import {
  applyConfigMutation,
  changePasswordMutation,
  createApiTokenMutation,
  deleteApiTokenMutation,
  getVersionOptions,
  listApiTokensOptions,
  planConfigMutation,
} from "../../../client/@tanstack/react-query.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { SecretOnce } from "../../../components/secret-once/SecretOnce";
import { errorMessage } from "../../../lib/api";
import { downloadFile } from "../../../lib/download";
import { type ListSearch, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { About } from "./-components/About";
import { ImportReview } from "./-components/ImportReview";
import { PasswordForm } from "./-components/PasswordForm";
import { TokensTable } from "./-components/TokensTable";
import { appliedBeforeError, formatChanges, type GeneratedKey, generatedKeys, withoutValue } from "./-lib/config-diff";

const SECTIONS = [
  { id: "password", label: "Password" },
  { id: "tokens", label: "API tokens" },
  { id: "backup", label: "Import / export" },
  { id: "about", label: "About" },
] as const;
/** The list parameters belong to the API tokens table. */
type SettingsSearch = ListSearch;

export const Route = createFileRoute("/_app/settings/")({
  validateSearch: (raw: Record<string, unknown>): SettingsSearch => parseListSearch(raw),
  component: SettingsPage,
});

function ErrorAlert({ title, error }: { title: string; error: unknown }) {
  return (
    <Alert status="danger" role="alert">
      <Alert.Indicator />
      <Alert.Content>
        <Alert.Title>{title}</Alert.Title>
        <Alert.Description>{errorMessage(error)}</Alert.Description>
      </Alert.Content>
    </Alert>
  );
}

function SettingsPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Settings"
        description="The admin password, management API tokens, and the configuration file."
      />
      <div className="grid gap-8 lg:grid-cols-[11rem_minmax(0,1fr)]">
        <nav aria-label="Settings sections" className="sticky top-8 hidden self-start lg:block">
          <ul className="border-border flex flex-col border-l">
            {SECTIONS.map((s) => (
              <li key={s.id}>
                <a
                  href={`#${s.id}`}
                  className="text-muted hover:text-foreground hover:border-ink-strong -ml-px block border-l border-transparent py-1.5 pl-3 text-sm transition-colors duration-150"
                >
                  {s.label}
                </a>
              </li>
            ))}
          </ul>
        </nav>
        <div className="flex min-w-0 flex-col gap-6">
          <Panel id="password" title="Password" description="The password that signs in to this console.">
            <PasswordPanel />
          </Panel>
          <Panel
            id="tokens"
            title="API tokens"
            description="Tokens call the management API with the same rights as the admin password."
          >
            <TokensPanel />
          </Panel>
          <Panel id="backup" title="Import / export" description="Providers, aliases and keys as one remora.yaml file.">
            <BackupPanel />
          </Panel>
          <Panel id="about" title="About" description="Versions and credits.">
            <AboutPanel />
          </Panel>
        </div>
      </div>
    </div>
  );
}

function Panel({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section
      id={id}
      aria-labelledby={`${id}-title`}
      className="bg-surface border-border flex scroll-mt-8 flex-col gap-4 rounded-sm border p-4 md:p-6"
    >
      <header className="flex flex-col gap-1">
        <h2 id={`${id}-title`} className="label-caps text-muted">
          {title}
        </h2>
        <p className="text-muted text-sm">{description}</p>
      </header>
      {children}
    </section>
  );
}

function PasswordPanel() {
  const [done, setDone] = useState(false);
  const change = useMutation({ ...changePasswordMutation(), onSuccess: () => setDone(true) });
  return (
    <div className="flex flex-col gap-4">
      {done ? (
        <Alert status="success">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>Password changed.</Alert.Title>
          </Alert.Content>
        </Alert>
      ) : null}
      <PasswordForm
        key={done ? "done" : "form"}
        isPending={change.isPending}
        error={change.error ? errorMessage(change.error) : undefined}
        onSubmit={({ current, next }) => {
          setDone(false);
          change.mutate({ body: { current, new: next } });
        }}
      />
    </div>
  );
}

function TokensPanel() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const { page, pageSize, q } = search;
  const update = (next: Partial<SettingsSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...next }), replace: true });

  const [now] = useState(() => new Date());
  const list = useQuery(
    listApiTokensOptions({ query: { limit: pageSize, offset: (page - 1) * pageSize, q: q || undefined } }),
  );
  const [creating, setCreating] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<ApiToken | null>(null);
  const remove = useMutation({
    ...deleteApiTokenMutation(),
    onSuccess: async () => {
      setDeleting(null);
      await invalidateOps(queryClient, "listApiTokens");
    },
  });

  const newToken = (
    <Button size="sm" variant="secondary" onPress={() => setCreating(true)}>
      <Plus aria-hidden="true" />
      New token
    </Button>
  );

  return (
    <DataList.Root>
      <DataList.Toolbar>
        <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search tokens" />
        <span className="ml-auto">{newToken}</span>
      </DataList.Toolbar>
      <DataList.State
        isLoading={list.isPending}
        error={list.error ?? undefined}
        isEmpty={(list.data?.items.length ?? 0) === 0}
        emptyText={q ? "No tokens match this search." : "No API tokens yet. Create one for each script or service."}
        emptyAction={q ? undefined : newToken}
      >
        <TokensTable
          items={list.data?.items ?? []}
          now={now}
          onDelete={(t) => {
            remove.reset();
            setDeleting(t);
          }}
        />
      </DataList.State>
      <DataList.Pagination
        page={page}
        pageSize={pageSize}
        total={list.data?.total ?? 0}
        onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
        onPageSizeChange={(n) => update({ pageSize: n as ListSearch["pageSize"] })}
      />
      <CreateTokenModal
        isOpen={creating}
        onClose={() => setCreating(false)}
        onCreated={(value) => {
          setCreating(false);
          setSecret(value);
        }}
      />
      <SecretOnce title="Token created" value={secret} onClose={() => setSecret(null)} />
      <ConfirmDelete
        title={`Delete ${deleting?.name ?? "token"}?`}
        isOpen={deleting !== null}
        isPending={remove.isPending}
        onCancel={() => setDeleting(null)}
        onConfirm={() => deleting && remove.mutate({ path: { id: deleting.id } })}
        body={
          remove.error ? (
            <p role="alert" className="text-danger">
              {errorMessage(remove.error)}
            </p>
          ) : (
            <p>Anything using this token loses access at once.</p>
          )
        }
      />
    </DataList.Root>
  );
}

/** Same rule as a client key: the value goes only to SecretOnce, never into a cache or the URL. */
function CreateTokenModal({
  isOpen,
  onClose,
  onCreated,
}: {
  isOpen: boolean;
  onClose(): void;
  onCreated(value: string): void;
}) {
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [name, setName] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const base = createApiTokenMutation();
  const create = useMutation({
    gcTime: 0,
    mutationFn: async (vars: Options<CreateApiTokenData>, ctx: MutationFunctionContext) => {
      const { value, ...token } = await base.mutationFn!(vars, ctx);
      onCreated(value);
      return token;
    },
    onSuccess: () => {
      setName("");
      setSubmitted(false);
      void invalidateOps(queryClient, "listApiTokens");
    },
  });
  const nameError = submitted && name.trim() === "" ? "Enter a name." : undefined;
  const close = () => {
    create.reset();
    onClose();
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={(open) => !open && !create.isPending && close()}>
      <Modal.Container>
        <Modal.Dialog>
          <Form
            className="flex flex-col"
            onSubmit={(e) => {
              e.preventDefault();
              setSubmitted(true);
              if (name.trim() === "") return;
              create.mutate({ body: { name: name.trim() } });
            }}
          >
            <Modal.Header>
              <Modal.Heading>New API token</Modal.Heading>
              <p className="text-muted text-sm">The secret is shown once, right after you create it.</p>
            </Modal.Header>
            <Modal.Body className="flex flex-col gap-4">
              <TextField value={name} onChange={setName} isRequired isInvalid={nameError !== undefined}>
                <Label>Name</Label>
                <Input placeholder="deploy" />
                <FieldError>{nameError}</FieldError>
              </TextField>
              {create.error ? <ErrorAlert title="Could not create the token" error={create.error} /> : null}
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={close} isDisabled={create.isPending}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" isPending={create.isPending}>
                Create token
              </Button>
            </Modal.Footer>
          </Form>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}

function BackupPanel() {
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [yaml, setYaml] = useState<string | null>(null);
  const [applied, setApplied] = useState(false);
  const exporter = useMutation({
    mutationFn: async () => (await exportConfig({ parseAs: "text", throwOnError: true })).data as string,
    onSuccess: (text) => downloadFile(text, "remora.yaml", "application/yaml"),
  });
  const plan = useMutation(planConfigMutation());
  // Generated client key values go only to SecretOnce, one at a time, never into the mutation cache.
  const [generated, setGenerated] = useState<GeneratedKey[]>([]);
  const base = applyConfigMutation();
  const apply = useMutation({
    gcTime: 0,
    mutationFn: async (vars: Options<ApplyConfigData>, ctx: MutationFunctionContext) => {
      try {
        const answer = await base.mutationFn!(vars, ctx);
        setGenerated(generatedKeys(answer.changes));
        return { changes: answer.changes.map(withoutValue) };
      } catch (err) {
        // A failed apply may have committed changes first: show their key values and stop showing stale data.
        const committed = appliedBeforeError(err);
        if (committed === undefined) throw err;
        setGenerated(generatedKeys(committed));
        void queryClient.invalidateQueries();
        const detail = (err as { error: object }).error;
        throw { error: { ...detail, applied: committed.map(withoutValue) } };
      }
    },
    onSuccess: async () => {
      setYaml(null);
      setApplied(true);
      plan.reset();
      await queryClient.invalidateQueries();
    },
  });

  const review = (text: string) => {
    setApplied(false);
    apply.reset();
    setYaml(text);
    plan.mutate({ body: { document: text, prune: true } });
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="border-border divide-separator flex flex-col divide-y rounded-sm border">
        <BackupRow
          title="Export"
          description={
            <>
              Download the configuration. Secrets are written as references (<code className="font-mono">env:NAME</code>
              ), never as values.
            </>
          }
        >
          <Button size="sm" variant="secondary" isPending={exporter.isPending} onPress={() => exporter.mutate()}>
            <ArrowDownToLine aria-hidden="true" />
            Export
          </Button>
        </BackupRow>
        <BackupRow
          title="Import"
          description="Choose a remora.yaml file. You see what would change before anything is applied."
        >
          <FileTrigger
            acceptedFileTypes={[".yaml", ".yml"]}
            onSelect={(files) => {
              const file = files?.[0];
              if (file) void file.text().then(review);
            }}
          >
            <Button size="sm" variant="secondary">
              <ArrowUpFromLine aria-hidden="true" />
              Import
            </Button>
          </FileTrigger>
        </BackupRow>
      </div>
      {exporter.error ? <ErrorAlert title="Could not export" error={exporter.error} /> : null}
      {applied ? (
        <Alert status="success">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>The configuration was applied.</Alert.Title>
          </Alert.Content>
        </Alert>
      ) : null}
      {yaml !== null ? (
        <div className="flex flex-col gap-3">
          <ImportReview
            diff={plan.data ? formatChanges(plan.data.changes) : undefined}
            isPending={apply.isPending}
            error={plan.error ? errorMessage(plan.error) : undefined}
            onApply={() => apply.mutate({ body: { document: yaml, prune: true } })}
            onCancel={() => {
              setYaml(null);
              plan.reset();
              apply.reset();
            }}
          />
          {apply.error ? <ErrorAlert title="Could not apply the file" error={apply.error} /> : null}
        </div>
      ) : null}
      <SecretOnce
        title={generated[0] ? `Key ${generated[0].name} created` : ""}
        example={`curl ${window.location.origin}/v1/models \\\n  -H "Authorization: Bearer $REMORA_KEY"`}
        value={generated[0]?.value ?? null}
        onClose={() => setGenerated((keys) => keys.slice(1))}
      />
    </div>
  );
}

function BackupRow({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 p-4">
      <div className="flex min-w-0 flex-col gap-0.5">
        <h3 className="text-sm font-medium">{title}</h3>
        <p className="text-muted max-w-xl text-sm">{description}</p>
      </div>
      {children}
    </div>
  );
}

function AboutPanel() {
  const version = useQuery(getVersionOptions());
  if (version.error) return <ErrorAlert title="Could not load the versions" error={version.error} />;
  if (!version.data) return <Skeleton className="h-24 w-full max-w-md" />;
  return <About remora={version.data.remora} engines={version.data.engines} />;
}
