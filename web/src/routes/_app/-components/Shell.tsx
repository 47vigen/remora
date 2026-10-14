import { BranchesRight, ChartColumn, Gear, Key, ListUl, Server } from "@gravity-ui/icons";
import { cn, Link } from "@heroui/react";
import type { ComponentType, ReactNode, SVGProps } from "react";

export type NavLinkProps = {
  href: string;
  className?: string;
  "aria-current"?: "page";
  children: ReactNode;
};

type NavItem = { href: string; label: string; icon: ComponentType<SVGProps<SVGSVGElement>> };

const SECTIONS: readonly NavItem[] = [
  { href: "/", label: "Overview", icon: ChartColumn },
  { href: "/providers", label: "Providers", icon: Server },
  { href: "/aliases", label: "Aliases", icon: BranchesRight },
  { href: "/keys", label: "Keys", icon: Key },
  { href: "/logs", label: "Logs", icon: ListUl },
  { href: "/settings", label: "Settings", icon: Gear },
];

function isCurrent(href: string, current: string): boolean {
  if (href === "/") return current === "/";
  return current === href || current.startsWith(`${href}/`);
}

const defaultLink = ({ href, className, children, ...rest }: NavLinkProps) => (
  <Link href={href} className={className} {...rest}>
    {children}
  </Link>
);

function Root({ children }: { children: ReactNode }) {
  return <div className="flex min-h-dvh flex-col md:flex-row">{children}</div>;
}

function Nav({
  current,
  renderLink = defaultLink,
  footer,
}: {
  current: string;
  /** Renders each link, so a router can supply its own link component. */
  renderLink?: (p: NavLinkProps) => ReactNode;
  footer?: ReactNode;
}) {
  return (
    <aside className="border-border flex shrink-0 flex-col gap-4 border-b p-4 md:w-56 md:border-r md:border-b-0">
      <span className="text-lg font-semibold">remora</span>
      <nav aria-label="Main">
        <ul className="flex flex-row flex-wrap gap-1 md:flex-col">
          {SECTIONS.map(({ href, label, icon: Icon }) => {
            const active = isCurrent(href, current);
            return (
              <li key={href}>
                {renderLink({
                  href,
                  "aria-current": active ? "page" : undefined,
                  className: cn(
                    "flex items-center gap-2 rounded-lg px-3 py-2 text-sm no-underline",
                    active ? "bg-default text-foreground font-medium" : "text-muted hover:text-foreground",
                  ),
                  children: (
                    <>
                      <Icon aria-hidden="true" className="size-4" />
                      {label}
                    </>
                  ),
                })}
              </li>
            );
          })}
        </ul>
      </nav>
      {footer ? <div className="text-muted mt-auto text-xs">{footer}</div> : null}
    </aside>
  );
}

function Header({ title, children }: { title?: ReactNode; children?: ReactNode }) {
  return (
    <header className="flex items-center justify-between gap-4 px-6 pt-6">
      {title ? <h1 className="text-xl font-semibold">{title}</h1> : <span />}
      <div className="flex items-center gap-2">{children}</div>
    </header>
  );
}

function Content({ children }: { children: ReactNode }) {
  return <main className="flex min-w-0 flex-1 flex-col gap-6 p-6">{children}</main>;
}

export const Shell = Object.assign(Root, { Root, Nav, Header, Content });
