import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const SRC = join(__dirname);
const files = (dir: string): string[] =>
  readdirSync(dir).flatMap((n) => (statSync(join(dir, n)).isDirectory() ? files(join(dir, n)) : [join(dir, n)]));
const own = files(SRC).filter((f) => !f.includes("/client/") && !f.endsWith("routeTree.gen.ts"));

test("the only CSS is the HeroUI entry", () => {
  const css = own.filter((f) => f.endsWith(".css")).map((f) => relative(SRC, f));
  expect(css).toEqual(["index.css"]);
  expect(readFileSync(join(SRC, "index.css"), "utf8").trim()).toBe('@import "tailwindcss";\n@import "@heroui/styles";');
});
test("no inline styles", () => {
  for (const f of own.filter((f) => f.endsWith(".tsx"))) expect(readFileSync(f, "utf8"), f).not.toMatch(/\sstyle=\{/);
});
test("no hook files in component folders", () => {
  expect(own.filter((f) => /\/-?components\/.*\/?use[A-Z][^/]*\.tsx?$/.test(f))).toEqual([]);
});
test("no virtualization", () => {
  for (const f of own) expect(readFileSync(f, "utf8"), f).not.toMatch(/\bVirtualizer\b/);
});
test("no engine or vendor brand outside derived-file headers and credits", () => {
  for (const f of own.filter((f) => !f.endsWith(".test.ts") && !f.includes("/settings/-components/About"))) {
    const body = readFileSync(f, "utf8").replace(
      /^\/\/ Derived from maximhq\/bifrost ui\/\S+ \(Apache-2\.0\); modified\.\n/,
      "",
    );
    expect(body, f).not.toMatch(/bifrost|maxim|cliproxy/i);
  }
});
