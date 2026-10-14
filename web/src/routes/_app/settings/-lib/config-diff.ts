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
