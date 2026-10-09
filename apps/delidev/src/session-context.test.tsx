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
import { i18n } from "./localization";
import { contextDocument, SessionContext, nativeContextObservation, contextCapacity } from "./session-context";

it.each([
 { harness: "codex", large: false }, { harness: "opencode", large: false },
 { harness: "codex", large: true }, { harness: "opencode", large: true },
])("preserves one exact $harness manual request through response loss and navigation (large=$large)", async ({ harness, large }) => {
 const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ initial_execution: { configuration: { harness } } }) });
 const requests: unknown[] = [];
 let action: unknown = null;
 const compact = vi.fn(async (request) => {
  requests.push(request);
  action = { id: newRequestId(), document: { action_id: request.mutation.requestId, state: "claimed" } };
  if (requests.length === 1) throw new ConnectError("Lost reply", Code.Unavailable);
  const bytes = encode({ type: "compact-session", input: { action_id: request.mutation.requestId, assignment: { session_id: session.id } } });
  const documentJson = large ? new TextEncoder().encode(new TextDecoder().decode(bytes) + " ".repeat(2 << 20)) : bytes;
  return { requestId: request.mutation.requestId, replayed: true, job: create(ResourceSchema, { kind: EntityKind.JOB, id: newRequestId(), schemaVersion: 1, revision: 1n, sessionId: session.id, documentJson }) };
 });
 const transport = createRouterTransport((router) => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SESSION_COMPACTION_V1, SystemCapability.CODEX_SESSION_COMPACTION_V1, SystemCapability.OPENCODE_SESSION_COMPACTION_V1] }) });
  router.service(SessionService, { compactSession: compact, getSessionContext: () => ({ documentJson: encode({ session_id: session.id, session_revision: "8", current_tokens: null, manual_action: action }), capabilities: action ? [] : [harness === "codex" ? SessionContextCapability.CODEX_MANUAL_COMPACTION_V1 : SessionContextCapability.OPENCODE_MANUAL_COMPACTION_V1] }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const view = (active: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{active ? <SessionContext session={session} /> : <p>Another conversation</p>}</MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view(true));
 await waitFor(() => expect((screen.getByRole("button", { name: "Compact context" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Compact context" }));
 await screen.findByRole("button", { name: "Retry the same compaction request" });
 rendered.rerender(view(false)); rendered.rerender(view(true));
 fireEvent.click(await screen.findByRole("button", { name: "Retry the same compaction request" }));
 await screen.findByText("Accepted by the server. Waiting for the selected Worker to finish.");
 expect(requests[1]).toEqual(requests[0]);
 expect(requests[0]).toMatchObject({ mutation: { id: session.id, expectedRevision: 8n } });
 expect(screen.getByText(harness === "codex" ? "Latest native-reported context tokens: Not reported" : "Current context tokens: Not reported")).toBeTruthy();
 expect((screen.getByRole("button", { name: "Compact context" }) as HTMLButtonElement).disabled).toBe(true);
 expect(compact).toHaveBeenCalledTimes(2);
});

it.each([undefined, encode({ session_id: "foreign", session_revision: "1" }), encode({ session_id: "original", session_revision: "0" }), new Uint8Array([255]), new Uint8Array((1 << 20) + 1)])("rejects unavailable or foreign context", (bytes) => {
 expect(contextDocument(bytes, "original")).toBeUndefined();
});

it("keeps failed capability reads separate from unsupported context and rechecks without compaction", async () => {
 const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ initial_execution: { configuration: { harness: "codex" } } }) });
 const status = vi.fn().mockRejectedValueOnce(new ConnectError("private-native-error", Code.PermissionDenied)).mockResolvedValue({ capabilities: [] });
 const compact = vi.fn();
 const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: status }); router.service(SessionService, { compactSession: compact }); });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionContext session={session} /></MutationIntents></QueryClientProvider></TransportProvider>);
 await screen.findByText(/Context capability could not be read/);
 expect(screen.queryByText(/Update the server and original Worker/)).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Retry context capability read" }));
 await waitFor(() => expect(status).toHaveBeenCalledTimes(2));
 expect(compact).not.toHaveBeenCalled();
});


