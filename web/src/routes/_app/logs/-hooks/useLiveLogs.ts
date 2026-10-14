// Derived from maximhq/bifrost ui/hooks/useWebSocket.tsx (Apache-2.0); modified.
import { useEffect, useEffectEvent, useState } from "react";

import { type LogSummary, toLogSummary } from "../-lib/filters";
import type { LogEntry } from "../../../../client";
import {
  WS_CONNECTING,
  WS_OPEN,
  adoptSocket,
  clearReconnectTimer,
  getSocket,
  isReconnectEnabled,
  resetRetryCount,
  scheduleReconnect,
  setActiveConnect,
  setReconnectEnabled,
  setSocket,
  startHeartbeat,
  stopHeartbeat,
  stopWebSocket,
} from "../../../../lib/live-socket";

const PATH = "/api/logs/live";

function liveUrl(): string {
  return `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}${PATH}`;
}

// The socket, heartbeat and reconnect bookkeeping live in lib/live-socket so a
// close event that fires after this hook unmounted consults that shared state
// instead of a stale closure. Only the message listener and per-mount React
// state live here.
const listeners = new Set<(entry: LogEntry) => void>();

/**
 * Streams new log entries while `enabled`. The session cookie authenticates the socket; each text frame is one
 * LogEntry. Turning it off, or leaving the page, closes the socket.
 */
export function useLiveLogs(enabled: boolean, onLog: (l: LogSummary) => void): { isConnected: boolean } {
  const [isConnected, setIsConnected] = useState(false);
  const deliver = useEffectEvent((entry: LogEntry) => onLog(toLogSummary(entry)));

  useEffect(() => {
    if (!enabled) return;
    let mounted = true;
    // Set when this mount adopts a socket created by an earlier mount;
    // removes the open/close listeners that keep this mount's state honest.
    let detachAdopted: (() => void) | null = null;
    const listener = (entry: LogEntry) => deliver(entry);
    listeners.add(listener);

    const connect = () => {
      if (!mounted || !isReconnectEnabled()) {
        return;
      }
      const existing = getSocket();
      if (existing && (existing.readyState === WS_OPEN || existing.readyState === WS_CONNECTING)) {
        // Another mount created this socket; its onopen/onclose cannot
        // update this mount's state, so observe the socket directly.
        detachAdopted?.();
        detachAdopted = adoptSocket(existing, {
          onOpen: () => {
            if (mounted) setIsConnected(true);
          },
          onClose: () => {
            if (mounted) setIsConnected(false);
          },
        });
        if (existing.readyState === WS_OPEN) setIsConnected(true);
        return;
      }

      const ws = new WebSocket(liveUrl());
      setSocket(ws);

      ws.onopen = () => {
        if (getSocket() !== ws) return;
        if (mounted) setIsConnected(true);
        resetRetryCount(); // Reset retry count on successful connection
        clearReconnectTimer();
        // The heartbeat belongs to the socket, not to this mount.
        startHeartbeat(ws);
      };

      ws.onmessage = (event: MessageEvent) => {
        let entry: LogEntry;
        try {
          entry = JSON.parse(String(event.data)) as LogEntry;
        } catch {
          return; // Not a log frame.
        }
        if (typeof entry !== "object" || entry === null || typeof entry.id !== "string") return;
        listeners.forEach((handler) => handler(entry));
      };

      ws.onclose = () => {
        if (mounted) setIsConnected(false);
        if (getSocket() === ws) {
          stopHeartbeat();
          setSocket(null);
        }
        // Reconnect through the currently mounted hook (if any), not
        // through this closure, so an unmounted hook never revives the loop.
        scheduleReconnect();
      };

      ws.onerror = () => {
        if (mounted) setIsConnected(false);
        ws.close();
      };
    };

    // A fresh mount means the page wants live entries: re-arm reconnection
    // that an earlier stop switched off.
    setReconnectEnabled(true);
    setActiveConnect(connect);
    connect();

    return () => {
      mounted = false;
      listeners.delete(listener);
      setActiveConnect(null);
      clearReconnectTimer();
      detachAdopted?.();
      detachAdopted = null;
      // Live mode ended or the page was left: close the socket.
      stopWebSocket();
      setIsConnected(false);
    };
  }, [enabled]);

  return { isConnected };
}
