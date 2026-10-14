import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { client } from "../../../client/client.gen";
import { describeRequest, renderRoute } from "../../../test/render-route";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

const k1 = {
  id: "k1",
  name: "ci",
  allow: ["fast"],
  enabled: true,
  prefix: "sk-ab",
  created_at: "2026-10-14T09:00:00Z",
};
const stats = (requests: number) => ({
  from: "",
  to: "",
  series: [],
  summary: { requests, errors: 0, error_rate: 0, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, cost_usd: 0 },
  groups: [
    {
      name: "k1",
      summary: {
        requests,
        errors: 0,
        error_rate: 0,
        prompt_tokens: 0,
        completion_tokens: 0,
        total_tokens: 0,
        cost_usd: 0,
      },
    },
  ],
});

function api(onCreate?: (body: unknown) => void) {
  return async (req: Request, url: URL) => {
    if (url.pathname === "/api/keys" && req.method === "GET") return Response.json({ items: [k1], total: 1 });
    if (url.pathname === "/api/keys" && req.method === "POST") {
      const body = (await req.json()) as { name: string; allow: string[] };
      onCreate?.(body);
      return Response.json({ ...k1, id: "k2", ...body, value: "sk-secret-value-123" }, { status: 201 });
    }
    if (url.pathname === "/api/keys/k1" && req.method === "GET") return Response.json(k1);
    if (url.pathname === "/api/keys/k1" && req.method === "PATCH")
      return Response.json({ ...k1, ...((await req.json()) as object) });
    if (url.pathname === "/api/keys/k1/usage") return Response.json(stats(7));
    if (url.pathname === "/api/stats") return Response.json(stats(42));
    if (url.pathname === "/api/aliases")
      return Response.json({ items: [{ name: "fast", kind: "chat", members: ["or/x"], capabilities: [] }], total: 1 });
    if (url.pathname.startsWith("/api/")) return Response.json({ items: [], total: 0 });
  };
}

test("the list shows 24h requests from the grouped stats", async () => {
  const { requests } = renderRoute("/keys", api());
  const row = await screen.findByRole("row", { name: /ci/ });
  await waitFor(() => expect(row).toHaveTextContent("42"));
  expect(requests.map(describeRequest).some((r) => r.startsWith("GET /api/stats?") && r.includes("group_by=key"))).toBe(
    true,
  );
});

test("a created key's value is shown once and kept nowhere else", async () => {
  const onCreate = vi.fn<(body: unknown) => void>();
  const { queryClient, router } = renderRoute("/keys", api(onCreate));
  await userEvent.click(await screen.findByRole("button", { name: "New key" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "laptop");
  await userEvent.type(within(dialog).getByRole("combobox", { name: "Allow" }), "fast{Enter}");
  await userEvent.click(within(dialog).getByRole("button", { name: "Create key" }));
  expect(onCreate).toHaveBeenCalledWith({ name: "laptop", allow: ["fast"] });

  expect(await screen.findByDisplayValue("sk-secret-value-123")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Done" }));
  await waitFor(() => expect(screen.queryByDisplayValue("sk-secret-value-123")).toBeNull());
  expect(document.body.innerHTML).not.toContain("sk-secret-value-123");
  expect(
    JSON.stringify(
      queryClient
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data),
    ),
  ).not.toContain("sk-secret-value-123");
  expect(
    JSON.stringify(
      queryClient
        .getMutationCache()
        .getAll()
        .map((m) => m.state.data),
    ),
  ).not.toContain("sk-secret-value-123");
  expect(router.state.location.href).not.toContain("sk-secret");
});

test("the switch enables or disables a key", async () => {
  const { requests } = renderRoute("/keys", api());
  const row = await screen.findByRole("row", { name: /ci/ });
  await userEvent.click(within(row).getByRole("switch", { name: "Enabled" }));
  await waitFor(() => expect(requests.some((r) => r.method === "PATCH")).toBe(true));
  const patch = requests.find((r) => r.method === "PATCH")!;
  expect(describeRequest(patch)).toBe("PATCH /api/keys/k1");
  expect(await patch.json()).toEqual({ enabled: false });
});

test("?key= opens the drawer with the allowlist and usage", async () => {
  const { requests } = renderRoute("/keys?key=k1", api());
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByRole("textbox", { name: "Name" })).toHaveValue("ci");
  expect(within(dialog).getByText("Matches 1: fast")).toBeInTheDocument();
  expect(await within(dialog).findByText("7")).toBeInTheDocument();
  expect(within(dialog).getByRole("link", { name: "View this key's logs" })).toHaveAttribute("href", "/logs?keyId=k1");
  await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(requests.some((r) => r.method === "PATCH")).toBe(true));
  expect(await requests.find((r) => r.method === "PATCH")!.json()).toEqual({ name: "ci", allow: ["fast"] });
});
