import { Chip, cn } from "@heroui/react";

export type Health =
  | "ok"
  | "degraded"
  | "down"
  | "unknown"
  | "active"
  | "cooling"
  | "disabled"
  | "error"
  | "success"
  | "suspended";

type ChipColor = "success" | "warning" | "danger" | "default";

const COLORS: Record<Health, ChipColor> = {
  ok: "success",
  success: "success",
  active: "success",
  degraded: "warning",
  cooling: "warning",
  down: "danger",
  error: "danger",
  suspended: "danger",
  disabled: "default",
  unknown: "default",
};

const DOT: Record<ChipColor, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  default: "bg-muted",
};

const timeFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });

export function StatusChip({ status, until }: { status: Health; until?: string }) {
  const color = COLORS[status];
  const label = status.charAt(0).toUpperCase() + status.slice(1);
  const end = status === "cooling" && until ? new Date(until) : undefined;
  return (
    <Chip color={color} variant="soft" size="sm" data-color={color} className="gap-1.5 pl-1.5">
      <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", DOT[color])} />
      <Chip.Label className="text-[11px]">
        {label}
        {end && !Number.isNaN(end.getTime()) ? (
          <>
            {" until "}
            <time dateTime={until}>{timeFormat.format(end)}</time>
          </>
        ) : null}
      </Chip.Label>
    </Chip>
  );
}
