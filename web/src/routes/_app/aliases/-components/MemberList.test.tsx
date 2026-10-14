import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { MemberList } from "./MemberList";

test("keyboard reorder moves a member down", async () => {
  const onReorder = vi.fn<(next: string[]) => void>();
  const user = userEvent.setup();
  render(<MemberList members={["a/1", "b/2"]} cooling={new Set()} onReorder={onReorder} onRemove={() => {}} />);
  await user.tab(); // focuses the first member
  await user.keyboard("{Enter}"); // picks it up; the first drop target is "between a/1 and b/2"
  await waitFor(() => expect(document.activeElement).toHaveAttribute("aria-label", "Insert between a/1 and b/2"));
  await user.keyboard("{ArrowDown}");
  expect(document.activeElement).toHaveAttribute("aria-label", "Insert after b/2");
  await user.keyboard("{Enter}");
  await waitFor(() => expect(onReorder).toHaveBeenCalledWith(["b/2", "a/1"]));
  expect(onReorder).toHaveBeenCalledTimes(1);
});

test("cooling members carry a badge and nested aliases are marked", () => {
  render(<MemberList members={["a/1", "free"]} cooling={new Set(["a/1"])} onReorder={() => {}} onRemove={() => {}} />);
  const [model, alias] = screen.getAllByRole("option");
  expect(within(model!).getByText("cooling")).toBeInTheDocument();
  expect(within(model!).queryByText("alias")).toBeNull();
  expect(within(model!).getByText("1")).toBeInTheDocument();
  expect(within(alias!).getByText("alias")).toBeInTheDocument();
  expect(within(alias!).queryByText("cooling")).toBeNull();
});

test("the remove button reports the member", async () => {
  const onRemove = vi.fn<(ref: string) => void>();
  render(<MemberList members={["a/1", "free"]} cooling={new Set()} onReorder={() => {}} onRemove={onRemove} />);
  await userEvent.click(screen.getByRole("button", { name: "Remove free" }));
  expect(onRemove).toHaveBeenCalledWith("free");
});
