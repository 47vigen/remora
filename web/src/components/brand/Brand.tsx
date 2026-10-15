import { cn } from "@heroui/react";

/** The mark: a small block riding on a long one, the way remora rides on the engines it runs. */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true" className={cn("size-5 shrink-0", className)}>
      <rect x="2" y="11" width="16" height="5" className="fill-foreground" />
      <rect x="5" y="6" width="8" height="3" className="fill-accent" />
      <rect x="15" y="6" width="3" height="3" className="fill-ink-faint" />
    </svg>
  );
}

export function Brand({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-2", className)}>
      <BrandMark />
      <span className="text-[15px] font-semibold tracking-[-0.02em]">remora</span>
    </span>
  );
}
