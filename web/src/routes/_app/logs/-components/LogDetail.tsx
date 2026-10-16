import { Copy } from "@gravity-ui/icons";
import { Alert, Button, Chip, Drawer, Spinner, Tabs, ToggleButton } from "@heroui/react";
import { type ReactNode, useState } from "react";

import { type ChatMessageContent, formatBody, formatTokens, getMessageFromContent } from "../-lib/format";
import type { LogDetail as Detail, LogEntry } from "../../../../client";
import { CodeBlock } from "../../../../components/code-block/CodeBlock";
import { ProviderIcon } from "../../../../components/provider-icon/ProviderIcon";
import { StatusChip } from "../../../../components/status-chip/StatusChip";
import { formatCost, formatLatency } from "../../../../lib/chart";

const statusOf = (s: LogEntry["status"]) => (s === "success" ? "success" : s === "error" ? "error" : "unknown");

/** A stored body as text: a string as it was stored, anything else as compact JSON (non-ASCII is not escaped). */
function bodyText(v: unknown): string | undefined {
  if (v === undefined || v === null) return undefined;
  return typeof v === "string" ? v : JSON.stringify(v);
}

/** The last chat message of a request or the first choice of a response, when the body has that shape. */
function excerpt(body: unknown, side: "request" | "response"): string {
  if (typeof body !== "object" || body === null) return "";
  if (side === "request") {
    const messages = (body as { messages?: { content?: ChatMessageContent }[] }).messages;
    return Array.isArray(messages) ? getMessageFromContent(messages.at(-1)?.content) : "";
  }
  const choices = (body as { choices?: { message?: { content?: ChatMessageContent } }[] }).choices;
  return Array.isArray(choices) ? getMessageFromContent(choices[0]?.message?.content) : "";
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="border-border grid grid-cols-[7.5rem_1fr] items-baseline gap-3 border-b py-2 last:border-b-0">
      <dt className="label-caps text-muted">{label}</dt>
      <dd className="min-w-0 text-[13px]">{children}</dd>
    </div>
  );
}

function Body({ value, streamed }: { value: unknown; streamed: boolean }) {
  const [wrap, setWrap] = useState(true);
  const raw = bodyText(value);
  if (raw === undefined) return <p className="text-muted text-sm">Not recorded.</p>;
  const { text, isJson } = formatBody(raw);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted text-sm">{streamed ? "Streamed response, shown as received." : null}</span>
        <div className="flex items-center gap-1">
          <ToggleButton size="sm" isSelected={wrap} onChange={setWrap}>
            Wrap
          </ToggleButton>
          <Button size="sm" variant="secondary" onPress={() => void navigator.clipboard.writeText(text)}>
            <Copy aria-hidden="true" className="size-4" />
            Copy
          </Button>
        </div>
      </div>
      <CodeBlock code={text} lang={isJson ? "json" : "text"} wrap={wrap} className="max-h-[60vh]" />
    </div>
  );
}

/** A JSON error (an upstream body) gets a code panel; a one-line message stays text. */
function ErrorBody({ error }: { error: string }) {
  const { text, isJson } = formatBody(error);
  return isJson ? (
    <CodeBlock code={text} className="bg-surface mt-1 max-h-48" />
  ) : (
    <Alert.Description dir="auto" className="font-mono text-xs break-words">
      {error}
    </Alert.Description>
  );
}

function Excerpt({ label, text }: { label: string; text: string }) {
  return (
    <div className="flex flex-col gap-1.5">
      <h3 className="label-caps text-muted">{label}</h3>
      <p dir="auto" className="bg-surface-secondary rounded-sm p-3 text-sm whitespace-pre-wrap">
        {text}
      </p>
    </div>
  );
}

function Overview({ log, keyName }: { log: Detail; keyName?: string }) {
  const prompt = excerpt(log.request, "request");
  const reply = excerpt(log.response, "response");
  return (
    <div className="flex flex-col gap-5">
      <dl className="flex flex-col">
        <Fact label="Provider">
          <span className="flex items-center gap-2 font-mono">
            <ProviderIcon type={log.provider} size="xs" />
            {log.provider}
          </span>
        </Fact>
        <Fact label="Alias">
          <span className="font-mono">{log.alias ?? "–"}</span>
        </Fact>
        <Fact label="Key">{keyName ?? <span className="font-mono">{log.key ?? "–"}</span>}</Fact>
        <Fact label="Latency">
          <span className="font-mono tabular-nums">
            {log.latency_ms !== undefined ? formatLatency(log.latency_ms) : "–"}
          </span>
        </Fact>
        <Fact label="Tokens">
          <span className="font-mono tabular-nums">{formatTokens(log.tokens.prompt, log.tokens.completion)}</span>
        </Fact>
        <Fact label="Cost">
          <span className="font-mono tabular-nums">{log.cost_usd !== undefined ? formatCost(log.cost_usd) : "–"}</span>
        </Fact>
        <Fact label="Attempt">
          <span className="font-mono tabular-nums">{log.attempt}</span>
        </Fact>
        <Fact label="Stream">{log.stream ? "Yes" : "No"}</Fact>
      </dl>
      {prompt ? <Excerpt label="Last message" text={prompt} /> : null}
      {reply ? <Excerpt label="Reply" text={reply} /> : null}
    </div>
  );
}

