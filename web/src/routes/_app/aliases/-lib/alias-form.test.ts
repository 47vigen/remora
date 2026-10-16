import type { Alias } from "../../../../client";
import {
  ALIAS_STRATEGIES,
  type AliasDraft,
  EMPTY_DRAFT,
  aliasFromDraft,
  chainNodes,
  draftFromAlias,
  memberKind,
  memberOptions,
  type ModelSource,
  toAliasTargets,
  validateAliasDraft,
} from "./alias-form";

const d: AliasDraft = {
  name: "fast",
  kind: "chat",
  members: ["openrouter/x", "free"],
  capabilities: [],
  contextWindow: null,
  strategy: "priority",
};

test("valid", () => expect(validateAliasDraft(d)).toEqual({}));
test("name is required", () => expect(validateAliasDraft({ ...d, name: " " }).name).toBe("Enter a name."));
test("name may contain / like the server allows", () =>
  expect(validateAliasDraft({ ...d, name: "honcho/chat" }).name).toBeUndefined());
test("name rejects spaces and other characters", () =>
  expect(validateAliasDraft({ ...d, name: "a b" }).name).toBeDefined());
test("at least one member", () =>
  expect(validateAliasDraft({ ...d, members: [] }).members).toBe("Add at least one member."));
test("no duplicate members", () =>
  expect(validateAliasDraft({ ...d, members: ["free", "free"] }).members).toBe("“free” is listed twice."));
test("an alias cannot contain itself", () =>
  expect(validateAliasDraft({ ...d, members: ["fast"] }).members).toBe("An alias cannot contain itself."));
test("context window is a positive integer or empty", () => {
  expect(validateAliasDraft({ ...d, contextWindow: 0 }).contextWindow).toBeDefined();
  expect(validateAliasDraft({ ...d, contextWindow: 1.5 }).contextWindow).toBeDefined();
  expect(validateAliasDraft({ ...d, contextWindow: 128000 }).contextWindow).toBeUndefined();
});
test("memberKind and memberOptions", () => {
  expect(memberKind("free")).toBe("alias");
  expect(memberKind("antigravity/gemini-3")).toBe("model");
  expect(memberOptions(["fast", "free"], [{ name: "or", models: ["m1"] } as ModelSource], "fast")).toEqual({
    aliases: ["free"],
    models: ["or/m1"],
  });
});
test("memberOptions merges sources with the same name and skips duplicates", () => {
  expect(
    memberOptions([], [{ name: "codex", models: ["a", "b"] }, { name: "codex", models: ["b"] }, { name: "or" }], "x"),
  ).toEqual({ aliases: [], models: ["codex/a", "codex/b"] });
});
test("draft and alias convert both ways; an empty context window is left out", () => {
  const alias = aliasFromDraft({ ...d, name: " fast " });
  expect(alias).toEqual({
    name: "fast",
    kind: "chat",
    members: ["openrouter/x", "free"],
    capabilities: [],
    strategy: "priority",
  });
  expect(draftFromAlias({ ...alias, context_window: 8000 })).toEqual({ ...d, contextWindow: 8000 });
});
test("a loaded strategy survives a save", () => {
  const alias: Alias = { name: "fast", kind: "chat", members: ["or/x"], capabilities: [], strategy: "p2c" };
  expect(aliasFromDraft(draftFromAlias(alias))).toEqual(alias);
});
test("a new draft is priority, and a save always sends the strategy", () => {
  expect(EMPTY_DRAFT.strategy).toBe("priority");
  const { strategy: _, ...withoutStrategy } = { name: "x", kind: "chat", members: ["or/x"], capabilities: [] } as Alias;
  expect(aliasFromDraft(draftFromAlias(withoutStrategy)).strategy).toBe("priority");
  expect(ALIAS_STRATEGIES.map((s) => s.id)).toEqual([
    "priority",
    "fill-first",
    "round-robin",
    "random",
    "p2c",
    "least-used",
  ]);
});
test("toAliasTargets marks cooling targets", () => {
  expect(
    toAliasTargets([
      { provider: "or", model: "x" },
      { provider: "cp", model: "y", cooling_until: "2026-10-14T10:30:00Z" },
    ]),
  ).toEqual([
    { provider: "or", model: "x", cooling: false },
    { provider: "cp", model: "y", cooling: true, coolingUntil: "2026-10-14T10:30:00Z" },
  ]);
});

test("chainNodes expands nested aliases, numbers models and marks the first one not cooling", () => {
  const nodes = chainNodes(
    ["or/a", "free", "loop", "ghost"],
    new Map([
      ["free", ["cp/b", "or/c"]],
      ["loop", ["fast"]],
    ]),
    new Map([["or/a", "2026-10-14T10:30:00Z"]]),
    "fast",
  );
  expect(nodes).toEqual([
    { ref: "or/a", depth: 0, kind: "model", position: 1, serving: false, coolingUntil: "2026-10-14T10:30:00Z" },
    { ref: "free", depth: 0, kind: "alias" },
    { ref: "cp/b", depth: 1, kind: "model", position: 2, serving: true },
    { ref: "or/c", depth: 1, kind: "model", position: 3, serving: false },
    { ref: "loop", depth: 0, kind: "alias" },
    { ref: "fast", depth: 1, kind: "alias", problem: "cycle" },
    { ref: "ghost", depth: 0, kind: "alias", problem: "unknown" },
  ]);
});
