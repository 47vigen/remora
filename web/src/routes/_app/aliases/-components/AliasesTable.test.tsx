import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { AliasesTable } from "./AliasesTable";

vi.setConfig({ testTimeout: 30_000 });

const items = [
  {
    name: "fast",
    kind: "chat" as const,
    members: ["or/x", "free", "cp/y", "cp/z", "or/w"],
    capabilities: [],
    strategy: "p2c" as const,
  },
  { name: "free", kind: "embeddings" as const, members: ["cp/y"], capabilities: [], strategy: "priority" as const },
];

test("shows name, kind, the chain inline, target count and cooling count", () => {
  render(
    <AliasesTable
      items={items}
      chainLength={new Map([["fast", 3]])}
      cooling={new Map([["fast", 2]])}
      onOpen={() => {}}
      onDelete={() => {}}
    />,
  );
  const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
  expect(headers).toEqual(["Name", "Kind", "Strategy", "Chain", "Targets", "Cooling", "Actions"]);
  const rows = screen.getAllByRole("row").slice(1);
  const cells = (row: HTMLElement) =>
    within(row)
      .getAllByRole("rowheader")
      .concat(within(row).getAllByRole("gridcell"))
      .slice(0, 6)
      .map((c) => c.textContent);
  expect(cells(rows[0]!)).toEqual(["fast", "chat", "Power of two", "or/x→free→cp/y+2", "3", "2 cooling"]);
  expect(cells(rows[1]!)).toEqual(["free", "embeddings", "Priority", "cp/y", "–", ""]);
  expect(within(rows[0]!).getByRole("link", { name: "fast" })).toHaveAttribute("href", "/aliases/fast");
});

test("the row menu opens or deletes the alias", async () => {
  const onOpen = vi.fn<(n: string) => void>();
  const onDelete = vi.fn<(n: string) => void>();
  render(
    <AliasesTable items={items} chainLength={new Map()} cooling={new Map()} onOpen={onOpen} onDelete={onDelete} />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Actions for free" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));
  expect(onDelete).toHaveBeenCalledWith("free");
  await userEvent.click(screen.getByRole("button", { name: "Actions for fast" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
  expect(onOpen).toHaveBeenCalledWith("fast");
});
