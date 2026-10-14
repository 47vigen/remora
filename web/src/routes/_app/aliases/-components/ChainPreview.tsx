import { Alert, Card, Chip, Spinner } from "@heroui/react";

import type { AliasTarget } from "../-lib/alias-form";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../../components/status-chip/StatusChip";

/** The flat list of targets the draft compiles to, in the order requests try them. */
export function ChainPreview({
  chain,
  error,
  isLoading,
}: {
  chain: AliasTarget[] | undefined;
  error?: string;
  isLoading: boolean;
}) {
  return (
    <Card>
      <Card.Header className="flex flex-row items-center justify-between gap-2">
        <Card.Title>Compiled chain</Card.Title>
        {isLoading ? <Spinner size="sm" /> : null}
      </Card.Header>
      <Card.Content>
        {error !== undefined ? (
          <Alert status="danger" role="alert">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>This alias does not compile</Alert.Title>
              <Alert.Description>{error}</Alert.Description>
            </Alert.Content>
          </Alert>
        ) : chain === undefined || chain.length === 0 ? (
          <p className="text-muted text-sm">Name the alias and add members to see the targets it tries, in order.</p>
        ) : (
          <ol className="flex flex-col gap-2">
            {chain.map((t, i) => (
              <li key={`${i}:${t.provider}/${t.model}`} className="flex items-center gap-3">
                <span className="text-muted w-5 text-right text-sm tabular-nums">{i + 1}</span>
                <ProviderIcon type={t.provider} size="sm" />
                <span className="min-w-0 flex-1 truncate font-mono text-sm">
                  {t.provider}/{t.model}
                </span>
                <Chip size="sm" variant={i === 0 ? "primary" : "secondary"}>
                  <Chip.Label>{i === 0 ? "Primary" : "Fallback"}</Chip.Label>
                </Chip>
                {t.cooling ? <StatusChip status="cooling" until={t.coolingUntil} /> : null}
              </li>
            ))}
          </ol>
        )}
      </Card.Content>
    </Card>
  );
}
