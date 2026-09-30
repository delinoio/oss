// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { expect, test } from "vitest";
import { document, encode, items, object, type Document } from "./documents";
import { Subagents } from "./subagents";
import { subagentFixture } from "./subagent-test-fixture";

function edit(row: Resource, change: (value: Document) => void) { const value = document(row); change(value); row.documentJson = encode(value); }
function codexUsage() { return { scope: "child-cumulative", total: "9223372036854775807", input: "0", output: "0", native_report: '{"total":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0,"totalTokens":9223372036854775807},"last":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0,"totalTokens":0},"modelContextWindow":null}' }; }
function mountPage(session: string, rows: Resource[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBAGENT_OBSERVATION_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: rows, nextPageToken: "next-original-page" }) });
  });
  const mounted = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><Subagents sessionId={session} revision="1" /></TransportProvider></QueryClientProvider>);
  return () => { mounted.unmount(); client.clear(); };
}

test.each(["foreign-session", "kind", "schema", "revision", "resource-id", "invalid-json", "utf8", "oversize", "duplicate-key", "harness", "version", "execution", "root", "child-id", "native-id", "parent-self", "status", "sources-empty", "source-family", "source-identity", "sequence-number", "sequence-range", "sequence-latest", "nonpartial", "block", "usage-number", "usage-scope", "usage-parity", "usage-family", "usage-duplicate", "usage-native-string"])("rejects the complete child page before exposing any content: %s", async (mode) => {
  const session = newRequestId(), valid = subagentFixture(session), invalid = subagentFixture(session);
  if (mode === "foreign-session") invalid.sessionId = newRequestId();
  else if (mode === "kind") invalid.kind = EntityKind.MESSAGE;
  else if (mode === "schema") invalid.schemaVersion = 2;
  else if (mode === "revision") invalid.revision = 0n;
  else if (mode === "resource-id") invalid.id = "foreign-id";
  else if (mode === "invalid-json") invalid.documentJson = new TextEncoder().encode("[]");
  else if (mode === "utf8") invalid.documentJson = new Uint8Array([255]);
  else if (mode === "oversize") invalid.documentJson = new Uint8Array((1 << 20) + 1);
  else if (mode === "duplicate-key") invalid.documentJson = new TextEncoder().encode(new TextDecoder().decode(invalid.documentJson).replace('"harness":"codex"', '"harness":"claude-code","harness":"codex"'));
  else edit(invalid, value => {
    const child = object(value.observation), source = object(items(value.sources)[0]);
    if (mode === "harness") value.harness = "unsupported";
    if (mode === "version") value.native_version = "0.151.1";
    if (mode === "execution") value.execution_id = "foreign-execution";
    if (mode === "root") value.root_id = child.native_id;
    if (mode === "child-id") child.id = newRequestId();
    if (mode === "native-id") child.native_id = "not-a-codex-uuid";
    if (mode === "parent-self") child.parent_id = child.native_id;
    if (mode === "status") child.status = "invented";
    if (mode === "sources-empty") value.sources = [];
    if (mode === "source-family") source.source = "claude-task";
    if (mode === "source-identity") child.source_id = "different-original";
    if (mode === "sequence-number") source.sequence = 3;
    if (mode === "sequence-range") value.first_sequence = "4";
    if (mode === "sequence-latest") value.last_sequence = "4";
    if (mode === "nonpartial") object(child.output).partial = false;
    if (mode === "block") object(child.output).blocks = [{ kind: "text", text: 123 }];
    if (mode.startsWith("usage-")) {
      const usage: Document = codexUsage(); child.usage = usage; source.usage = usage;
      if (mode === "usage-number") usage.total = 1;
      if (mode === "usage-scope") usage.scope = "parent-inclusive";
      if (mode === "usage-parity") usage.total = "9223372036854775806";
      if (mode === "usage-family") usage.native_report = '{"total_tokens":1,"tool_uses":0,"duration_ms":0}';
      if (mode === "usage-duplicate") usage.native_report = String(usage.native_report).replace('"inputTokens":0', '"inputTokens":7,"inputTokens":0');
      if (mode === "usage-native-string") usage.native_report = String(usage.native_report).replace('"inputTokens":0', '"inputTokens":"0"');
    }
  });
  const dispose = mountPage(session, [valid, invalid]);
  try {
    expect(await screen.findByText("The retained child page is unavailable or inconsistent.")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByText("Original child output")).toBeNull();
    expect((screen.getByRole("button", { name: "Next child page" }) as HTMLButtonElement).disabled).toBe(true);
  } finally { dispose(); }
});

