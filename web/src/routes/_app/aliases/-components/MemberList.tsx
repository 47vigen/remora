import { ArrowDown, ArrowUp, Grip, Xmark } from "@gravity-ui/icons";
import { Button, Chip, ListBox } from "@heroui/react";
import { useDragAndDrop } from "react-aria-components";

import { memberKind, thinkingOf } from "../-lib/alias-form";
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
 * The alias's members in fallback order. Items reorder with the move buttons, by drag, or by keyboard (Enter to
 * pick up, arrows to move, Enter to drop) through React Aria's drag and drop.
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

  const move = (from: number, to: number) => {
    const next = [...members];
    [next[from], next[to]] = [next[to]!, next[from]!];
    onReorder(next);
  };

  return (
    <ListBox
      aria-label="Members"
      selectionMode="none"
      dragAndDropHooks={dragAndDropHooks}
      items={members.map((ref) => ({ id: ref }))}
      className="border-border divide-separator divide-y rounded-sm border p-0"
      renderEmptyState={() => (
        <p className="text-muted p-3 text-sm">No members yet. Add an alias or a provider model below.</p>
      )}
    >
      {({ id: ref }) => {
        const index = members.indexOf(ref);
        const { base, level } = thinkingOf(ref);
        return (
          <ListBox.Item id={ref} textValue={ref} className="flex items-center gap-2.5 rounded-none px-2 py-1.5">
            <Grip aria-hidden="true" className="text-muted size-4 shrink-0 cursor-grab" />
            <span className="text-muted w-4 text-right font-mono text-xs tabular-nums">{index + 1}</span>
            {memberKind(ref) === "alias" ? (
              <Chip size="sm" variant="soft">
                <Chip.Label className="text-[11px]">alias</Chip.Label>
              </Chip>
            ) : (
              <ProviderIcon type={ref.slice(0, ref.indexOf("/"))} size="sm" />
            )}
            <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{base}</span>
            {level ? (
              <Chip size="sm" variant="soft">
                <Chip.Label className="text-[11px]">
                  {level === "0"
                    ? "thinking off"
                    : /^\d+$/.test(level)
                      ? `thinking ${level} tokens`
                      : `thinking ${level}`}
                </Chip.Label>
              </Chip>
            ) : null}
            {cooling.has(ref) ? (
              <Chip size="sm" variant="soft" color="warning">
                <Chip.Label className="text-[11px]">cooling</Chip.Label>
              </Chip>
            ) : null}
            <span className="flex shrink-0 items-center">
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                aria-label={`Move ${ref} up`}
                isDisabled={index === 0}
                onPress={() => move(index, index - 1)}
              >
                <ArrowUp aria-hidden="true" className="size-4" />
              </Button>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                aria-label={`Move ${ref} down`}
                isDisabled={index === members.length - 1}
                onPress={() => move(index, index + 1)}
              >
                <ArrowDown aria-hidden="true" className="size-4" />
              </Button>
              <Button isIconOnly size="sm" variant="ghost" aria-label={`Remove ${ref}`} onPress={() => onRemove(ref)}>
                <Xmark aria-hidden="true" className="size-4" />
              </Button>
            </span>
          </ListBox.Item>
        );
      }}
    </ListBox>
  );
}
