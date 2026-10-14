import { Card, Link } from "@heroui/react";

import type { Cooling } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";

export const COOLING_LIMIT = 10;

export function CoolingList({ items, total }: { items: Cooling[]; total: number }) {
  return (
    <Card>
      <Card.Header className="flex flex-row items-center justify-between">
        <Card.Title>Cooling members</Card.Title>
        {total > COOLING_LIMIT ? <Link href="/aliases?cooling=true">View all {total}</Link> : null}
      </Card.Header>
      <Card.Content>
        {items.length === 0 ? (
          <p className="text-muted text-sm">No alias member is cooling down.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {items.slice(0, COOLING_LIMIT).map((c) => (
              <li key={`${c.provider}/${c.model}`} className="flex flex-wrap items-center justify-between gap-2">
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-mono text-sm">
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
      </Card.Content>
    </Card>
  );
}
