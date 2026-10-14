import { render, screen } from "@testing-library/react";

import { StatusChip } from "./StatusChip";

test.each([
  ["ok", "success", "Ok"],
  ["success", "success", "Success"],
  ["active", "success", "Active"],
  ["degraded", "warning", "Degraded"],
  ["cooling", "warning", "Cooling"],
  ["down", "danger", "Down"],
  ["error", "danger", "Error"],
  ["disabled", "default", "Disabled"],
  ["unknown", "default", "Unknown"],
] as const)("%s uses HeroUI color %s", (status, color, label) => {
  const { container } = render(<StatusChip status={status} />);
  const chip = container.querySelector('[data-slot="chip"]');
  expect(chip).toHaveAttribute("data-color", color);
  expect(chip).toHaveTextContent(label);
});

test("cooling shows the time it ends", () => {
  render(<StatusChip status="cooling" until="2026-10-14T10:30:00Z" />);
  expect(screen.getByText(/until/)).toBeInTheDocument();
  expect(screen.getByText(/until/).closest("time") ?? screen.getByText(/until/)).toBeInTheDocument();
});

test("until is ignored for other states", () => {
  render(<StatusChip status="ok" until="2026-10-14T10:30:00Z" />);
  expect(screen.queryByText(/until/)).toBeNull();
});
