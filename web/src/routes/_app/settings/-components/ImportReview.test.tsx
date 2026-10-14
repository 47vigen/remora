import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ImportReview } from "./ImportReview";

vi.setConfig({ testTimeout: 30_000 });

test("shows the diff line by line and applies it", async () => {
  const onApply = vi.fn<() => void>();
  render(<ImportReview diff={"+ alias fast\n- key old"} isPending={false} onApply={onApply} onCancel={() => {}} />);
  const pre = screen.getByText((_, el) => el?.tagName === "PRE");
  expect(pre.textContent).toBe("+ alias fast\n- key old");
  await userEvent.click(screen.getByRole("button", { name: "Apply" }));
  expect(onApply).toHaveBeenCalled();
});

test("an empty diff says so and cannot be applied", () => {
  render(<ImportReview diff="" isPending={false} onApply={() => {}} onCancel={() => {}} />);
  expect(screen.getByText("No changes.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
});

test("an error renders as an alert", async () => {
  const onCancel = vi.fn<() => void>();
  render(
    <ImportReview
      diff={undefined}
      isPending={false}
      error="line 3: unknown kind"
      onApply={() => {}}
      onCancel={onCancel}
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent("line 3: unknown kind");
  expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onCancel).toHaveBeenCalled();
});
