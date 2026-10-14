import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";

import { client } from "../client/client.gen";
import { routeTree } from "../routeTree.gen";

/** Answers one request; return undefined to fall through to the defaults (session, version) or a 404. */
export type FakeApi = (req: Request, url: URL) => Response | undefined | Promise<Response | undefined>;

/**
 * Renders the whole app at `path` against a fake API behind the generated client's fetch. Every request is
 * recorded in `requests`, so a test can assert what a page sent.
 */
export function renderRoute(path: string, api: FakeApi) {
  // The app layout follows the system theme; jsdom has no matchMedia.
  window.matchMedia ??= (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList;
  const requests: Request[] = [];
  const fetch = async (input: RequestInfo | URL): Promise<Response> => {
    const req = input as Request;
    requests.push(req.clone());
    const url = new URL(req.url);
    const answer = await api(req, url);
    if (answer) return answer;
    if (url.pathname === "/api/auth/session") return Response.json({ authenticated: true, via: "session" });
    if (url.pathname === "/api/system/version") return Response.json({ remora: "0.1.0", engines: [] });
    return Response.json({ error: { message: "not found", type: "not_found", code: "not_found" } }, { status: 404 });
  };
  client.setConfig({ baseUrl: "http://localhost", fetch });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  const view = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { ...view, router, queryClient, requests };
}

/** A request's method and path with its query, e.g. "GET /api/aliases?limit=25&offset=0". */
export const describeRequest = (r: Request) => {
  const u = new URL(r.url);
  return `${r.method} ${u.pathname}${u.search}`;
};
