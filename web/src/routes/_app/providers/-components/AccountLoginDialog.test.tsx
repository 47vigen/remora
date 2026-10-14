import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { LoginPhase } from "../-hooks/useAccountLogin";
import { AccountLoginDialog } from "./AccountLoginDialog";

function setup(login: LoginPhase) {
  const h = {
    onSubmitRedirect: vi.fn<(pasted: string) => void>(),
    onCancel: vi.fn<() => void>(),
    onClose: vi.fn<() => void>(),
    onRestart: vi.fn<() => void>(),
  };
  render(<AccountLoginDialog login={login} {...h} />);
  return h;
}

test("redirect phase: link opens in a new tab and pasting submits", async () => {
  const h = setup({ phase: "redirect", state: "s", authUrl: "https://auth.example/x", submitting: false });
  const link = screen.getByRole("link", { name: "Open the sign-in page" });
  expect(link).toHaveAttribute("href", "https://auth.example/x");
  expect(link).toHaveAttribute("target", "_blank");
  expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
  expect(screen.getByText(/lands on a page that does not load/)).toBeInTheDocument();
  await userEvent.type(screen.getByRole("textbox", { name: "Address after sign-in" }), "http://localhost/cb?code=1");
  await userEvent.click(screen.getByRole("button", { name: "Finish sign-in" }));
  expect(h.onSubmitRedirect).toHaveBeenCalledWith("http://localhost/cb?code=1");
});

test("redirect phase shows the parse error", () => {
  setup({ phase: "redirect", state: "s", authUrl: "https://auth.example/x", submitting: false, error: "x" });
  expect(screen.getByRole("alert")).toHaveTextContent("x");
});

test("device phase shows the code and the link", () => {
  setup({ phase: "device", state: "s", verificationUrl: "https://auth.example/device", userCode: "ABCD-1234" });
  expect(screen.getByText("ABCD-1234")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Open the verification page" })).toHaveAttribute(
    "href",
    "https://auth.example/device",
  );
  expect(screen.getByText("Waiting for you to approve…")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Copy code" })).toBeInTheDocument();
});

test("failed phase offers Start again", async () => {
  const h = setup({ phase: "failed", message: "Sign-in timed out. Start again." });
  expect(screen.getByText("Sign-in timed out. Start again.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Start again" }));
  expect(h.onRestart).toHaveBeenCalled();
});

test("cancel calls onCancel", async () => {
  const h = setup({ phase: "waiting", state: "s" });
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(h.onCancel).toHaveBeenCalled();
});
