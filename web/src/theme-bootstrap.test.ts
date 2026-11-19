import { readFileSync } from "node:fs";

import { afterEach, expect, test, vi } from "vitest";

function bootstrap(stored: string | null, prefersDark: boolean) {
  if (stored === null) localStorage.removeItem("heroui-theme");
  else localStorage.setItem("heroui-theme", stored);
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: prefersDark }));
  const html = readFileSync("index.html", "utf8");
  const script = html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
  expect(script).toBeDefined();
  window.eval(script!);
}

afterEach(() => {
  localStorage.clear();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("data-theme");
  vi.unstubAllGlobals();
});

test.each([
  ["dark", false, "dark"],
  ["light", true, "light"],
  ["system", true, "dark"],
  ["system", false, "light"],
  [null, true, "dark"],
  [null, false, "light"],
])("bootstrap resolves %s with system dark=%s to %s", (stored, prefersDark, expected) => {
  bootstrap(stored, prefersDark);
  expect(document.documentElement.dataset.theme).toBe(expected);
  expect(document.documentElement.classList.contains("dark")).toBe(expected === "dark");
});
