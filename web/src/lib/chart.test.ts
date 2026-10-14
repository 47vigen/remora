import { CHART_COLORS, formatCost, formatLatency } from "./chart";

test("chart colours are HeroUI variables only", () => {
  for (const v of Object.values(CHART_COLORS))
    expect(v).toMatch(/^var\(--(accent|success|danger|warning|muted|border)\)$/);
});

test("formatters", () => {
  expect(formatLatency(950)).toBe("950ms");
  expect(formatLatency(1500)).toBe("1.50s");
  expect(formatCost(0)).toBe("$0");
  expect(formatCost(0.004)).toBe("$0.0040");
});
