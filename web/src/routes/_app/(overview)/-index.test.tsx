import { screen, within } from "@testing-library/react";

import { getStatsOptions } from "../../../client/@tanstack/react-query.gen";
import { client } from "../../../client/client.gen";
import { renderRoute } from "../../../test/render-route";
import { statsFrom } from "./index";

afterEach(() => client.setConfig({ baseUrl: "", fetch: undefined }));

test("shows the API timeout in each traffic chart", async () => {
  const message = "The request log took too long to answer; try a shorter range.";
  renderRoute("/", (_req, url) => {
    if (url.pathname === "/api/stats") {
      return Response.json({ error: { type: "api_error", code: "engine_timeout", message } }, { status: 504 });
    }
  });
  for (const title of ["Requests", "Tokens", "Cost", "Latency"]) {
    const heading = await screen.findByRole("heading", { name: title });
    expect(within(heading.closest("section")!).getByText(message)).toBeInTheDocument();
  }
});
describe("overview stats query", () => {
  it.each([
    ["1h", "2026-01-08T12:34:00.000Z", "2026-01-08T11:34:00.000Z", 60_000],
    ["7d", "2026-01-08T00:00:00.000Z", "2026-01-01T00:00:00.000Z", 86_400_000],
  ] as const)("keeps the %s key stable within a bucket", (range, start, expected, bucket) => {
    const now = Date.parse(start);
    const key = (time: number) =>
      getStatsOptions({ query: { group_by: "provider", from: statsFrom(range, time) } }).queryKey;
    expect(statsFrom(range, now + 1)).toBe(expected);
    expect(key(now + 1)).toEqual(key(now + bucket - 1));
    expect(key(now + bucket)).not.toEqual(key(now + 1));
  });

  it("leaves the default window to the server", () => {
    expect(statsFrom("24h", Date.now())).toBeUndefined();
  });
});
