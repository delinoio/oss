// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SessionContextCapability, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { contextDocument, SessionContext } from "./session-context";

it("preserves one exact manual request through response loss and navigation", async () => {
 const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ initial_execution: { configuration: { harness: "codex" } } }) });
 const requests: unknown[] = [];
 let action: unknown = null;
 const compact = vi.fn(async (request) => {
  requests.push(request);
  action = { id: newRequestId(), document: { action_id: request.mutation.requestId, state: "claimed" } };
  if (requests.length === 1) throw new ConnectError("Lost reply", Code.Unavailable);
  return { requestId: request.mutation.requestId, replayed: true, job: create(ResourceSchema, { kind: EntityKind.JOB, id: newRequestId(), sessionId: session.id, documentJson: encode({ input: { action_id: request.mutation.requestId, assignment: { session_id: session.id } } }) }) };
 });
 const transport = createRouterTransport((router) => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SESSION_COMPACTION_V1, SystemCapability.CODEX_SESSION_COMPACTION_V1] }) });
  router.service(SessionService, { compactSession: compact, getSessionContext: () => ({ documentJson: encode({ session_id: session.id, session_revision: "8", current_tokens: null, manual_action: action }), capabilities: action ? [] : [SessionContextCapability.CODEX_MANUAL_COMPACTION_V1] }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const view = (active: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{active ? <SessionContext session={session} /> : <p>Another conversation</p>}</MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view(true));
 await waitFor(() => expect((screen.getByRole("button", { name: "Compact context" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Compact context" }));
 await screen.findByRole("button", { name: "Retry the same compaction request" });
 rendered.rerender(view(false)); rendered.rerender(view(true));
 fireEvent.click(await screen.findByRole("button", { name: "Retry the same compaction request" }));
 await screen.findByText("Compaction operation: claimed");
 expect(requests[1]).toEqual(requests[0]);
 expect(requests[0]).toMatchObject({ mutation: { id: session.id, expectedRevision: 8n } });
 expect(screen.getByText("Current context tokens: Not reported")).toBeTruthy();
 expect((screen.getByRole("button", { name: "Compact context" }) as HTMLButtonElement).disabled).toBe(true);
 expect(compact).toHaveBeenCalledTimes(2);
});

it.each([undefined, encode({ session_id: "foreign", session_revision: "1" }), encode({ session_id: "original", session_revision: "0" }), new Uint8Array([255]), new Uint8Array((1 << 20) + 1)])("rejects unavailable or foreign context", (bytes) => {
 expect(contextDocument(bytes, "original")).toBeUndefined();
});
