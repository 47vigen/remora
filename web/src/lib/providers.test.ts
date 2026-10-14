// Derived from maximhq/bifrost ui/lib/constants/logs.test.ts (Apache-2.0); modified.
import { describe, expect, it } from "vitest";

import {
  getOAuthProviderLabel,
  getProviderLabel,
  ICON_FOR_OAUTH,
  isOAuthProvider,
  KnownProvidersNames,
  OAUTH_PROVIDERS,
  RequestTypeLabels,
  RequestTypes,
} from "./providers";

describe("logs constants", () => {
  it("registers realtime turn as a known request type", () => {
    expect(RequestTypes).toContain("realtime.turn");
    expect(RequestTypeLabels["realtime.turn"]).toBe("Realtime Turn");
  });

  it("registers model retrieve as a known request type", () => {
    expect(RequestTypes).toContain("model_retrieve");
    expect(RequestTypeLabels.model_retrieve).toBe("Retrieve Model");
  });

  it("registers live sessions as known request types", () => {
    for (const type of ["live", "live.session", "live_content", "live_sideband"] as const) {
      expect(RequestTypes).toContain(type);
      expect(RequestTypeLabels[type]).toBeTruthy();
    }
    expect(RequestTypeLabels["live.session"]).toBe("Live Session");
    expect(RequestTypeLabels["live_content"]).toBe("Live Recording");
    expect(RequestTypeLabels["live_sideband"]).toBe("Live Sideband");
  });
});

describe("getProviderLabel", () => {
  it("labels a known provider, ignoring case and spaces", () => {
    expect(getProviderLabel("openai")).toBe("OpenAI");
    expect(getProviderLabel(" OpenRouter ")).toBe("OpenRouter");
  });

  it("returns a custom provider's name as is", () => {
    expect(getProviderLabel("my-gateway")).toBe("my-gateway");
  });

  it("does not treat inherited object keys as providers", () => {
    expect(getProviderLabel("constructor")).toBe("constructor");
  });
});

describe("subscription providers", () => {
  it("lists the names users see, never the engine's", () => {
    expect(OAUTH_PROVIDERS).toEqual(["antigravity", "claude", "codex", "devin", "grok", "kimi", "kimi-ai", "meta"]);
    expect(OAUTH_PROVIDERS).not.toContain("xai");
    expect(getOAuthProviderLabel("kimi-ai")).toBe("Kimi AI");
    expect(isOAuthProvider("grok")).toBe(true);
    expect(isOAuthProvider("openai")).toBe(false);
  });

  it("borrows icons only from known keys", () => {
    expect(ICON_FOR_OAUTH).toEqual({ codex: "openai", claude: "anthropic", antigravity: "gemini", grok: "xai" });
    for (const v of Object.values(ICON_FOR_OAUTH)) expect(KnownProvidersNames).toContain(v);
  });
});
