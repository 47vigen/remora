import type { Account } from "../../../../client/types.gen";
import { accountHealth, isCooling } from "./accounts";

const now = Date.parse("2026-10-14T10:00:00Z");
const a = (patch: Partial<Account>): Account => ({
  id: "1",
  provider: "codex",
  enabled: true,
  status: "active",
  quota: [],
  models: [],
  ...patch,
});

test("accountHealth", () => {
  expect(accountHealth(a({}), now)).toBe("active");
  expect(accountHealth(a({ enabled: false, cooldown_until: "2026-10-14T11:00:00Z" }), now)).toBe("disabled");
  expect(accountHealth(a({ cooldown_until: "2026-10-14T11:00:00Z" }), now)).toBe("cooling");
  expect(accountHealth(a({ cooldown_until: "2026-10-14T09:00:00Z" }), now)).toBe("active");
  expect(accountHealth(a({ status: "error" }), now)).toBe("error");
  expect(accountHealth(a({ status: "missing" }), now)).toBe("error");
  expect(accountHealth(a({ status: "refreshing" }), now)).toBe("unknown");
});

test("isCooling ignores a bad timestamp", () => {
  expect(isCooling(a({ cooldown_until: "soon" }), now)).toBe(false);
});