function nativeContextFixture() {
 return { harness: "codex", source: "last-request-total", tokens: "9007199254740993", observation_id: newRequestId(), execution_id: newRequestId(), native_thread_id: newRequestId(), native_turn_id: newRequestId(), sequence: "3", observed_at: "2026-10-09T01:00:00Z", status: "latest" };
}
it("validates closed context provenance and exact decimal counts without numeric coercion", () => {
 const native = nativeContextFixture();
 expect(nativeContextObservation(native)?.tokens).toBe("9007199254740993");
 expect(nativeContextObservation({ ...native, tokens: "0" })?.tokens).toBe("0");
 for (const patch of [{ tokens: 20 }, { tokens: "01" }, { tokens: "-1" }, { tokens: "9223372036854775808" }, { sequence: "0" }, { sequence: 3 }, { sequence: "18446744073709551616" }, { status: "fresh" }, { harness: "claude-code" }, { source: "cumulative" }, { observation_id: "bad" }, { native_thread_id: "child" }, { native_turn_id: "bad" }, { observed_at: "2026-02-30T01:00:00Z" }, { extra: true }]) expect(nativeContextObservation({ ...native, ...patch })).toBeUndefined();
 expect(nativeContextObservation(null)).toBeUndefined();
 const value = { session_id: "original", session_revision: "1", execution_id: native.execution_id, current_tokens: null, native_context: native };
 expect(contextDocument(encode(value), "original")?.native_context).toEqual(native);
 const source = { initial_execution: { configuration: { harness: "codex" } }, execution: { execution_id: native.execution_id, native_thread_id: native.native_thread_id, native_turn_id: native.native_turn_id } };
 expect(contextDocument(encode(value), "original", source)).toBeDefined();
 expect(contextDocument(encode(value), "original", { ...source, execution: { ...source.execution, native_thread_id: newRequestId() } })).toBeUndefined();
 expect(contextDocument(encode(value), "original", { ...source, initial_execution: { configuration: { harness: "claude-code" } } })).toBeUndefined();
 expect(contextDocument(encode({ ...value, execution_id: newRequestId() }), "original")).toBeUndefined();
 expect(contextDocument(encode({ ...value, native_context: null }), "original")).toBeUndefined();
 expect(contextDocument(encode({ ...value, current_tokens: 20 }), "original")).toBeUndefined();
 expect(contextDocument(encode({ session_id: "original", session_revision: "1", current_tokens: null }), "original")).toBeDefined();
});

it.each(["en", "ko"])("renders exact native snapshots, historical transitions and retained read failures in %s without compaction", async language => {
 await i18n.changeLanguage(language);
 try {
  const native = nativeContextFixture();
  const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ initial_execution: { configuration: { harness: "codex" } }, execution: { execution_id: native.execution_id, native_thread_id: native.native_thread_id, native_turn_id: native.native_turn_id } }) });
  let observation: unknown = native, fail = false;
  const read = vi.fn(() => { if (fail) throw new ConnectError("Read failed", Code.Unavailable); return { documentJson: encode({ session_id: session.id, session_revision: "8", execution_id: native.execution_id, current_tokens: null, native_context: observation }), capabilities: [] }; });
  const compact = vi.fn();
  const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SESSION_COMPACTION_V1] }) }); router.service(SessionService, { getSessionContext: read, compactSession: compact }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionContext session={session} /></MutationIntents></QueryClientProvider></TransportProvider>);
  await screen.findByText(language === "en" ? "Latest native-reported context tokens: 9007199254740993" : "네이티브가 최근 보고한 컨텍스트 토큰: 9007199254740993");
  await screen.findByText(language === "en" ? "Latest reported snapshot" : "최근 보고된 스냅샷");
  expect(document.querySelector('time[datetime="2026-10-09T01:00:00Z"]')).toBeTruthy();
  const refresh = () => { void client.invalidateQueries({ refetchType: "active" }); };
  observation = { ...native, status: "historical", tokens: "0" }; refresh();
  await screen.findByText(language === "en" ? "Latest native-reported context tokens: 0" : "네이티브가 최근 보고한 컨텍스트 토큰: 0");
  await screen.findByText(language === "en" ? "Historical snapshot" : "이전 스냅샷");
  observation = { ...native, tokens: "2" }; refresh();
  await screen.findByText(language === "en" ? "Latest reported snapshot" : "최근 보고된 스냅샷");
  fail = true; refresh();
  await screen.findByText(language === "en" ? /Context refresh failed/ : /컨텍스트 새로고침에 실패/);
  expect(screen.queryByText(language === "en" ? "Latest reported snapshot" : "최근 보고된 스냅샷")).toBeNull();
  expect(screen.getByText(language === "en" ? "Latest native-reported context tokens: 2" : "네이티브가 최근 보고한 컨텍스트 토큰: 2")).toBeTruthy();
  expect(compact).not.toHaveBeenCalled();
 } finally { await i18n.changeLanguage("en"); }
});

