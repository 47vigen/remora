import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { client } from "../../../client/client.gen";
import { describeRequest, renderRoute } from "../../../test/render-route";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

const fast = { name: "fast", kind: "chat", members: ["or/x", "free"], capabilities: ["tools"], context_window: 8000 };
const list = { items: [fast], total: 1 };
const cooling = {
  items: [{ provider: "or", model: "x", until: "2026-10-14T10:30:00Z", reason: "429", aliases: ["fast"] }],
  total: 1,
};

test("the list sends paging and filters and shows chain and cooling counts", async () => {
  const { requests } = renderRoute("/aliases?page=2&pageSize=50&kind=chat&q=fa", (_req, url) => {
    if (url.pathname === "/api/aliases") return Response.json({ items: [fast], total: 51 });
    if (url.pathname === "/api/cooling") return Response.json(cooling);
    if (url.pathname === "/api/aliases/fast/chain")
      return Response.json({
        targets: [
          { provider: "or", model: "x" },
          { provider: "cp", model: "y" },
          { provider: "cp", model: "z" },
        ],
      });
  });
  const row = await screen.findByRole("row", { name: /fast/ });
  await waitFor(() => expect(within(row).getByText("3")).toBeInTheDocument());
  expect(within(row).getByText("1 cooling")).toBeInTheDocument();
  expect(requests.map(describeRequest)).toContain("GET /api/aliases?limit=50&offset=50&q=fa&kind=chat");
});

test("the editor loads the alias, previews the chain and saves the draft", async () => {
  const { requests, router } = renderRoute("/aliases/fast", async (req, url) => {
    if (url.pathname === "/api/aliases/fast" && req.method === "GET") return Response.json(fast);
    if (url.pathname === "/api/aliases/fast" && req.method === "PUT") return Response.json(await req.json());
    if (url.pathname === "/api/aliases") return Response.json(list);
    if (url.pathname === "/api/cooling") return Response.json(cooling);
    if (url.pathname === "/api/providers") return Response.json({ items: [], total: 0 });
    if (url.pathname === "/api/accounts") return Response.json({ items: [], total: 0 });
    if (url.pathname === "/api/aliases/compile")
      return Response.json({ targets: [{ provider: "or", model: "x", cooling_until: "2026-10-14T10:30:00Z" }] });
  });
  expect(await screen.findByRole("heading", { name: "fast" })).toBeInTheDocument();
  const preview = screen.getByRole("heading", { name: "Fallback chain" }).closest("section")!;
  await waitFor(() => expect(within(preview).getByText("Compiles to 1 target")).toBeInTheDocument());
  const nodes = within(preview).getAllByRole("listitem");
  expect(nodes.map((n) => n.textContent)).toEqual([expect.stringContaining("or/x"), expect.stringContaining("free")]);
  expect(nodes[0]).toHaveTextContent("Cooling");
  expect(nodes[1]).toHaveTextContent("Unresolved");
  expect(requests.filter((r) => r.url.endsWith("/api/aliases/compile"))).toHaveLength(1);
  expect(screen.queryByText("Unsaved changes")).toBeNull();
  const members = screen.getByRole("listbox", { name: "Members" });
  expect(
    within(members)
      .getAllByRole("option")
      .map((o) => o.textContent),
  ).toEqual([expect.stringContaining("or/x"), expect.stringContaining("free")]);
  expect(within(within(members).getAllByRole("option")[0]!).getByText("cooling")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: "Remove free" }));
  expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
  const put = requests.find((r) => r.method === "PUT")!;
  expect(describeRequest(put)).toBe("PUT /api/aliases/fast");
  expect(await put.json()).toEqual({ ...fast, members: ["or/x"], strategy: "priority" });
  await waitFor(() => expect(router.state.location.pathname).toBe("/aliases/fast"));
});

test("choosing a strategy saves it", async () => {
  const { requests } = renderRoute("/aliases/fast", async (req, url) => {
    if (url.pathname === "/api/aliases/fast" && req.method === "GET") return Response.json(fast);
    if (url.pathname === "/api/aliases/fast" && req.method === "PUT") return Response.json(await req.json());
    if (url.pathname === "/api/aliases") return Response.json(list);
    if (url.pathname === "/api/cooling") return Response.json(cooling);
    if (url.pathname === "/api/aliases/compile") return Response.json({ targets: [] });
    if (url.pathname.startsWith("/api/")) return Response.json({ items: [], total: 0 });
  });
  expect(await screen.findByRole("heading", { name: "fast" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: /Strategy/ }));
  await userEvent.click(await screen.findByRole("option", { name: /Least used/ }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(requests.some((r) => r.method === "PUT")).toBe(true));
  expect(await requests.find((r) => r.method === "PUT")!.json()).toMatchObject({ strategy: "least-used" });
});

test("a new alias with no members is not sent", async () => {
  const { requests } = renderRoute("/aliases/new", (_req, url) => {
    if (url.pathname === "/api/aliases") return Response.json(list);
    if (url.pathname.startsWith("/api/")) return Response.json({ items: [], total: 0 });
  });
  await userEvent.type(await screen.findByRole("textbox", { name: "Name" }), "slow");
  await userEvent.click(screen.getByRole("button", { name: "Create alias" }));
  expect(await screen.findByText("Add at least one member.")).toBeInTheDocument();
  expect(requests.some((r) => r.method === "POST")).toBe(false);
});
