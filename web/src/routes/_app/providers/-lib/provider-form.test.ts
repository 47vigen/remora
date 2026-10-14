import type { Provider } from "../../../../client/types.gen";
import {
  displayGroup,
  fromProvider,
  type ProviderDraft,
  toCreateBody,
  toUpdateBody,
  validateProviderDraft,
} from "./provider-form";

const base: ProviderDraft = {
  kind: "builtin",
  type: "openrouter",
  name: "openrouter",
  baseUrl: "",
  allowPrivateNetwork: false,
  keys: [{ name: "main", value: "k1" }],
};

test("valid API-key draft", () => expect(validateProviderDraft(base, false)).toEqual({}));

test("name must be a slug, because it becomes the <provider> in <provider>/<model>", () => {
  const oc: ProviderDraft = { ...base, kind: "openai-compatible", type: "", baseUrl: "http://h" };
  expect(validateProviderDraft({ ...oc, name: "Open Router" }, false).name).toBe(
    "Use lowercase letters, digits, - and _.",
  );
  expect(validateProviderDraft({ ...oc, name: "a/b" }, false).name).toBeDefined();
  expect(validateProviderDraft({ ...oc, name: "" }, false).name).toBe("Name is required.");
});

test("openai-compatible needs an http(s) base URL", () => {
  const oc = { ...base, kind: "openai-compatible", type: "", name: "local" } as const;
  expect(validateProviderDraft(oc, false).baseUrl).toBe("Base URL is required.");
  expect(validateProviderDraft({ ...oc, baseUrl: "ftp://x" }, false).baseUrl).toBe("Use an http or https URL.");
  expect(validateProviderDraft({ ...oc, baseUrl: "https://h/v1" }, false).baseUrl).toBeUndefined();
});

test("a key is required on create, optional on edit", () => {
  expect(validateProviderDraft({ ...base, keys: [{ name: "main", value: "" }] }, false).keys).toBe(
    "Add at least one key.",
  );
  expect(validateProviderDraft({ ...base, keys: [{ name: "main", value: "" }] }, true).keys).toBeUndefined();
});

test("a provider type that needs no key may have none", () => {
  expect(validateProviderDraft({ ...base, type: "ollama", name: "ollama", keys: [] }, false)).toEqual({});
});

test("every key with a value needs a name", () => {
  expect(validateProviderDraft({ ...base, keys: [{ name: " ", value: "k" }] }, false).keys).toBe("Name every key.");
});

test("decisions and speech take one key", () => {
  const ts: ProviderDraft = {
    ...base,
    kind: "typesafe-compatible",
    type: "",
    name: "decide",
    baseUrl: "https://h",
    keys: [
      { name: "a", value: "1" },
      { name: "b", value: "2" },
    ],
  };
  expect(validateProviderDraft(ts, false).keys).toBe("Use one key.");
});

test("a builtin's name must equal its type", () => {
  expect(validateProviderDraft({ ...base, name: "or" }, false).name).toBeDefined();
  expect(validateProviderDraft({ ...base, type: "" }, false).type).toBe("Choose a provider type.");
});

test("speech-to-text is always named soniox", () => {
  const s: ProviderDraft = { ...base, kind: "soniox", type: "", name: "speech" };
  expect(validateProviderDraft(s, false).name).toBeDefined();
  expect(validateProviderDraft({ ...s, name: "soniox" }, false)).toEqual({});
});

test("displayGroup derives the group from kind", () => {
  expect(
    ["builtin", "openai-compatible", "typesafe-compatible", "soniox"].map((k) => displayGroup(k as Provider["kind"])),
  ).toEqual(["api_key", "api_key", "decisions", "speech"]);
});

test("toCreateBody trims, drops blank keys and sends type only for builtin", () => {
  expect(
    toCreateBody({
      ...base,
      name: " openrouter ",
      keys: [
        { name: "main", value: " k1 " },
        { name: "b", value: "" },
      ],
    }),
  ).toMatchObject({ name: "openrouter", kind: "builtin", type: "openrouter", keys: [{ name: "main", value: "k1" }] });
  const oc = toCreateBody({ ...base, kind: "openai-compatible", type: "x", name: "local", baseUrl: " http://h " });
  expect(oc).not.toHaveProperty("type");
  expect(oc).toMatchObject({ base_url: "http://h", allow_private_network: false });
  expect(toCreateBody(base)).not.toHaveProperty("base_url");
});

test("toUpdateBody keeps a key whose value is blank, so the server keeps the stored value", () => {
  expect(
    toUpdateBody({
      ...base,
      keys: [
        { name: "main", value: "" },
        { name: "new", value: " k2 " },
        { name: " ", value: "" },
      ],
    }).keys,
  ).toEqual([{ name: "main" }, { name: "new", value: "k2" }]);
});

test("fromProvider fills the draft with key names and no values", () => {
  const p: Provider = {
    name: "local",
    kind: "openai-compatible",
    base_url: "http://h",
    key_count: 1,
    status: "ok",
    keys: [{ name: "main" }],
  };
  expect(fromProvider(p)).toEqual({
    kind: "openai-compatible",
    type: "",
    name: "local",
    baseUrl: "http://h",
    allowPrivateNetwork: false,
    keys: [{ name: "main", value: "" }],
  });
});
