import { render, screen } from "@testing-library/react";

import { KeyUsage } from "./KeyUsage";

test("shows the last day's totals and links to the key's logs", () => {
  render(
    <KeyUsage
      isLoading={false}
      logsHref="/logs?keyId=k1"
      totals={{
        requests: 1234,
        errors: 12,
        error_rate: 0.0097,
        prompt_tokens: 1000,
        completion_tokens: 500,
        total_tokens: 1500,
        cost_usd: 0.004,
      }}
    />,
  );
  expect(screen.getByText("1.23K")).toBeInTheDocument();
  expect(screen.getByText("12 (1.0%)")).toBeInTheDocument();
  expect(screen.getByText("1.5K")).toBeInTheDocument();
  expect(screen.getByText("$0.0040")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "View this key's logs" })).toHaveAttribute("href", "/logs?keyId=k1");
});

test("no traffic reads as zero", () => {
  render(<KeyUsage isLoading={false} logsHref="/logs?keyId=k1" totals={undefined} />);
  expect(screen.getByText("No requests in the last 24 hours.")).toBeInTheDocument();
});
