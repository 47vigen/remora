import { render, screen, within } from "@testing-library/react";

import { ChainPreview } from "./ChainPreview";

test("draws the chain in order, the first model not cooling as serving, nested aliases under their name", () => {
  render(
    <ChainPreview
      isLoading={false}
      targets={2}
      nodes={[
        { ref: "cp/y", depth: 0, kind: "model", position: 1, coolingUntil: "2026-10-14T10:30:00Z" },
        { ref: "free", depth: 0, kind: "alias" },
        { ref: "or/x", depth: 1, kind: "model", position: 2, serving: true },
        { ref: "ghost", depth: 0, kind: "alias", problem: "unknown" },
      ]}
    />,
  );
  expect(screen.getByRole("heading", { name: "Fallback chain" })).toBeInTheDocument();
  const items = within(screen.getByRole("list")).getAllByRole("listitem");
  expect(items).toHaveLength(4);
  expect(items[0]).toHaveTextContent("1");
  expect(items[0]).toHaveTextContent("cp/y");
  expect(items[0]).toHaveTextContent("Cooling");
  expect(items[0]).not.toHaveTextContent("Serving");
  expect(items[1]).toHaveTextContent("free");
  expect(items[1]).toHaveTextContent("Alias");
  expect(items[2]).toHaveTextContent("or/x");
  expect(items[2]).toHaveTextContent("Serving");
  expect(items[2]).toHaveClass("pl-5");
  expect(items[3]).toHaveTextContent("Unresolved");
  expect(screen.getByText("Compiles to 2 targets")).toBeInTheDocument();
});

test("a compile error is shown as an alert", () => {
  render(<ChainPreview isLoading={false} nodes={[]} error="Alias “a” has a cycle: a → b → a" />);
  expect(screen.getByRole("alert")).toHaveTextContent("Alias “a” has a cycle: a → b → a");
});

test("an empty draft explains what the preview shows", () => {
  render(<ChainPreview isLoading={false} nodes={[]} />);
  expect(screen.getByText("Name the alias and add members to see the targets it tries, in order.")).toBeInTheDocument();
});
