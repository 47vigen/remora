import { act, renderHook } from "@testing-library/react";

import type { LogSummary } from "../-lib/filters";
import { getSocket, stopWebSocket } from "../../../../lib/live-socket";
import { useLiveLogs } from "./useLiveLogs";

class FakeSocket {
  static instances: FakeSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(readonly url: string) {
    FakeSocket.instances.push(this);
  }
  addEventListener() {}
  removeEventListener() {}
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
}

beforeEach(() => {
  FakeSocket.instances = [];
  vi.stubGlobal("WebSocket", FakeSocket);
});
afterEach(() => {
  stopWebSocket();
  vi.unstubAllGlobals();
});

const entry = {
  id: "l1",
  attempt: 1,
  time: "2026-10-14T10:00:00Z",
  provider: "or",
  model: "x",
  status: "success",
  tokens: { prompt: 1, completion: 2, total: 3 },
  stream: false,
};

test("connects to the live endpoint on this host, maps frames and reports the state", () => {
  const onLog = vi.fn<(l: LogSummary) => void>();
  const { result, unmount } = renderHook(() => useLiveLogs(true, onLog));
  const ws = FakeSocket.instances[0]!;
  expect(ws.url).toBe(`ws://${location.host}/api/logs/live`);
  expect(result.current.isConnected).toBe(false);
  act(() => ws.open());
  expect(result.current.isConnected).toBe(true);
  act(() => ws.onmessage?.({ data: JSON.stringify(entry) }));
  expect(onLog).toHaveBeenCalledWith(expect.objectContaining({ id: "l1", provider: "or", fallback: false }));
  act(() => ws.onmessage?.({ data: "not json" }));
  expect(onLog).toHaveBeenCalledTimes(1);
  unmount();
  expect(ws.readyState).toBe(3);
  expect(getSocket()).toBeNull();
});

test("nothing connects while disabled, and turning it off closes the socket", () => {
  const { rerender } = renderHook(({ on }) => useLiveLogs(on, () => {}), { initialProps: { on: false } });
  expect(FakeSocket.instances).toHaveLength(0);
  rerender({ on: true });
  expect(FakeSocket.instances).toHaveLength(1);
  rerender({ on: false });
  expect(FakeSocket.instances[0]!.readyState).toBe(3);
});

test("a dropped connection reconnects with backoff", () => {
  vi.useFakeTimers();
  try {
    const { result } = renderHook(() => useLiveLogs(true, () => {}));
    const first = FakeSocket.instances[0]!;
    act(() => first.open());
    act(() => first.close());
    expect(result.current.isConnected).toBe(false);
    act(() => vi.advanceTimersByTime(1000));
    expect(FakeSocket.instances).toHaveLength(2);
  } finally {
    vi.useRealTimers();
  }
});
