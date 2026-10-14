import { formatChanges } from "./config-diff";

test("one line per change: +, ~ or -, then the kind and name", () => {
  expect(
    formatChanges([
      { op: "create", kind: "alias", name: "fast", fields: [] },
      { op: "update", kind: "provider", name: "or", fields: ["base_url", "keys"] },
      { op: "delete", kind: "key", name: "old", fields: [] },
    ]),
  ).toBe("+ alias fast\n~ provider or (base_url, keys)\n- key old");
  expect(formatChanges([])).toBe("");
});
