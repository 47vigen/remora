import { Check, Copy } from "@gravity-ui/icons";
import { Alert, Button, InputGroup, Label, Modal, TextField } from "@heroui/react";
import { useState } from "react";

/**
 * Shows a newly created secret once. The value lives only in the caller's state and in this dialog; it is never
 * written anywhere but the clipboard, and the dialog unmounts it as soon as the caller sets `value` back to null.
 * `example` shows how to use it and must not contain the value itself.
 */
export function SecretOnce({
  title,
  value,
  example,
  onClose,
}: {
  title: string;
  value: string | null;
  example?: string;
  onClose(): void;
}) {
  const [copied, setCopied] = useState(false);
  const isOpen = value !== null;

  return (
    <Modal.Backdrop isOpen={isOpen} isDismissable={false} isKeyboardDismissDisabled>
      <Modal.Container size="lg">
        <Modal.Dialog>
          <Modal.Header>
            <Modal.Heading>{title}</Modal.Heading>
          </Modal.Header>
          <Modal.Body className="flex flex-col gap-4">
            {value !== null ? (
              <>
                <Alert status="warning">
                  <Alert.Indicator />
                  <Alert.Content>
                    <Alert.Title>Copy it now. You will not see it again.</Alert.Title>
                    <Alert.Description>If it is lost, delete it and create a new one.</Alert.Description>
                  </Alert.Content>
                </Alert>
                <TextField isReadOnly value={value} fullWidth>
                  <Label className="label-caps text-muted">Secret</Label>
                  <InputGroup fullWidth className="h-12">
                    <InputGroup.Input className="font-mono text-[15px] tracking-tight" />
                    <InputGroup.Suffix className="pe-1.5">
                      <Button
                        size="sm"
                        variant={copied ? "ghost" : "secondary"}
                        className={copied ? "text-success" : undefined}
                        onPress={() => void navigator.clipboard.writeText(value).then(() => setCopied(true))}
                      >
                        {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
                        {copied ? "Copied" : "Copy"}
                      </Button>
                    </InputGroup.Suffix>
                  </InputGroup>
                </TextField>
                {example ? (
                  <div className="flex flex-col gap-1.5">
                    <span className="label-caps text-muted">Use it like this</span>
                    <pre className="bg-surface-secondary border-border overflow-x-auto rounded-sm border p-3 font-mono text-xs leading-5">
                      {example}
                    </pre>
                  </div>
                ) : null}
              </>
            ) : null}
          </Modal.Body>
          <Modal.Footer>
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
