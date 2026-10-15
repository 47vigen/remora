import { AlertDialog, Button } from "@heroui/react";
import type { ReactNode } from "react";

/** Asks before something is destroyed; `body` says what stops working. */
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
      <AlertDialog.Container size="sm">
        <AlertDialog.Dialog>
          <AlertDialog.Header>
            <AlertDialog.Icon status="danger" />
            <AlertDialog.Heading>{title}</AlertDialog.Heading>
          </AlertDialog.Header>
          <AlertDialog.Body className="text-muted text-sm">{body}</AlertDialog.Body>
          <AlertDialog.Footer>
            <Button size="sm" variant="secondary" isDisabled={isPending} onPress={onCancel}>
              Cancel
            </Button>
            <Button size="sm" variant="danger" isPending={isPending} onPress={onConfirm}>
              Delete
            </Button>
          </AlertDialog.Footer>
        </AlertDialog.Dialog>
      </AlertDialog.Container>
    </AlertDialog.Backdrop>
  );
}
