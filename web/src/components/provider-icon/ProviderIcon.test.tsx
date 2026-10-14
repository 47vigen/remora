import { readFileSync } from "node:fs";
import { join } from "node:path";

import { render } from "@testing-library/react";

import { ProviderIcon } from "./ProviderIcon";

test.each(["openai", "codex", "anthropic", "claude", "openrouter", "antigravity"])("renders an svg for %s", (t) => {
  const { container } = render(<ProviderIcon type={t} />);
  expect(container.querySelector("svg")).not.toBeNull();
});

test("unknown type renders nothing", () => {
  const { container } = render(<ProviderIcon type="__proto__" />);
  expect(container.firstChild).toBeNull();
});

test("no copied icon references an image file", () => {
  expect(readFileSync(join(__dirname, "icons.tsx"), "utf8")).not.toMatch(/<img|\/images\//);
});
