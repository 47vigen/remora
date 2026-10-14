import { ComboBox, FieldError, Header, Input, Label, ListBox } from "@heroui/react";
import { use, useState } from "react";
import { ComboBoxStateContext } from "react-aria-components";

export type AddComboSection = { title: string; items: readonly string[] };

/**
 * A box that adds one value at a time: pick a suggestion, or type any value and press Enter. `onAdd` returns
 * whether it took the value; a refused value stays in the box so the caller's `error` can explain why.
 */
export function AddCombo({
  label,
  sections,
  onAdd,
  error,
  placeholder,
  onInputChange,
}: {
  label: string;
  sections: readonly AddComboSection[];
  onAdd(value: string): boolean;
  error?: string;
  placeholder?: string;
  onInputChange?(value: string): void;
}) {
  const [text, setText] = useState("");
  const change = (v: string) => {
    setText(v);
    onInputChange?.(v);
  };
  const commit = (v: string) => {
    const value = v.trim();
    if (value !== "" && onAdd(value)) change("");
  };
  const visible = sections.filter((s) => s.items.length > 0);

  return (
    <ComboBox
      allowsCustomValue
      allowsEmptyCollection
      // Opened by typing (see CommitInput), so clearing the box after an add does not reopen the list.
      menuTrigger="manual"
      inputValue={text}
      onInputChange={change}
      selectedKey={null}
      onSelectionChange={(key) => {
        if (key !== null) commit(String(key));
      }}
      isInvalid={error !== undefined}
      fullWidth
    >
      <Label>{label}</Label>
      <ComboBox.InputGroup>
        <CommitInput placeholder={placeholder} onCommit={() => commit(text)} />
        <ComboBox.Trigger />
      </ComboBox.InputGroup>
      <FieldError>{error}</FieldError>
      <ComboBox.Popover>
        <ListBox renderEmptyState={() => <p className="text-muted p-2 text-sm">Press Enter to add it as typed.</p>}>
          {visible.map((s) => (
            <ListBox.Section key={s.title} aria-label={s.title}>
              <Header>{s.title}</Header>
              {s.items.map((item) => (
                <ListBox.Item key={item} id={item} textValue={item}>
                  {item}
                </ListBox.Item>
              ))}
            </ListBox.Section>
          ))}
        </ListBox>
      </ComboBox.Popover>
    </ComboBox>
  );
}

/** The text input; Enter with no highlighted suggestion adds the typed text as it is and closes the list. */
function CommitInput({ placeholder, onCommit }: { placeholder?: string; onCommit(): void }) {
  const state = use(ComboBoxStateContext);
  return (
    <Input
      placeholder={placeholder}
      onInput={() => {
        if (state && !state.isOpen) state.open(null, "input");
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.currentTarget.getAttribute("aria-activedescendant")) {
          e.preventDefault();
          onCommit();
          state?.close();
        }
      }}
    />
  );
}
