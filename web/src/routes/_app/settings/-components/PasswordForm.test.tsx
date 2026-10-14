import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { PasswordForm } from "./PasswordForm";

vi.setConfig({ testTimeout: 30_000 });

test("submit is disabled while pending", () => {
  render(<PasswordForm isPending onSubmit={() => {}} />);
  expect(screen.getByRole("button", { name: /Change password/ })).toBeDisabled();
});

test("field errors render under their fields and nothing is sent", async () => {
  const onSubmit = vi.fn<(p: { current: string; next: string }) => void>();
  render(<PasswordForm isPending={false} onSubmit={onSubmit} />);
  await userEvent.type(screen.getByLabelText("New password"), "short");
  await userEvent.type(screen.getByLabelText("Confirm new password"), "other");
  await userEvent.click(screen.getByRole("button", { name: "Change password" }));
  expect(screen.getByText("Enter your current password.")).toBeInTheDocument();
  expect(screen.getByText("Use at least 12 characters.")).toBeInTheDocument();
  expect(screen.getByText("Passwords do not match.")).toBeInTheDocument();
  expect(onSubmit).not.toHaveBeenCalled();
});

test("a valid form sends current and new", async () => {
  const onSubmit = vi.fn<(p: { current: string; next: string }) => void>();
  render(<PasswordForm isPending={false} onSubmit={onSubmit} error="Current password is wrong." />);
  expect(screen.getByRole("alert")).toHaveTextContent("Current password is wrong.");
  await userEvent.type(screen.getByLabelText("Current password"), "old");
  await userEvent.type(screen.getByLabelText("New password"), "a-long-new-password");
  await userEvent.type(screen.getByLabelText("Confirm new password"), "a-long-new-password");
  await userEvent.click(screen.getByRole("button", { name: "Change password" }));
  expect(onSubmit).toHaveBeenCalledWith({ current: "old", next: "a-long-new-password" });
});
