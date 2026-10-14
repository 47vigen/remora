import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { AddCombo } from "./AddCombo";

// User-event interaction with React Aria is slow on a loaded machine; the default 5 s is too tight.
vi.setConfig({ testTimeout: 30_000 });

const sections = [
  { title: "Aliases", items: ["free"] },
  { title: "Provider models", items: ["or/m1", "or/m2"] },
];

test("Enter adds the typed value and clears the box", async () => {
  const onAdd = vi.fn<(v: string) => boolean>(() => true);
  render(<AddCombo label="Add member" sections={sections} onAdd={onAdd} />);
  const box = screen.getByRole("combobox", { name: "Add member" });
  await userEvent.type(box, " custom/x {Enter}");
  expect(onAdd).toHaveBeenCalledWith("custom/x");
  expect(box).toHaveValue("");
});

test("a refused value stays in the box with the error", async () => {
  const onAdd = vi.fn<(v: string) => boolean>(() => false);
  const { rerender } = render(<AddCombo label="Add member" sections={sections} onAdd={onAdd} />);
  const box = screen.getByRole("combobox", { name: "Add member" });
  await userEvent.type(box, "free{Enter}");
  rerender(<AddCombo label="Add member" sections={sections} onAdd={onAdd} error="“free” is listed twice." />);
  expect(box).toHaveValue("free");
  expect(screen.getByText("“free” is listed twice.")).toBeInTheDocument();
});

test("choosing a suggestion adds it", async () => {
  const onAdd = vi.fn<(v: string) => boolean>(() => true);
  render(<AddCombo label="Add member" sections={sections} onAdd={onAdd} />);
  const box = screen.getByRole("combobox", { name: "Add member" });
  await userEvent.type(box, "or/");
  expect(screen.getByRole("group", { name: "Provider models" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("option", { name: "or/m2" }));
  expect(onAdd).toHaveBeenCalledTimes(1);
  expect(onAdd).toHaveBeenCalledWith("or/m2");
  expect(box).toHaveValue("");
});
