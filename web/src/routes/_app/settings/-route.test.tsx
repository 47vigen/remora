import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { client } from "../../../client/client.gen";
import { describeRequest, renderRoute } from "../../../test/render-route";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

const token = { id: "t1", name: "ci", prefix: "rt-ab", created_at: "2026-10-14T09:00:00Z" };

test("the password tab sends current and new", async () => {
  const { requests } = renderRoute("/settings", (req, url) => {
    if (url.pathname === "/api/settings/password" && req.method === "PUT") return new Response(null, { status: 204 });
  });
  await userEvent.type(await screen.findByLabelText("Current password"), "old-password");
  await userEvent.type(screen.getByLabelText("New password"), "a-long-new-password");
  await userEvent.type(screen.getByLabelText("Confirm new password"), "a-long-new-password");
  await userEvent.click(screen.getByRole("button", { name: "Change password" }));
  expect(await screen.findByText("Password changed.")).toBeInTheDocument();
  const put = requests.find((r) => r.method === "PUT")!;
  expect(await put.json()).toEqual({ current: "old-password", new: "a-long-new-password" });
});

test("a created token's value is shown once and kept nowhere else", async () => {
  const { queryClient, requests } = renderRoute("/settings?tab=tokens", async (req, url) => {
    if (url.pathname === "/api/settings/tokens" && req.method === "GET")
      return Response.json({ items: [token], total: 1 });
    if (url.pathname === "/api/settings/tokens" && req.method === "POST")
      return Response.json(
        { ...token, id: "t2", ...((await req.json()) as object), value: "rt-secret-456" },
        { status: 201 },
      );
  });
  expect(await screen.findByText("rt-ab…")).toBeInTheDocument();
  expect(requests.map(describeRequest)).toContain("GET /api/settings/tokens?limit=25&offset=0");
  await userEvent.click(screen.getByRole("button", { name: "New token" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "deploy");
  await userEvent.click(within(dialog).getByRole("button", { name: "Create token" }));
  expect(await screen.findByDisplayValue("rt-secret-456")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Done" }));
  await waitFor(() => expect(document.body.innerHTML).not.toContain("rt-secret-456"));
  const cached = [
    ...queryClient
      .getQueryCache()
      .getAll()
      .map((q) => q.state.data),
    ...queryClient
      .getMutationCache()
      .getAll()
      .map((m) => m.state.data),
  ];
  expect(JSON.stringify(cached)).not.toContain("rt-secret-456");
});

test("import reviews the dry run, then applies the same document", async () => {
  const yaml = "aliases:\n  - name: fast\n";
  const { requests } = renderRoute("/settings?tab=backup", (_req, url) => {
    if (url.pathname === "/api/config/plan")
      return Response.json({
        changes: [
          { op: "create", kind: "alias", name: "fast", fields: [] },
          { op: "delete", kind: "key", name: "old", fields: [] },
        ],
      });
    if (url.pathname === "/api/config/apply") return Response.json({ changes: [] });
  });
  await screen.findByRole("button", { name: "Import" });
  const input = document.querySelector<HTMLInputElement>('input[type="file"]')!;
  await userEvent.upload(input, new File([yaml], "remora.yaml", { type: "application/yaml" }));
  const pre = await screen.findByText((_, el) => el?.tagName === "PRE");
  expect(pre.textContent).toBe("+ alias fast\n- key old");
  await userEvent.click(screen.getByRole("button", { name: "Apply" }));
  expect(await screen.findByText("The configuration was applied.")).toBeInTheDocument();
  const sent = requests.filter((r) => r.url.includes("/api/config/"));
  expect(sent.map(describeRequest)).toEqual(["POST /api/config/plan", "POST /api/config/apply"]);
  for (const r of sent) expect(await r.json()).toEqual({ document: yaml, prune: true });
});

test("export downloads the YAML text", async () => {
  const createObjectURL = vi.fn<(b: Blob) => string>(() => "blob:x");
  URL.createObjectURL = createObjectURL;
  URL.revokeObjectURL = () => {};
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  renderRoute("/settings?tab=backup", (_req, url) => {
    if (url.pathname === "/api/config/export")
      return new Response("providers: []\n", { headers: { "Content-Type": "application/yaml" } });
  });
  await userEvent.click(await screen.findByRole("button", { name: "Export" }));
  await waitFor(() => expect(click).toHaveBeenCalled());
  expect(await createObjectURL.mock.calls[0]![0].text()).toBe("providers: []\n");
  click.mockRestore();
});

test("about lists versions and credits", async () => {
  renderRoute("/settings?tab=about", (_req, url) => {
    if (url.pathname === "/api/system/version")
      return Response.json({ remora: "0.1.0", engines: [{ name: "router", version: "v1" }] });
  });
  expect(await screen.findByText("router")).toBeInTheDocument();
  expect(screen.getByText(/Apache-2\.0/)).toBeInTheDocument();
});
