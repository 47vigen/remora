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
  const row = await screen.findByRole("row", { name: /local/ });
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
