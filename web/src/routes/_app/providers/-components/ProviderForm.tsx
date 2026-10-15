import { Eye, EyeSlash, Plus, TrashBin } from "@gravity-ui/icons";
import {
  Alert,
  Button,
  Checkbox,
  cn,
  Description,
  FieldError,
  Form,
  Input,
  InputGroup,
  Label,
  Radio,
  RadioGroup,
  SearchField,
  TextField,
} from "@heroui/react";
import { type ReactNode, useState } from "react";

import {
  type DraftErrors,
  hasBaseUrl,
  KIND_LABELS,
  PROVIDER_KINDS,
  type ProviderDraft,
  SPEECH_PROVIDER_NAME,
  takesOneKey,
} from "../-lib/provider-form";
import type { Provider } from "../../../../client/types.gen";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { ModelPlaceholders } from "../../../../lib/provider-meta";
import { getProviderLabel, KnownProvidersNames, ProviderLabels } from "../../../../lib/providers";

const TYPES = [...KnownProvidersNames].sort((a, b) => ProviderLabels[a].localeCompare(ProviderLabels[b]));

const KIND_HINTS: Record<Provider["kind"], string> = {
  builtin: "A known API, such as OpenAI or OpenRouter.",
  "openai-compatible": "Any server that speaks the OpenAI API.",
  "typesafe-compatible": "An endpoint that returns typed decisions.",
  soniox: "Transcribes audio to text.",
};

// A selectable tile: the whole box is the radio, the accent border marks the choice.
const TILE =
  "bg-surface border-border m-0 rounded-sm border transition-colors duration-150 hover:bg-surface-secondary has-checked:border-accent has-checked:bg-accent/10 has-[input:focus-visible]:ring-2 has-[input:focus-visible]:ring-focus";

function modelHint(type: string): string | undefined {
  return Object.prototype.hasOwnProperty.call(ModelPlaceholders, type)
    ? ModelPlaceholders[type as keyof typeof ModelPlaceholders]
    : undefined;
}

function Block({ legend, children }: { legend: string; children: ReactNode }) {
  return (
    <fieldset className="border-border flex min-w-0 flex-col gap-4 border-t pt-4 first:border-t-0 first:pt-0">
      <legend className="label-caps text-muted float-left mb-1 w-full">{legend}</legend>
      {children}
    </fieldset>
  );
}

function SecretInput({
  label,
  value,
  placeholder,
  onChange,
}: {
  label: string;
  value: string;
  placeholder: string;
  onChange(v: string): void;
}) {
  const [shown, setShown] = useState(false);
  return (
    <TextField
      className="min-w-0 flex-1"
      type={shown ? "text" : "password"}
      autoComplete="off"
      value={value}
      onChange={onChange}
    >
      <Label className="sr-only">{label}</Label>
      <InputGroup>
        <InputGroup.Input className="font-mono" placeholder={placeholder} />
        <InputGroup.Suffix className="pe-0">
          <Button
            isIconOnly
            size="sm"
            variant="ghost"
            aria-label={shown ? `Hide ${label.toLowerCase()}` : `Show ${label.toLowerCase()}`}
            onPress={() => setShown(!shown)}
          >
            {shown ? <EyeSlash aria-hidden="true" className="size-4" /> : <Eye aria-hidden="true" className="size-4" />}
          </Button>
        </InputGroup.Suffix>
      </InputGroup>
    </TextField>
  );
}

