import { cn, Surface } from "@heroui/react";
import type { ReactNode } from "react";

export type Tone = "success" | "danger" | "accent" | "warning";

const DOT: Record<Tone, string> = {
  success: "bg-success",
  danger: "bg-danger",
  accent: "bg-accent",
  warning: "bg-warning",
};

function Root({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <Surface className="rounded-lg px-3 py-2 shadow-lg">
      <div className="text-muted mb-1 text-xs">{title}</div>
      <div className="flex flex-col gap-1 text-sm">{children}</div>
    </Surface>
  );
}

function Row({ tone, label, value }: { tone?: Tone; label: string; value: ReactNode }) {
  return (
    <div className={cn("flex items-center justify-between gap-4", !tone && "border-border border-t pt-1")}>
      <span className="flex items-center gap-1.5">
        {tone ? <span className={cn("size-2 rounded-full", DOT[tone])} /> : null}
        <span className="text-muted">{label}</span>
      </span>
      <span className="font-medium tabular-nums">{value}</span>
    </div>
  );
}

export const ChartTooltip = Object.assign(Root, { Root, Row });
