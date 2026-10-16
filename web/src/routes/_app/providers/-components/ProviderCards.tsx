import { Ellipsis, PencilToSquare, PlugConnection, Plus, TrashBin } from "@gravity-ui/icons";
import { Button, cn, Dropdown, Label, Link } from "@heroui/react";
import type { ReactNode } from "react";

import { displayGroup, GROUP_LABELS } from "../-lib/provider-form";
import type { Provider } from "../../../../client/types.gen";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { getOAuthProviderLabel, getProviderLabel } from "../../../../lib/providers";
import { ProviderMark } from "./ProviderMark";

export type SubscriptionRow = { provider: string; accounts: number };

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

function target(p: Provider): string {
  if (p.kind === "builtin") return p.type ? getProviderLabel(p.type) : "";
  return p.base_url ?? "";
}

/** One card: the whole surface opens the provider through a stretched link; the menu sits above it. */
function Card({
  href,
  label,
  mark,
  title,
  sub,
  menu,
  aside,
  footer,
  muted,
}: {
  href: string;
  label: string;
  mark: ReactNode;
  title: ReactNode;
  sub: string;
  menu?: ReactNode;
  /** Text on the right that is part of the card's click target. */
  aside?: ReactNode;
  footer?: ReactNode;
  muted?: boolean;
}) {
  return (
    <article
      aria-label={label}
      className={cn(
        "group bg-surface border-border hover:border-muted/40 relative flex min-w-0 flex-col rounded-sm border transition-colors duration-150",
        muted && "bg-transparent border-dashed",
      )}
    >
      <div className="flex items-start gap-3 p-4">
        {mark}
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <Link
            href={href}
            className="text-foreground static truncate text-sm font-medium no-underline after:absolute after:inset-0 after:content-['']"
          >
            {title}
          </Link>
          <span className="text-muted truncate text-xs">{sub || " "}</span>
        </div>
        {aside}
        {menu ? <div className="relative z-10 -mt-1 -mr-2 shrink-0">{menu}</div> : null}
      </div>
      {footer ? (
        <div className="border-border text-muted mt-auto flex items-center justify-between gap-3 border-t px-4 py-2.5">
          {footer}
        </div>
      ) : null}
    </article>
  );
}

function ProviderMenu({
  p,
  pending,
  onTest,
  onEdit,
  onDelete,
}: {
  p: Provider;
  pending: boolean;
  onTest(p: Provider): void;
  onEdit(p: Provider): void;
  onDelete(p: Provider): void;
}) {
  return (
    <Dropdown>
      <Button isIconOnly size="sm" variant="ghost" aria-label={`Actions for ${p.name}`} isPending={pending}>
        <Ellipsis aria-hidden="true" className="size-4" />
      </Button>
      <Dropdown.Popover placement="bottom end">
        <Dropdown.Menu
          aria-label={`Actions for ${p.name}`}
          onAction={(key) => {
            if (key === "test") onTest(p);
            else if (key === "edit") onEdit(p);
            else if (key === "delete") onDelete(p);
          }}
        >
          <Dropdown.Item id="test" textValue="Test">
            <PlugConnection aria-hidden="true" className="text-muted size-4 shrink-0" />
            <Label>Test</Label>
          </Dropdown.Item>
          <Dropdown.Item id="edit" textValue="Edit">
            <PencilToSquare aria-hidden="true" className="text-muted size-4 shrink-0" />
            <Label>Edit</Label>
          </Dropdown.Item>
          <Dropdown.Item id="delete" textValue="Delete" variant="danger">
            <TrashBin aria-hidden="true" className="text-danger size-4 shrink-0" />
            <Label>Delete</Label>
          </Dropdown.Item>
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}

const grid = "grid grid-cols-[repeat(auto-fill,minmax(17rem,1fr))] gap-3";

/** Providers as cards: subscriptions with accounts, then API providers, then subscriptions still to connect. */
export function ProviderCards({
  items,
  subscriptions,
  onTest,
  onEdit,
  onDelete,
  testing,
}: {
  items: Provider[];
  subscriptions: SubscriptionRow[];
  onTest(p: Provider): void;
  onEdit(p: Provider): void;
  onDelete(p: Provider): void;
  /** The name of the provider whose test is running. */
  testing?: string;
}) {
  const connected = subscriptions.filter((s) => s.accounts > 0).sort((x, y) => y.accounts - x.accounts);
  const idle = subscriptions.filter((s) => s.accounts === 0);
  const sub = (s: SubscriptionRow) => {
    const name = getOAuthProviderLabel(s.provider);
    return (
      <Card
        key={`sub:${s.provider}`}
        href={`/providers/${s.provider}`}
        label={name}
        mark={<ProviderMark type={s.provider} name={name} size="md" />}
        title={name}
        sub={s.accounts > 0 ? "Signed-in accounts" : "Not connected"}
        muted={s.accounts === 0}
        aside={
          s.accounts > 0 ? undefined : (
            <span className="text-muted group-hover:text-foreground mt-2 mr-2 flex items-center gap-1 text-xs transition-colors duration-150">
              <Plus aria-hidden="true" className="size-3.5" />
              Connect
            </span>
          )
        }
        footer={
          s.accounts > 0 ? (
            <>
              <span className="text-xs">{GROUP_LABELS.subscription}</span>
              <span className="text-foreground font-mono text-xs tabular-nums">{plural(s.accounts, "account")}</span>
            </>
          ) : undefined
        }
      />
    );
  };

  return (
    <div className="flex flex-col gap-3">
      {connected.length + items.length > 0 ? (
        <div className={grid}>
          {connected.map(sub)}
          {items.map((p) => (
            <Card
              key={p.name}
              href={`/providers/${p.name}`}
              label={p.name}
              mark={<ProviderMark type={p.type ?? p.name} name={p.name} size="md" />}
              title={<span className="font-mono text-[13px]">{p.name}</span>}
              sub={target(p)}
              menu={
                <ProviderMenu p={p} pending={testing === p.name} onTest={onTest} onEdit={onEdit} onDelete={onDelete} />
              }
              footer={
                <>
                  <span className="flex items-center gap-2 text-xs">
                    {GROUP_LABELS[displayGroup(p.kind)]}
                    <span aria-hidden="true">·</span>
                    <span className="font-mono tabular-nums">{plural(p.key_count, "key")}</span>
                  </span>
                  <StatusChip status={p.status} />
                </>
              }
            />
          ))}
        </div>
      ) : null}
      {idle.length > 0 ? (
        <>
          <h2 className="label-caps text-muted mt-3">Subscriptions to connect</h2>
          <div className={grid}>{idle.map(sub)}</div>
        </>
      ) : null}
    </div>
  );
}
