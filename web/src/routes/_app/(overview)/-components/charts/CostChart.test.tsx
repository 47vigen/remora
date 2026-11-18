import { render, screen } from "@testing-library/react";

import { formatCurrencyNumber } from "../../../../../lib/numbers";
import { CostChart } from "./CostChart";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

test.each([[0], [2]])("empty cost with %i buckets draws a faded baseline on the actual axes", (buckets) => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return this.classList.contains("recharts-responsive-container") ? new DOMRect(0, 0, 480, 200) : new DOMRect();
  });

  const rows = Array.from({ length: buckets }, (_, index) => ({
    index,
    time: "2026-01-01T00:00:00Z",
    label: `00:0${index}`,
    promptTokens: 0,
    completionTokens: 0,
    cost: 0,
  }));
  const { container } = render(<CostChart rows={rows} />);
  expect(screen.getByText("No data in this window.")).toBeVisible();
  const zeroTick = [...container.querySelectorAll(".recharts-yAxis-tick-labels text")].find(
    (tick) => tick.textContent === formatCurrencyNumber(0),
  );
  const line = container.querySelector("line.recharts-reference-line-line");
  expect(zeroTick).toBeInTheDocument();
  expect(container.querySelector(".recharts-xAxis")).toBeInTheDocument();
  expect(line).toHaveAttribute("y1", zeroTick?.getAttribute("y"));
  expect(line?.getAttribute("y1")).toBe(line?.getAttribute("y2"));
  expect(Number(line?.getAttribute("x2"))).toBeGreaterThan(Number(line?.getAttribute("x1")));
  expect(line).toHaveAttribute("stroke-opacity", "0.4");
  expect(line?.closest('[aria-hidden="true"]')).not.toBeNull();
});
