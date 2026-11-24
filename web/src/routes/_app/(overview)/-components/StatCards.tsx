import { Alert, cn } from "@heroui/react";
import type { ReactNode } from "react";

import { errorMessage } from "../../../../lib/api";

/** One sheet of figures split by hairlines, the way a spec table reads. */
function Root({ children, className, error }: { children?: ReactNode; className?: string; error?: unknown }) {
  if (error) {
    return (
      <Alert status="danger" role="alert" className={className}>
        <Alert.Indicator />
        <Alert.Content>
          <Alert.Title>Could not load the statistics</Alert.Title>
          <Alert.Description>{errorMessage(error)}</Alert.Description>
        </Alert.Content>
      </Alert>
    );
  }
  return (
    <dl
      className={cn(
        "bg-border border-border grid grid-cols-2 gap-px overflow-hidden rounded-sm border md:grid-cols-5 max-md:[&>:last-child:nth-child(odd)]:col-span-2",
        className,
      )}
    >
      {children}
    </dl>
  );
}

function Item({ label, value, hint, trend }: { label: string; value: string; hint?: string; trend?: ReactNode }) {
  return (
    <div className="bg-surface relative flex min-h-28 flex-col gap-2 p-4">
      <dt className="label-caps text-muted">{label}</dt>
      <dd className="text-[28px] leading-none font-medium tracking-[-0.03em] tabular-nums">{value}</dd>
      {hint || trend ? (
        <dd className="mt-auto flex flex-col gap-1">
          {hint ? <span className="text-muted text-xs">{hint}</span> : null}
          {trend}
        </dd>
      ) : null}
    </div>
  );
}

export const StatCards = Object.assign(Root, { Root, Item });
