import { render, screen } from "@testing-library/react";

import { Attention } from "./Attention";

test("the health card identifies providers counted as healthy from traffic", () => {
  const health = { ok: 3, degraded: 0, down: 0, unknown: 0, fromTraffic: 2 };
  render(<Attention health={health} pool={[]} quota={[]} cooling={[]} coolingTotal={0} now={new Date(0)} />);

  expect(screen.getByText("Providers healthy").parentElement).toHaveTextContent("3/3");
  expect(screen.getByText("2 from traffic")).toBeInTheDocument();
});
