import type { Provider, ProviderWritable } from "../../../../client/types.gen";
import { isKeyRequiredByProvider } from "../../../../lib/provider-meta";
import { KnownProvidersNames, type ProviderName } from "../../../../lib/providers";

export const DISPLAY_GROUPS = ["api_key", "decisions", "speech", "subscription"] as const;
export type DisplayGroup = (typeof DISPLAY_GROUPS)[number];

export const GROUP_LABELS: Record<DisplayGroup, string> = {
  api_key: "API key",
  decisions: "Decisions",
  speech: "Speech-to-text",
  subscription: "Subscription",
};

export const PROVIDER_KINDS = [
  "builtin",
  "openai-compatible",
  "typesafe-compatible",
  "soniox",
] as const satisfies readonly Provider["kind"][];

export const KIND_LABELS: Record<Provider["kind"], string> = {
  builtin: "Built-in",
  "openai-compatible": "OpenAI-compatible",
  "typesafe-compatible": "Decisions endpoint",
  soniox: "Speech-to-text",
};

export const PROVIDER_STATUSES = ["ok", "degraded", "down", "unknown"] as const satisfies readonly Provider["status"][];

/** The display group of a provider kind. Subscription is never a provider kind: it is derived from accounts. */
export function displayGroup(kind: Provider["kind"]): Exclude<DisplayGroup, "subscription"> {
  switch (kind) {
    case "builtin":
    case "openai-compatible":
      return "api_key";
    case "typesafe-compatible":
      return "decisions";
    case "soniox":
      return "speech";
  }
}

export type ProviderDraft = {
  kind: Provider["kind"];
  /** builtin only */
  type: string;
  name: string;
  baseUrl: string;
  allowPrivateNetwork: boolean;
  keys: { name: string; value: string }[];
};

export type DraftErrors = Partial<Record<keyof ProviderDraft, string>>;

/** The name a speech-to-text provider always has. */
export const SPEECH_PROVIDER_NAME = "soniox";

const SLUG = /^[a-z0-9_-]+$/;

export const hasBaseUrl = (kind: Provider["kind"]) => kind === "openai-compatible" || kind === "typesafe-compatible";
export const takesOneKey = (kind: Provider["kind"]) => kind === "typesafe-compatible" || kind === "soniox";

function isKnownType(type: string): type is ProviderName {
  return (KnownProvidersNames as readonly string[]).includes(type);
}

/** A new draft of the given kind, with one empty key row. */
export function emptyDraft(kind: Provider["kind"]): ProviderDraft {
  return {
    kind,
    type: "",
    name: kind === "soniox" ? SPEECH_PROVIDER_NAME : "",
    baseUrl: "",
    allowPrivateNetwork: false,
    keys: [{ name: "default", value: "" }],
  };
}

/** The draft for editing a stored provider: key names only, because key values are write-only. */
export function fromProvider(p: Provider): ProviderDraft {
  return {
    kind: p.kind,
    type: p.type ?? "",
    name: p.name,
    baseUrl: p.base_url ?? "",
    allowPrivateNetwork: p.allow_private_network ?? false,
    keys: (p.keys ?? []).map((k) => ({ name: k.name, value: "" })),
  };
}

function keyRequired(d: ProviderDraft): boolean {
  if (d.kind !== "builtin") return true;
  return isKnownType(d.type) ? isKeyRequiredByProvider[d.type] : true;
}

/** Checks a draft before it is sent. On edit a blank key value keeps the stored one, so no key is required. */
export function validateProviderDraft(d: ProviderDraft, isEdit: boolean): DraftErrors {
  const errors: DraftErrors = {};
  const name = d.name.trim();

  if (d.kind === "builtin" && !isKnownType(d.type)) errors.type = "Choose a provider type.";

  if (name === "") errors.name = "Name is required.";
  else if (!SLUG.test(name)) errors.name = "Use lowercase letters, digits, - and _.";
  else if (d.kind === "builtin" && isKnownType(d.type) && name !== d.type)
    errors.name = "A built-in provider is named after its type.";
  else if (d.kind === "soniox" && name !== SPEECH_PROVIDER_NAME)
    errors.name = `The speech-to-text provider is named ${SPEECH_PROVIDER_NAME}.`;

  if (hasBaseUrl(d.kind)) {
    const url = d.baseUrl.trim();
    if (url === "") errors.baseUrl = "Base URL is required.";
    else {
      let parsed: URL | undefined;
      try {
        parsed = new URL(url);
      } catch {
        parsed = undefined;
      }
      if (!parsed || (parsed.protocol !== "http:" && parsed.protocol !== "https:"))
        errors.baseUrl = "Use an http or https URL.";
    }
  }

  const filled = d.keys.filter((k) => k.value.trim() !== "");
  if (filled.some((k) => k.name.trim() === "")) errors.keys = "Name every key.";
  else if (takesOneKey(d.kind) && d.keys.filter((k) => k.name.trim() !== "" || k.value.trim() !== "").length > 1)
    errors.keys = "Use one key.";
  else if (!isEdit && filled.length === 0 && keyRequired(d)) errors.keys = "Add at least one key.";

  return errors;
}

function common(d: ProviderDraft): ProviderWritable {
  const body: ProviderWritable = { name: d.name.trim(), kind: d.kind };
  if (d.kind === "builtin") body.type = d.type;
  if (hasBaseUrl(d.kind)) {
    body.base_url = d.baseUrl.trim();
    body.allow_private_network = d.allowPrivateNetwork;
  }
  return body;
}

/** The create request: trimmed, blank keys dropped, `type` only for builtin and `base_url` only where it applies. */
export function toCreateBody(d: ProviderDraft): ProviderWritable {
  return {
    ...common(d),
    keys: d.keys
      .filter((k) => k.name.trim() !== "" && k.value.trim() !== "")
      .map((k) => ({ name: k.name.trim(), value: k.value.trim() })),
  };
}

/** The update request: a named key with a blank value is sent without a value, so the server keeps the stored one. */
export function toUpdateBody(d: ProviderDraft): ProviderWritable {
  return {
    ...common(d),
    keys: d.keys
      .filter((k) => k.name.trim() !== "")
      .map((k) => (k.value.trim() === "" ? { name: k.name.trim() } : { name: k.name.trim(), value: k.value.trim() })),
  };
}
