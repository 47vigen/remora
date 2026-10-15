import { Alert, Chip, cn, Spinner } from "@heroui/react";

import type { ChainNode } from "../-lib/alias-form";
import { SectionLabel } from "../../../../components/page-header/PageHeader";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../../components/status-chip/StatusChip";

// Indent and the elbow that joins a nested node back to the rail (rail sits at 5px, each level is 20px).
const INDENT = ["", "pl-5 before:w-[15px]", "pl-10 before:w-[35px]", "pl-15 before:w-[55px]", "pl-20 before:w-[75px]"];

/** The draft's fallback chain as a pipeline: the order a request walks, nested aliases indented under their name. */
export function ChainPreview({
  nodes,
  targets,
  error,
  isLoading,
}: {
  nodes: ChainNode[];
  /** How many targets the server compiled the draft to, once it has. */
  targets?: number;
  error?: string;
  isLoading: boolean;
}) {
  return (
    <section className="bg-surface border-border flex flex-col gap-4 rounded-sm border p-4">
      <SectionLabel aside={isLoading ? <Spinner size="sm" /> : null}>Fallback chain</SectionLabel>
      {error !== undefined ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>This alias does not compile</Alert.Title>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}
      {nodes.length === 0 ? (
        <p className="text-muted text-sm">Name the alias and add members to see the targets it tries, in order.</p>
      ) : (
        <ol className="before:bg-border relative flex flex-col before:absolute before:top-3 before:bottom-3 before:left-[5px] before:w-px">
          {nodes.map((n, i) => (
            <Node key={`${i}:${n.ref}`} node={n} />
          ))}
        </ol>
      )}
      {targets !== undefined && error === undefined ? (
        <p className="text-muted border-separator border-t pt-3 font-mono text-xs tabular-nums">
          Compiles to {targets} {targets === 1 ? "target" : "targets"}
        </p>
      ) : null}
    </section>
  );
}

function Node({ node: n }: { node: ChainNode }) {
  const cooling = n.coolingUntil !== undefined;
  const depth = Math.min(n.depth, INDENT.length - 1);
  return (
    <li
      className={cn(
        "relative flex min-h-9 items-center gap-2.5 py-1",
        depth > 0 && "before:bg-border before:absolute before:top-1/2 before:left-[5px] before:h-px",
        INDENT[depth],
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "relative size-[11px] shrink-0 rounded-[1px] border",
          n.serving
            ? "bg-accent border-accent-foreground/50"
            : cooling
              ? "bg-warning/25 border-warning"
              : n.kind === "alias"
                ? "bg-surface border-ink-strong"
                : n.problem
                  ? "bg-surface border-danger"
                  : "bg-surface border-muted",
        )}
      />
      {n.kind === "model" ? (
        <>
          <span className="text-muted w-4 shrink-0 text-right font-mono text-xs tabular-nums">{n.position}</span>
          <ProviderIcon type={n.ref.slice(0, n.ref.indexOf("/"))} size="sm" />
        </>
      ) : null}
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono text-[13px]",
          n.kind === "alias" ? "text-ink-strong" : cooling && "text-muted",
        )}
      >
        {n.ref}
      </span>
      {n.serving ? (
        <Chip size="sm" variant="primary" color="accent">
          <Chip.Label className="text-[11px]">Serving</Chip.Label>
        </Chip>
      ) : cooling ? (
        <StatusChip status="cooling" until={n.coolingUntil} />
      ) : n.problem === "cycle" ? (
        <span className="label-caps text-danger">Cycle</span>
      ) : n.problem === "unknown" ? (
        <span className="label-caps text-muted">Unresolved</span>
      ) : n.kind === "alias" ? (
        <span className="label-caps text-muted">Alias</span>
      ) : (
        <span className="label-caps text-muted">Fallback</span>
      )}
    </li>
  );
}
