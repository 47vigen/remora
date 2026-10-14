import { Plus, TrashBin } from "@gravity-ui/icons";
import {
  Alert,
  Button,
  Checkbox,
  Description,
  FieldError,
  Form,
  Input,
  Label,
  ListBox,
  Select,
  TextField,
} from "@heroui/react";

import {
  type DraftErrors,
  hasBaseUrl,
  KIND_LABELS,
  PROVIDER_KINDS,
  type ProviderDraft,
  SPEECH_PROVIDER_NAME,
  takesOneKey,
} from "../-lib/provider-form";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { ModelPlaceholders } from "../../../../lib/provider-meta";
import { KnownProvidersNames, ProviderLabels } from "../../../../lib/providers";

const TYPES = [...KnownProvidersNames].sort((a, b) => ProviderLabels[a].localeCompare(ProviderLabels[b]));

function modelHint(type: string): string | undefined {
  return Object.prototype.hasOwnProperty.call(ModelPlaceholders, type)
    ? ModelPlaceholders[type as keyof typeof ModelPlaceholders]
    : undefined;
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
  const set = (patch: Partial<ProviderDraft>) => onChange({ ...draft, ...patch });
  const setKey = (i: number, patch: Partial<ProviderDraft["keys"][number]>) =>
    set({ keys: draft.keys.map((k, j) => (j === i ? { ...k, ...patch } : k)) });
  const nameLocked = isEdit || draft.kind === "builtin" || draft.kind === "soniox";
  const hint = draft.kind === "builtin" ? modelHint(draft.type) : undefined;

  return (
    <Form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <Select
        isDisabled={isEdit}
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
        <Select.Trigger>
          <Select.Value />
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox aria-label="Kind">
            {PROVIDER_KINDS.map((k) => (
              <ListBox.Item key={k} id={k} textValue={KIND_LABELS[k]}>
                {KIND_LABELS[k]}
                <ListBox.ItemIndicator />
              </ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
      </Select>

      {draft.kind === "builtin" ? (
        <Select
          isDisabled={isEdit}
          isInvalid={errors.type !== undefined}
          value={draft.type === "" ? null : draft.type}
          placeholder="Choose a provider"
          onChange={(key) => {
            const type = typeof key === "string" ? key : "";
            set({ type, name: type });
          }}
        >
          <Label>Provider type</Label>
          <Select.Trigger>
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          {hint ? <Description>Models look like: {hint.replace(/^e\.g\. /, "")}</Description> : null}
          <FieldError>{errors.type}</FieldError>
          <Select.Popover>
            <ListBox aria-label="Provider type">
              {TYPES.map((t) => (
                <ListBox.Item key={t} id={t} textValue={ProviderLabels[t]}>
                  <ProviderIcon type={t} size="xs" />
                  {ProviderLabels[t]}
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
      ) : null}

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
            <Input placeholder="https://api.example.com/v1" />
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

      <fieldset className="flex flex-col gap-3">
        <legend className="mb-2 text-sm font-medium">{takesOneKey(draft.kind) ? "Key" : "Keys"}</legend>
        {isEdit && keyHints.length > 0 ? (
          <p className="text-muted text-sm">Stored keys: {keyHints.join(", ")}</p>
        ) : null}
        {draft.keys.map((k, i) => (
          <div key={i} className="flex items-end gap-2">
            <TextField className="w-32" value={k.name} onChange={(name) => setKey(i, { name })}>
              <Label className="sr-only">{`Key ${i + 1} name`}</Label>
              <Input placeholder="name" className="font-mono" />
            </TextField>
            <TextField
              className="flex-1"
              type="password"
              autoComplete="off"
              value={k.value}
              onChange={(value) => setKey(i, { value })}
            >
              <Label className="sr-only">{`Key ${i + 1} value`}</Label>
              <Input placeholder={isEdit ? "Leave blank to keep" : "Secret value"} />
            </TextField>
            {draft.keys.length > 1 ? (
              <Button
                isIconOnly
                variant="ghost"
                aria-label={`Remove key ${i + 1}`}
                onPress={() => set({ keys: draft.keys.filter((_, j) => j !== i) })}
              >
                <TrashBin aria-hidden="true" />
              </Button>
            ) : null}
          </div>
        ))}
        {errors.keys ? (
          <p role="alert" className="text-danger text-sm">
            {errors.keys}
          </p>
        ) : null}
        {!takesOneKey(draft.kind) ? (
          <Button
            variant="ghost"
            size="sm"
            className="self-start"
            onPress={() => set({ keys: [...draft.keys, { name: "", value: "" }] })}
          >
            <Plus aria-hidden="true" />
            Add key
          </Button>
        ) : null}
      </fieldset>

      {error ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}

      <div className="flex justify-end gap-2">
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
