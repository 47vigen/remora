// The key allowlist rules, matching the server's proxy.Allowed exactly: "*" matches every name, a pattern ending in
// "/*" matches any depth below its prefix, and everything else follows Go's path.Match (a port follows below).
// Names and patterns are compared as Unicode code points, as Go compares valid UTF-8.

type PatternError = "unclosed" | "range" | "escape";

class BadPattern extends Error {
  constructor(readonly kind: PatternError) {
    super(kind);
  }
}

/** Splits off the leading run of stars and the chunk up to the next star outside a class (Go's scanChunk). */
function scanChunk(pattern: string[]): { star: boolean; chunk: string[]; rest: string[] } {
  let i = 0;
  let star = false;
  while (i < pattern.length && pattern[i] === "*") {
    i++;
    star = true;
  }
  const p = pattern.slice(i);
  let inRange = false;
  let j = 0;
  scan: for (; j < p.length; j++) {
    switch (p[j]) {
      case "\\":
        if (j + 1 < p.length) j++;
        break;
      case "[":
        inRange = true;
        break;
      case "]":
        inRange = false;
        break;
      case "*":
        if (!inRange) break scan;
        break;
    }
  }
  return { star, chunk: p.slice(0, j), rest: p.slice(j) };
}

/** Reads one possibly escaped character of a class (Go's getEsc). */
function getEsc(chunk: string[]): { r: string; rest: string[] } {
  if (chunk.length === 0) throw new BadPattern("unclosed");
  if (chunk[0] === "-" || chunk[0] === "]") throw new BadPattern("range");
  let c = chunk;
  if (c[0] === "\\") {
    c = c.slice(1);
    if (c.length === 0) throw new BadPattern("unclosed");
  }
  const rest = c.slice(1);
  if (rest.length === 0) throw new BadPattern("unclosed");
  return { r: c[0]!, rest };
}

const cp = (s: string) => s.codePointAt(0)!;

/** Matches one chunk at the start of `s`; returns the rest of `s`, or null on no match (Go's matchChunk). */
function matchChunk(chunk: string[], s: string[]): string[] | null {
  let failed = false;
  let c = chunk;
  let rest = s;
  while (c.length > 0) {
    if (!failed && rest.length === 0) failed = true;
    const head = c[0]!;
    if (head === "[") {
      let r = "";
      if (!failed) {
        r = rest[0]!;
        rest = rest.slice(1);
      }
      c = c.slice(1);
      let negated = false;
      if (c.length > 0 && c[0] === "^") {
        negated = true;
        c = c.slice(1);
      }
      let match = false;
      let ranges = 0;
      for (;;) {
        if (c.length > 0 && c[0] === "]" && ranges > 0) {
          c = c.slice(1);
          break;
        }
        const lo = getEsc(c);
        let hi = lo.r;
        c = lo.rest;
        if (c[0] === "-") {
          const h = getEsc(c.slice(1));
          hi = h.r;
          c = h.rest;
        }
        if (!failed && cp(lo.r) <= cp(r) && cp(r) <= cp(hi)) match = true;
        ranges++;
      }
      if (match === negated) failed = true;
    } else if (head === "?") {
      if (!failed) {
        if (rest[0] === "/") failed = true;
        rest = rest.slice(1);
      }
      c = c.slice(1);
    } else {
      if (head === "\\") {
        c = c.slice(1);
        if (c.length === 0) throw new BadPattern("escape");
      }
      if (!failed) {
        if (c[0] !== rest[0]) failed = true;
        rest = rest.slice(1);
      }
      c = c.slice(1);
    }
  }
  return failed ? null : rest;
}

/** Go's path.Match. Throws BadPattern where Go returns ErrBadPattern. */
function pathMatch(pattern: string, name: string): boolean {
  let p = Array.from(pattern);
  let n = Array.from(name);
  outer: while (p.length > 0) {
    const { star, chunk, rest } = scanChunk(p);
    p = rest;
    if (star && chunk.length === 0) return !n.includes("/");
    const t = matchChunk(chunk, n);
    if (t !== null && (t.length === 0 || p.length > 0)) {
      n = t;
      continue;
    }
    if (star) {
      for (let i = 0; i < n.length && n[i] !== "/"; i++) {
        const t2 = matchChunk(chunk, n.slice(i + 1));
        if (t2 !== null) {
          if (p.length === 0 && t2.length > 0) continue;
          n = t2;
          continue outer;
        }
      }
    }
    // Before failing, check that the rest of the pattern is well-formed, as Go does.
    while (p.length > 0) {
      const next = scanChunk(p);
      p = next.rest;
      matchChunk(next.chunk, []);
    }
    return false;
  }
  return n.length === 0;
}

function safeMatch(pattern: string, name: string): boolean {
  try {
    return pathMatch(pattern, name);
  } catch (err) {
    if (err instanceof BadPattern) return false;
    throw err;
  }
}

/** Whether `name` (an alias or `<provider>/<model>`) is allowed by `pattern`; a malformed pattern never matches. */
export function globMatch(pattern: string, name: string): boolean {
  if (name === "") return false;
  if (pattern === "*") return true;
  if (pattern.endsWith("/*")) {
    const prefix = pattern.slice(0, -2);
    const depth = prefix.split("/").length - 1;
    const segments = name.split("/");
    if (segments.length < depth + 2) return false;
    const head = segments.slice(0, depth + 1).join("/");
    const below = segments.slice(depth + 1).join("/");
    if (below === "") return false;
    return safeMatch(prefix, head);
  }
  return safeMatch(pattern, name);
}

/** Checks the whole pattern's syntax, every chunk, the way Go does on a failed match. */
function patternError(pattern: string): PatternError | undefined {
  let p = Array.from(pattern);
  try {
    while (p.length > 0) {
      const next = scanChunk(p);
      p = next.rest;
      matchChunk(next.chunk, []);
    }
  } catch (err) {
    if (err instanceof BadPattern) return err.kind;
    throw err;
  }
  return undefined;
}

const PATTERN_ERRORS: Record<PatternError, string> = {
  unclosed: "Unclosed [ in pattern.",
  range: "Malformed range in [ ].",
  escape: "A pattern cannot end with \\.",
};

/** The reason a pattern cannot be saved, or undefined when it is fine. */
export function validatePattern(p: string): string | undefined {
  if (p.trim() === "") return "Enter an alias name or provider/model.";
  if (/\s/.test(p)) return "Spaces are not allowed.";
  const err = patternError(p);
  return err ? PATTERN_ERRORS[err] : undefined;
}

/** The names any of the patterns allow, in the order given. */
export function previewMatches(patterns: string[], names: string[]): string[] {
  return names.filter((n) => patterns.some((p) => globMatch(p, n)));
}
