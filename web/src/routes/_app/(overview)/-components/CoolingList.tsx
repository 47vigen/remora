import { Link } from "@heroui/react";

import type { Cooling } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";

export const COOLING_LIMIT = 10;

/** Alias members resting after repeated rate limits, with why and until when. */
export function CoolingList({ items, total }: { items: Cooling[]; total: number }) {
  return (
    <section className="bg-surface border-border flex flex-col gap-3 rounded-sm border p-4">
      <header className="flex items-center justify-between gap-3">
        <h3 className="label-caps text-muted">Cooling members</h3>
        {total > COOLING_LIMIT ? (
          <Link href="/aliases?cooling=true" className="text-xs">
            View all {total}
          </Link>
        ) : null}
      </header>
      {items.length === 0 ? (
        <p className="text-muted text-sm">No alias member is cooling down. Every fallback chain is at full strength.</p>
      ) : (
        <ul className="divide-separator flex flex-col divide-y">
          {items.slice(0, COOLING_LIMIT).map((c) => (
            <li key={`${c.provider}/${c.model}`} className="flex flex-wrap items-center justify-between gap-2 py-2">
              <span className="flex min-w-0 flex-col">
                <span className="truncate font-mono text-[13px]">
                  {c.provider}/{c.model}
                </span>
                <span className="text-muted truncate text-xs" dir="auto">
                  {c.reason}
                  {c.aliases.length > 0 ? ` · ${c.aliases.join(", ")}` : ""}
                </span>
              </span>
              <StatusChip status="cooling" until={c.until} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
