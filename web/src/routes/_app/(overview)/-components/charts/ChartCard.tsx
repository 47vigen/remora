import { Card } from "@heroui/react";
import type { ReactNode } from "react";

/** A titled card with a fixed-height plot area; shows `emptyText` instead of the chart when there is no data. */
export function ChartCard({
  title,
  isEmpty,
  emptyText = "No data in this window.",
  children,
}: {
  title: string;
  isEmpty: boolean;
  emptyText?: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <Card.Header>
        <Card.Title>{title}</Card.Title>
      </Card.Header>
      <Card.Content className="h-56">
        {isEmpty ? <p className="text-muted flex h-full items-center justify-center text-sm">{emptyText}</p> : children}
      </Card.Content>
    </Card>
  );
}
