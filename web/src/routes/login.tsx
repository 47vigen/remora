import { Button, FieldError, Form, Input, Label, TextField, useTheme } from "@heroui/react";
import { useMutation } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { loginMutation } from "../client/@tanstack/react-query.gen";
import { Brand } from "../components/brand/Brand";
import { FanOut } from "../components/brand/FanOut";
import { errorMessage, hasErrorCode } from "../lib/api";
import { safeRedirect } from "../lib/redirect";

export const Route = createFileRoute("/login")({
  validateSearch: (raw: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof raw.redirect === "string" ? raw.redirect : undefined,
  }),
  component: LoginPage,
});

function LoginPage() {
  useTheme("system");
  const search = Route.useSearch();
  const navigate = useNavigate();
  const queryClient = Route.useRouteContext({ select: (c) => c.queryClient });
  const [password, setPassword] = useState("");
  const login = useMutation({
    ...loginMutation(),
    onSuccess: async () => {
      queryClient.clear();
      await navigate({ href: safeRedirect(search.redirect), replace: true });
    },
  });
  const error = login.error
    ? hasErrorCode(login.error, "unauthorized")
      ? "Wrong password."
      : errorMessage(login.error)
    : undefined;

  return (
    <main className="bg-background grid min-h-dvh lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
      <section className="flex flex-col px-6 py-8 sm:px-12">
        <Brand />
        <div className="mx-auto flex w-full max-w-sm flex-1 flex-col justify-center gap-8 py-12">
          <div className="flex flex-col gap-2">
            <h1 className="text-[28px] leading-tight font-medium tracking-[-0.03em]">Sign in to remora</h1>
            <p className="text-muted text-sm">Enter the admin password of this gateway.</p>
          </div>
          <Form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              login.mutate({ body: { password } });
            }}
          >
            <TextField
              name="password"
              type="password"
              isRequired
              autoComplete="current-password"
              value={password}
              onChange={(v) => {
                setPassword(v);
                if (login.error) login.reset();
              }}
              isInvalid={error !== undefined}
            >
              <Label>Password</Label>
              <Input className="h-10" />
              <FieldError>{error}</FieldError>
            </TextField>
            <Button type="submit" variant="primary" size="lg" isPending={login.isPending} fullWidth>
              Sign in
            </Button>
          </Form>
          <p className="text-muted text-xs leading-5">
            It came from <code className="text-foreground font-mono">REMORA_ADMIN_PASSWORD</code> on the first start, or
            was printed once to the log when that was unset.
          </p>
        </div>
      </section>
      <aside className="border-border bg-surface relative hidden flex-col justify-between overflow-hidden border-l p-12 lg:flex">
        <p className="label-caps text-muted">[ One endpoint · every provider ]</p>
        <FanOut className="mx-auto max-w-[560px]" />
        <p className="text-muted max-w-sm text-sm leading-6">
          Chat, embeddings, speech and decisions through one OpenAI-compatible door, with fallback chains that reach for
          paid capacity only when the free kind runs out.
        </p>
      </aside>
    </main>
  );
}
