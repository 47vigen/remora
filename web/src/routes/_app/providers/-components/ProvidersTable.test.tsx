import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { Provider } from "../../../../client/types.gen";
import { ProvidersTable } from "./ProvidersTable";

const items: Provider[] = [
  { name: "openrouter", kind: "builtin", type: "openrouter", key_count: 2, status: "ok" },
  { name: "local", kind: "openai-compatible", base_url: "http://h", key_count: 1, status: "down" },
  { name: "decide", kind: "typesafe-compatible", base_url: "https://d", key_count: 1, status: "unknown" },
  { name: "soniox", kind: "soniox", key_count: 1, status: "ok" },
];
const subscriptions = [{ provider: "codex", accounts: 3 }];

function setup(testing?: string) {
  const handlers = {
    onOpen: vi.fn<(name: string) => void>(),
    onTest: vi.fn<(p: Provider) => void>(),
    onEdit: vi.fn<(p: Provider) => void>(),
    onDelete: vi.fn<(p: Provider) => void>(),
  };
  render(<ProvidersTable items={items} subscriptions={subscriptions} testing={testing} {...handlers} />);
  return handlers;
}

const rowOf = (name: string) => screen.getByRole("row", { name: new RegExp(name) });

test("one row per provider with its display group, and one per subscription provider with its account count", () => {
  setup();
  expect(within(rowOf("openrouter")).getByText("API key")).toBeInTheDocument();
  expect(within(rowOf("local")).getByText("API key")).toBeInTheDocument();
  expect(within(rowOf("decide")).getByText("Decisions")).toBeInTheDocument();
  expect(within(rowOf("soniox")).getByText("Speech-to-text")).toBeInTheDocument();
  const codex = rowOf("Codex");
  expect(within(codex).getByText("Subscription")).toBeInTheDocument();
  expect(within(codex).getByText("3 accounts")).toBeInTheDocument();
  expect(within(codex).queryByRole("button", { name: /Actions/ })).toBeNull();
});

test("a provider row shows its target under the name", () => {
  setup();
  expect(rowOf("openrouter")).toHaveTextContent("OpenRouter");
  expect(within(rowOf("local")).getByText("http://h")).toBeInTheDocument();
});

const choose = async (row: string, action: string) => {
  await userEvent.click(within(rowOf(row)).getByRole("button", { name: `Actions for ${row}` }));
  await userEvent.click(await screen.findByRole("menuitem", { name: action }));
};

test("the row menu tests, edits and deletes without opening the row; the row being tested shows a pending menu", async () => {
  const h = setup("local");
  await choose("openrouter", "Test");
  expect(h.onTest).toHaveBeenCalledWith(items[0]);
  await choose("soniox", "Edit");
  expect(h.onEdit).toHaveBeenCalledWith(items[3]);
  await choose("decide", "Delete");
  expect(h.onDelete).toHaveBeenCalledWith(items[2]);
  expect(h.onOpen).not.toHaveBeenCalled();
  expect(within(rowOf("local")).getByRole("button", { name: "Actions for local" })).toHaveAttribute(
    "data-pending",
    "true",
  );
});

test("pressing a row calls onOpen with the provider name", async () => {
  const h = setup();
  await userEvent.click(within(rowOf("openrouter")).getByText("2 keys"));
  expect(h.onOpen).toHaveBeenCalledWith("openrouter");
  await userEvent.click(within(rowOf("Codex")).getByText("3 accounts"));
  expect(h.onOpen).toHaveBeenCalledWith("codex");
});
