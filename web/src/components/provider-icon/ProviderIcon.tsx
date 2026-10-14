import { cn } from "@heroui/react";

import { ICON_FOR_OAUTH } from "../../lib/providers";
import { ProviderIcons, resolveProviderIconKey } from "./icons";

const SIZES = { xs: 16, sm: 20, md: 24 } as const;

/** The decorative mark of a provider type or subscription provider; nothing when there is none. */
export function ProviderIcon({
  type,
  size = "sm",
  className,
}: {
  type: string;
  size?: keyof typeof SIZES;
  className?: string;
}) {
  const borrowed = Object.prototype.hasOwnProperty.call(ICON_FOR_OAUTH, type) ? ICON_FOR_OAUTH[type] : undefined;
  const key = borrowed ?? resolveProviderIconKey(type);
  if (!key) return null;
  const Icon = ProviderIcons[key];
  // Decorative: the provider's name is always shown next to it, and some marks carry a <title>.
  return (
    <span aria-hidden="true" className={cn("inline-flex shrink-0", className)}>
      <Icon size={SIZES[size]} />
    </span>
  );
}
