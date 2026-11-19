import { render, screen } from "@testing-library/react";

import { Shell } from "./Shell";

test("Shell.Nav marks the current page and lists every section", () => {
  render(<Shell.Nav current="/keys" />);
  for (const name of ["Overview", "Providers", "Quota", "Aliases", "Keys", "Logs", "Settings"])
    expect(screen.getByRole("link", { name })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Keys" })).toHaveAttribute("aria-current", "page");
  expect(screen.getByRole("link", { name: "Overview" })).not.toHaveAttribute("aria-current");
});

test("Shell.Nav marks a parent section on a nested page", () => {
  render(<Shell.Nav current="/providers/openai" />);
  expect(screen.getByRole("link", { name: "Providers" })).toHaveAttribute("aria-current", "page");
});

test("Shell.Nav renders links through the given link component", () => {
  render(
    <Shell.Nav
      current="/"
      renderLink={({ href, children, ...rest }) => (
        <a {...rest} href={href} data-testid="custom">
          {children}
        </a>
      )}
    />,
  );
  expect(screen.getAllByTestId("custom")).toHaveLength(7);
});

test("Shell.Nav renders navigation groups and links in order", () => {
  render(<Shell.Nav current="/" />);
  const nav = screen.getByRole("navigation", { name: "Main" });
  const groups = Array.from(nav.children).slice(0, 3);
  expect(
    groups.map((group) => ({
      label: group.querySelector("span")?.textContent,
      links: Array.from(group.querySelectorAll("a")).map((link) => link.textContent),
    })),
  ).toEqual([
    { label: "Monitor", links: ["Overview", "Logs", "Quota"] },
    { label: "Route", links: ["Providers", "Aliases"] },
    { label: "Access", links: ["Keys"] },
  ]);
  expect(screen.getByRole("link", { name: "Quota" })).toHaveAttribute("href", "/quota");
});