it("polls retained context and refreshes only reads, without invoking compaction", async () => {
 vi.useFakeTimers();
 const native = nativeContextFixture();
 const session = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ initial_execution: { configuration: { harness: "codex" } }, execution: { execution_id: native.execution_id, native_thread_id: native.native_thread_id, native_turn_id: native.native_turn_id } }) });
 const read = vi.fn(() => ({ documentJson: encode({ session_id: session.id, session_revision: "8", execution_id: native.execution_id, current_tokens: null, native_context: native }), capabilities: [] }));
 const compact = vi.fn();
 const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SESSION_COMPACTION_V1] }) }); router.service(SessionService, { getSessionContext: read, compactSession: compact }); });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const rendered = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionContext session={session} /></MutationIntents></QueryClientProvider></TransportProvider>);
 try {
  await vi.waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  await vi.advanceTimersByTimeAsync(5050);
  await vi.waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  await vi.advanceTimersByTimeAsync(5050);
  await vi.waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  expect(compact).not.toHaveBeenCalled();
 } finally { rendered.unmount(); client.clear(); vi.useRealTimers(); }
});

it("uses capacity only from the exact same safe native Usage observation",()=>{
 const native=nativeContextFixture();const sessionId=newRequestId();const paired=nativeContextObservation({...native,tokens:"300",sequence:"2"})!;const document={harness:"codex",execution_id:paired.execution_id,native_thread_id:paired.native_thread_id,native_turn_id:paired.native_turn_id,sequence:2,observation:{last_request:{total:300},context_window:200}};
 const row=create(ResourceSchema,{kind:EntityKind.USAGE,id:paired.observation_id,sessionId,schemaVersion:1,revision:1n,documentJson:encode(document)});expect(contextCapacity(row,sessionId,paired)).toBe(200);
 for(const field of ["execution_id","native_thread_id","native_turn_id","harness"])expect(contextCapacity({...row,documentJson:encode({...document,[field]:"wrong"})},sessionId,paired)).toBeUndefined();
 for(const sequence of [1,-1,9007199254740992])expect(contextCapacity({...row,documentJson:encode({...document,sequence})},sessionId,paired)).toBeUndefined();
 for(const observation of [{last_request:{total:301},context_window:200},{last_request:{total:300},context_window:0},{last_request:{total:300},context_window:9007199254740992},{last_request:{total:9007199254740992},context_window:200}])expect(contextCapacity({...row,documentJson:encode({...document,observation})},sessionId,paired)).toBeUndefined();
 for(const changed of [{...row,id:newRequestId()},{...row,sessionId:newRequestId()},{...row,kind:EntityKind.SESSION},{...row,schemaVersion:2}])expect(contextCapacity(changed,sessionId,paired)).toBeUndefined();
});
