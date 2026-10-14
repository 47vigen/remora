import type { Change } from "../../../../client";

const OPS: Record<Change["op"], string> = { create: "+", update: "~", delete: "-" };

/** The import dry run as text: one line per change, "+", "~" or "-", then the kind and name (and changed fields). */
export function formatChanges(changes: Change[]): string {
  return changes
    .map(
      (c) =>
        `${OPS[c.op]} ${c.kind} ${c.name}${c.op === "update" && c.fields.length > 0 ? ` (${c.fields.join(", ")})` : ""}`,
    )
    .join("\n");
}

/** A client key value apply generated, shown to the administrator once. */
export type GeneratedKey = { name: string; value: string };

/** The client key values apply generated, from its answer or from a failed apply's `error.applied`. */
export function generatedKeys(changes: Change[] | undefined): GeneratedKey[] {
  return (changes ?? []).flatMap((c) => (c.kind === "key" && c.value ? [{ name: c.name, value: c.value }] : []));
}

/** The changes a failed apply committed before it stopped, if its error body carries them. */
export function appliedBeforeError(err: unknown): Change[] | undefined {
  if (typeof err !== "object" || err === null) return undefined;
  const detail = (err as { error?: { applied?: unknown } }).error;
  return Array.isArray(detail?.applied) ? (detail.applied as Change[]) : undefined;
}

/** A change without its generated value, safe to keep in a cache. */
export function withoutValue({ op, kind, name, fields }: Change): Change {
  return { op, kind, name, fields };
}
