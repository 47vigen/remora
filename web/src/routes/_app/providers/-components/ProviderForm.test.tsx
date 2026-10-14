import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import { emptyDraft, type ProviderDraft } from "../-lib/provider-form";
import { ProviderForm } from "./ProviderForm";

function Harness({
  initial,
  isEdit = false,
  keyHints = [],
}: {
  initial: ProviderDraft;
  isEdit?: boolean;
  keyHints?: string[];
}) {
  const [draft, setDraft] = useState(initial);
  return (
    <ProviderForm
      draft={draft}
      errors={{}}
      isEdit={isEdit}
      keyHints={keyHints}
      isPending={false}
      onChange={setDraft}
      onSubmit={() => {}}
      onCancel={() => {}}
    />
  );
}

test("choosing a kind shows its fields", async () => {
  render(<Harness initial={emptyDraft("builtin")} />);
  expect(screen.getByRole("button", { name: /Provider type/ })).toBeInTheDocument();
  expect(screen.queryByRole("textbox", { name: "Base URL" })).toBeNull();

  await userEvent.click(screen.getByRole("button", { name: /Kind/ }));
  await userEvent.click(screen.getByRole("option", { name: "OpenAI-compatible" }));
  expect(screen.queryByRole("button", { name: /Provider type/ })).toBeNull();
  expect(screen.getByRole("textbox", { name: "Base URL" })).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: /Kind/ }));
  await userEvent.click(screen.getByRole("option", { name: "Built-in" }));
  expect(screen.getByRole("button", { name: /Provider type/ })).toBeInTheDocument();
  expect(screen.queryByRole("textbox", { name: "Base URL" })).toBeNull();
});

test("in edit mode key hints are shown and a blank key keeps its value", () => {
  render(
    <Harness
      isEdit
      keyHints={["main", "backup"]}
      initial={{
        ...emptyDraft("openai-compatible"),
        name: "local",
        baseUrl: "http://h",
        keys: [{ name: "main", value: "" }],
      }}
    />,
  );
  expect(screen.getByText("Stored keys: main, backup")).toBeInTheDocument();
  expect(screen.getByLabelText("Key 1 value")).toHaveAttribute("placeholder", "Leave blank to keep");
  expect(screen.getByRole("textbox", { name: "Name" })).toBeDisabled();
});

test("a builtin's name follows its type", async () => {
  render(<Harness initial={emptyDraft("builtin")} />);
  await userEvent.click(screen.getByRole("button", { name: /Provider type/ }));
  await userEvent.click(screen.getByRole("option", { name: "OpenRouter" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("openrouter");
});
