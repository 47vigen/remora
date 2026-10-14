import { Plus } from "@gravity-ui/icons";
import {
  Alert,
  Button,
  Drawer,
  FieldError,
  Form,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  TextField,
} from "@heroui/react";
import { type MutationFunctionContext, useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState } from "react";

import type { CreateKeyData, Key, Options } from "../../../client";
import {
  createKeyMutation,
  deleteKeyMutation,
  getKeyOptions,
  getKeyUsageOptions,
  getStatsOptions,
  listAccountsOptions,
  listAliasesOptions,
  listKeysOptions,
  listProvidersOptions,
  updateKeyMutation,
} from "../../../client/@tanstack/react-query.gen";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { DataList } from "../../../components/data-list/DataList";
import { SecretOnce } from "../../../components/secret-once/SecretOnce";
import { errorMessage } from "../../../lib/api";
import { type ListSearch, parseEnum, parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { AllowlistEditor } from "./-components/AllowlistEditor";
import { KeysTable } from "./-components/KeysTable";
import { KeyUsage } from "./-components/KeyUsage";

const ENABLED = ["yes", "no"] as const;
type KeysSearch = ListSearch & { enabled?: (typeof ENABLED)[number]; key?: string };

/** The API caps a page at 200; the enabled filter has no server side, so it filters one 200-row page here. */
const MAX_LIMIT = 200;
const DAY_MS = 24 * 60 * 60 * 1000;

export const Route = createFileRoute("/_app/keys/")({
  validateSearch: (raw: Record<string, unknown>): KeysSearch => ({
    ...parseListSearch(raw),
    enabled: parseEnum(raw.enabled, ENABLED),
    key: typeof raw.key === "string" && raw.key !== "" ? raw.key : undefined,
  }),
  component: KeysPage,
});

/** Allowlist suggestions (aliases and provider wildcards) and preview candidates (aliases and known models). */
function useAllowlistOptions() {
  const aliases = useQuery(listAliasesOptions({ query: { limit: MAX_LIMIT } }));
  const providers = useQuery(listProvidersOptions({ query: { limit: MAX_LIMIT } }));
  const accounts = useQuery(listAccountsOptions({ query: { limit: MAX_LIMIT } }));
  return useMemo(() => {
    const aliasNames = (aliases.data?.items ?? []).map((a) => a.name);
    const providerNames = new Set([
      ...(providers.data?.items ?? []).map((p) => p.name),
      ...(accounts.data?.items ?? []).map((a) => a.provider),
    ]);
    const models = new Set((accounts.data?.items ?? []).flatMap((a) => a.models.map((m) => `${a.provider}/${m}`)));
    return {
      suggestions: [...aliasNames, ...[...providerNames].map((p) => `${p}/*`)],
      candidates: [...aliasNames, ...models],
    };
  }, [aliases.data, providers.data, accounts.data]);
}

function KeysPage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const { page, pageSize, q, enabled } = search;
  const update = (next: Partial<KeysSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, page: 1, ...next }), replace: true });
  const openKey = (key: string | undefined) => void navigate({ search: (prev) => ({ ...prev, key }) });

  const [now] = useState(() => new Date());
  const from = new Date(now.getTime() - DAY_MS).toISOString();
  const options = useAllowlistOptions();

  const list = useQuery(
    listKeysOptions({
      query: enabled
        ? { limit: MAX_LIMIT, q: q || undefined }
        : { limit: pageSize, offset: (page - 1) * pageSize, q: q || undefined },
    }),
  );
  const filtered = enabled ? (list.data?.items ?? []).filter((k) => k.enabled === (enabled === "yes")) : undefined;
  const items = filtered ? filtered.slice((page - 1) * pageSize, page * pageSize) : (list.data?.items ?? []);
  const total = filtered ? filtered.length : (list.data?.total ?? 0);

  const stats = useQuery(getStatsOptions({ query: { group_by: "key", from } }));
  const groups = stats.data?.groups ?? [];
  const usage = new Map<string, (typeof groups)[number]>();
  for (const k of items) {
    // Groups are named by key id; a name match is accepted as well.
    const g = groups.find((x) => x.name === k.id) ?? groups.find((x) => x.name === k.name);
    if (g) usage.set(k.id, g);
  }

  const [creating, setCreating] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Key | null>(null);

  const toggle = useMutation({
    ...updateKeyMutation(),
    onSettled: () => invalidateOps(queryClient, "listKeys", "getKey"),
  });
  const remove = useMutation({
    ...deleteKeyMutation(),
    onSuccess: async () => {
      setDeleting(null);
      await invalidateOps(queryClient, "listKeys", "getKey");
    },
  });

  return (
    <DataList.Root>
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold">Keys</h1>
        <Button variant="primary" onPress={() => setCreating(true)}>
          <Plus aria-hidden="true" />
          New key
        </Button>
      </div>
      <DataList.Toolbar>
        <DataList.Search value={q} onChange={(next) => update({ q: next })} placeholder="Search keys" />
        <DataList.Filters>
          <Select
            className="w-40"
            value={enabled ?? "all"}
            onChange={(v) => update({ enabled: parseEnum(v, ENABLED) })}
          >
            <Label>State</Label>
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox aria-label="State">
                <ListBox.Item id="all" textValue="All keys">
                  All keys
                  <ListBox.ItemIndicator />
                </ListBox.Item>
                <ListBox.Item id="yes" textValue="Enabled">
                  Enabled
                  <ListBox.ItemIndicator />
                </ListBox.Item>
                <ListBox.Item id="no" textValue="Disabled">
                  Disabled
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              </ListBox>
            </Select.Popover>
          </Select>
        </DataList.Filters>
      </DataList.Toolbar>
      {toggle.error ? <ErrorAlert title="Could not change the key" error={toggle.error} /> : null}
      <DataList.State
        isLoading={list.isPending}
        error={list.error ?? undefined}
        isEmpty={items.length === 0}
        emptyText={q || enabled ? "No keys match these filters." : "No keys yet."}
      >
        <KeysTable
          items={items}
          usage={usage}
          now={now}
          onOpen={(k) => openKey(k.id)}
          onToggle={(k, on) => toggle.mutate({ path: { id: k.id }, body: { enabled: on } })}
          onDelete={(k) => {
            remove.reset();
            setDeleting(k);
          }}
        />
      </DataList.State>
      <DataList.Pagination
        page={page}
        pageSize={pageSize}
        total={total}
        onPageChange={(n) => void navigate({ search: (prev) => ({ ...prev, page: n }) })}
        onPageSizeChange={(n) => update({ pageSize: n as ListSearch["pageSize"] })}
      />

      <CreateKeyModal
        isOpen={creating}
        suggestions={options.suggestions}
        candidates={options.candidates}
        onClose={() => setCreating(false)}
        onCreated={(value) => {
          setCreating(false);
          setSecret(value);
        }}
      />
      <SecretOnce title="Key created" value={secret} onClose={() => setSecret(null)} />
      <EditKeyDrawer
        id={search.key}
        from={from}
        suggestions={options.suggestions}
        candidates={options.candidates}
        onClose={() => openKey(undefined)}
      />
      <ConfirmDelete
        title={`Delete ${deleting?.name ?? "key"}?`}
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
            <p>Clients using this key stop working at once. This cannot be undone.</p>
          )
        }
      />
    </DataList.Root>
  );
}

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

