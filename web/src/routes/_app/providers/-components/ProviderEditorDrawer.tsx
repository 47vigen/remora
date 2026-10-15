import { Drawer, toast } from "@heroui/react";

import { useProviderEditor } from "../-hooks/useProviderEditor";
import { emptyDraft, fromProvider } from "../-lib/provider-form";
import type { Provider } from "../../../../client/types.gen";
import { ProviderForm } from "./ProviderForm";

function ProviderEditor({ provider, onClose }: { provider?: Provider; onClose(): void }) {
  const isEdit = provider !== undefined;
  const editor = useProviderEditor({
    initial: provider ? fromProvider(provider) : emptyDraft("builtin"),
    isEdit,
    onSaved: (p) => {
      toast.success(isEdit ? `Saved ${p.name}` : `Added ${p.name}`);
      onClose();
    },
  });
  return (
    <ProviderForm
      draft={editor.draft}
      errors={editor.errors}
      isEdit={isEdit}
      keyHints={provider?.keys?.map((k) => k.name) ?? []}
      isPending={editor.isPending}
      error={editor.error}
      onChange={editor.setDraft}
      onSubmit={editor.submit}
      onCancel={onClose}
    />
  );
}

/** The side sheet that adds a provider, or edits `provider` when given. `editorKey` resets the draft. */
export function ProviderEditorDrawer({
  isOpen,
  provider,
  editorKey,
  onClose,
}: {
  isOpen: boolean;
  provider?: Provider;
  editorKey: string;
  onClose(): void;
}) {
  return (
    <Drawer.Backdrop
      isOpen={isOpen}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Drawer.Content placement="right">
        <Drawer.Dialog className="sm:w-xl">
          <Drawer.Header>
            <Drawer.Heading>{provider ? `Edit ${provider.name}` : "Add provider"}</Drawer.Heading>
          </Drawer.Header>
          <Drawer.Body>
            {isOpen ? <ProviderEditor key={editorKey} provider={provider} onClose={onClose} /> : null}
          </Drawer.Body>
        </Drawer.Dialog>
      </Drawer.Content>
    </Drawer.Backdrop>
  );
}
