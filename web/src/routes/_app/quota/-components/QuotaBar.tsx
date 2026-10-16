import { cn, Label, Meter } from "@heroui/react";

import { type Level, level, percentLabel, windowLabel } from "../-lib/quota";
import type { QuotaWindow } from "../../../../client/types.gen";
import { formatRelative } from "../../../../lib/time";

const COLOR: Record<Level, "success" | "warning" | "danger" | "default"> = {
  ok: "success",
  warn: "warning",
  critical: "danger",
  unknown: "default",
};

/** One quota window: a meter colored by how much is left, muted when unknown, with its reset countdown. */
export function QuotaBar({ window: w, now }: { window: QuotaWindow; now: Date }) {
  const lvl = level(w.remaining);
  const label = `${windowLabel(w)} · ${percentLabel(w.remaining)}`;
  return (
    <Meter
      value={Math.round((w.remaining ?? 0) * 100)}
      valueLabel={label}
      color={COLOR[lvl]}
      size="sm"
      className={cn("min-w-40 flex-1", lvl === "unknown" && "opacity-60")}
    >
      <Label className="text-xs">{label}</Label>
      {w.resets_at ? (
        <span className="text-muted text-right text-xs">resets {formatRelative(w.resets_at, now)}</span>
      ) : null}
      <Meter.Track>
        <Meter.Fill />
      </Meter.Track>
    </Meter>
  );
}
