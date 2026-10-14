import { AlertDialog, Button } from "@heroui/react";
import type { ReactNode } from "react";

export function ConfirmDelete({
  title,
  body,
  isOpen,
  isPending,
  onConfirm,
  onCancel,
}: {
  title: string;
  body: ReactNode;
  isOpen: boolean;
  isPending: boolean;
  onConfirm(): void;
  onCancel(): void;
}) {
  return (
    <AlertDialog.Backdrop
      isOpen={isOpen}
      onOpenChange={(open) => {
        if (!open && !isPending) onCancel();
      }}
    >
      <AlertDialog.Container>
        <AlertDialog.Dialog>
          <AlertDialog.Header>
            <AlertDialog.Icon status="danger" />
            <AlertDialog.Heading>{title}</AlertDialog.Heading>
          </AlertDialog.Header>
          <AlertDialog.Body>{body}</AlertDialog.Body>
          <AlertDialog.Footer>
            <Button variant="tertiary" isDisabled={isPending} onPress={onCancel}>
              Cancel
            </Button>
            <Button variant="danger" isPending={isPending} onPress={onConfirm}>
              Delete
            </Button>
          </AlertDialog.Footer>
        </AlertDialog.Dialog>
      </AlertDialog.Container>
    </AlertDialog.Backdrop>
  );
}
