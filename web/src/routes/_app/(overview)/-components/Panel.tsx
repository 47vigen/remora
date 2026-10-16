import { cn } from "@heroui/react";
import type { ReactNode } from "react";

/** Every overview tile: one header bar of the same height, the title in the label voice and an aside on the right. */
export function Panel({
  title,
  aside,
  className,
  children,
}: {
  title: ReactNode;
  aside?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section
      className={cn("bg-surface border-border flex min-w-0 flex-col overflow-hidden rounded-sm border", className)}
    >
      <header className="border-border flex h-12 shrink-0 items-center justify-between gap-3 border-b px-4">
        <h3 className="label-caps text-muted truncate">{title}</h3>
        {aside}
      </header>
      {children}
    </section>
  );
}
