import { Button, Description, Input, Label, Modal, TextField } from "@heroui/react";
import { useState } from "react";

/**
 * Shows a newly created secret once. The value lives only in the caller's state and in this dialog; it is never
 * written anywhere but the clipboard, and the dialog unmounts it as soon as the caller sets `value` back to null.
 */
export function SecretOnce({ title, value, onClose }: { title: string; value: string | null; onClose(): void }) {
  const [copied, setCopied] = useState(false);
  const isOpen = value !== null;

  return (
    <Modal.Backdrop isOpen={isOpen} isDismissable={false} isKeyboardDismissDisabled>
      <Modal.Container>
        <Modal.Dialog>
          <Modal.Header>
            <Modal.Heading>{title}</Modal.Heading>
          </Modal.Header>
          <Modal.Body className="flex flex-col gap-2">
            {value !== null ? (
              <TextField isReadOnly value={value} fullWidth>
                <Label>Secret</Label>
                <Input className="font-mono" />
                <Description>Copy it now. You will not see it again.</Description>
              </TextField>
            ) : null}
          </Modal.Body>
          <Modal.Footer>
            <Button
              variant="secondary"
              onPress={() => {
                if (value === null) return;
                void navigator.clipboard.writeText(value).then(() => setCopied(true));
              }}
            >
              {copied ? "Copied" : "Copy"}
            </Button>
            <Button
              variant="primary"
              onPress={() => {
                setCopied(false);
                onClose();
              }}
            >
              Done
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
