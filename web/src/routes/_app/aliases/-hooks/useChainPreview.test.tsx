import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";

import type { AliasDraft } from "../-lib/alias-form";
import { client } from "../../../../client/client.gen";
import { useChainPreview } from "./useChainPreview";

const draft: AliasDraft = {
  name: "fast",
  kind: "chat",
  members: ["or/x"],
  capabilities: [],
  contextWindow: null,
  strategy: "priority",
};

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

afterEach(() => {
  client.setConfig({ baseUrl: "", fetch: undefined });
});

test("compiles the draft once typing pauses and maps cooling targets", async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(async () =>
    Response.json({
      targets: [
        { provider: "or", model: "x" },
        { provider: "cp", model: "y", cooling_until: "2026-10-14T10:30:00Z" },
      ],
    }),
  );
  client.setConfig({ baseUrl: "http://localhost", fetch });
  const { result } = renderHook(() => useChainPreview(draft), { wrapper });
  expect(result.current.isLoading).toBe(true);
  await waitFor(() => expect(result.current.chain).toHaveLength(2));
  expect(result.current.chain?.[1]).toEqual({
    provider: "cp",
    model: "y",
    cooling: true,
    coolingUntil: "2026-10-14T10:30:00Z",
  });
  expect(fetch).toHaveBeenCalledTimes(1);
  const req = fetch.mock.calls[0]![0] as Request;
  expect(new URL(req.url).pathname).toBe("/api/aliases/compile");
  expect(await req.json()).toEqual({
    name: "fast",
    kind: "chat",
    members: ["or/x"],
    capabilities: [],
    strategy: "priority",
  });
});

test("a refused compile becomes the error message", async () => {
  client.setConfig({
    baseUrl: "http://localhost",
    fetch: async () =>
      Response.json(
        { error: { message: "Alias “a” has a cycle: a → b → a", type: "invalid_request", code: "invalid_request" } },
        { status: 400 },
      ),
  });
  const { result } = renderHook(() => useChainPreview(draft), { wrapper });
  await waitFor(() => expect(result.current.error).toBe("Alias “a” has a cycle: a → b → a"));
  expect(result.current.chain).toBeUndefined();
});

test("a draft without a name or members is not sent", () => {
  const fetch = vi.fn<typeof globalThis.fetch>();
  client.setConfig({ baseUrl: "http://localhost", fetch });
  const { result } = renderHook(() => useChainPreview({ ...draft, members: [] }), { wrapper });
  expect(result.current).toEqual({ chain: undefined, error: undefined, isLoading: false });
  expect(fetch).not.toHaveBeenCalled();
});
