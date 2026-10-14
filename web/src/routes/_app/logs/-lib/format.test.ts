import { formatBody, formatTime, formatTokens, getMessageFromContent } from "./format";

const ZWNJ = "‌";

test("formatBody preserves Persian text with ZWNJ byte for byte", () => {
  const raw = `{"content":"می${ZWNJ}خواهم"}`;
  const out = formatBody(raw);
  expect(out.isJson).toBe(true);
  expect(out.text).toContain(`می${ZWNJ}خواهم`);
  expect(out.text).toBe(`{\n  "content": "می${ZWNJ}خواهم"\n}`);
  expect(JSON.parse(out.text)).toEqual(JSON.parse(raw));
});

test("formatBody shows a stream raw", () => {
  const sse = `data: {"choices":[{"delta":{"content":"سلام"}}]}\n\ndata: [DONE]\n\n`;
  expect(formatBody(sse)).toEqual({ text: sse, isJson: false });
});

test("formatBody leaves a bare JSON scalar and plain text alone", () => {
  expect(formatBody("hello")).toEqual({ text: "hello", isJson: false });
  expect(formatBody("42")).toEqual({ text: "42", isJson: false });
});

test("formatTokens / formatTime", () => {
  expect(formatTokens(1200, 340)).toBe("1.2K → 340");
  expect(formatTime("2026-10-14T10:00:00Z", new Date("2026-10-14T10:00:12Z"))).toBe("12s ago");
  expect(formatTime("2026-10-14T08:02:03Z", new Date("2026-10-14T10:00:00Z"))).toMatch(/^Oct 14, \d\d:02:03$/);
});

test("getMessageFromContent reads the last text block", () => {
  expect(getMessageFromContent("plain")).toBe("plain");
  expect(
    getMessageFromContent([{ type: "text", text: "a" }, { type: "image_url" }, { type: "output_text", text: "b" }]),
  ).toBe("b");
  expect(getMessageFromContent(undefined)).toBe("");
});

test("formatBody never rewrites values: big numbers and escapes stay as sent", () => {
  const raw = '{"n":12345678901234567890,"s":"a\\u200cb \\"q\\"","e":{},"l":[1, 2.50]}';
  expect(formatBody(raw).text).toBe(
    '{\n  "n": 12345678901234567890,\n  "s": "a\\u200cb \\"q\\"",\n  "e": {},\n  "l": [\n    1,\n    2.50\n  ]\n}',
  );
});
