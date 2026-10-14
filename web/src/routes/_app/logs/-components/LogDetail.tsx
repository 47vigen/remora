import { Alert, Button, Chip, Drawer, Spinner, Tabs } from "@heroui/react";
import type { ReactNode } from "react";

import { type ChatMessageContent, formatBody, formatTokens, getMessageFromContent } from "../-lib/format";
import type { LogDetail as Detail, LogEntry } from "../../../../client";
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

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted text-xs">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

function Body({ value, streamed }: { value: unknown; streamed: boolean }) {
  const raw = bodyText(value);
  if (raw === undefined) return <p className="text-muted text-sm">Not recorded.</p>;
  const { text } = formatBody(raw);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted text-sm">{streamed ? "Streamed response, shown as received." : null}</span>
        <Button size="sm" variant="secondary" onPress={() => void navigator.clipboard.writeText(text)}>
          Copy
        </Button>
      </div>
      <pre dir="auto" className="bg-surface-secondary rounded-md p-3 font-mono text-xs break-words whitespace-pre-wrap">
        {text}
      </pre>
    </div>
  );
}

function Overview({ log }: { log: Detail }) {
  const fallback = log.attempt > 1 || log.parent_id !== undefined;
  const prompt = excerpt(log.request, "request");
  const reply = excerpt(log.response, "response");
  return (
    <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <Field label="Status">
        <StatusChip status={statusOf(log.status)} />
      </Field>
      <Field label="Time">
        <time dateTime={log.time}>{new Date(log.time).toLocaleString()}</time>
      </Field>
      <Field label="Alias">{log.alias ?? "–"}</Field>
      <Field label="Served by">
        <span className="flex flex-wrap items-center gap-2">
          <ProviderIcon type={log.provider} size="xs" />
          <span className="font-mono">
            {log.provider}/{log.model}
          </span>
          {fallback ? (
            <Chip size="sm" variant="soft" color="warning">
              <Chip.Label>fallback</Chip.Label>
            </Chip>
          ) : null}
        </span>
      </Field>
      <Field label="Key">{log.key ?? "–"}</Field>
      <Field label="Latency">{log.latency_ms !== undefined ? formatLatency(log.latency_ms) : "–"}</Field>
      <Field label="Tokens">{formatTokens(log.tokens.prompt, log.tokens.completion)}</Field>
      <Field label="Cost">{log.cost_usd !== undefined ? formatCost(log.cost_usd) : "–"}</Field>
      <Field label="Attempt">{log.attempt}</Field>
      <Field label="Stream">{log.stream ? "Yes" : "No"}</Field>
      {log.error ? (
        <div className="sm:col-span-2">
          <Field label="Error">
            <span dir="auto" className="text-danger">
              {log.error}
            </span>
          </Field>
        </div>
      ) : null}
      {prompt ? (
        <div className="sm:col-span-2">
          <Field label="Last message">
            <span dir="auto" className="whitespace-pre-wrap">
              {prompt}
            </span>
          </Field>
        </div>
      ) : null}
      {reply ? (
        <div className="sm:col-span-2">
          <Field label="Reply">
            <span dir="auto" className="whitespace-pre-wrap">
              {reply}
            </span>
          </Field>
        </div>
      ) : null}
    </dl>
  );
}

function FallbackPath({ attempts }: { attempts: LogEntry[] }) {
  const served = attempts.findLastIndex((a) => a.status === "success");
  if (attempts.length === 0) return <p className="text-muted text-sm">This request made one attempt.</p>;
  return (
    <ol className="flex flex-col gap-3">
      {attempts.map((a, i) => (
        <li key={a.id} className="border-border flex flex-col gap-1 rounded-md border p-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted w-5 text-right text-sm tabular-nums">{i + 1}</span>
            <ProviderIcon type={a.provider} size="xs" />
            <span className="font-mono text-sm">
              {a.provider}/{a.model}
            </span>
            <StatusChip status={statusOf(a.status)} />
            {a.latency_ms !== undefined ? (
              <span className="text-muted text-sm">{formatLatency(a.latency_ms)}</span>
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
  isLoading,
  onClose,
}: {
  log: Detail | undefined;
  isLoading: boolean;
  onClose(): void;
}) {
  return (
    <Drawer.Backdrop isOpen onOpenChange={(open) => !open && onClose()}>
      <Drawer.Content placement="right" className="sm:max-w-2xl">
        <Drawer.Dialog>
          <Drawer.Header>
            <Drawer.Heading>Request</Drawer.Heading>
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
              <Tabs aria-label="Log detail">
                <Tabs.ListContainer>
                  <Tabs.List aria-label="Log detail">
                    <Tabs.Tab id="overview">
                      Overview
                      <Tabs.Indicator />
                    </Tabs.Tab>
                    <Tabs.Tab id="request">
                      Request
                      <Tabs.Indicator />
                    </Tabs.Tab>
                    <Tabs.Tab id="response">
                      Response
                      <Tabs.Indicator />
                    </Tabs.Tab>
                    <Tabs.Tab id="fallback">
                      Fallback path
                      <Tabs.Indicator />
                    </Tabs.Tab>
                  </Tabs.List>
                </Tabs.ListContainer>
                <Tabs.Panel id="overview" className="pt-4">
                  <Overview log={log} />
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
            )}
          </Drawer.Body>
        </Drawer.Dialog>
      </Drawer.Content>
    </Drawer.Backdrop>
  );
}
