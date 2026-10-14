import { render, screen, within } from "@testing-library/react";

import { ChainPreview } from "./ChainPreview";

test("lists the compiled targets in order, primary first, with cooling marked", () => {
  render(
    <ChainPreview
      isLoading={false}
      chain={[
        { provider: "or", model: "x", cooling: false },
        { provider: "cp", model: "y", cooling: true, coolingUntil: "2026-10-14T10:30:00Z" },
      ]}
    />,
  );
  expect(screen.getByRole("heading", { name: "Compiled chain" })).toBeInTheDocument();
  const items = within(screen.getByRole("list")).getAllByRole("listitem");
  expect(items).toHaveLength(2);
  expect(items[0]).toHaveTextContent("1");
  expect(items[0]).toHaveTextContent("or/x");
  expect(items[0]).toHaveTextContent("Primary");
  expect(items[0]).not.toHaveTextContent("Cooling");
  expect(items[1]).toHaveTextContent("2");
  expect(items[1]).toHaveTextContent("cp/y");
  expect(items[1]).toHaveTextContent("Fallback");
  expect(items[1]).toHaveTextContent("Cooling");
});

test("a compile error is shown as an alert", () => {
  render(<ChainPreview isLoading={false} chain={undefined} error="Alias “a” has a cycle: a → b → a" />);
  expect(screen.getByRole("alert")).toHaveTextContent("Alias “a” has a cycle: a → b → a");
});

test("an empty draft explains what the preview shows", () => {
  render(<ChainPreview isLoading={false} chain={undefined} />);
  expect(screen.getByText("Name the alias and add members to see the targets it tries, in order.")).toBeInTheDocument();
});
