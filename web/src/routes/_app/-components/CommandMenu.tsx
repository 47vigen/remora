import { Magnifier } from "@gravity-ui/icons";
import { Header, Kbd, ListBox, Modal, SearchField, useFilter } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { createContext, type ReactNode, use, useEffect, useState } from "react";
import { Autocomplete } from "react-aria-components";

import { listAliasesOptions, listKeysOptions, listProvidersOptions } from "../../../client/@tanstack/react-query.gen";

type Entry = { id: string; label: string; hint?: string; href: string };

const PAGES: Entry[] = [
  { id: "page:/", label: "Overview", href: "/" },
  { id: "page:/quota", label: "Quota", href: "/quota" },
  { id: "page:/providers", label: "Providers", href: "/providers" },
  { id: "page:/aliases", label: "Aliases", href: "/aliases" },
  { id: "page:/keys", label: "Keys", href: "/keys" },
  { id: "page:/logs", label: "Logs", href: "/logs" },
  { id: "page:/settings", label: "Settings", href: "/settings" },
];

const ACTIONS: Entry[] = [
  { id: "do:provider", label: "Add a provider", href: "/providers?edit=new" },
  { id: "do:alias", label: "Create an alias", href: "/aliases/new" },
  { id: "do:key", label: "Create a key", href: "/keys?create=true" },
  { id: "do:live", label: "Watch live logs", href: "/logs?live=true" },
];

const LIMIT = 200;
const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

function Section({ title, items }: { title: string; items: Entry[] }) {
  if (items.length === 0) return null;
  return (
    <ListBox.Section>
      <Header className="label-caps text-muted px-2 pt-3 pb-1">{title}</Header>
      {items.map((e) => (
        <ListBox.Item key={e.id} id={e.id} href={e.href} textValue={`${e.label} ${e.hint ?? ""}`}>
          <span className="flex min-w-0 flex-1 items-center justify-between gap-3">
            <span className="truncate">{e.label}</span>
            {e.hint ? <span className="text-muted truncate font-mono text-xs">{e.hint}</span> : null}
          </span>
        </ListBox.Item>
      ))}
    </ListBox.Section>
  );
}

const OpenContext = createContext<() => void>(() => {});

/** The search field look-alike that opens the menu. */
function Trigger() {
  const open = use(OpenContext);
  return (
    <button
      type="button"
      onClick={open}
      className="border-border bg-surface text-muted hover:text-foreground hover:bg-surface-secondary flex h-8 w-full items-center gap-2 rounded-sm border px-2.5 text-[13px] transition-colors duration-150 max-sm:w-8 max-sm:justify-center max-sm:px-0"
    >
      <Magnifier aria-hidden="true" className="size-3.5 shrink-0" />
      <span className="hidden flex-1 text-left sm:inline">Search</span>
      <Kbd className="hidden h-5 px-1.5 font-mono text-[11px] sm:inline-flex">{isMac ? "⌘K" : "Ctrl K"}</Kbd>
      <span className="sr-only sm:hidden">Search</span>
    </button>
  );
}

/** Jump to any page, provider, alias or key, or start a common task. Opens with ⌘K / Ctrl+K or a Trigger. */
function Root({ children }: { children: ReactNode }) {
  const [isOpen, setOpen] = useState(false);
  const { contains } = useFilter({ sensitivity: "base" });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const providers = useQuery({ ...listProvidersOptions({ query: { limit: LIMIT } }), enabled: isOpen });
  const aliases = useQuery({ ...listAliasesOptions({ query: { limit: LIMIT } }), enabled: isOpen });
  const keys = useQuery({ ...listKeysOptions({ query: { limit: LIMIT } }), enabled: isOpen });

  const providerItems: Entry[] = (providers.data?.items ?? []).map((p) => ({
    id: `provider:${p.name}`,
    label: p.name,
    hint: "provider",
    href: `/providers/${encodeURIComponent(p.name)}`,
  }));
  const aliasItems: Entry[] = (aliases.data?.items ?? []).map((a) => ({
    id: `alias:${a.name}`,
    label: a.name,
    hint: a.kind,
    href: `/aliases/${encodeURIComponent(a.name)}`,
  }));
  const keyItems: Entry[] = (keys.data?.items ?? []).map((k) => ({
    id: `key:${k.id}`,
    label: k.name,
    hint: `${k.prefix}…`,
    href: `/keys?key=${encodeURIComponent(k.id)}`,
  }));

  return (
    <OpenContext value={() => setOpen(true)}>
      {children}
      <Modal.Backdrop isOpen={isOpen} onOpenChange={setOpen} isDismissable className="no-motion">
        <Modal.Container placement="top" size="lg" className="pt-[12vh]">
          <Modal.Dialog aria-label="Search" className="overflow-hidden p-0">
            <Autocomplete filter={contains}>
              <div className="border-border border-b p-2">
                {/* oxlint-disable-next-line jsx-a11y/no-autofocus -- a command menu exists to be typed into at once */}
                <SearchField aria-label="Search" autoFocus className="w-full">
                  <SearchField.Group className="border-0 bg-transparent">
                    <SearchField.SearchIcon />
                    <SearchField.Input placeholder="Jump to a page, provider, alias or key" />
                    <SearchField.ClearButton />
                  </SearchField.Group>
                </SearchField>
              </div>
              <ListBox
                aria-label="Results"
                className="scrollbar max-h-[min(60vh,440px)] overflow-y-auto p-1.5"
                onAction={() => setOpen(false)}
                renderEmptyState={() => <p className="text-muted px-3 py-8 text-center text-sm">Nothing matches.</p>}
              >
                <Section title="Go to" items={PAGES} />
                <Section title="Actions" items={ACTIONS} />
                <Section title="Providers" items={providerItems} />
                <Section title="Aliases" items={aliasItems} />
                <Section title="Keys" items={keyItems} />
              </ListBox>
            </Autocomplete>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </OpenContext>
  );
}

export const CommandMenu = Object.assign(Root, { Root, Trigger });
