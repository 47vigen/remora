import { render, screen } from "@testing-library/react";

import { StatCards } from "./StatCards";

test("renders six labelled values", () => {
  const items = [
    ["Requests", "8K"],
    ["Error rate", "1.0%"],
    ["p50", "420ms"],
    ["p95", "2.60s"],
    ["Tokens", "1.5M"],
    ["Cost", "$1.23"],
  ] as const;
  render(
    <StatCards.Root>
      {items.map(([label, value]) => (
        <StatCards.Item key={label} label={label} value={value} />
      ))}
    </StatCards.Root>,
  );
  for (const [label, value] of items) {
    const term = screen.getByText(label, { selector: "dt" });
    expect(term.nextElementSibling).toHaveTextContent(value);
  }
});

test("an item shows its hint", () => {
  render(
    <StatCards.Root>
      <StatCards.Item label="Requests" value="8K" hint="last 24 hours" />
    </StatCards.Root>,
  );
  expect(screen.getByText("last 24 hours")).toBeInTheDocument();
});

test("shows the API timeout instead of stale figures", () => {
  const message = "The request log took too long to answer; try a shorter range.";
  render(
    <StatCards.Root error={{ error: { code: "engine_timeout", message } }}>
      <StatCards.Item label="Requests" value="8K" />
    </StatCards.Root>,
  );
  expect(screen.getByRole("alert")).toHaveTextContent(message);
  expect(screen.queryByText("8K")).not.toBeInTheDocument();
});
