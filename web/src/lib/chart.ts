// Derived from maximhq/bifrost ui/app/workspace/dashboard/utils/chartUtils.ts (Apache-2.0); modified.
// Format timestamp based on bucket size
export function formatTimestamp(timestamp: string, bucketSizeSeconds: number): string {
  const date = new Date(timestamp);

  if (bucketSizeSeconds >= 86400) {
    // Daily buckets: "Jan 20"
    return date.toLocaleDateString("en-US", { month: "short", day: "numeric" });
  } else if (bucketSizeSeconds >= 3600) {
    // Hourly buckets: "10:00"
    return date.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
  } else {
    // Sub-hourly: "10:15"
    return date.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
  }
}

// Format full timestamp for tooltip
export function formatFullTimestamp(timestamp: string): string {
  const date = new Date(timestamp);
  return date.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

// Format cost values
export function formatCost(cost: number): string {
  if (cost === 0) {
    return `$0`;
  }
  if (cost < 0.0001) return "<$0.0001";
  if (cost < 0.01) {
    return `$${cost.toFixed(4)}`;
  }
  return `$${cost.toFixed(2)}`;
}

// Format latency values
export function formatLatency(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${ms.toFixed(0)}ms`;
}

// Measures use the accent and its deep ink; status hues are kept for outcomes only.
export const CHART_COLORS = {
  success: "var(--accent)",
  error: "var(--danger)",
  promptTokens: "var(--accent)",
  completionTokens: "var(--ink-strong)",
  cost: "var(--accent)",
  p50: "var(--accent)",
  p95: "var(--ink-strong)",
  grid: "var(--separator)",
  axis: "var(--muted)",
} as const;

/** Axis tick text: the mono label voice at a size that stays legible. */
export const AXIS_TICK = { fontSize: 10.5, fill: "var(--muted)", fontFamily: "var(--font-mono)" } as const;
