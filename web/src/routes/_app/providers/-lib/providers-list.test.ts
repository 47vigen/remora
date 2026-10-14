import type { Account, Provider } from "../../../../client/types.gen";
import { providerListRows } from "./providers-list";

const p = (name: string, kind: Provider["kind"], status: Provider["status"] = "ok"): Provider => ({
  name,
  kind,
  key_count: 1,
  status,
});
const providers = [p("openrouter", "builtin"), p("local", "openai-compatible", "down"), p("soniox", "soniox")];
const accounts = [{ provider: "codex" }, { provider: "codex" }, { provider: "claude" }] as Account[];
const all = { page: 1, pageSize: 25 as const, q: "" };

test("without filters, every subscription provider comes first with its account count, then the providers", () => {
  const r = providerListRows(providers, accounts, all);
  expect(r.total).toBe(8 + 3);
  expect(r.subscriptions.find((s) => s.provider === "codex")).toEqual({ provider: "codex", accounts: 2 });
  expect(r.subscriptions.find((s) => s.provider === "grok")).toEqual({ provider: "grok", accounts: 0 });
  expect(r.items.map((i) => i.name)).toEqual(["openrouter", "local", "soniox"]);
});

test("the group filter derives from kind; subscription shows only subscription rows", () => {
  expect(providerListRows(providers, accounts, { ...all, group: "api_key" })).toMatchObject({
    subscriptions: [],
    total: 2,
  });
  expect(providerListRows(providers, accounts, { ...all, group: "speech" }).items.map((i) => i.name)).toEqual([
    "soniox",
  ]);
  const subs = providerListRows(providers, accounts, { ...all, group: "subscription" });
  expect(subs.items).toEqual([]);
  expect(subs.total).toBe(8);
});

test("the status filter leaves out subscription rows, which have no provider status", () => {
  const r = providerListRows(providers, accounts, { ...all, status: "down" });
  expect(r.subscriptions).toEqual([]);
  expect(r.items.map((i) => i.name)).toEqual(["local"]);
});

test("the search matches subscription providers by name or label", () => {
  const r = providerListRows([], accounts, { ...all, q: "kimi ai" });
  expect(r.subscriptions.map((s) => s.provider)).toEqual(["kimi-ai"]);
});

test("pages over subscription rows and providers together", () => {
  const r = providerListRows(providers, accounts, { ...all, page: 2, pageSize: 25, q: "" });
  expect(r.subscriptions).toEqual([]);
  expect(r.items).toEqual([]);
  const small = providerListRows(providers, accounts, { page: 2, pageSize: 25, q: "" });
  expect(small.total).toBe(11);
});
