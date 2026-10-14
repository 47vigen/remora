import { Button, Card, FieldError, Form, Input, Label, TextField, useTheme } from "@heroui/react";
import { useMutation } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { loginMutation } from "../client/@tanstack/react-query.gen";
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
    <main className="flex min-h-dvh items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <Card.Header>
          <Card.Title>Sign in to remora</Card.Title>
          <Card.Description>Enter the admin password.</Card.Description>
        </Card.Header>
        <Card.Content>
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
              <Input />
              <FieldError>{error}</FieldError>
            </TextField>
            <Button type="submit" variant="primary" isPending={login.isPending} fullWidth>
              Sign in
            </Button>
          </Form>
        </Card.Content>
      </Card>
    </main>
  );
}
