import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { client } from "../../../client/client.gen";
import { renderRoute } from "../../../test/render-route";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

const local = {
  name: "local",
  kind: "openai-compatible",
  base_url: "http://h",
  key_count: 1,
  status: "ok",
  keys: [{ name: "main" }],
};

test("the list opens the editor from ?edit= and the row menu", async () => {
  const { router } = renderRoute("/providers", (_req, url) => {
    if (url.pathname === "/api/providers") return Response.json({ items: [local], total: 1 });
    if (url.pathname === "/api/accounts") return Response.json({ items: [], total: 0 });
  });
  const row = await screen.findByRole("article", { name: "local" });
  await userEvent.click(within(row).getByRole("button", { name: "Actions for local" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
  await waitFor(() => expect(router.state.location.search).toMatchObject({ edit: "local" }));
  expect(await screen.findByRole("heading", { name: "Edit local" })).toBeInTheDocument();
});

test("the detail page shows the facts and keys, edits in a drawer and returns to the list after delete", async () => {
  const { router, requests } = renderRoute("/providers/local", (req, url) => {
    if (url.pathname === "/api/providers/local" && req.method === "DELETE") return new Response(null, { status: 204 });
    if (url.pathname === "/api/providers/local") return Response.json(local);
    if (url.pathname === "/api/providers") return Response.json({ items: [], total: 0 });
    if (url.pathname === "/api/accounts") return Response.json({ items: [], total: 0 });
  });
  expect(await screen.findByRole("heading", { name: /local/ })).toBeInTheDocument();
  expect(within(screen.getByRole("main")).getByRole("link", { name: "Providers" })).toHaveAttribute(
    "href",
    "/providers",
  );
  expect(await screen.findByText("http://h")).toBeInTheDocument();
  expect(screen.getByText("main")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: "Edit" }));
  await waitFor(() => expect(router.state.location.search).toMatchObject({ edit: "1" }));
  expect(await screen.findByRole("heading", { name: "Edit local" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(router.state.location.search).not.toHaveProperty("edit"));

  await userEvent.click(screen.getByRole("button", { name: "Delete" }));
  await userEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/providers"));
  expect(requests.some((r) => r.method === "DELETE")).toBe(true);
});

test("a subscription provider sets its strategy and says when priorities do not apply", async () => {
  const { requests } = renderRoute("/providers/codex", async (req, url) => {
    if (url.pathname === "/api/accounts/strategies" && req.method === "GET")
      return Response.json({ items: [{ provider: "codex", strategy: "round-robin" }], total: 1 });
    if (url.pathname === "/api/accounts/strategies/codex" && req.method === "PUT") {
      const body = (await req.json()) as { strategy: string };
      return Response.json({ provider: "codex", ...body });
    }
    if (url.pathname.startsWith("/api/")) return Response.json({ items: [], total: 0 });
  });
  expect(await screen.findByText(/Account priorities do not apply/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: /Strategy/ }));
  await userEvent.click(await screen.findByRole("option", { name: /Fill first/ }));
  await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
  const put = requests.find((r) => r.method === "PUT")!;
  expect(new URL(put.url).pathname).toBe("/api/accounts/strategies/codex");
  expect(await put.json()).toEqual({ strategy: "fill-first" });
});

test("the status filter and provider chips use the same health labels", async () => {
  renderRoute("/providers", (_req, url) => {
    if (url.pathname === "/api/providers") {
      return Response.json({ items: [local, { ...local, name: "untested", status: "unknown" }], total: 2 });
    }
    if (url.pathname === "/api/accounts") return Response.json({ items: [], total: 0 });
  });
  expect(within(await screen.findByRole("article", { name: "local" })).getByText("Healthy")).toBeInTheDocument();
  expect(within(screen.getByRole("article", { name: "untested" })).getByText("Not tested")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: /Status/ }));
  for (const name of ["Healthy", "Degraded", "Down", "Not tested"]) {
    expect(await screen.findByRole("option", { name })).toBeInTheDocument();
  }
});

function pagedProviders(count: number) {
  return (_req: Request, url: URL) => {
    if (url.pathname === "/api/accounts") return Response.json({ items: [], total: 0 });
    if (url.pathname !== "/api/providers") return;
    const offset = Number(url.searchParams.get("offset") ?? 0);
    const limit = Number(url.searchParams.get("limit") ?? 50);
    const items = Array.from({ length: count }, (_, i) => ({ ...local, name: `upstream-${i + 1}` }));
    return Response.json({ items: items.slice(offset, offset + limit), total: count });
  };
}

test("pagination counts configured providers, not subscription connection cards", async () => {
  renderRoute("/providers", pagedProviders(3));
  expect(await screen.findByText("1–3 of 3")).toBeInTheDocument();
  expect(await screen.findByRole("heading", { name: "Subscriptions to connect" })).toBeInTheDocument();
});

test("the providers list requests the page beyond the old cap", async () => {
  const { requests } = renderRoute("/providers?page=9", pagedProviders(203));
  expect(await screen.findByRole("article", { name: "upstream-203" })).toBeInTheDocument();
  expect(screen.getByText("201–203 of 203")).toBeInTheDocument();
  const page = requests.map((r) => new URL(r.url)).find((u) => u.pathname === "/api/providers");
  expect(Object.fromEntries(page!.searchParams)).toMatchObject({ limit: "25", offset: "200" });
});

test("the command menu debounces server search and finds the 250th key", async () => {
  const searches: string[] = [];
  renderRoute("/providers", (req, url) => {
    if (url.pathname === "/api/keys") {
      const q = url.searchParams.get("q");
      if (q) searches.push(q);
      const items = q === "Client 250" ? [{ id: "key-250", name: "Client 250", prefix: "sk" }] : [];
      return Response.json({ items, total: items.length });
    }
    return (
      pagedProviders(0)(req, url) ??
      (url.pathname === "/api/aliases" ? Response.json({ items: [], total: 0 }) : undefined)
    );
  });
  await screen.findByRole("heading", { name: "Providers" });
  await userEvent.keyboard("{Meta>}k{/Meta}");
  const dialog = await screen.findByRole("dialog", { name: "Search" });
  await userEvent.type(within(dialog).getByPlaceholderText("Jump to a page, provider, alias or key"), "Client 250");
  expect(await within(dialog).findByRole("option", { name: /Client 250/ })).toHaveAttribute(
    "href",
    "/keys?key=key-250",
  );
  expect(searches).toEqual(["Client 250"]);
});