/**
 * Collects a name and an allowlist. The created value goes straight to the caller's state for SecretOnce: the
 * mutation result keeps the key without its value, the mutation is collected at once (gcTime 0), and nothing
 * writes the response into the query cache.
 */
function CreateKeyModal({
  isOpen,
  suggestions,
  candidates,
  onClose,
  onCreated,
}: {
  isOpen: boolean;
  suggestions: string[];
  candidates: string[];
  onClose(): void;
  onCreated(value: string): void;
}) {
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [name, setName] = useState("");
  const [allow, setAllow] = useState<string[]>([]);
  const [submitted, setSubmitted] = useState(false);
  const base = createKeyMutation();
  const create = useMutation({
    gcTime: 0,
    // The value goes to the caller and is stripped from the result, so the mutation cache never holds it.
    mutationFn: async (vars: Options<CreateKeyData>, ctx: MutationFunctionContext) => {
      const { value, ...key } = await base.mutationFn!(vars, ctx);
      onCreated(value);
      return key;
    },
    onSuccess: () => {
      setName("");
      setAllow([]);
      setSubmitted(false);
      void invalidateOps(queryClient, "listKeys");
    },
  });
  const nameError = submitted && name.trim() === "" ? "Enter a name." : undefined;
  const close = () => {
    create.reset();
    onClose();
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={(open) => !open && !create.isPending && close()}>
      <Modal.Container size="lg">
        <Modal.Dialog>
          <Form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              setSubmitted(true);
              if (name.trim() === "") return;
              create.mutate({ body: { name: name.trim(), allow } });
            }}
          >
            <Modal.Header>
              <Modal.Heading>New key</Modal.Heading>
            </Modal.Header>
            <Modal.Body className="flex flex-col gap-4">
              <TextField value={name} onChange={setName} isRequired isInvalid={nameError !== undefined}>
                <Label>Name</Label>
                <Input placeholder="ci" />
                <FieldError>{nameError}</FieldError>
              </TextField>
              <AllowlistEditor value={allow} suggestions={suggestions} candidates={candidates} onChange={setAllow} />
              {create.error ? <ErrorAlert title="Could not create the key" error={create.error} /> : null}
            </Modal.Body>
            <Modal.Footer>
              <Button variant="tertiary" onPress={close} isDisabled={create.isPending}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" isPending={create.isPending}>
                Create key
              </Button>
            </Modal.Footer>
          </Form>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}

