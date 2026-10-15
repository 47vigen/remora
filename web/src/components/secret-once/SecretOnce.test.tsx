import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { SecretOnce } from "./SecretOnce";

test("shows the value once and removes it from the DOM on close", async () => {
  const user = userEvent.setup();
  const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  const onClose = vi.fn<() => void>();
  const { rerender } = render(<SecretOnce title="Key created" value="sk-test-123" onClose={onClose} />);
  expect(screen.getByRole("dialog", { name: "Key created" })).toBeInTheDocument();
  expect(screen.getByDisplayValue("sk-test-123")).toHaveAttribute("readonly");
  expect(screen.getByText("Copy it now. You will not see it again.")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Copy" }));
  expect(writeText).toHaveBeenCalledWith("sk-test-123");
  expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Done" }));
  expect(onClose).toHaveBeenCalled();
  rerender(<SecretOnce title="Key created" value={null} onClose={onClose} />);
  expect(screen.queryByDisplayValue("sk-test-123")).toBeNull();
  expect(document.body.innerHTML).not.toContain("sk-test-123");
});

test("is not dismissed by Escape", async () => {
  const onClose = vi.fn<() => void>();
  render(<SecretOnce title="Key created" value="sk-test-123" onClose={onClose} />);
  await userEvent.keyboard("{Escape}");
  expect(onClose).not.toHaveBeenCalled();
  expect(screen.getByRole("dialog", { name: "Key created" })).toBeInTheDocument();
});

test("shows a usage example when given one", () => {
  render(<SecretOnce title="Key created" value="sk-test-123" example="curl /v1/models" onClose={() => {}} />);
  expect(screen.getByText("Use it like this")).toBeInTheDocument();
  expect(screen.getByText("curl /v1/models")).toBeInTheDocument();
});
