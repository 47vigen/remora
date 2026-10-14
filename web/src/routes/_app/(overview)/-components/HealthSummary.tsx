import { Card, Link } from "@heroui/react";

import type { HealthCounts } from "../-lib/overview";
import { StatusChip } from "../../../../components/status-chip/StatusChip";

const PROBLEMS = [
  { status: "down", label: "down" },
  { status: "degraded", label: "degraded" },
  { status: "unknown", label: "not tested" },
] as const;

export function HealthSummary({ counts }: { counts: HealthCounts }) {
  return (
    <Card>
      <Card.Header>
        <Card.Title>Provider health</Card.Title>
      </Card.Header>
      <Card.Content>
        <ul className="flex flex-wrap items-center gap-4">
          <li className="flex items-center gap-2">
            <StatusChip status="ok" />
            <span>{counts.ok} healthy</span>
          </li>
          {PROBLEMS.filter(({ status }) => counts[status] > 0).map(({ status, label }) => (
            <li key={status} className="flex items-center gap-2">
              <StatusChip status={status} />
              <Link href={`/providers?status=${status}`}>
                {counts[status]} {label}
              </Link>
            </li>
          ))}
        </ul>
      </Card.Content>
    </Card>
  );
}
