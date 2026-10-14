import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";

import { LOGIN_DEADLINE_MS, LOGIN_POLL_MS } from "../-lib/login";
import { client } from "../../../../client/client.gen";
import { useAccountLogin } from "./useAccountLogin";

type Call = { method: string; path: string; body?: unknown };

function fakeServer(routes: Record<string, () => unknown>) {
  const calls: Call[] = [];
  const fetch = vi.fn<typeof globalThis.fetch>(async (input) => {
    const req = input as Request;
    const path = new URL(req.url).pathname;
    const text = await req.text();
    calls.push({ method: req.method, path, body: text ? JSON.parse(text) : undefined });
    const route = routes[`${req.method} ${path}`];
    if (!route) return new Response(null, { status: 204 });
    return Response.json(route());
  });
  client.setConfig({ baseUrl: "http://localhost", fetch });
  return calls;
}

function setup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return renderHook(() => useAccountLogin("codex"), { wrapper });
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

test("redirect flow: start, paste the address, then poll until the account is signed in", async () => {
  let status = "wait";
  const calls = fakeServer({
    "POST /api/accounts/logins": () => ({ state: "s1", url: "https://auth.example/x" }),
    "GET /api/accounts/logins/s1": () => ({ status }),
  });
  const { result } = setup();
  act(() => result.current.start());
  await waitFor(() => expect(result.current.login).toMatchObject({ phase: "redirect", state: "s1" }));
  expect(calls[0]).toEqual({ method: "POST", path: "/api/accounts/logins", body: { provider: "codex" } });

  act(() => result.current.submitRedirect("http://localhost:1455/cb?code=abc&state=other"));
  expect(result.current.login).toMatchObject({ phase: "redirect", error: expect.stringContaining("different") });
  expect(calls).toHaveLength(1);

  act(() => result.current.submitRedirect(" http://localhost:1455/cb?code=abc&state=s1 "));
  await waitFor(() => expect(result.current.login).toEqual({ phase: "waiting", state: "s1" }));
  expect(calls[1]).toEqual({
    method: "POST",
    path: "/api/accounts/logins/s1/complete",
    body: { redirect_url: "http://localhost:1455/cb?code=abc&state=s1" },
  });

  status = "ok";
  await waitFor(() => expect(result.current.login).toEqual({ phase: "done" }), { timeout: LOGIN_POLL_MS * 3 });
});

test("device flow polls until cancelled, and cancelling stops the polling", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const calls = fakeServer({
    "POST /api/accounts/logins": () => ({ state: "d1", url: "https://auth.example/device", user_code: "ABCD-1234" }),
    "GET /api/accounts/logins/d1": () => ({ status: "wait" }),
  });
  const { result } = setup();
  act(() => result.current.start());
  await waitFor(() =>
    expect(result.current.login).toEqual({
      phase: "device",
      state: "d1",
      verificationUrl: "https://auth.example/device",
      userCode: "ABCD-1234",
    }),
  );
  await act(() => vi.advanceTimersByTimeAsync(LOGIN_POLL_MS * 2));
  const polls = () => calls.filter((c) => c.method === "GET").length;
  expect(polls()).toBeGreaterThanOrEqual(2);

  act(() => result.current.cancel());
  expect(result.current.login).toEqual({ phase: "idle" });
  await waitFor(() =>
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/accounts/logins/d1")).toBe(true),
  );
  const before = polls();
  await act(() => vi.advanceTimersByTimeAsync(LOGIN_POLL_MS * 3));
  expect(polls()).toBe(before);
});

test("polling gives up after the deadline", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  fakeServer({
    "POST /api/accounts/logins": () => ({ state: "d2", url: "https://auth.example/device", user_code: "C" }),
    "GET /api/accounts/logins/d2": () => ({ status: "wait" }),
  });
  const { result } = setup();
  act(() => result.current.start());
  await waitFor(() => expect(result.current.login.phase).toBe("device"));
  vi.setSystemTime(Date.now() + LOGIN_DEADLINE_MS + 1);
  await act(() => vi.advanceTimersByTimeAsync(LOGIN_POLL_MS));
  await waitFor(() =>
    expect(result.current.login).toEqual({ phase: "failed", message: "Sign-in timed out. Start again." }),
  );
});
