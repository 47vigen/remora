import { ArrowUpRightFromSquare, Copy } from "@gravity-ui/icons";
import { Alert, Button, cn, Description, Form, Input, Label, Link, Modal, Spinner, TextField } from "@heroui/react";
import { type ReactNode, useState } from "react";

import type { LoginPhase } from "../-hooks/useAccountLogin";

function Waiting({ text }: { text: string }) {
  return (
    <p className="text-muted flex items-center gap-2 text-sm">
      <Spinner size="sm" />
      {text}
    </p>
  );
}

/** Steps numbered by their position: a mono index on a hairline rail, the instruction beside it. */
function Steps({ children }: { children: ReactNode[] }) {
  return (
    <ol className="flex flex-col">
      {children.map((step, i) => (
        <li key={i} className="grid grid-cols-[1.75rem_1fr] gap-x-3">
          <span className="flex flex-col items-center">
            <span className="border-border bg-surface-secondary text-muted flex size-6 items-center justify-center rounded-sm border font-mono text-[11px] tabular-nums">
              {i + 1}
            </span>
            {i < children.length - 1 ? <span aria-hidden="true" className="bg-border w-px flex-1" /> : null}
          </span>
          <div className={cn("flex min-w-0 flex-col gap-2 pt-0.5 text-sm", i < children.length - 1 && "pb-5")}>
            {step}
          </div>
        </li>
      ))}
    </ol>
  );
}

function OpenLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <Link href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 self-start">
      {children}
      <ArrowUpRightFromSquare aria-hidden="true" className="size-3.5" />
    </Link>
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
      <Steps>
        {[
          <OpenLink key="open" href={login.authUrl}>
            Open the sign-in page
          </OpenLink>,
          <TextField
            key="paste"
            value={pasted}
            onChange={setPasted}
            isInvalid={login.error !== undefined}
            autoComplete="off"
          >
            <Label>Address after sign-in</Label>
            <Description>
              After signing in, your browser lands on a page that does not load. Copy that page's full address and paste
              it here.
            </Description>
            <Input className="font-mono text-[13px]" placeholder="http://localhost:…/callback?code=…" />
          </TextField>,
        ]}
      </Steps>
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
    <div className="flex flex-col gap-4">
      <Steps>
        {[
          <OpenLink key="open" href={login.verificationUrl}>
            Open the verification page
          </OpenLink>,
          <div key="code" className="flex flex-col gap-2">
            <p>Enter this code there:</p>
            <div className="bg-surface-secondary border-border flex items-center justify-between gap-3 rounded-sm border px-3 py-2">
              <code className="font-mono text-2xl font-medium tracking-widest">{login.userCode}</code>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                aria-label="Copy code"
                onPress={() => void navigator.clipboard?.writeText(login.userCode)}
              >
                <Copy aria-hidden="true" className="size-4" />
              </Button>
            </div>
          </div>,
        ]}
      </Steps>
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
