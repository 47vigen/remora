import type { Alias, AliasKind, ChainTarget } from "../../../../client";

/** The shared alias kinds, in the order the editor offers them. */
export const ALIAS_KINDS = ["chat", "embeddings", "transcription", "decisions"] as const satisfies readonly AliasKind[];

/** Capabilities the editor suggests; any other value may be typed. */
export const CAPABILITY_SUGGESTIONS = ["tools", "structured_outputs", "reasoning", "vision"] as const;

export type AliasDraft = {
  name: string;
  kind: (typeof ALIAS_KINDS)[number];
  members: string[];
  capabilities: string[];
  contextWindow: number | null;
};

/** One step of a compiled chain as the preview shows it. */
export type AliasTarget = { provider: string; model: string; cooling: boolean; coolingUntil?: string };

/** Anything that names a provider and, when known, the models it serves (an API provider or an account group). */
export type ModelSource = { name: string; models?: readonly string[] };

export const EMPTY_DRAFT: AliasDraft = { name: "", kind: "chat", members: [], capabilities: [], contextWindow: null };

/** Returns one message per invalid field; an empty object means the draft can be saved. */
export function validateAliasDraft(d: AliasDraft): Partial<Record<keyof AliasDraft, string>> {
  const errors: Partial<Record<keyof AliasDraft, string>> = {};
  const name = d.name.trim();
  if (name === "") errors.name = "Enter a name.";
  else if (name.includes("/")) errors.name = "A name cannot contain “/”; it would read as a provider model.";
  else if (/\s/.test(name)) errors.name = "Spaces are not allowed.";

  if (d.members.length === 0) errors.members = "Add at least one member.";
  else if (d.members.includes(name)) errors.members = "An alias cannot contain itself.";
  else {
    const seen = new Set<string>();
    for (const m of d.members) {
      if (seen.has(m)) {
        errors.members = `“${m}” is listed twice.`;
        break;
      }
      seen.add(m);
    }
  }

  if (d.contextWindow !== null && !(Number.isInteger(d.contextWindow) && d.contextWindow > 0))
    errors.contextWindow = "Use a whole number above 0, or leave it empty.";
  return errors;
}

/** A reference with a "/" is a provider model; anything else names another alias. */
export function memberKind(ref: string): "alias" | "model" {
  return ref.includes("/") ? "model" : "alias";
}

/** The members the add box offers: other aliases, and every known provider model, without duplicates. */
export function memberOptions(
  aliases: string[],
  providers: readonly ModelSource[],
  self: string,
): { aliases: string[]; models: string[] } {
  const models = new Set<string>();
  for (const p of providers) for (const m of p.models ?? []) models.add(`${p.name}/${m}`);
  return { aliases: aliases.filter((a) => a !== self), models: [...models] };
}

export function draftFromAlias(a: Alias): AliasDraft {
  return {
    name: a.name,
    kind: a.kind,
    members: [...a.members],
    capabilities: [...a.capabilities],
    contextWindow: a.context_window ?? null,
  };
}

export function aliasFromDraft(d: AliasDraft): Alias {
  const alias: Alias = { name: d.name.trim(), kind: d.kind, members: d.members, capabilities: d.capabilities };
  if (d.contextWindow !== null) alias.context_window = d.contextWindow;
  return alias;
}

export function toAliasTargets(targets: readonly ChainTarget[]): AliasTarget[] {
  return targets.map((t) =>
    t.cooling_until
      ? { provider: t.provider, model: t.model, cooling: true, coolingUntil: t.cooling_until }
      : { provider: t.provider, model: t.model, cooling: false },
  );
}
