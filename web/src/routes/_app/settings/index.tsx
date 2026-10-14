import { ArrowDownToLine, ArrowUpFromLine, Plus } from "@gravity-ui/icons";
import { Alert, Button, Card, FieldError, Form, Input, Label, Modal, Tabs, TextField } from "@heroui/react";
import { type MutationFunctionContext, useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { FileTrigger } from "react-aria-components";

import { type ApiToken, type CreateApiTokenData, exportConfig, type Options } from "../../../client";
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
import { SecretOnce } from "../../../components/secret-once/SecretOnce";
import { errorMessage } from "../../../lib/api";
import { downloadFile } from "../../../lib/download";
import { type ListSearch, parseEnum, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { About } from "./-components/About";
import { ImportReview } from "./-components/ImportReview";
import { PasswordForm } from "./-components/PasswordForm";
import { TokensTable } from "./-components/TokensTable";
import { formatChanges } from "./-lib/config-diff";

const TABS = ["password", "tokens", "backup", "about"] as const;
type Tab = (typeof TABS)[number];
type SettingsSearch = ListSearch & { tab: Tab };

export const Route = createFileRoute("/_app/settings/")({
  validateSearch: (raw: Record<string, unknown>): SettingsSearch => ({
    ...parseListSearch(raw),
    tab: parseEnum(raw.tab, TABS) ?? "password",
  }),
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
  const search = Route.useSearch();
  const navigate = Route.useNavigate();

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold">Settings</h1>
      <Tabs
        selectedKey={search.tab}
        onSelectionChange={(key) => {
          const tab = parseEnum(key, TABS) ?? "password";
          void navigate({ search: { ...parseListSearch({}), tab }, replace: true });
        }}
      >
        <Tabs.ListContainer>
          <Tabs.List aria-label="Settings">
            <Tabs.Tab id="password">
              Password
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab id="tokens">
              API tokens
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab id="backup">
              Backup
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab id="about">
              About
              <Tabs.Indicator />
            </Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>
        <Tabs.Panel id="password" className="pt-6">
          <PasswordPanel />
        </Tabs.Panel>
        <Tabs.Panel id="tokens" className="pt-6">
          <TokensPanel />
        </Tabs.Panel>
        <Tabs.Panel id="backup" className="pt-6">
          <BackupPanel />
        </Tabs.Panel>
        <Tabs.Panel id="about" className="pt-6">
          <AboutPanel />
        </Tabs.Panel>
      </Tabs>
    </div>
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

  return (
    <DataList.Root>
      <DataList.Toolbar>
        <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search tokens" />
        <Button variant="primary" onPress={() => setCreating(true)}>
          <Plus aria-hidden="true" />
          New token
        </Button>
      </DataList.Toolbar>
      <p className="text-muted text-sm">Tokens call the management API with the same rights as the admin password.</p>
      <DataList.State
        isLoading={list.isPending}
        error={list.error ?? undefined}
        isEmpty={(list.data?.items.length ?? 0) === 0}
        emptyText={q ? "No tokens match this search." : "No API tokens yet."}
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
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              setSubmitted(true);
              if (name.trim() === "") return;
              create.mutate({ body: { name: name.trim() } });
            }}
          >
            <Modal.Header>
              <Modal.Heading>New API token</Modal.Heading>
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
              <Button variant="tertiary" onPress={close} isDisabled={create.isPending}>
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
  const apply = useMutation({
    ...applyConfigMutation(),
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
    <div className="flex max-w-2xl flex-col gap-6">
      <Card>
        <Card.Header>
          <Card.Title>Export</Card.Title>
          <Card.Description>
            Download providers, aliases and keys as remora.yaml. Secrets are written as references (
            <code>env:NAME</code>), never as values.
          </Card.Description>
        </Card.Header>
        <Card.Footer className="flex flex-col items-start gap-3">
          <Button variant="secondary" isPending={exporter.isPending} onPress={() => exporter.mutate()}>
            <ArrowDownToLine aria-hidden="true" />
            Export
          </Button>
          {exporter.error ? <ErrorAlert title="Could not export" error={exporter.error} /> : null}
        </Card.Footer>
      </Card>
      <Card>
        <Card.Header>
          <Card.Title>Import</Card.Title>
          <Card.Description>
            Choose a remora.yaml file. You see what would change before anything is applied.
          </Card.Description>
        </Card.Header>
        <Card.Footer className="flex flex-col items-start gap-3">
          <FileTrigger
            acceptedFileTypes={[".yaml", ".yml"]}
            onSelect={(files) => {
              const file = files?.[0];
              if (file) void file.text().then(review);
            }}
          >
            <Button variant="secondary">
              <ArrowUpFromLine aria-hidden="true" />
              Import
            </Button>
          </FileTrigger>
          {applied ? (
            <Alert status="success">
              <Alert.Indicator />
              <Alert.Content>
                <Alert.Title>The configuration was applied.</Alert.Title>
              </Alert.Content>
            </Alert>
          ) : null}
        </Card.Footer>
      </Card>
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
    </div>
  );
}

function AboutPanel() {
  const version = useQuery(getVersionOptions());
  if (version.error) return <ErrorAlert title="Could not load the versions" error={version.error} />;
  if (!version.data) return null;
  return <About remora={version.data.remora} engines={version.data.engines} />;
}
