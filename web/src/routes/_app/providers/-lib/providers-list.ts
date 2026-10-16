import type { SubscriptionRow } from "../-components/ProviderCards";
import type { Account, Provider } from "../../../../client/types.gen";
import type { ListSearch } from "../../../../lib/list-search";
import { OAUTH_PROVIDERS, OAuthProviderLabels } from "../../../../lib/providers";
import { type DisplayGroup, displayGroup } from "./provider-form";

export type ProvidersFilter = ListSearch & { group?: DisplayGroup; status?: Provider["status"] };

/** The most rows the list reads in one request; the API's page-size limit. */
export const FETCH_LIMIT = 200;

/**
 * Builds one page of the providers list. Subscription rows (one per subscription provider, with its account count)
 * come first, then the API-key, decisions and speech providers. The group and status filters and the paging apply
 * here, because a display group spans two kinds and subscription rows are not providers. The search `q` was
 * already applied by the server to `providers`; here it filters the subscription rows.
 */
export function providerListRows(
  providers: Provider[],
  accounts: Account[],
  f: ProvidersFilter,
): { subscriptions: SubscriptionRow[]; items: Provider[]; total: number } {
  const q = f.q.trim().toLowerCase();
  const showSubs = (f.group === undefined || f.group === "subscription") && f.status === undefined;
  const subs: SubscriptionRow[] = showSubs
    ? OAUTH_PROVIDERS.filter(
        (name) => q === "" || name.includes(q) || OAuthProviderLabels[name].toLowerCase().includes(q),
      ).map((name) => ({ provider: name, accounts: accounts.filter((a) => a.provider === name).length }))
    : [];
  const items =
    f.group === "subscription"
      ? []
      : providers.filter(
          (p) =>
            (f.group === undefined || displayGroup(p.kind) === f.group) &&
            (f.status === undefined || p.status === f.status),
        );

  const total = subs.length + items.length;
  const from = (f.page - 1) * f.pageSize;
  const to = from + f.pageSize;
  return {
    subscriptions: subs.slice(from, to),
    items: items.slice(Math.max(0, from - subs.length), Math.max(0, to - subs.length)),
    total,
  };
}
