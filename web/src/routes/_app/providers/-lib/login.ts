export type ParsedRedirect = { ok: true; url: string } | { ok: false; error: string };

/** How often the dialog asks whether the sign-in finished. */
export const LOGIN_POLL_MS = 2000;
/** How long the dialog waits for a sign-in before it gives up. */
export const LOGIN_DEADLINE_MS = 600_000;

/**
 * Checks the address a remote browser landed on after signing in. A `state` that differs from the one this login
 * started with belongs to another attempt; an absent `state` is left for the server to judge.
 */
export function parseRedirectUrl(pasted: string, expectedState: string): ParsedRedirect {
  const text = pasted.trim();
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return { ok: false, error: "Paste the full address from the browser's address bar." };
  }
  if (url.protocol !== "http:" && url.protocol !== "https:")
    return { ok: false, error: "Paste the full address from the browser's address bar." };

  const state = url.searchParams.get("state");
  if (state !== null && state !== "" && state !== expectedState)
    return { ok: false, error: "This address belongs to a different sign-in attempt. Start again." };
  const error = url.searchParams.get("error");
  if (error) return { ok: false, error: `The provider refused the sign-in: ${error}` };
  if (!url.searchParams.get("code"))
    return { ok: false, error: "The address has no authorization code. Copy it after the sign-in finishes." };
  return { ok: true, url: text };
}
