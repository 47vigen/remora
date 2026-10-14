import { Alert, Button, FieldError, Form, Input, Label, TextField } from "@heroui/react";
import { useState } from "react";

import { validatePassword } from "../-lib/password";

type Field = "current" | "next" | "confirm";
const FIELDS: { id: Field; label: string; autoComplete: string }[] = [
  { id: "current", label: "Current password", autoComplete: "current-password" },
  { id: "next", label: "New password", autoComplete: "new-password" },
  { id: "confirm", label: "Confirm new password", autoComplete: "new-password" },
];

export function PasswordForm({
  isPending,
  error,
  onSubmit,
}: {
  isPending: boolean;
  error?: string;
  onSubmit(p: { current: string; next: string }): void;
}) {
  const [values, setValues] = useState<Record<Field, string>>({ current: "", next: "", confirm: "" });
  const [submitted, setSubmitted] = useState(false);
  const errors = submitted ? validatePassword(values) : {};

  return (
    <Form
      className="flex max-w-sm flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        setSubmitted(true);
        if (Object.keys(validatePassword(values)).length > 0) return;
        onSubmit({ current: values.current, next: values.next });
      }}
    >
      {FIELDS.map((f) => (
        <TextField
          key={f.id}
          type="password"
          autoComplete={f.autoComplete}
          value={values[f.id]}
          onChange={(v) => setValues((s) => ({ ...s, [f.id]: v }))}
          isInvalid={errors[f.id] !== undefined}
        >
          <Label>{f.label}</Label>
          <Input />
          <FieldError>{errors[f.id]}</FieldError>
        </TextField>
      ))}
      {error ? (
        <Alert status="danger" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>Could not change the password</Alert.Title>
            <Alert.Description>{error}</Alert.Description>
          </Alert.Content>
        </Alert>
      ) : null}
      <Button type="submit" variant="primary" isPending={isPending} isDisabled={isPending} className="self-start">
        Change password
      </Button>
    </Form>
  );
}