/** The `?key=<id>` drawer: rename, edit the allowlist, and see the key's last day of traffic. */
function EditKeyDrawer({
  id,
  from,
  suggestions,
  candidates,
  onClose,
}: {
  id: string | undefined;
  from: string;
  suggestions: string[];
  candidates: string[];
  onClose(): void;
}) {
  const keyQuery = useQuery({ ...getKeyOptions({ path: { id: id ?? "" } }), enabled: id !== undefined });
  const usage = useQuery({
    ...getKeyUsageOptions({ path: { id: id ?? "" }, query: { from } }),
    enabled: id !== undefined,
  });

  return (
    <Drawer.Backdrop isOpen={id !== undefined} onOpenChange={(open) => !open && onClose()}>
      <Drawer.Content placement="right">
        <Drawer.Dialog>
          <Drawer.Header>
            <Drawer.Heading>{keyQuery.data?.name ?? "Key"}</Drawer.Heading>
          </Drawer.Header>
          <Drawer.Body className="flex flex-col gap-6">
            {keyQuery.isPending ? (
              <Spinner />
            ) : keyQuery.isError ? (
              <ErrorAlert title="Could not load this key" error={keyQuery.error} />
            ) : (
              <KeyForm
                key={keyQuery.data.id}
                value={keyQuery.data}
                suggestions={suggestions}
                candidates={candidates}
                onDone={onClose}
              />
            )}
            <KeyUsage
              totals={usage.data?.summary}
              isLoading={usage.isPending && id !== undefined}
              logsHref={`/logs?keyId=${encodeURIComponent(id ?? "")}`}
            />
          </Drawer.Body>
        </Drawer.Dialog>
      </Drawer.Content>
    </Drawer.Backdrop>
  );
}

function KeyForm({
  value,
  suggestions,
  candidates,
  onDone,
}: {
  value: Key;
  suggestions: string[];
  candidates: string[];
  onDone(): void;
}) {
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [name, setName] = useState(value.name);
  const [allow, setAllow] = useState(value.allow);
  const save = useMutation({
    ...updateKeyMutation(),
    onSuccess: async () => {
      await invalidateOps(queryClient, "listKeys", "getKey");
      onDone();
    },
  });
  const nameError = name.trim() === "" ? "Enter a name." : undefined;

  return (
    <Form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (nameError) return;
        save.mutate({ path: { id: value.id }, body: { name: name.trim(), allow } });
      }}
    >
      <TextField value={name} onChange={setName} isRequired isInvalid={nameError !== undefined}>
        <Label>Name</Label>
        <Input />
        <FieldError>{nameError}</FieldError>
      </TextField>
      <p className="text-muted font-mono text-sm">{value.prefix}…</p>
      <AllowlistEditor value={allow} suggestions={suggestions} candidates={candidates} onChange={setAllow} />
      {save.error ? <ErrorAlert title="Could not save the key" error={save.error} /> : null}
      <div className="flex gap-2">
        <Button type="submit" variant="primary" isPending={save.isPending}>
          Save
        </Button>
        <Button variant="tertiary" onPress={onDone}>
          Cancel
        </Button>
      </div>
    </Form>
  );
}
