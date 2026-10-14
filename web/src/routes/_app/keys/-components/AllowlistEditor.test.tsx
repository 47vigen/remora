import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { AllowlistEditor } from "./AllowlistEditor";

// User-event interaction with React Aria is slow on a loaded machine; the default 5 s is too tight.
vi.setConfig({ testTimeout: 30_000 });

test("adds a valid pattern and rejects an invalid one", async () => {
  const onChange = vi.fn<(v: string[]) => void>();
  render(
    <AllowlistEditor value={[]} suggestions={["fast", "openrouter/*"]} candidates={["fast"]} onChange={onChange} />,
  );
  await userEvent.type(screen.getByRole("combobox", { name: "Allow" }), "openrouter/*{Enter}");
  expect(onChange).toHaveBeenCalledWith(["openrouter/*"]);
  await userEvent.type(screen.getByRole("combobox", { name: "Allow" }), "a b{Enter}");
  expect(screen.getByText("Spaces are not allowed.")).toBeInTheDocument();
  expect(onChange).toHaveBeenCalledTimes(1);
});

test("a pattern already in the list is refused", async () => {
  const onChange = vi.fn<(v: string[]) => void>();
  render(<AllowlistEditor value={["fast"]} suggestions={[]} candidates={[]} onChange={onChange} />);
  await userEvent.type(screen.getByRole("combobox", { name: "Allow" }), "fast{Enter}");
  expect(screen.getByText("Already allowed.")).toBeInTheDocument();
  expect(onChange).not.toHaveBeenCalled();
});

test("removing a pattern reports the rest", async () => {
  const onChange = vi.fn<(v: string[]) => void>();
  render(<AllowlistEditor value={["fast", "or/*"]} suggestions={[]} candidates={[]} onChange={onChange} />);
  const tag = screen.getByRole("row", { name: "fast" });
  await userEvent.click(within(tag).getByRole("button"));
  expect(onChange).toHaveBeenCalledWith(["or/*"]);
});

test("an empty allowlist warns that the key can call nothing", () => {
  render(<AllowlistEditor value={[]} suggestions={[]} candidates={[]} onChange={() => {}} />);
  expect(screen.getByRole("alert")).toHaveTextContent("This key cannot call any model.");
});

test("shows what the patterns match", () => {
  render(<AllowlistEditor value={["f*"]} suggestions={[]} candidates={["fast", "free", "slow"]} onChange={() => {}} />);
  expect(screen.getByText("Matches 2: fast, free")).toBeInTheDocument();
  expect(screen.queryByRole("alert")).toBeNull();
});
