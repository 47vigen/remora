import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { Provider } from "../../../../client/types.gen";
import { ProviderCards } from "./ProviderCards";

const items: Provider[] = [
  { name: "openrouter", kind: "builtin", type: "openrouter", key_count: 2, status: "ok" },
  { name: "local", kind: "openai-compatible", base_url: "http://h", key_count: 1, status: "down" },
  { name: "decide", kind: "typesafe-compatible", base_url: "https://d", key_count: 1, status: "unknown" },
  { name: "soniox", kind: "soniox", key_count: 1, status: "ok" },
];
const subscriptions = [
  { provider: "codex", accounts: 3 },
  { provider: "kimi", accounts: 0 },
];

function setup(testing?: string) {
  const handlers = {
    onTest: vi.fn<(p: Provider) => void>(),
    onEdit: vi.fn<(p: Provider) => void>(),
    onDelete: vi.fn<(p: Provider) => void>(),
  };
  render(<ProviderCards items={items} subscriptions={subscriptions} testing={testing} {...handlers} />);
  return handlers;
}

const card = (name: string) => screen.getByRole("article", { name });

test("one card per provider with its display group, and one per subscription provider with its account count", () => {
  setup();
  expect(within(card("openrouter")).getByText("API key")).toBeInTheDocument();
  expect(within(card("local")).getByText("API key")).toBeInTheDocument();
  expect(within(card("decide")).getByText("Decisions")).toBeInTheDocument();
  expect(within(card("soniox")).getByText("Speech-to-text")).toBeInTheDocument();
  const codex = card("Codex");
  expect(within(codex).getByText("3 accounts")).toBeInTheDocument();
  expect(within(codex).queryByRole("button", { name: /Actions/ })).toBeNull();
});

test("a card shows its target under the name and links to the provider", () => {
  setup();
  expect(card("openrouter")).toHaveTextContent("OpenRouter");
  expect(within(card("local")).getByText("http://h")).toBeInTheDocument();
  expect(within(card("local")).getByRole("link", { name: "local" })).toHaveAttribute("href", "/providers/local");
  expect(within(card("Codex")).getByRole("link", { name: "Codex" })).toHaveAttribute("href", "/providers/codex");
});

test("subscriptions without accounts sit apart, inviting a connection", () => {
  setup();
  expect(screen.getByText("Subscriptions to connect")).toBeInTheDocument();
  expect(within(card("Kimi")).getByText("Connect")).toBeInTheDocument();
});

const choose = async (name: string, action: string) => {
  await userEvent.click(within(card(name)).getByRole("button", { name: `Actions for ${name}` }));
  await userEvent.click(await screen.findByRole("menuitem", { name: action }));
};

test("the card menu tests, edits and deletes; the card being tested shows a pending menu", async () => {
  const h = setup("local");
  await choose("openrouter", "Test");
  expect(h.onTest).toHaveBeenCalledWith(items[0]);
  await choose("soniox", "Edit");
  expect(h.onEdit).toHaveBeenCalledWith(items[3]);
  await choose("decide", "Delete");
  expect(h.onDelete).toHaveBeenCalledWith(items[2]);
  expect(within(card("local")).getByRole("button", { name: "Actions for local" })).toHaveAttribute(
    "data-pending",
    "true",
  );
});
