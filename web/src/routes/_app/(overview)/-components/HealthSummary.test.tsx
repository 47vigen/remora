import { render, screen } from "@testing-library/react";

import { HealthSummary } from "./HealthSummary";

test("shows healthy and down counts and links to the down providers", () => {
  render(<HealthSummary counts={{ ok: 3, degraded: 0, down: 1, unknown: 0 }} />);
  expect(screen.getByText("3 healthy")).toBeInTheDocument();
  expect(screen.getByText("1 down")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /1 down/ })).toHaveAttribute("href", "/providers?status=down");
});

test("hides a zero count of problems", () => {
  render(<HealthSummary counts={{ ok: 2, degraded: 0, down: 0, unknown: 0 }} />);
  expect(screen.getByText("2 healthy")).toBeInTheDocument();
  expect(screen.queryByText(/down/)).toBeNull();
});
