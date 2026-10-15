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
  expect(screen.getByRole("radiogroup", { name: "Provider type" })).toBeInTheDocument();
  expect(screen.queryByRole("textbox", { name: "Base URL" })).toBeNull();

  await userEvent.click(screen.getByRole("radio", { name: "OpenAI-compatible" }));
  expect(screen.queryByRole("radiogroup", { name: "Provider type" })).toBeNull();
  expect(screen.getByRole("textbox", { name: "Base URL" })).toBeInTheDocument();

  await userEvent.click(screen.getByRole("radio", { name: "Built-in" }));
  expect(screen.getByRole("radiogroup", { name: "Provider type" })).toBeInTheDocument();
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
  expect(screen.getByText(/Stored keys/)).toHaveTextContent("Stored keys: main, backup");
  expect(screen.getByLabelText("Key 1 value")).toHaveAttribute("placeholder", "Leave blank to keep");
  expect(screen.getByRole("textbox", { name: "Name" })).toBeDisabled();
});

test("a builtin's name follows its type", async () => {
  render(<Harness initial={emptyDraft("builtin")} />);
  await userEvent.type(screen.getByRole("searchbox", { name: "Filter provider types" }), "router");
  expect(screen.queryByRole("radio", { name: "OpenAI" })).toBeNull();
  await userEvent.click(screen.getByRole("radio", { name: "OpenRouter" }));
  expect(screen.getByRole("radio", { name: "OpenRouter" })).toBeChecked();
  expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue("openrouter");
});

test("a key value is hidden until revealed", async () => {
  render(<Harness initial={emptyDraft("builtin")} />);
  const value = screen.getByLabelText("Key 1 value");
  expect(value).toHaveAttribute("type", "password");
  await userEvent.click(screen.getByRole("button", { name: "Show key 1 value" }));
  expect(value).toHaveAttribute("type", "text");
});
