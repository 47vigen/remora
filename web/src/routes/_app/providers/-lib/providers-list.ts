import type { Provider } from "../../../../client/types.gen";
import { OAUTH_PROVIDERS, OAuthProviderLabels } from "../../../../lib/providers";
import { type DisplayGroup, displayGroup, PROVIDER_KINDS } from "./provider-form";

export function providerKinds(group?: DisplayGroup) {
  return group && group !== "subscription" ? PROVIDER_KINDS.filter((kind) => displayGroup(kind) === group) : undefined;
}

export function subscriptionRows({
  q,
  group,
  status,
}: {
  q: string;
  group?: DisplayGroup;
  status?: Provider["status"];
}) {
  if ((group && group !== "subscription") || status) return [];
  const needle = q.toLowerCase();
  return OAUTH_PROVIDERS.filter(
    (name) => name.includes(needle) || OAuthProviderLabels[name].toLowerCase().includes(needle),
  );
}
