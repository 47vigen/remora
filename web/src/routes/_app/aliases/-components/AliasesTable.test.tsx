import { render, screen, within } from "@testing-library/react";

import { AliasesTable } from "./AliasesTable";

test("shows name, kind, member count, chain length and cooling count", () => {
  render(
    <AliasesTable
      items={[
        { name: "fast", kind: "chat", members: ["or/x", "free"], capabilities: [] },
        { name: "free", kind: "embeddings", members: ["cp/y"], capabilities: [] },
      ]}
      chainLength={new Map([["fast", 3]])}
      cooling={new Map([["fast", 2]])}
    />,
  );
  const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
  expect(headers).toEqual(["Name", "Kind", "Members", "Chain", "Cooling"]);
  const rows = screen.getAllByRole("row").slice(1);
  const fast = within(rows[0]!).getAllByRole("rowheader").concat(within(rows[0]!).getAllByRole("gridcell"));
  expect(fast.map((c) => c.textContent)).toEqual(["fast", "chat", "2", "3", "2 cooling"]);
  const free = within(rows[1]!).getAllByRole("rowheader").concat(within(rows[1]!).getAllByRole("gridcell"));
  expect(free.map((c) => c.textContent)).toEqual(["free", "embeddings", "1", "–", ""]);
  expect(within(rows[0]!).getByRole("link", { name: "fast" })).toHaveAttribute("href", "/aliases/fast");
});
