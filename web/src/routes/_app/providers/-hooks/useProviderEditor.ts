import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { type ProviderDraft, toCreateBody, toUpdateBody, validateProviderDraft } from "../-lib/provider-form";
import { createProviderMutation, updateProviderMutation } from "../../../../client/@tanstack/react-query.gen";
import type { Provider } from "../../../../client/types.gen";
import { errorMessage } from "../../../../lib/api";
import { invalidateOps } from "../../../../lib/query";

/**
 * Holds a provider draft and saves it: create when `isEdit` is false, otherwise update the provider named
 * `draft.name`. Validation messages appear after the first submit.
 */
export function useProviderEditor({
  initial,
  isEdit,
  onSaved,
}: {
  initial: ProviderDraft;
  isEdit: boolean;
  onSaved(p: Provider): void;
}) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState(initial);
  const [submitted, setSubmitted] = useState(false);
  const onSuccess = async (p: Provider) => {
    await invalidateOps(qc, "listProviders", "getProvider");
    onSaved(p);
  };
  const create = useMutation({ ...createProviderMutation(), onSuccess });
  const update = useMutation({ ...updateProviderMutation(), onSuccess });
  const active = isEdit ? update : create;
  const errors = submitted ? validateProviderDraft(draft, isEdit) : {};

  return {
    draft,
    setDraft(d: ProviderDraft) {
      setDraft(d);
      if (active.error) active.reset();
    },
    errors,
    isPending: active.isPending,
    error: active.error ? errorMessage(active.error) : undefined,
    submit() {
      setSubmitted(true);
      if (Object.keys(validateProviderDraft(draft, isEdit)).length > 0) return;
      if (isEdit) update.mutate({ path: { name: draft.name }, body: toUpdateBody(draft) });
      else create.mutate({ body: toCreateBody(draft) });
    },
  };
}
