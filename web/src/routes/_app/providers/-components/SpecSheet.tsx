import { cn } from "@heroui/react";
import type { ReactNode } from "react";

/** The facts of one thing on a sheet split by hairlines, the way a spec table reads. Each fact draws its own ring
 * into the 1px gap, so a short last row leaves blank surface rather than a grey hole. */
function Root({ children }: { children: ReactNode }) {
  return (
    <dl className="bg-surface border-border grid grid-cols-[repeat(auto-fit,minmax(10rem,1fr))] gap-px overflow-hidden rounded-sm border">
      {children}
    </dl>
  );
}

function Item({ label, children, mono, wide }: { label: string; children: ReactNode; mono?: boolean; wide?: boolean }) {
  return (
    <div className={cn("bg-surface ring-border flex min-w-0 flex-col gap-1.5 p-4 ring-1", wide && "col-span-2")}>
      <dt className="label-caps text-muted">{label}</dt>
      <dd className={cn("truncate text-sm", mono && "font-mono text-[13px]")}>{children}</dd>
    </div>
  );
}

export const SpecSheet = Object.assign(Root, { Root, Item });
