import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { client } from "../../../client/client.gen";
import { renderRoute } from "../../../test/render-route";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

const account = (id: string, provider: string, email: string) => ({
  id,
  provider,
  email,
  enabled: true,
  status: "active",
  quota: [],
  models: [],
});
const providers = [
  {
    provider: "antigravity",
    supported: true,
    available: 1,
    exhausted: 0,
    cooling: 0,
    sign_in: 0,
    suspended: 0,
    accounts: [
      {
        account: account("1", "antigravity", "a@example.test"),
        quota: {
          plan: "Pro",
          windows: [{ scope: "gemini", window: "5h", remaining: 0.8, resets_at: null }],
          limit_reached: false,
          source: "poll",
          fetched_at: new Date().toISOString(),
          checked_at: new Date().toISOString(),
          error: "",
        },
      },
    ],
  },
  {
    provider: "kimi",
    supported: false,
    available: 1,
    exhausted: 0,
    cooling: 0,
    sign_in: 0,
    suspended: 0,
    accounts: [{ account: account("2", "kimi", "k@example.test") }],
  },
];

test("one section per provider; Refresh all posts the provider and reports skipped accounts", async () => {
  const posted: unknown[] = [];
  renderRoute("/quota", async (req, url) => {
    if (url.pathname === "/api/quota" && req.method === "GET") return Response.json(providers);
    if (url.pathname === "/api/quota/refresh" && req.method === "POST") {
      posted.push(await req.json());
      return Response.json({ queued: [], skipped: ["1"] }, { status: 202 });
    }
  });
  const ag = await screen.findByRole("region", { name: "antigravity" });
  expect(screen.getByRole("region", { name: "kimi" })).toBeInTheDocument();
  await userEvent.click(within(ag).getByRole("button", { name: "Refresh all" }));
  expect(await screen.findByText("1 account(s) were checked less than a minute ago.")).toBeInTheDocument();
  expect(posted).toEqual([{ provider: "antigravity" }]);
});
