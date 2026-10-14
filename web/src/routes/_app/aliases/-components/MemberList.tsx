import { Grip, Xmark } from "@gravity-ui/icons";
import { Button, Chip, ListBox } from "@heroui/react";
import { useDragAndDrop } from "react-aria-components";

import { memberKind } from "../-lib/alias-form";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";

/** Moves `moved` (in their current order) before or after `target`. */
function reorder(members: string[], moved: Set<string>, target: string, position: "before" | "after"): string[] {
  const rest = members.filter((m) => !moved.has(m));
  const picked = members.filter((m) => moved.has(m));
  const at = rest.indexOf(target);
  if (at < 0) return members;
  const index = position === "before" ? at : at + 1;
  return [...rest.slice(0, index), ...picked, ...rest.slice(index)];
}

/**
 * The alias's members in fallback order. Items reorder by drag or by keyboard (Enter to pick up, arrows to move,
 * Enter to drop), through React Aria's drag and drop.
 */
export function MemberList({
  members,
  cooling,
  onReorder,
  onRemove,
}: {
  members: string[];
  cooling: Set<string>;
  onReorder(next: string[]): void;
  onRemove(ref: string): void;
}) {
  const { dragAndDropHooks } = useDragAndDrop({
    getItems: (keys) => [...keys].map((k) => ({ "text/plain": String(k) })),
    onReorder: (e) => {
      if (e.target.dropPosition === "on") return;
      const moved = new Set([...e.keys].map(String));
      const next = reorder(members, moved, String(e.target.key), e.target.dropPosition);
      if (next.some((m, i) => m !== members[i])) onReorder(next);
    },
  });

  return (
    <ListBox
      aria-label="Members"
      selectionMode="none"
      dragAndDropHooks={dragAndDropHooks}
      items={members.map((ref) => ({ id: ref }))}
      renderEmptyState={() => <p className="text-muted p-3 text-sm">No members yet.</p>}
    >
      {({ id: ref }) => {
        const position = members.indexOf(ref) + 1;
        return (
          <ListBox.Item id={ref} textValue={ref} className="flex items-center gap-3">
            <Grip aria-hidden="true" className="text-muted size-4 shrink-0" />
            <span className="text-muted w-5 text-right text-sm tabular-nums">{position}</span>
            {memberKind(ref) === "alias" ? (
              <Chip size="sm" variant="soft" color="accent">
                <Chip.Label>alias</Chip.Label>
              </Chip>
            ) : (
              <ProviderIcon type={ref.slice(0, ref.indexOf("/"))} size="sm" />
            )}
            <span className="min-w-0 flex-1 truncate font-mono text-sm">{ref}</span>
            {cooling.has(ref) ? (
              <Chip size="sm" variant="soft" color="warning">
                <Chip.Label>cooling</Chip.Label>
              </Chip>
            ) : null}
            <Button isIconOnly size="sm" variant="ghost" aria-label={`Remove ${ref}`} onPress={() => onRemove(ref)}>
              <Xmark aria-hidden="true" />
            </Button>
          </ListBox.Item>
        );
      }}
    </ListBox>
  );
}
