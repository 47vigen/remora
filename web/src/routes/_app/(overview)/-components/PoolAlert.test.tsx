import { render, screen } from "@testing-library/react";

import { PoolAlert } from "./PoolAlert";

test("names the accounts out of each pool and links to the provider", () => {
  render(
    <PoolAlert
      pool={[
        {
          provider: "antigravity",
          enabled: 20,
          out: [
            { id: "3", label: "a@example.com", reason: "signed out" },
            { id: "7", label: "b@example.com", reason: "missing" },
          ],
        },
        { provider: "codex", enabled: 2, out: [] },
      ]}
    />,
  );
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  expect(screen.getByText("2 of 20 antigravity accounts are out of the pool")).toBeInTheDocument();
  expect(screen.getByText(/a@example\.com/)).toBeInTheDocument();
  expect(screen.getByText(/b@example\.com/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /antigravity accounts/ })).toHaveAttribute("href", "/providers/antigravity");
});

test("renders nothing when every account is in the pool", () => {
  const { container } = render(<PoolAlert pool={[{ provider: "codex", enabled: 2, out: [] }]} />);
  expect(container).toBeEmptyDOMElement();
});
