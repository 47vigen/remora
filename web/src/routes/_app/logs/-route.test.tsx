import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { client } from "../../../client/client.gen";
import { stopWebSocket } from "../../../lib/live-socket";
import { describeRequest, renderRoute } from "../../../test/render-route";

const findRow = async (text: string) => (await screen.findByText(text)).closest<HTMLElement>('[role="row"]')!;

class FakeSocket {
  static last: FakeSocket | undefined;
  readyState = 0;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(readonly url: string) {
    FakeSocket.last = this;
  }
  addEventListener() {}
  removeEventListener() {}
  send() {}
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
}

beforeEach(() => vi.stubGlobal("WebSocket", FakeSocket));
afterEach(() => {
  stopWebSocket();
  vi.unstubAllGlobals();
  client.setConfig({ baseUrl: "", fetch: undefined });
});

const entry = (id: string, over: object = {}) => ({
  id,
  attempt: 1,
  time: new Date().toISOString(),
  key: "k1",
  alias: "fast",
  provider: "or",
  model: "x",
  status: "success",
  latency_ms: 120,
  tokens: { prompt: 10, completion: 5, total: 15 },
  stream: false,
  ...over,
});

function api(req: Request, url: URL) {
  if (url.pathname === "/api/logs") return Response.json({ items: [entry("l1")], total: 1 });
  if (url.pathname === "/api/logs/l1")
    return Response.json({ ...entry("l1"), request: { messages: [] }, response: {}, fallback_path: [entry("l1")] });
  if (url.pathname === "/api/keys")
    return Response.json({
      items: [{ id: "k1", name: "ci", allow: [], enabled: true, prefix: "sk", created_at: "" }],
      total: 1,
    });
  if (
    url.pathname.startsWith("/api/") &&
    req.method === "GET" &&
    url.pathname !== "/api/auth/session" &&
    url.pathname !== "/api/system/version"
  )
    return Response.json({ items: [], total: 0 });
}

test("filters from the URL become the list query, with the time window", async () => {
  const { requests } = renderRoute("/logs?keyId=k1&status=error&servedModel=or/x&range=1h&q=fa", api);
  await findRow("or/x");
  const sent = requests.map(describeRequest).find((r) => r.startsWith("GET /api/logs?"))!;
  const params = new URL(`http://x${sent.slice(4)}`).searchParams;
  expect(Object.fromEntries(params)).toMatchObject({
    limit: "25",
    offset: "0",
    q: "fa",
    key: "k1",
    status: "error",
    model: "or/x",
  });
  expect(new Date(params.get("to")!).getTime() - new Date(params.get("from")!).getTime()).toBe(3_600_000);
});

test("a row opens the detail drawer through ?log=", async () => {
  const { router } = renderRoute("/logs", api);
  const row = await findRow("or/x");
  expect(within(row).getByText("ci")).toBeInTheDocument();
  await userEvent.click(row);
  await waitFor(() => expect(router.state.location.search).toMatchObject({ log: "l1" }));
  expect(await screen.findByRole("tab", { name: "Fallback path" })).toBeInTheDocument();
});

test("live mode prepends matching entries and hides paging", async () => {
  renderRoute("/logs?live=true&alias=fast", api);
  await findRow("or/x");
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  const ws = FakeSocket.last!;
  expect(ws.url).toMatch(/\/api\/logs\/live$/);
  act(() => {
    ws.readyState = 1;
    ws.onopen?.();
  });
  expect(await screen.findByText("Connected")).toBeInTheDocument();
  act(() => ws.onmessage?.({ data: JSON.stringify(entry("l9", { model: "new-model" })) }));
  act(() => ws.onmessage?.({ data: JSON.stringify(entry("l8", { alias: "slow", model: "skipped" })) }));
  const rows = await screen.findAllByRole("row");
  expect(rows[1]).toHaveTextContent("or/new-model");
  expect(screen.queryByText("or/skipped")).toBeNull();
});

test("live mode drops the old filter's rows when a filter changes", async () => {
  const { router } = renderRoute("/logs?live=true&alias=fast", (req, url) => {
    if (url.pathname === "/api/logs" && url.searchParams.get("alias") === "slow")
      return Response.json({ items: [entry("l5", { alias: "slow", model: "slow-model" })], total: 1 });
    return api(req, url);
  });
  await findRow("or/x");
  const ws = FakeSocket.last!;
  act(() => {
    ws.readyState = 1;
    ws.onopen?.();
  });
  act(() => ws.onmessage?.({ data: JSON.stringify(entry("l9", { model: "live-model" })) }));
  await findRow("or/live-model");
  act(() => router.history.push("/logs?live=true&alias=slow"));
  await findRow("or/slow-model");
  expect(screen.queryByText("or/live-model")).toBeNull();
  expect(screen.queryByText("or/x")).toBeNull();
});

test("a custom range shows the date picker", async () => {
  renderRoute("/logs?range=custom", api);
  expect(await screen.findByText("Dates")).toBeInTheDocument();
});

test("Clear drops every filter and the search", async () => {
  const { router } = renderRoute("/logs?status=error&alias=fast&range=1h&q=fa", api);
  await findRow("or/x");
  await userEvent.click(screen.getByRole("button", { name: "Clear" }));
  await waitFor(() => expect(router.state.location.search).toMatchObject({ q: "", range: "24h" }));
  expect(router.state.location.search).not.toHaveProperty("status");
  expect(router.state.location.search).not.toHaveProperty("alias");
});
