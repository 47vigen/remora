import { providerKinds, subscriptionRows } from "./providers-list";

test("a display group maps to all its provider kinds", () => {
  expect(providerKinds("api_key")).toEqual(["builtin", "openai-compatible"]);
  expect(providerKinds("decisions")).toEqual(["typesafe-compatible"]);
});

test("subscription connections match by name or label, outside the configured list", () => {
  expect(subscriptionRows({ q: "kimi ai" })).toEqual(["kimi-ai"]);
  expect(subscriptionRows({ q: "", group: "api_key" })).toEqual([]);
  expect(subscriptionRows({ q: "", status: "down" })).toEqual([]);
});
