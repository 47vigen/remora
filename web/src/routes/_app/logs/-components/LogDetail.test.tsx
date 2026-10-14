import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import "../../../../test/dom-shims";
import type { LogDetail as Detail, LogEntry } from "../../../../client";
import { LogDetail } from "./LogDetail";

vi.setConfig({ testTimeout: 30_000 });

const ZWNJ = "‌";
const attempt = (over: Partial<LogEntry>): LogEntry => ({
  id: "a",
  attempt: 1,
  time: "2026-10-14T10:00:00Z",
  provider: "or",
  model: "x",
  status: "success",
  tokens: { prompt: 0, completion: 0, total: 0 },
  stream: false,
  ...over,
});
const log: Detail = {
  ...attempt({
    id: "l2",
    attempt: 2,
    parent_id: "l1",
    provider: "cp",
    model: "y",
    alias: "fast",
    key: "k1",
    latency_ms: 950,
  }),
  tokens: { prompt: 1200, completion: 340, total: 1540 },
  cost_usd: 0.004,
  request: { messages: [{ role: "user", content: `می${ZWNJ}خواهم` }] },
  response: { choices: [{ message: { content: "سلام" } }] },
  fallback_path: [
    attempt({ id: "l1", status: "error", error: "429 Too Many Requests", latency_ms: 80 }),
    attempt({ id: "l2", attempt: 2, provider: "cp", model: "y", latency_ms: 950 }),
  ],
};

test("the overview shows who served the request and that it fell back", () => {
  render(<LogDetail log={log} isLoading={false} onClose={() => {}} />);
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("cp/y")).toBeInTheDocument();
  expect(within(dialog).getByText("fallback")).toBeInTheDocument();
  expect(within(dialog).getByText("fast")).toBeInTheDocument();
  expect(within(dialog).getByText("950ms")).toBeInTheDocument();
  expect(within(dialog).getByText("1.2K → 340")).toBeInTheDocument();
  expect(within(dialog).getByText("$0.0040")).toBeInTheDocument();
});

test("the fallback path lists attempts in order and marks the served one", async () => {
  render(<LogDetail log={log} isLoading={false} onClose={() => {}} />);
  await userEvent.click(screen.getByRole("tab", { name: "Fallback path" }));
  const items = within(screen.getByRole("tabpanel")).getAllByRole("listitem");
  expect(items).toHaveLength(2);
  expect(items[0]).toHaveTextContent("or/x");
  expect(items[0]).toHaveTextContent("429 Too Many Requests");
  expect(items[0]).not.toHaveTextContent("Served");
  expect(items[1]).toHaveTextContent("cp/y");
  expect(items[1]).toHaveTextContent("Served");
});

test("bodies render with dir=auto and keep Persian text", async () => {
  render(<LogDetail log={log} isLoading={false} onClose={() => {}} />);
  await userEvent.click(screen.getByRole("tab", { name: "Request" }));
  const req = within(screen.getByRole("tabpanel")).getByText((_, el) => el?.tagName === "PRE");
  expect(req).toHaveAttribute("dir", "auto");
  expect(req.textContent).toContain(`می${ZWNJ}خواهم`);
  await userEvent.click(screen.getByRole("tab", { name: "Response" }));
  const res = within(screen.getByRole("tabpanel")).getByText((_, el) => el?.tagName === "PRE");
  expect(res).toHaveAttribute("dir", "auto");
  expect(res.textContent).toContain("سلام");
});

test("a streamed response is shown raw with a note", async () => {
  const sse = `data: {"choices":[{"delta":{"content":"سلام"}}]}\n\ndata: [DONE]\n\n`;
  render(
    <LogDetail
      log={{ ...log, stream: true, response: sse as unknown as Detail["response"] }}
      isLoading={false}
      onClose={() => {}}
    />,
  );
  await userEvent.click(screen.getByRole("tab", { name: "Response" }));
  const panel = screen.getByRole("tabpanel");
  expect(within(panel).getByText("Streamed response, shown as received.")).toBeInTheDocument();
  expect(within(panel).getByText((_, el) => el?.tagName === "PRE").textContent).toBe(sse);
});
