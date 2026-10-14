import { parseRedirectUrl } from "./login";

const S = "st-1";

test.each([
  [
    `http://localhost:1455/auth/callback?code=abc&state=${S}`,
    { ok: true, url: `http://localhost:1455/auth/callback?code=abc&state=${S}` },
  ],
  [
    `  http://localhost:54545/callback?code=abc&state=${S}\n`,
    { ok: true, url: `http://localhost:54545/callback?code=abc&state=${S}` },
  ],
  [`http://localhost:51121/?code=abc`, { ok: true, url: `http://localhost:51121/?code=abc` }], // state absent: the server decides
  [
    `http://localhost:1455/auth/callback?code=abc&state=other`,
    { ok: false, error: "This address belongs to a different sign-in attempt. Start again." },
  ],
  [
    `http://localhost:1455/auth/callback?error=access_denied&state=${S}`,
    { ok: false, error: "The provider refused the sign-in: access_denied" },
  ],
  ["not a url", { ok: false, error: "Paste the full address from the browser's address bar." }],
  [
    "localhost:1455/auth/callback?code=abc",
    { ok: false, error: "Paste the full address from the browser's address bar." },
  ],
  [
    `http://localhost:1455/auth/callback?state=${S}`,
    { ok: false, error: "The address has no authorization code. Copy it after the sign-in finishes." },
  ],
])("parseRedirectUrl(%j)", (pasted, want) => expect(parseRedirectUrl(pasted, S)).toEqual(want));
