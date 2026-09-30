// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { expect, test } from "vitest";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ResourceService, SystemService, SystemCapability } from "@delinoio/delidev-api-client";
import { Subagents, SubagentRows } from "./subagents";

test("shows native hierarchy, missing telemetry and exact observed child counters without controls", () => {
  const root = newRequestId(), parent = newRequestId();
  const row = (native: string, parentId: string, model: string | null, usage: unknown) => create(ResourceSchema, {
    id: newRequestId(), revision: 1n, kind: EntityKind.SUBAGENT, schemaVersion: 1,
    documentJson: new TextEncoder().encode(JSON.stringify({ root_id: root, execution_id: newRequestId(), harness: "codex", native_version: "0.151.0",
      sources: [{ source: "codex-collaboration", source_id: "original-spawn", sequence: 3 }],
      observation: { native_id: native, parent_id: parentId, status: "running", requested_model: "requested-only", observed_model: model, output: null, usage },
    })),
  });
  render(<SubagentRows rows={[row(parent, root, null, null), row("nested-native-child", parent, "observed-model", { scope: "child-cumulative", total: "18446744073709551615", input: null, output: "0" })]} />);
  expect(screen.getByText("nested-native-child")).toBeTruthy();
  expect(screen.getAllByText(parent).length).toBe(2);
  expect(screen.getByText("Observed: observed-model")).toBeTruthy();
  expect(screen.getByText("Observed: Unavailable")).toBeTruthy();
  expect(screen.getByText("Total: 18446744073709551615")).toBeTruthy();
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
 await waitFor(() => expect(screen.getByRole("button", { name: "Next child page" }).hasAttribute("disabled")).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Next child page" }));
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
