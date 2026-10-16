import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { QuotaProvider } from "../../../../client/types.gen";
import { ProviderQuota, type RefreshTarget } from "./ProviderQuota";

const now = new Date("2026-10-16T12:00:00Z");
const account = (id: string, email: string, provider = "antigravity") => ({
  id,
  provider,
  email,
  enabled: true,
  priority: 0,
  status: "active",
  quota: [],
  models: [],
});

const antigravity: QuotaProvider = {
  provider: "antigravity",
  supported: true,
  available: 1,
  exhausted: 1,
  cooling: 0,
  sign_in: 2,
  next_reset: "2026-10-16T14:00:00Z",
  accounts: [
    {
      account: account("1", "a@example.test"),
      quota: {
        plan: "Pro",
        windows: [
          { scope: "gemini", window: "5h", remaining: 0.8, resets_at: "2026-10-16T15:00:00Z" },
          { scope: "gemini", window: "weekly", remaining: null, resets_at: null },
        ],
        limit_reached: false,
        source: "poll",
        fetched_at: "2026-10-16T11:56:00Z",
        checked_at: "2026-10-16T11:56:00Z",
        error: "",
      },
    },
    {
      account: account("2", "b@example.test"),
      quota: {
        plan: "",
        windows: [],
        limit_reached: false,
        source: "poll",
        checked_at: now.toISOString(),
        error: "sign in again",
      },
    },
  ],
};

function setup(provider: QuotaProvider) {
  const onRefresh = vi.fn<(t: RefreshTarget) => void>();
  render(
    <ProviderQuota provider={provider} rows={provider.accounts} now={now} pending={false} onRefresh={onRefresh} />,
  );
  return onRefresh;
}

test("the header counts accounts and refreshes the whole provider", async () => {
  const onRefresh = setup(antigravity);
  for (const text of ["Available 1", "Exhausted 1", "Sign in 2", "Next reset in 2h"])
    expect(screen.getByText(text)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Refresh all" }));
  expect(onRefresh).toHaveBeenCalledWith({ provider: "antigravity" });
});

test("a row shows each window, never 0% for unknown, the error and its own refresh", async () => {
  const onRefresh = setup(antigravity);
  expect(screen.getByText("Gemini · 5h · 80%")).toBeInTheDocument();
  expect(screen.getByText("Gemini · weekly · Unknown")).toBeInTheDocument();
  expect(screen.queryByText(/(^|\D)0%/)).toBeNull();
  expect(screen.getByText("updated 4m ago · poll")).toBeInTheDocument();
  expect(screen.getByText("sign in again")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Refresh quota of a@example.test" }));
  expect(onRefresh).toHaveBeenCalledWith({ account_id: "1" });
});

test("an unsupported provider says so and shows no meter", () => {
  setup({
    provider: "kimi",
    supported: false,
    available: 1,
    exhausted: 0,
    cooling: 0,
    sign_in: 0,
    accounts: [{ account: account("3", "k@example.test", "kimi") }],
  });
  expect(screen.getByText("Quota is not reported for this provider.")).toBeInTheDocument();
  expect(screen.queryByRole("meter")).toBeNull();
  expect(screen.queryByRole("button", { name: /Refresh/ })).toBeNull();
});
