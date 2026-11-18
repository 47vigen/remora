import { render, screen } from "@testing-library/react";

import "../../../../../test/dom-shims";
import { seriesRows } from "../../-lib/overview";
import { CHART_COLORS } from "../../../../../lib/chart";
import { LatencyChart } from "./LatencyChart";

test("the latency chart labels the peak bucket p95 and plots all p95 points inside its axes", () => {
  const rows = seriesRows(
    [4520, 12].map((p95, i) => ({
      time: `2026-01-01T0${i}:00:00Z`,
      requests: 1,
      errors: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      total_tokens: 0,
      cost_usd: 0,
      latency_p50_ms: 10,
      latency_p95_ms: p95,
    })),
  );
  const measure = vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 640, 240));
  const { container } = render(<LatencyChart rows={rows} />);
  measure.mockRestore();

  const plot = container.querySelector("svg defs clipPath rect");
  expect(plot).not.toBeNull();
  const top = Number(plot?.getAttribute("y"));
  const bottom = top + Number(plot?.getAttribute("height"));
  const dots = container.querySelectorAll(`circle.recharts-line-dot[fill="${CHART_COLORS.p95}"]`);
  expect(dots).toHaveLength(2);
  for (const dot of dots) {
    const y = Number(dot.getAttribute("cy"));
    expect(y).toBeGreaterThanOrEqual(top);
    expect(y).toBeLessThanOrEqual(bottom);
  }
  expect(screen.getByText("Peak bucket p95 4.52s")).toBeInTheDocument();
});