test.each(["duplicate-product", "duplicate-native", "cycle", "foreign-parent-execution", "oversized-page"])("rejects inconsistent complete page ownership: %s", async mode => {
  const session = newRequestId(), one = subagentFixture(session), two = subagentFixture(session);
  edit(two, value => { const first = document(one); value.execution_id = first.execution_id; value.root_id = first.root_id; object(value.observation).parent_id = first.root_id; });
  if (mode === "duplicate-product") two.id = one.id;
  if (mode === "duplicate-native") edit(two, value => { object(value.observation).native_id = object(document(one).observation).native_id; });
  if (mode === "cycle") { edit(one, value => { object(value.observation).parent_id = object(document(two).observation).native_id; }); edit(two, value => { object(value.observation).parent_id = object(document(one).observation).native_id; }); }
  if (mode === "foreign-parent-execution") edit(two, value => { value.execution_id = newRequestId(); object(value.observation).parent_id = object(document(one).observation).native_id; });
  const dispose = mountPage(session, mode === "oversized-page" ? Array.from({ length: 51 }, () => subagentFixture(session)) : [one, two]);
  try { expect(await screen.findByText("The retained child page is unavailable or inconsistent.")).toBeTruthy(); expect(screen.queryByRole("table")).toBeNull(); } finally { dispose(); }
});

test("renders exact signed native counters and a parent outside the current bounded page", async () => {
  const session = newRequestId(), row = subagentFixture(session);
  edit(row, value => { const usage = codexUsage(); object(value.observation).parent_id = newRequestId(); object(value.observation).usage = usage; object(items(value.sources)[0]).usage = usage; });
  const dispose = mountPage(session, [row]);
  try { expect(await screen.findByText("Total: 9223372036854775807")).toBeTruthy(); expect(screen.queryByText("The retained child page is unavailable or inconsistent.")).toBeNull(); } finally { dispose(); }
});

test("keeps prior verified Claude output/model and exact uint64 task usage after a task omission", async () => {
  const session = newRequestId(), row = subagentFixture(session);
  edit(row, value => {
    value.harness = "claude-code"; value.native_version = "2.1.236"; value.last_sequence = "4";
    const child = object(value.observation), tool = { id: newRequestId(), native_id: "original-Agent", name: "Agent" };
    child.native_id = "child-original-Agent"; child.parent_tool_id = tool.native_id; child.tool = tool; child.source = "claude-task"; child.source_id = "task-original";
    const usage = { scope: "child-cumulative", total: "18446744073709551615", input: null, output: null, native_report: '{"total_tokens":18446744073709551615,"tool_uses":0,"duration_ms":0}' };
    child.usage = usage; value.sources = [{ source: "claude-content", source_id: "content-original", sequence: "3" }, { source: "claude-task", source_id: "task-original", sequence: "4", usage }];
  });
  const dispose = mountPage(session, [row]);
  try { expect(await screen.findByText("Total: 18446744073709551615")).toBeTruthy(); expect(screen.getByText("Original child output")).toBeTruthy(); expect(screen.getByText("Observed: observed-model")).toBeTruthy(); } finally { dispose(); }
});

test("preserves exact nested Claude response usage when the latest task has no usage", async () => {
  const session = newRequestId(), row = subagentFixture(session);
  edit(row, value => {
    value.harness = "claude-code"; value.native_version = "2.1.236"; value.last_sequence = "4";
    const child = object(value.observation), tool = { id: newRequestId(), native_id: "original-Agent", name: "Agent" };
    child.native_id = "child-original-Agent"; child.parent_tool_id = tool.native_id; child.tool = tool; child.source = "claude-task"; child.source_id = "task-original";
    const usage = { scope: "child-response", total: null, input: "9223372036854775807", output: "0", native_report: '{"input_tokens":9223372036854775807,"output_tokens":0,"output_tokens_details":{"thinking_tokens":0},"iterations":[{"type":"compaction","input_tokens":0,"output_tokens":0}]}' };
    child.usage = usage; value.sources = [{ source: "claude-content", source_id: "content-original", sequence: "3", usage }, { source: "claude-task", source_id: "task-original", sequence: "4" }];
  });
  const dispose = mountPage(session, [row]);
  try { expect(await screen.findByText("Input: 9223372036854775807")).toBeTruthy(); expect(screen.getByText("Original child output")).toBeTruthy(); expect(screen.getByText("Total: Unavailable")).toBeTruthy(); } finally { dispose(); }
});
