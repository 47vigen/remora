import { TrashBin } from "@gravity-ui/icons";
import {
  Alert,
  Button,
  Card,
  Description,
  FieldError,
  Form,
  Input,
  Label,
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
import { errorMessage } from "../../../lib/api";
import { parseListSearch } from "../../../lib/list-search";
import { invalidateOps } from "../../../lib/query";
import { ChainPreview } from "./-components/ChainPreview";
import { MemberList } from "./-components/MemberList";
import { useChainPreview } from "./-hooks/useChainPreview";
import {
  ALIAS_KINDS,
  type AliasDraft,
  aliasFromDraft,
  CAPABILITY_SUGGESTIONS,
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
  const coolingRefs = useMemo(
    () => new Set((cooling.data?.items ?? []).map((c) => `${c.provider}/${c.model}`)),
    [cooling.data],
  );

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

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold">{original === undefined ? "New alias" : original}</h1>
        {original !== undefined ? (
          <Button variant="danger-soft" onPress={() => setConfirmDelete(true)}>
            <TrashBin aria-hidden="true" />
            Delete
          </Button>
        ) : null}
      </div>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Card>
          <Card.Header>
            <Card.Title>Definition</Card.Title>
          </Card.Header>
          <Card.Content>
            <Form
              className="flex flex-col gap-5"
              onSubmit={(e) => {
                e.preventDefault();
                save();
              }}
            >
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
              <div className="flex flex-col gap-2">
                <span className="text-sm font-medium">Members, in fallback order</span>
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
              <div className="flex flex-col gap-2">
                <TagGroup
                  aria-label="Capabilities"
                  onRemove={(keys) => set({ capabilities: draft.capabilities.filter((c) => !keys.has(c)) })}
                >
                  <Label>Capabilities</Label>
                  <TagGroup.List
                    items={draft.capabilities.map((c) => ({ id: c }))}
                    renderEmptyState={() => <span className="text-muted text-sm">None advertised.</span>}
                  >
                    {(c) => <Tag id={c.id}>{c.id}</Tag>}
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
                  <NumberField.Input placeholder="From the models" />
                </NumberField.Group>
                <Description>Tokens. Leave empty to use what the members report.</Description>
                <FieldError>{shown.contextWindow}</FieldError>
              </NumberField>
              {saveError ? (
                <Alert status="danger" role="alert">
                  <Alert.Indicator />
                  <Alert.Content>
                    <Alert.Title>Could not save this alias</Alert.Title>
                    <Alert.Description>{errorMessage(saveError)}</Alert.Description>
                  </Alert.Content>
                </Alert>
              ) : null}
              <div className="flex gap-2">
                <Button type="submit" variant="primary" isPending={isSaving}>
                  {original === undefined ? "Create alias" : "Save"}
                </Button>
                <Button
                  variant="tertiary"
                  onPress={() => void navigate({ to: "/aliases", search: parseListSearch({}) })}
                >
                  Cancel
                </Button>
              </div>
            </Form>
          </Card.Content>
        </Card>
        <div className="lg:sticky lg:top-4 lg:self-start">
          <ChainPreview chain={preview.chain} error={preview.error} isLoading={preview.isLoading} />
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
