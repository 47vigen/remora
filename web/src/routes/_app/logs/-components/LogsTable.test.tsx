import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { LogSummary } from "../-lib/filters";
import { LogsTable } from "./LogsTable";

vi.setConfig({ testTimeout: 30_000 });

const row = (over: Partial<LogSummary>): LogSummary => ({
  id: "l1",
  ts: "2026-10-14T09:59:48Z",
  status: "success",
  provider: "or",
  model: "x",
  alias: "fast",
  keyName: "ci",
  latencyMs: 950,
  tokens: { prompt: 1200, completion: 340, total: 1540 },
  costUsd: 0.004,
  stream: false,
  fallback: false,
  ...over,
});

test("rows show the request, and a fallback is marked", () => {
  render(
    <LogsTable
      items={[row({}), row({ id: "l2", fallback: true, status: "error" })]}
      now={new Date("2026-10-14T10:00:00Z")}
      onOpen={() => {}}
    />,
  );
  const [, first, second] = screen.getAllByRole("row");
  expect(first).toHaveTextContent("12s ago");
  expect(first).toHaveTextContent("fast");
  expect(first).toHaveTextContent("or/x");
  expect(first).toHaveTextContent("1.2K → 340");
  expect(first).toHaveTextContent("950ms");
  expect(first).toHaveTextContent("$0.0040");
  expect(within(first!).queryByText("fallback")).toBeNull();
  expect(within(second!).getByText("fallback")).toBeInTheDocument();
  expect(within(second!).getByText("Error")).toBeInTheDocument();
});

test("pressing a row opens it", async () => {
  const onOpen = vi.fn<(id: string) => void>();
  render(<LogsTable items={[row({}), row({ id: "l2" })]} now={new Date("2026-10-14T10:00:00Z")} onOpen={onOpen} />);
  await userEvent.click(screen.getAllByRole("row")[2]!);
  expect(onOpen).toHaveBeenCalledWith("l2");
});