function FallbackPath({ attempts }: { attempts: LogEntry[] }) {
  const served = attempts.findLastIndex((a) => a.status === "success");
  if (attempts.length === 0) return <p className="text-muted text-sm">This request made one attempt.</p>;
  return (
    <ol className="flex flex-col gap-3">
      {attempts.map((a, i) => (
        <li key={a.id} className="border-border flex flex-col gap-1 rounded-sm border p-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted w-5 text-right text-sm tabular-nums">{i + 1}</span>
            <ProviderIcon type={a.provider} size="xs" />
            <span className="font-mono text-[13px]">
              {a.provider}/{a.model}
            </span>
            <StatusChip status={statusOf(a.status)} />
            {a.latency_ms !== undefined ? (
              <span className="text-muted font-mono text-xs tabular-nums">{formatLatency(a.latency_ms)}</span>
            ) : null}
            {i === served ? (
              <Chip size="sm" variant="primary" color="success">
                <Chip.Label>Served</Chip.Label>
              </Chip>
            ) : null}
          </div>
          {a.error ? (
            <p dir="auto" className="text-danger text-sm">
              {a.error}
            </p>
          ) : null}
        </li>
      ))}
    </ol>
  );
}

/** The `?log=<id>` drawer: overview, request and response bodies, and every attempt of the request. */
export function LogDetail({
  log,
  keyName,
  isLoading,
  onClose,
}: {
  log: Detail | undefined;
  keyName?: string;
  isLoading: boolean;
  onClose(): void;
}) {
  return (
    <Drawer.Backdrop isOpen onOpenChange={(open) => !open && onClose()}>
      <Drawer.Content placement="right">
        <Drawer.Dialog className="sm:w-2xl">
          <Drawer.CloseTrigger />
          <Drawer.Header className="border-border flex flex-col items-start gap-2 border-b pb-4">
            <span className="label-caps text-muted">Request</span>
            <Drawer.Heading className="text-base font-medium">
              {log ? (
                <span className="flex items-center gap-2">
                  <ProviderIcon type={log.provider} size="xs" />
                  <span className="font-mono">
                    {log.provider}/{log.model}
                  </span>
                </span>
              ) : (
                "Log entry"
              )}
            </Drawer.Heading>
            {log ? (
              <div className="flex flex-wrap items-center gap-2">
                <StatusChip status={statusOf(log.status)} />
                {log.attempt > 1 || log.parent_id !== undefined ? (
                  <Chip size="sm" variant="soft" color="warning">
                    <Chip.Label className="text-[11px]">fallback</Chip.Label>
                  </Chip>
                ) : null}
                <time dateTime={log.time} className="text-muted font-mono text-xs tabular-nums">
                  {new Date(log.time).toLocaleString()}
                </time>
                <span className="text-muted font-mono text-xs select-all">{log.id}</span>
              </div>
            ) : null}
          </Drawer.Header>
          <Drawer.Body>
            {isLoading ? (
              <Spinner />
            ) : !log ? (
              <Alert status="danger" role="alert">
                <Alert.Indicator />
                <Alert.Content>
                  <Alert.Title>This log entry could not be loaded.</Alert.Title>
                </Alert.Content>
              </Alert>
            ) : (
              <div className="flex flex-col gap-4 pt-1">
                {log.error ? (
                  <Alert status="danger">
                    <Alert.Indicator />
                    <Alert.Content>
                      <Alert.Title>The request failed</Alert.Title>
                      <ErrorBody error={log.error} />
                    </Alert.Content>
                  </Alert>
                ) : null}
                <Tabs aria-label="Log detail">
                  <Tabs.ListContainer className="w-fit">
                    <Tabs.List aria-label="Log detail" className="p-0.5">
                      <Tabs.Tab id="overview" className="h-7 px-3 text-[13px] whitespace-nowrap">
                        Overview
                        <Tabs.Indicator />
                      </Tabs.Tab>
                      <Tabs.Tab id="request" className="h-7 px-3 text-[13px] whitespace-nowrap">
                        Request
                        <Tabs.Indicator />
                      </Tabs.Tab>
                      <Tabs.Tab id="response" className="h-7 px-3 text-[13px] whitespace-nowrap">
                        Response
                        <Tabs.Indicator />
                      </Tabs.Tab>
                      <Tabs.Tab id="fallback" className="h-7 px-3 text-[13px] whitespace-nowrap">
                        Fallback path
                        <Tabs.Indicator />
                      </Tabs.Tab>
                    </Tabs.List>
                  </Tabs.ListContainer>
                  <Tabs.Panel id="overview" className="pt-4">
                    <Overview log={log} keyName={keyName} />
                  </Tabs.Panel>
                  <Tabs.Panel id="request" className="pt-4">
                    <Body value={log.request} streamed={false} />
                  </Tabs.Panel>
                  <Tabs.Panel id="response" className="pt-4">
                    <Body value={log.response} streamed={log.stream} />
                  </Tabs.Panel>
                  <Tabs.Panel id="fallback" className="pt-4">
                    <FallbackPath attempts={log.fallback_path} />
                  </Tabs.Panel>
                </Tabs>
              </div>
            )}
          </Drawer.Body>
        </Drawer.Dialog>
      </Drawer.Content>
    </Drawer.Backdrop>
  );
}
