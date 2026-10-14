import { Card } from "@heroui/react";
import type { ReactNode } from "react";

function Root({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6">{children}</dl>;
}

function Item({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Card>
      <Card.Content className="flex flex-col gap-1">
        <dt className="text-muted text-sm">{label}</dt>
        <dd className="text-2xl font-semibold tabular-nums">{value}</dd>
        {hint ? <dd className="text-muted text-xs">{hint}</dd> : null}
      </Card.Content>
    </Card>
  );
}

export const StatCards = Object.assign(Root, { Root, Item });
