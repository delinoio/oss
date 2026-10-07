// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, test } from "vitest";
import { EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResourceService, SystemService, SystemCapability } from "@delinoio/delidev-api-client";
import { Subagents, SubagentRows } from "./subagents";
import { document, encode, object } from "./documents";
import { subagentFixture } from "./subagent-test-fixture";

test("shows native hierarchy, missing telemetry and exact observed child counters without controls", () => {
  const session = newRequestId(), one = subagentFixture(session), two = subagentFixture(session);
  const first = document(one), second = document(two), parent = String(object(first.observation).native_id), nested = String(object(second.observation).native_id);
  object(first.observation).observed_model = null; object(first.observation).output = null;
  second.execution_id = first.execution_id; second.root_id = first.root_id; object(second.observation).parent_id = parent;
  const usage = { scope: "child-cumulative", total: "9223372036854775807", input: "0", output: "0", native_report: '{"total":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0,"totalTokens":9223372036854775807},"last":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0,"totalTokens":0}}' };
  object(second.observation).usage = usage; object((second.sources as unknown[])[0]).usage = usage;
  one.documentJson = encode(first); two.documentJson = encode(second);
  render(<SubagentRows sessionId={session} rows={[one, two]} />);
  expect(screen.getByText(nested)).toBeTruthy();
  expect(screen.getAllByText(parent).length).toBe(2);
  expect(screen.getByText("Observed: observed-model")).toBeTruthy();
  expect(screen.getByText("Observed: Unavailable")).toBeTruthy();
  expect(screen.getByText("Total: 9223372036854775807")).toBeTruthy();
  expect(screen.getByText("Output: 0")).toBeTruthy();
  expect(screen.getAllByText("Requested: requested-only").length).toBe(2);
  expect(screen.queryByRole("button")).toBeNull();
});

test("capability gates original session reads and paging never invokes child controls", async () => {
 const session = newRequestId();
 const reads: string[] = [];
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBAGENT_OBSERVATION_V1] }) });
  router.service(ResourceService, { listResources: request => {
   expect(request.filter?.sessionId).toBe(session);
   expect(request.filter?.kind).toBe(EntityKind.SUBAGENT);
   reads.push(request.filter?.pageToken ?? "");
   return { resources: [], nextPageToken: request.filter?.pageToken ? "" : "original-page" };
  }});
 });
 const query = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const view = (revision: string) => <QueryClientProvider client={query}><TransportProvider transport={transport}><Subagents sessionId={session} revision={revision} /></TransportProvider></QueryClientProvider>;
 const mounted = render(view("1"));
 fireEvent.click(screen.getByText("Subagents"));
 await waitFor(() => expect(screen.getByRole("button", { name: "Load more Subagents" }).hasAttribute("disabled")).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Load more Subagents" }));
 await waitFor(() => expect(reads).toContain("original-page"));
 const before = reads.length;
 mounted.rerender(view("2"));
 await waitFor(() => expect(reads.length).toBeGreaterThan(before));
 expect(reads.at(-1)).toBe("original-page");
 query.clear();
});

test("does not query children when the server has no observation capability", async () => {
 let reads = 0;
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
  router.service(ResourceService, { listResources: () => { reads++; return { resources: [] }; } });
 });
 const query = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<QueryClientProvider client={query}><TransportProvider transport={transport}><Subagents sessionId={newRequestId()} revision="1" /></TransportProvider></QueryClientProvider>);
 await waitFor(() => expect(screen.getByText("This server does not support child-agent observations.")).toBeTruthy());
 expect(reads).toBe(0);
 query.clear();
});
