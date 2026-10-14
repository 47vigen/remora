import { Alert, Label, Tag, TagGroup } from "@heroui/react";
import { useState } from "react";

import { previewMatches, validatePattern } from "../-lib/glob";
import { AddCombo } from "../../../../components/add-combo/AddCombo";

/** How many matched names the preview lists before it summarises the rest. */
const PREVIEW_NAMES = 8;

/**
 * Edits a key's allowlist: alias names and `<provider>/<model>` globs. The preview applies the server's own
 * matching rules to the known aliases and models, so it shows exactly what the key will be able to call.
 */
export function AllowlistEditor({
  value,
  suggestions,
  candidates,
  onChange,
}: {
  value: string[];
  suggestions: string[];
  candidates: string[];
  onChange(v: string[]): void;
}) {
  const [error, setError] = useState<string>();
  const matches = previewMatches(value, candidates);
  const more = matches.length - PREVIEW_NAMES;

  const add = (pattern: string) => {
    const problem = validatePattern(pattern) ?? (value.includes(pattern) ? "Already allowed." : undefined);
    setError(problem);
    if (problem !== undefined) return false;
    onChange([...value, pattern]);
    return true;
  };

  return (
    <div className="flex flex-col gap-3">
      <TagGroup aria-label="Allowed" onRemove={(keys) => onChange(value.filter((v) => !keys.has(v)))}>
        <Label>Allowed</Label>
        <TagGroup.List
          items={value.map((v) => ({ id: v }))}
          renderEmptyState={() => <span className="text-muted text-sm">Nothing allowed yet.</span>}
        >
          {(item) => (
            <Tag id={item.id} textValue={item.id} className="font-mono">
              {item.id}
            </Tag>
          )}
        </TagGroup.List>
      </TagGroup>
      {value.length === 0 ? (
        <Alert status="warning" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>This key cannot call any model.</Alert.Title>
            <Alert.Description>Allow an alias, a provider model, or a pattern such as provider/*.</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}
      <AddCombo
        label="Allow"
        placeholder="fast, openrouter/*, *"
        sections={[{ title: "Suggestions", items: suggestions.filter((s) => !value.includes(s)) }]}
        onAdd={add}
        error={error}
        onInputChange={() => setError(undefined)}
      />
      {value.length > 0 ? (
        <p className="text-muted text-sm">
          {matches.length === 0
            ? "Matches no alias or model known today."
            : `Matches ${matches.length}: ${matches.slice(0, PREVIEW_NAMES).join(", ")}${more > 0 ? ` and ${more} more` : ""}`}
        </p>
      ) : null}
    </div>
  );
}
