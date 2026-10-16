import { ArrowLeft, TrashBin } from "@gravity-ui/icons";
import {
  Alert,
  Button,
  Description,
  FieldError,
  Form,
  Input,
  Label,
  Link,
  ListBox,
  NumberField,
  Select,
  Spinner,
  Tag,
  TagGroup,
  TextField,
} from "@heroui/react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";

import {
  createAliasMutation,
  deleteAliasMutation,
  getAliasOptions,
  listAccountsOptions,
  listAliasesOptions,
  listCoolingOptions,
  listProvidersOptions,
  updateAliasMutation,
} from "../../../client/@tanstack/react-query.gen";
import { AddCombo } from "../../../components/add-combo/AddCombo";
import { ConfirmDelete } from "../../../components/confirm-delete/ConfirmDelete";
import { PageHeader } from "../../../components/page-header/PageHeader";
import { errorMessage } from "../../../lib/api";
import { parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { ChainPreview } from "./-components/ChainPreview";
import { MemberList } from "./-components/MemberList";
import { useChainPreview } from "./-hooks/useChainPreview";
import {
  ALIAS_KINDS,
  ALIAS_STRATEGIES,
  type AliasDraft,
  aliasFromDraft,
  CAPABILITY_SUGGESTIONS,
  chainNodes,
  draftFromAlias,
  EMPTY_DRAFT,
  memberOptions,
  type ModelSource,
  validateAliasDraft,
} from "./-lib/alias-form";

const NEW = "new";
/** The API caps a page at 200; the add box offers what fits in one page of each list. */
const MAX_LIMIT = 200;

export const Route = createFileRoute("/_app/aliases/$name")({
  component: AliasPage,
});

function AliasPage() {
  const { name } = Route.useParams();
  const isNew = name === NEW;
  const alias = useQuery({ ...getAliasOptions({ path: { name } }), enabled: !isNew });

  if (isNew) return <AliasEditor key={NEW} initial={EMPTY_DRAFT} original={undefined} />;
  if (alias.isPending)
    return (
      <div className="flex justify-center py-12">
        <Spinner />
      </div>
    );
  if (alias.isError)
    return (
      <Alert status="danger" role="alert">
        <Alert.Indicator />
        <Alert.Content>
          <Alert.Title>Could not load this alias</Alert.Title>
          <Alert.Description>{errorMessage(alias.error)}</Alert.Description>
        </Alert.Content>
      </Alert>
    );
  return <AliasEditor key={name} initial={draftFromAlias(alias.data)} original={name} />;
}

function AliasEditor({ initial, original }: { initial: AliasDraft; original: string | undefined }) {
  const navigate = useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [draft, setDraft] = useState<AliasDraft>(initial);
  const [submitted, setSubmitted] = useState(false);
  const [memberError, setMemberError] = useState<string>();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const preview = useChainPreview(draft);

  const aliases = useQuery(listAliasesOptions({ query: { limit: MAX_LIMIT } }));
  const providers = useQuery(listProvidersOptions({ query: { limit: MAX_LIMIT } }));
  const accounts = useQuery(listAccountsOptions({ query: { limit: MAX_LIMIT } }));
  const cooling = useQuery(listCoolingOptions({ query: { limit: MAX_LIMIT } }));

  const options = useMemo(() => {
    const sources: ModelSource[] = [
      ...(providers.data?.items ?? []).map((p) => ({ name: p.name })),
      ...(accounts.data?.items ?? []).map((a) => ({ name: a.provider, models: a.models })),
    ];
    const all = memberOptions(
      (aliases.data?.items ?? []).map((a) => a.name),
      sources,
      draft.name.trim(),
    );
    const taken = new Set(draft.members);
    return [
      { title: "Aliases", items: all.aliases.filter((a) => !taken.has(a)) },
      { title: "Provider models", items: all.models.filter((m) => !taken.has(m)) },
    ];
  }, [aliases.data, providers.data, accounts.data, draft.members, draft.name]);
  const coolingUntil = useMemo(() => {
    const until = new Map((cooling.data?.items ?? []).map((c) => [`${c.provider}/${c.model}`, c.until]));
    for (const t of preview.chain ?? []) if (t.coolingUntil) until.set(`${t.provider}/${t.model}`, t.coolingUntil);
    return until;
  }, [cooling.data, preview.chain]);
  const coolingRefs = useMemo(() => new Set(coolingUntil.keys()), [coolingUntil]);
  const nodes = useMemo(
    () =>
      chainNodes(
        draft.members,
        new Map((aliases.data?.items ?? []).map((a) => [a.name, a.members])),
        coolingUntil,
        draft.name.trim(),
      ),
    [draft.members, draft.name, aliases.data, coolingUntil],
  );
  const isDirty = JSON.stringify(draft) !== JSON.stringify(initial);

  const errors = validateAliasDraft(draft);
  const shown = submitted ? errors : {};
  const set = (patch: Partial<AliasDraft>) => setDraft((d) => ({ ...d, ...patch }));

  const onSaved = async (saved: string) => {
    await invalidateOps(queryClient, "listAliases", "getAlias", "getAliasChain", "listCooling");
    await navigate({ to: "/aliases/$name", params: { name: saved } });
  };
  const create = useMutation({ ...createAliasMutation(), onSuccess: (a) => onSaved(a.name) });
  const update = useMutation({ ...updateAliasMutation(), onSuccess: (a) => onSaved(a.name) });
  const remove = useMutation({
    ...deleteAliasMutation(),
    onSuccess: async () => {
      setConfirmDelete(false);
      await invalidateOps(queryClient, "listAliases", "getAlias", "getAliasChain", "listCooling");
      await navigate({ to: "/aliases", search: parseListSearch({}) });
    },
  });
  const saveError = create.error ?? update.error;
  const isSaving = create.isPending || update.isPending;

  const save = () => {
    setSubmitted(true);
    if (Object.keys(errors).length > 0) return;
    const body = aliasFromDraft(draft);
    if (original === undefined) create.mutate({ body });
    else update.mutate({ path: { name: original }, body });
  };

  const addMember = (ref: string) => {
    if (draft.members.includes(ref)) {
      setMemberError(`“${ref}” is listed twice.`);
      return false;
    }
    if (ref === draft.name.trim()) {
      setMemberError("An alias cannot contain itself.");
      return false;
    }
    setMemberError(undefined);
    set({ members: [...draft.members, ref] });
    return true;
  };

  const legend = "label-caps text-muted pt-4 pb-3";

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        before={
          <Link href="/aliases" className="text-muted hover:text-foreground w-fit gap-1.5 text-sm no-underline">
            <ArrowLeft aria-hidden="true" className="size-4" />
            Aliases
          </Link>
        }
        title={<span className="font-mono">{original ?? "New alias"}</span>}
        description={
          original === undefined
            ? "Name it, choose its kind, and list the members to try in order."
            : "The strategy picks the first member; requests fall back past any that fail or are cooling."
        }
        actions={
          <>
            {isDirty ? (
              <output className="label-caps text-warning flex items-center gap-1.5">
                <span aria-hidden="true" className="bg-warning size-1.5 rounded-full" />
                Unsaved changes
              </output>
            ) : null}
            {original !== undefined ? (
              <Button size="sm" variant="danger-soft" onPress={() => setConfirmDelete(true)}>
                <TrashBin aria-hidden="true" className="size-4" />
                Delete
              </Button>
            ) : null}
            <Button size="sm" variant="primary" isPending={isSaving} onPress={save}>
              {original === undefined ? "Create alias" : "Save"}
            </Button>
          </>
        }
      />
      {saveError ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>Could not save this alias</Alert.Title>
            <Alert.Description>{errorMessage(saveError)}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}
      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]">
        <Form
          className="bg-surface border-border divide-separator flex flex-col divide-y rounded-sm border"
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <fieldset className="min-w-0 px-4 pb-4">
            <legend className={legend}>Identity</legend>
            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                value={draft.name}
                onChange={(name) => set({ name })}
                isReadOnly={original !== undefined}
                isRequired
                isInvalid={shown.name !== undefined}
              >
                <Label>Name</Label>
                <Input className="font-mono" placeholder="fast" />
                {original !== undefined ? (
                  <Description>An alias keeps its name; create a new one to rename.</Description>
                ) : null}
                <FieldError>{shown.name}</FieldError>
              </TextField>
              <Select
                value={draft.kind}
                onChange={(k) => set({ kind: ALIAS_KINDS.find((x) => x === k) ?? draft.kind })}
              >
                <Label>Kind</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox aria-label="Kind">
                    {ALIAS_KINDS.map((k) => (
                      <ListBox.Item key={k} id={k} textValue={k}>
                        {k}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
              <Select
                value={draft.strategy}
                onChange={(v) => set({ strategy: ALIAS_STRATEGIES.find((x) => x.id === v)?.id ?? draft.strategy })}
              >
                <Label>Strategy</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Description>{ALIAS_STRATEGIES.find((x) => x.id === draft.strategy)?.description}</Description>
                <Select.Popover>
                  <ListBox aria-label="Strategy">
                    {ALIAS_STRATEGIES.map((s) => (
                      <ListBox.Item key={s.id} id={s.id} textValue={s.label}>
                        <div className="flex flex-col">
                          <Label>{s.label}</Label>
                          <Description>{s.description}</Description>
                        </div>
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
            </div>
          </fieldset>
          <fieldset className="min-w-0 px-4 pb-4">
            <legend className={legend}>Members, in fallback order</legend>
            <div className="flex flex-col gap-3">
              <MemberList
                members={draft.members}
                cooling={coolingRefs}
                onReorder={(members) => set({ members })}
                onRemove={(ref) => set({ members: draft.members.filter((m) => m !== ref) })}
              />
              {shown.members !== undefined ? (
                <p role="alert" className="text-danger text-sm">
                  {shown.members}
                </p>
              ) : null}
              <AddCombo
                label="Add member"
                placeholder="An alias, or provider/model"
                sections={options}
                onAdd={addMember}
                error={memberError}
                onInputChange={() => setMemberError(undefined)}
              />
            </div>
          </fieldset>
          <fieldset className="min-w-0 px-4 pb-4">
            <legend className={legend}>Capabilities</legend>
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <TagGroup
                  aria-label="Capabilities"
                  onRemove={(keys) => set({ capabilities: draft.capabilities.filter((c) => !keys.has(c)) })}
                >
                  <Label>Advertised</Label>
                  <TagGroup.List
                    items={draft.capabilities.map((c) => ({ id: c }))}
                    renderEmptyState={() => <span className="text-muted text-sm">None advertised.</span>}
                  >
                    {(c) => (
                      <Tag id={c.id} className="font-mono text-xs">
                        {c.id}
                      </Tag>
                    )}
                  </TagGroup.List>
                </TagGroup>
                <AddCombo
                  label="Add capability"
                  sections={[
                    {
                      title: "Suggested",
                      items: CAPABILITY_SUGGESTIONS.filter((c) => !draft.capabilities.includes(c)),
                    },
                  ]}
                  onAdd={(c) => {
                    if (!draft.capabilities.includes(c)) set({ capabilities: [...draft.capabilities, c] });
                    return true;
                  }}
                />
              </div>
              <NumberField
                value={draft.contextWindow ?? Number.NaN}
                onChange={(v) => set({ contextWindow: Number.isNaN(v) ? null : v })}
                minValue={1}
                step={1}
                isInvalid={shown.contextWindow !== undefined}
              >
                <Label>Context window</Label>
                <NumberField.Group>
                  <NumberField.Input className="font-mono" placeholder="From the models" />
                </NumberField.Group>
                <Description>Tokens. Leave empty to use what the members report.</Description>
                <FieldError>{shown.contextWindow}</FieldError>
              </NumberField>
            </div>
          </fieldset>
        </Form>
        <div className="lg:sticky lg:top-4">
          <ChainPreview
            nodes={nodes}
            targets={preview.chain?.length}
            error={preview.error}
            isLoading={preview.isLoading}
          />
        </div>
      </div>
      {original !== undefined ? (
        <ConfirmDelete
          title={`Delete ${original}?`}
          isOpen={confirmDelete}
          isPending={remove.isPending}
          onCancel={() => {
            setConfirmDelete(false);
            remove.reset();
          }}
          onConfirm={() => remove.mutate({ path: { name: original } })}
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
      ) : null}
    </div>
  );
}
