import { cn } from "@heroui/react";

import { resolveProviderIconKey } from "../../../../components/provider-icon/icons";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { ICON_FOR_OAUTH } from "../../../../lib/providers";

const hasIcon = (type: string) =>
  Object.prototype.hasOwnProperty.call(ICON_FOR_OAUTH, type) || resolveProviderIconKey(type) !== undefined;

/** A provider's mark in a hairline square; its initial in mono when it has no icon. */
export function ProviderMark({ type, name, size = "sm" }: { type: string; name: string; size?: "sm" | "md" }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "bg-surface-secondary border-border text-muted inline-flex shrink-0 items-center justify-center rounded-sm border font-mono text-xs uppercase",
        size === "sm" ? "size-8" : "size-9",
      )}
    >
      {hasIcon(type) ? <ProviderIcon type={type} size={size === "sm" ? "xs" : "sm"} /> : name.charAt(0)}
    </span>
  );
}
