import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { ApiToken } from "../../../../client";
import { TokensTable } from "./TokensTable";

vi.setConfig({ testTimeout: 30_000 });

const t: ApiToken = {
  id: "t1",
  name: "ci",
  prefix: "rt-ab",
  created_at: "2026-10-14T09:00:00Z",
  last_used_at: "2026-10-14T09:59:00Z",
};

test("rows show name, prefix, created and last used, and delete reports the token", async () => {
  const onDelete = vi.fn<(t: ApiToken) => void>();
  render(
    <TokensTable
      items={[t, { ...t, id: "t2", name: "old", last_used_at: undefined }]}
      now={new Date("2026-10-14T10:00:00Z")}
      onDelete={onDelete}
    />,
  );
  const [, first, second] = screen.getAllByRole("row");
  expect(first).toHaveTextContent("ci");
  expect(first).toHaveTextContent("rt-ab…");
  expect(first).toHaveTextContent("1h ago");
  expect(first).toHaveTextContent("1m ago");
  expect(second).toHaveTextContent("Never");
  await userEvent.click(within(second!).getByRole("button", { name: "Delete old" }));
  expect(onDelete).toHaveBeenCalledWith(expect.objectContaining({ id: "t2" }));
});

test("rows show scope and expiry, defaulting to admin and no expiry", () => {
  render(
    <TokensTable
      items={[
        { ...t, scope: "operate", expires_at: "2026-10-14T11:00:00Z" },
        { ...t, id: "t2" },
      ]}
      now={new Date("2026-10-14T10:00:00Z")}
      onDelete={() => {}}
    />,
  );
  expect(screen.getByRole("columnheader", { name: "Scope" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Expiry" })).toBeInTheDocument();
  const [, first, second] = screen.getAllByRole("row");
  expect(first).toHaveTextContent("operate");
  expect(within(first!).getByText("in 1h")).toHaveAttribute("datetime", "2026-10-14T11:00:00Z");
  expect(second).toHaveTextContent("admin");
  expect(second).toHaveTextContent("Never");
});
