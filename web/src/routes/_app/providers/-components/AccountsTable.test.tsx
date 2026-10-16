import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { Account } from "../../../../client/types.gen";
import { AccountsTable } from "./AccountsTable";

const future = new Date(Date.now() + 3_600_000).toISOString();
const items: Account[] = [
  {
    id: "1",
    provider: "codex",
    email: "one@example.com",
    enabled: true,
    priority: 0,
    status: "active",
    cooldown_until: future,
    quota: [{ model: "gpt-x", remaining_fraction: 0.7 }],
    models: ["a", "b", "c", "d", "e"],
  },
  {
    id: "2",
    provider: "codex",
    email: "two@example.com",
    enabled: true,
    priority: 0,
    status: "active",
    quota: [],
    models: ["a"],
  },
];

function setup() {
  const h = {
    onToggle: vi.fn<(a: Account, enabled: boolean) => void>(),
    onPriority: vi.fn<(a: Account, priority: number) => void>(),
    onResetCooldown: vi.fn<(a: Account) => void>(),
    onDelete: vi.fn<(a: Account) => void>(),
  };
  render(<AccountsTable items={items} {...h} />);
  return h;
}
const rowOf = (name: string) => screen.getByRole("row", { name: new RegExp(name) });

test("a cooling account shows its end time and can reset the cooldown; an active one cannot", async () => {
  const h = setup();
  const one = rowOf("one@example.com");
  expect(within(one).getByText(/Cooling until/)).toBeInTheDocument();
  await userEvent.click(within(one).getByRole("button", { name: "Actions for one@example.com" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "Reset cooldown" }));
  expect(h.onResetCooldown).toHaveBeenCalledWith(items[0]);
  await userEvent.click(within(rowOf("two@example.com")).getByRole("button", { name: "Actions for two@example.com" }));
  expect(await screen.findByRole("menuitem", { name: "Reset cooldown" })).toHaveAttribute("aria-disabled", "true");
});

test("quota renders a meter of the remaining share", () => {
  setup();
  const meter = within(rowOf("one@example.com")).getByRole("meter");
  expect(meter).toHaveAttribute("aria-valuenow", "70");
  expect(within(rowOf("one@example.com")).getByText("gpt-x: 70% left")).toBeInTheDocument();
});

test("more than 3 models render 3 chips and +N", () => {
  setup();
  const one = rowOf("one@example.com");
  expect(within(one).getByText("c")).toBeInTheDocument();
  expect(within(one).queryByText("d")).toBeNull();
  expect(within(one).getByText("+2")).toBeInTheDocument();
});

test("toggling the switch calls onToggle and deleting calls onDelete", async () => {
  const h = setup();
  await userEvent.click(within(rowOf("two@example.com")).getByRole("switch"));
  expect(h.onToggle).toHaveBeenCalledWith(items[1], false);
  await userEvent.click(within(rowOf("two@example.com")).getByRole("button", { name: "Actions for two@example.com" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));
  expect(h.onDelete).toHaveBeenCalledWith(items[1]);
});

test("the priority column shows each number and an edit calls onPriority", async () => {
  const h = setup();
  const field = within(rowOf("two@example.com")).getByRole("textbox", { name: "Priority of two@example.com" });
  expect(field).toHaveValue("0");
  await userEvent.clear(field);
  await userEvent.type(field, "5");
  await userEvent.tab();
  expect(h.onPriority).toHaveBeenCalledWith(items[1], 5);
});
