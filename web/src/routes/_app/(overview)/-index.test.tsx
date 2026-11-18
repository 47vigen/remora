import { getStatsOptions } from "../../../client/@tanstack/react-query.gen";
import { statsFrom } from "./index";

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
