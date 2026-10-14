import { Copy } from "@gravity-ui/icons";
import { Alert, Button, Form, Input, Label, Link, Modal, Spinner, TextField } from "@heroui/react";
import { useState } from "react";

import type { LoginPhase } from "../-hooks/useAccountLogin";

function Waiting({ text }: { text: string }) {
  return (
    <p className="text-muted flex items-center gap-2 text-sm">
      <Spinner size="sm" />
      {text}
    </p>
  );
}

function RedirectStep({
  login,
  onSubmitRedirect,
}: {
  login: Extract<LoginPhase, { phase: "redirect" }>;
  onSubmitRedirect(pasted: string): void;
}) {
  const [pasted, setPasted] = useState("");
  return (
    <Form
      id="account-login-redirect"
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmitRedirect(pasted);
      }}
    >
      <ol className="flex list-decimal flex-col gap-3 pl-5 text-sm">
        <li>
          <Link href={login.authUrl} target="_blank" rel="noopener noreferrer">
            Open the sign-in page
          </Link>
        </li>
        <li>
          After signing in, your browser lands on a page that does not load. Copy that page's full address and paste it
          here.
        </li>
      </ol>
      <TextField value={pasted} onChange={setPasted} isInvalid={login.error !== undefined} autoComplete="off">
        <Label>Address after sign-in</Label>
        <Input className="font-mono" placeholder="http://localhost:…/callback?code=…" />
      </TextField>
      {login.error ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Description>{login.error}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}
    </Form>
  );
}

function DeviceStep({ login }: { login: Extract<LoginPhase, { phase: "device" }> }) {
  return (
    <div className="flex flex-col gap-4 text-sm">
      <p>
        <Link href={login.verificationUrl} target="_blank" rel="noopener noreferrer">
          Open the verification page
        </Link>{" "}
        and enter this code:
      </p>
      <div className="flex items-center gap-3">
        <code className="font-mono text-3xl font-semibold tracking-widest">{login.userCode}</code>
        <Button
          isIconOnly
          variant="ghost"
          aria-label="Copy code"
          onPress={() => void navigator.clipboard?.writeText(login.userCode)}
        >
          <Copy aria-hidden="true" />
        </Button>
      </div>
      <Waiting text="Waiting for you to approve…" />
    </div>
  );
}

export function AccountLoginDialog({
  login,
  onSubmitRedirect,
  onCancel,
  onClose,
  onRestart,
}: {
  login: LoginPhase;
  onSubmitRedirect(pasted: string): void;
  onCancel(): void;
  onClose(): void;
  onRestart(): void;
}) {
  const settled = login.phase === "done" || login.phase === "failed";
  return (
    <Modal.Backdrop
      isOpen
      isDismissable={false}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Modal.Container>
        <Modal.Dialog>
          <Modal.Header>
            <Modal.Heading>Add an account</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            {login.phase === "idle" || login.phase === "starting" ? <Waiting text="Starting the sign-in…" /> : null}
            {login.phase === "redirect" ? (
              <RedirectStep key={login.state} login={login} onSubmitRedirect={onSubmitRedirect} />
            ) : null}
            {login.phase === "device" ? <DeviceStep login={login} /> : null}
            {login.phase === "waiting" ? <Waiting text="Finishing the sign-in…" /> : null}
            {login.phase === "done" ? <p className="text-sm">The account is signed in.</p> : null}
            {login.phase === "failed" ? (
              <Alert status="danger">
                <Alert.Indicator />
                <Alert.Content>
                  <Alert.Description>{login.message}</Alert.Description>
                </Alert.Content>
              </Alert>
            ) : null}
          </Modal.Body>
          <Modal.Footer>
            {settled ? (
              <Button variant="tertiary" onPress={onClose}>
                Close
              </Button>
            ) : (
              <Button variant="tertiary" onPress={onCancel}>
                Cancel
              </Button>
            )}
            {login.phase === "redirect" ? (
              <Button type="submit" form="account-login-redirect" variant="primary" isPending={login.submitting}>
                Finish sign-in
              </Button>
            ) : null}
            {login.phase === "failed" ? (
              <Button variant="primary" onPress={onRestart}>
                Start again
              </Button>
            ) : null}
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
