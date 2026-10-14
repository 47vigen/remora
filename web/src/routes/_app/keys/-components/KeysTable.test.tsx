import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { Key, StatsGroup } from "../../../../client";
import { KeysTable } from "./KeysTable";

// User-event interaction with React Aria is slow on a loaded machine; the default 5 s is too tight.
vi.setConfig({ testTimeout: 30_000 });

const summary = (requests: number): StatsGroup["summary"] => ({
  requests,
  errors: 0,
  error_rate: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  total_tokens: 0,
  cost_usd: 0,
});
const k1: Key = {
  id: "k1",
  name: "ci",
  allow: ["fast", "or/*", "cp/*", "free", "slow"],
  enabled: true,
  prefix: "sk-ab",
  created_at: "2026-10-14T09:00:00Z",
};
const k2: Key = {
  id: "k2",
  name: "laptop",
  allow: ["*"],
  enabled: false,
  prefix: "sk-cd",
  created_at: "2026-10-12T10:00:00Z",
};
const now = new Date("2026-10-14T10:00:00Z");

function setup() {
  const handlers = {
    onOpen: vi.fn<(k: Key) => void>(),
    onToggle: vi.fn<(k: Key, on: boolean) => void>(),
    onDelete: vi.fn<(k: Key) => void>(),
  };
  render(
    <KeysTable
      items={[k1, k2]}
      usage={new Map([["k1", { name: "k1", summary: summary(1234) }]])}
      now={now}
      {...handlers}
    />,
  );
  const [, row1, row2] = screen.getAllByRole("row");
  return { handlers, row1: row1!, row2: row2! };
}

test("rows show name, prefix, allowlist chips, enabled switch, created time and 24h requests", () => {
  const { row1, row2 } = setup();
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual([
    "Name",
    "Allowed",
    "Enabled",
    "Created",
    "Requests 24h",
    "Actions",
  ]);
  expect(row1).toHaveTextContent("ci");
  expect(row1).toHaveTextContent("sk-ab…");
  for (const p of ["fast", "or/*", "cp/*"]) expect(within(row1).getByText(p)).toBeInTheDocument();
  expect(within(row1).queryByText("free")).toBeNull();
  expect(within(row1).getByText("+2")).toBeInTheDocument();
  expect(within(row1).getByRole("switch", { name: "Enabled" })).toBeChecked();
  expect(row1).toHaveTextContent("1h ago");
  expect(row1).toHaveTextContent("1.23K");
  expect(within(row2).getByRole("switch", { name: "Enabled" })).not.toBeChecked();
  expect(row2).toHaveTextContent("2d ago");
  expect(within(row2).getAllByRole("gridcell")[3]).toHaveTextContent(/^0$/);
});

test("the row actions report the key", async () => {
  const { handlers, row1, row2 } = setup();
  await userEvent.click(within(row1).getByRole("button", { name: "ci" }));
  expect(handlers.onOpen).toHaveBeenCalledWith(k1);
  await userEvent.click(within(row2).getByRole("switch", { name: "Enabled" }));
  expect(handlers.onToggle).toHaveBeenCalledWith(k2, true);
  await userEvent.click(within(row2).getByRole("button", { name: "Delete laptop" }));
  expect(handlers.onDelete).toHaveBeenCalledWith(k2);
});