export function ProviderForm({
  draft,
  errors,
  isEdit,
  keyHints,
  isPending,
  error,
  onChange,
  onSubmit,
  onCancel,
}: {
  draft: ProviderDraft;
  errors: DraftErrors;
  isEdit: boolean;
  /** The names of the keys already stored, shown on edit because key values are write-only. */
  keyHints: string[];
  isPending: boolean;
  /** A server error to show above the buttons. */
  error?: string;
  onChange(d: ProviderDraft): void;
  onSubmit(): void;
  onCancel(): void;
}) {
  const [typeFilter, setTypeFilter] = useState("");
  const set = (patch: Partial<ProviderDraft>) => onChange({ ...draft, ...patch });
  const setKey = (i: number, patch: Partial<ProviderDraft["keys"][number]>) =>
    set({ keys: draft.keys.map((k, j) => (j === i ? { ...k, ...patch } : k)) });
  const nameLocked = isEdit || draft.kind === "builtin" || draft.kind === "soniox";
  const hint = draft.kind === "builtin" ? modelHint(draft.type) : undefined;
  const q = typeFilter.trim().toLowerCase();
  const types = TYPES.filter((t) => q === "" || t.includes(q) || ProviderLabels[t].toLowerCase().includes(q));

  return (
    <Form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <Block legend="Provider">
        {isEdit ? (
          <p className="flex items-center gap-2 text-sm">
            {draft.type ? <ProviderIcon type={draft.type} size="xs" /> : null}
            <span>
              {draft.kind === "builtin" && draft.type ? getProviderLabel(draft.type) : KIND_LABELS[draft.kind]}
            </span>
            <span className="text-muted">· the kind of a stored provider cannot change</span>
          </p>
        ) : (
          <>
            <RadioGroup
              value={draft.kind}
              onChange={(key) => {
                const kind = PROVIDER_KINDS.find((k) => k === key);
                if (!kind || kind === draft.kind) return;
                set({
                  kind,
                  type: kind === "builtin" ? draft.type : "",
                  name: kind === "soniox" ? SPEECH_PROVIDER_NAME : kind === "builtin" ? draft.type : "",
                  keys: takesOneKey(kind) ? draft.keys.slice(0, 1) : draft.keys,
                });
              }}
            >
              <Label>Kind</Label>
              <div className="grid gap-2 sm:grid-cols-2">
                {PROVIDER_KINDS.map((k) => (
                  <Radio key={k} value={k} className={cn(TILE, "relative gap-0.5 px-3 py-2.5")}>
                    {/* The label stretches over the whole tile, so the hint below it is clickable too. */}
                    <Radio.Content className="static text-[13px] after:absolute after:inset-0">
                      {KIND_LABELS[k]}
                    </Radio.Content>
                    <Description className="ps-0">{KIND_HINTS[k]}</Description>
                  </Radio>
                ))}
              </div>
            </RadioGroup>

            {draft.kind === "builtin" ? (
              <RadioGroup
                isInvalid={errors.type !== undefined}
                value={draft.type === "" ? null : draft.type}
                onChange={(type) => set({ type, name: type })}
              >
                <div className="flex flex-wrap items-end justify-between gap-2">
                  <Label>Provider type</Label>
                  <SearchField
                    aria-label="Filter provider types"
                    value={typeFilter}
                    onChange={setTypeFilter}
                    className="w-44"
                  >
                    <SearchField.Group className="h-8 text-[13px]">
                      <SearchField.SearchIcon />
                      <SearchField.Input placeholder="Filter types" />
                      <SearchField.ClearButton />
                    </SearchField.Group>
                  </SearchField>
                </div>
                <div className="grid max-h-72 grid-cols-2 gap-1.5 overflow-y-auto p-0.5 sm:grid-cols-3">
                  {types.map((t) => (
                    <Radio key={t} value={t} className={TILE}>
                      <Radio.Content className="flex w-full min-w-0 items-center gap-2 px-2.5 py-2">
                        <ProviderIcon type={t} size="xs" />
                        <span className="truncate text-[13px] font-normal">{ProviderLabels[t]}</span>
                      </Radio.Content>
                    </Radio>
                  ))}
                  {types.length === 0 ? (
                    <p className="text-muted col-span-full py-4 text-center text-sm">No type matches.</p>
                  ) : null}
                </div>
                {hint ? <Description>Models look like: {hint.replace(/^e\.g\. /, "")}</Description> : null}
                <FieldError>{errors.type}</FieldError>
              </RadioGroup>
            ) : null}
          </>
        )}
      </Block>

      <Block legend="Connection">
        <TextField
          isDisabled={nameLocked}
          isInvalid={errors.name !== undefined}
          value={draft.name}
          onChange={(name) => set({ name })}
        >
          <Label>Name</Label>
          <Input className="font-mono" placeholder="my-provider" />
          <Description>Clients call its models as name/model.</Description>
          <FieldError>{errors.name}</FieldError>
        </TextField>

        {hasBaseUrl(draft.kind) ? (
          <>
            <TextField
              type="url"
              isInvalid={errors.baseUrl !== undefined}
              value={draft.baseUrl}
              onChange={(baseUrl) => set({ baseUrl })}
            >
              <Label>Base URL</Label>
              <Input className="font-mono" placeholder="https://api.example.com/v1" />
              <FieldError>{errors.baseUrl}</FieldError>
            </TextField>
            <Checkbox
              isSelected={draft.allowPrivateNetwork}
              onChange={(allowPrivateNetwork) => set({ allowPrivateNetwork })}
            >
              <Checkbox.Control>
                <Checkbox.Indicator />
              </Checkbox.Control>
              <Checkbox.Content>
                <Label>Allow a private network address</Label>
                <Description>Needed when the base URL points at this machine or the local network.</Description>
              </Checkbox.Content>
            </Checkbox>
          </>
        ) : null}
      </Block>

      <Block legend={takesOneKey(draft.kind) ? "Key" : "Keys"}>
        {isEdit && keyHints.length > 0 ? (
          <p className="text-muted text-sm">
            Stored keys: <span className="text-foreground font-mono text-[13px]">{keyHints.join(", ")}</span>
          </p>
        ) : null}
        <div className="flex flex-col gap-2">
          {draft.keys.map((k, i) => (
            <div key={i} className="flex items-center gap-2">
              <TextField className="w-32 shrink-0" value={k.name} onChange={(name) => setKey(i, { name })}>
                <Label className="sr-only">{`Key ${i + 1} name`}</Label>
                <Input placeholder="name" className="font-mono" />
              </TextField>
              <SecretInput
                label={`Key ${i + 1} value`}
                value={k.value}
                placeholder={isEdit ? "Leave blank to keep" : "Secret value"}
                onChange={(value) => setKey(i, { value })}
              />
              {draft.keys.length > 1 ? (
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  aria-label={`Remove key ${i + 1}`}
                  onPress={() => set({ keys: draft.keys.filter((_, j) => j !== i) })}
                >
                  <TrashBin aria-hidden="true" className="size-4" />
                </Button>
              ) : null}
            </div>
          ))}
        </div>
        {errors.keys ? (
          <p role="alert" className="text-danger text-sm">
            {errors.keys}
          </p>
        ) : null}
        {!takesOneKey(draft.kind) ? (
          <Button
            variant="secondary"
            size="sm"
            className="self-start"
            onPress={() => set({ keys: [...draft.keys, { name: "", value: "" }] })}
          >
            <Plus aria-hidden="true" />
            Add key
          </Button>
        ) : null}
      </Block>

      {error ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      <div className="border-border flex justify-end gap-2 border-t pt-4">
        <Button variant="tertiary" isDisabled={isPending} onPress={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" isPending={isPending}>
          {isEdit ? "Save" : "Add provider"}
        </Button>
      </div>
    </Form>
  );
}
