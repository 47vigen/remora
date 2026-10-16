import type { Alias, AliasKind, ChainTarget, RoutingStrategy } from "../../../../client";

/** The shared alias kinds, in the order the editor offers them. */
export const ALIAS_KINDS = ["chat", "embeddings", "transcription", "decisions"] as const satisfies readonly AliasKind[];

/** The routing strategies, in the order the editor offers them, each with its meaning for an alias. */
export const ALIAS_STRATEGIES: { id: RoutingStrategy; label: string; description: string }[] = [
  { id: "priority", label: "Priority", description: "Try members in order." },
  { id: "fill-first", label: "Fill first", description: "Same as priority for an alias." },
  { id: "round-robin", label: "Round robin", description: "Members take turns." },
  { id: "random", label: "Random", description: "A random member goes first." },
  {
    id: "p2c",
    label: "Power of two",
    description: "Two random members; the one with fewer requests in the last minute goes first.",
  },
  {
    id: "least-used",
    label: "Least used",
    description: "The member with the fewest requests in the last minute goes first.",
  },
];

/** Capabilities the editor suggests; any other value may be typed. */
export const CAPABILITY_SUGGESTIONS = ["tools", "structured_outputs", "reasoning", "vision"] as const;

export type AliasDraft = {
  name: string;
  kind: (typeof ALIAS_KINDS)[number];
  members: string[];
  capabilities: string[];
  contextWindow: number | null;
  strategy: RoutingStrategy;
};

/** One step of a compiled chain as the preview shows it. */
export type AliasTarget = { provider: string; model: string; cooling: boolean; coolingUntil?: string };

/** Anything that names a provider and, when known, the models it serves (an API provider or an account group). */
export type ModelSource = { name: string; models?: readonly string[] };

export const EMPTY_DRAFT: AliasDraft = {
  name: "",
  kind: "chat",
  members: [],
  capabilities: [],
  contextWindow: null,
  strategy: "priority",
};

/** Returns one message per invalid field; an empty object means the draft can be saved. */
export function validateAliasDraft(d: AliasDraft): Partial<Record<keyof AliasDraft, string>> {
  const errors: Partial<Record<keyof AliasDraft, string>> = {};
  const name = d.name.trim();
  if (name === "") errors.name = "Enter a name.";
  else if (!/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/.test(name))
    errors.name = "Use letters, digits and . _ : / -, up to 128 characters.";

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

/**
 * Splits a member ref "<provider>/<model>(<level>)" into the ref without its thinking suffix and the level, for
 * display only; the server validates.
 */
export function thinkingOf(ref: string): { base: string; level?: string } {
  const open = ref.lastIndexOf("(");
  if (open < 0 || !ref.endsWith(")") || memberKind(ref) === "alias") return { base: ref };
  return { base: ref.slice(0, open), level: ref.slice(open + 1, -1) };
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
    strategy: a.strategy ?? "priority",
  };
}

export function aliasFromDraft(d: AliasDraft): Alias {
  const alias: Alias = {
    name: d.name.trim(),
    kind: d.kind,
    members: d.members,
    capabilities: d.capabilities,
    strategy: d.strategy,
  };
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

/** One row of the fallback pipeline: a member, or a member of a nested alias, at its nesting depth. */
export type ChainNode = {
  ref: string;
  depth: number;
  kind: "alias" | "model";
  /** A model's place in the order requests try them. */
  position?: number;
  coolingUntil?: string;
  /** The first model that is not cooling: where a request lands now. */
  serving?: boolean;
  /** An alias that is not in the loaded list, or one that contains itself. */
  problem?: "unknown" | "cycle";
};

/** Expands members depth-first through nested aliases, in the order a request walks them. */
export function chainNodes(
  members: readonly string[],
  aliasMembers: ReadonlyMap<string, readonly string[]>,
  coolingUntil: ReadonlyMap<string, string>,
  self: string,
): ChainNode[] {
  const nodes: ChainNode[] = [];
  let position = 0;
  let served = false;
  const walk = (refs: readonly string[], depth: number, path: ReadonlySet<string>) => {
    for (const ref of refs) {
      if (memberKind(ref) === "model") {
        const until = coolingUntil.get(ref);
        const serving = !served && until === undefined;
        served ||= serving;
        nodes.push({
          ref,
          depth,
          kind: "model",
          position: ++position,
          serving,
          ...(until ? { coolingUntil: until } : {}),
        });
        continue;
      }
      const inner = aliasMembers.get(ref);
      if (path.has(ref)) nodes.push({ ref, depth, kind: "alias", problem: "cycle" });
      else if (inner === undefined) nodes.push({ ref, depth, kind: "alias", problem: "unknown" });
      else {
        nodes.push({ ref, depth, kind: "alias" });
        walk(inner, depth + 1, new Set(path).add(ref));
      }
    }
  };
  walk(members, 0, new Set([self]));
  return nodes;
}
