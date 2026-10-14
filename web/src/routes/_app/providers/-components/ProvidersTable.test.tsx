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
  expect(within(codex).queryByRole("button", { name: "Test" })).toBeNull();
});

test("pressing Test calls onTest, and the row being tested shows a pending button", async () => {
  const h = setup("local");
  await userEvent.click(within(rowOf("openrouter")).getByRole("button", { name: "Test" }));
  expect(h.onTest).toHaveBeenCalledWith(items[0]);
  expect(h.onOpen).not.toHaveBeenCalled();
  expect(within(rowOf("local")).getByRole("button", { name: /Test/ })).toHaveAttribute("data-pending", "true");
});

test("pressing Delete calls onDelete", async () => {
  const h = setup();
  await userEvent.click(within(rowOf("decide")).getByRole("button", { name: "Delete" }));
  expect(h.onDelete).toHaveBeenCalledWith(items[2]);
});

test("pressing a row calls onOpen with the provider name", async () => {
  const h = setup();
  await userEvent.click(within(rowOf("openrouter")).getByText("2 keys"));
  expect(h.onOpen).toHaveBeenCalledWith("openrouter");
  await userEvent.click(within(rowOf("Codex")).getByText("3 accounts"));
  expect(h.onOpen).toHaveBeenCalledWith("codex");
});
