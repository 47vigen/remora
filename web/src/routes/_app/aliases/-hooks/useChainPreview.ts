import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { type AliasDraft, type AliasTarget, aliasFromDraft, toAliasTargets } from "../-lib/alias-form";
import { compileAlias } from "../../../../client";
import { errorMessage } from "../../../../lib/api";

const DEBOUNCE_MS = 300;

/**
 * Compiles the unsaved draft on the server, 300 ms after it last changed, and keeps the previous chain on screen
 * while the next one loads. A draft with no name or no members is not sent.
 */
export function useChainPreview(draft: AliasDraft): { chain?: AliasTarget[]; error?: string; isLoading: boolean } {
  const body = aliasFromDraft(draft);
  const key = JSON.stringify(body);
  const [settled, setSettled] = useState(key);
  useEffect(() => {
    if (key === settled) return;
    const t = setTimeout(() => setSettled(key), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [key, settled]);

  const sent = JSON.parse(settled) as ReturnType<typeof aliasFromDraft>;
  const enabled = sent.name !== "" && sent.members.length > 0;
  const query = useQuery({
    queryKey: [{ _id: "compileAlias", body: sent }],
    queryFn: async ({ signal }) => (await compileAlias({ body: sent, signal, throwOnError: true })).data,
    enabled,
    placeholderData: keepPreviousData,
    retry: false,
  });

  if (!enabled) return { chain: undefined, error: undefined, isLoading: false };
  return {
    chain: query.isError || !query.data ? undefined : toAliasTargets(query.data.targets),
    error: query.isError ? errorMessage(query.error) : undefined,
    isLoading: key !== settled || query.isFetching,
  };
}
