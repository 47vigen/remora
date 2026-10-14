import { globMatch, previewMatches, validatePattern } from "./glob";

// Cases mirror proxy.Allowed: "*" matches any name, a trailing "/*" matches any depth below its prefix, everything
// else follows Go's path.Match. Every expected value was checked against the Go implementation.
test.each([
  ["fast", "fast", true],
  ["fast", "faster", false],
  ["*", "fast", true],
  ["*", "openrouter/x", true], // bare * matches every name (server special case)
  ["antigravity/*", "antigravity/gemini-3", true],
  ["antigravity/*", "antigravity/a/b", true], // trailing /* matches any depth (server special case)
  ["antigravity/*", "antigravity/", false], // ... but needs something below the prefix
  ["antigravity/*", "antigravity", false],
  ["org/team/*", "org/team/m", true], // the prefix may hold a slash
  ["org/team/*", "org/team", false],
  ["up-?/*", "up-1/m/x", true], // ... or a glob
  ["*/*", "a/b/c", true],
  ["openai/gpt-*", "openai/gpt-5/x", false], // a non-trailing * still never crosses /
  ["a*", "a/b", false],
  ["*/gpt-*", "openai/gpt-5", true],
  ["fa?t", "fast", true],
  ["?", "é", true], // ? is one character, not one byte
  ["[à-ü]", "é", true],
  ["[a-c]ode", "bode", true],
  ["[!a-c]ode", "bode", true], // Go has no ! negation: the class is "!" plus a-c
  ["[^a-c]ode", "bode", false],
  ["[^a-c]ode", "dode", true],
  ["[\\]]", "]", true],
  ["a*b*c", "axxbyyc", true],
  ["\\*", "*", true],
  ["[", "[", false], // malformed → no match (Go returns ErrBadPattern)
  ["[]a]", "]", false],
  ["[a-]", "a", false],
  ["a\\", "a", false],
  ["*[", "x", false],
  ["x", "", false], // an empty name never matches
])("globMatch(%j, %j) = %j", (p, n, want) => expect(globMatch(p, n)).toBe(want));

test("validatePattern", () => {
  expect(validatePattern("")).toBe("Enter an alias name or provider/model.");
  expect(validatePattern("a b")).toBe("Spaces are not allowed.");
  expect(validatePattern("[")).toBe("Unclosed [ in pattern.");
  expect(validatePattern("ab[")).toBe("Unclosed [ in pattern.");
  expect(validatePattern("a\\")).toBe("A pattern cannot end with \\.");
  expect(validatePattern("[]a]")).toBe("Malformed range in [ ].");
  expect(validatePattern("antigravity/*")).toBeUndefined();
  expect(validatePattern("*")).toBeUndefined();
});

test("previewMatches", () =>
  expect(previewMatches(["fast", "or/*"], ["fast", "free", "or/x", "cp/y"])).toEqual(["fast", "or/x"]));
