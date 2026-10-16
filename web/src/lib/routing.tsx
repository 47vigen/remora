import { ArrowsRotateRight, BarsAscendingAlignLeft, Cup, ListOl, ScalesUnbalanced, Shuffle } from "@gravity-ui/icons";
import type { ComponentType, SVGProps } from "react";

import type { RoutingStrategy } from "../client/types.gen";

/** One glyph per routing strategy, shared by the alias and account pool pickers. */
export const STRATEGY_ICONS: Record<RoutingStrategy, ComponentType<SVGProps<SVGSVGElement>>> = {
  priority: ListOl,
  "fill-first": Cup,
  "round-robin": ArrowsRotateRight,
  random: Shuffle,
  p2c: ScalesUnbalanced,
  "least-used": BarsAscendingAlignLeft,
};

/** The strategy's glyph in a hairline square, sized to sit beside an option's label and description. */
export function StrategyIcon({ id }: { id: RoutingStrategy }) {
  const Icon = STRATEGY_ICONS[id];
  return (
    <span
      aria-hidden="true"
      className="bg-surface-secondary border-border text-muted inline-flex size-8 shrink-0 items-center justify-center rounded-sm border"
    >
      <Icon className="size-4" />
    </span>
  );
}

/** The strategy's glyph alone, for a select trigger beside the chosen strategy's name. */
export function StrategyValue({ id, text }: { id: RoutingStrategy; text: string }) {
  const Icon = STRATEGY_ICONS[id];
  return (
    <span className="flex min-w-0 items-center gap-2">
      <Icon aria-hidden="true" className="text-muted size-4 shrink-0" />
      <span className="truncate">{text}</span>
    </span>
  );
}
