// Derived from maximhq/bifrost ui/app/workspace/logs/views/columns.tsx (Apache-2.0); modified.
import { formatCompactNumber } from "../../../../lib/numbers";
import { formatRelative } from "../../../../lib/time";

type ContentBlock = { type: string; text?: string };
export type ChatMessageContent = string | ContentBlock[];

/** The text of a chat message: the string itself, or the last text block of a block list. */
export function getMessageFromContent(content?: ChatMessageContent): string {
  if (content == undefined) {
    return "";
  }
  if (typeof content === "string") {
    return content;
  }
  let lastTextContentBlock = "";
  for (const block of content) {
    if ((block.type === "text" || block.type === "input_text" || block.type === "output_text") && block.text) {
      lastTextContentBlock = block.text;
    }
  }
  return lastTextContentBlock;
}

/**
 * Re-indents a JSON document without parsing its values: strings, numbers and escapes are copied exactly as they
 * were sent, so the text a user sees is the text that went over the wire, only laid out on more lines.
 */
function reindent(raw: string): string {
  let out = "";
  let depth = 0;
  const newline = () => `\n${"  ".repeat(depth)}`;
  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i]!;
    if (ch === '"') {
      let j = i + 1;
      while (j < raw.length && raw[j] !== '"') j += raw[j] === "\\" ? 2 : 1;
      out += raw.slice(i, j + 1);
      i = j;
    } else if (ch === "{" || ch === "[") {
      const close = ch === "{" ? "}" : "]";
      let j = i + 1;
      while (j < raw.length && /\s/.test(raw[j]!)) j++;
      if (raw[j] === close) {
        out += ch + close;
        i = j;
      } else {
        depth++;
        out += ch + newline();
      }
    } else if (ch === "}" || ch === "]") {
      depth--;
      out += newline() + ch;
    } else if (ch === ",") {
      out += "," + newline();
    } else if (ch === ":") {
      out += ": ";
    } else if (!/\s/.test(ch)) {
      out += ch;
    }
  }
  return out;
}

/** A JSON object or array laid out for reading; anything else (an SSE stream, plain text) is returned as is. */
export function formatBody(raw: string): { text: string; isJson: boolean } {
  const head = raw.trimStart()[0];
  if (head !== "{" && head !== "[") return { text: raw, isJson: false };
  try {
    JSON.parse(raw);
  } catch {
    return { text: raw, isJson: false };
  }
  return { text: reindent(raw), isJson: true };
}

/** Prompt and completion tokens, e.g. "1.2K → 340". */
export function formatTokens(prompt: number, completion: number): string {
  return `${formatCompactNumber(prompt)} → ${formatCompactNumber(completion)}`;
}

const timeFormat = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});

/** Relative under an hour ("12s ago"), otherwise the local date and time ("Oct 14, 10:02:03"). */
export function formatTime(iso: string, now: Date): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "–";
  if (Math.abs(now.getTime() - t.getTime()) < 3_600_000) return formatRelative(iso, now);
  return timeFormat.format(t);
}
