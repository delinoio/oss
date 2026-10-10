// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BudgetState, EntityKind, ForkPurpose, ForkWorkspace, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionForkProvider } from "./session-fork";
import { SessionView } from "./session";
import { SessionTabKind, SessionTabsProvider, sessionTabKey, useSessionTabsStore, type SessionTabsStore } from "./session-tabs";
import { useDirectSidechat } from "./sidechat-creation";

function fixture(uncertain = false, options: { readFailure?: boolean; terminal?: string } = {}) {
  const machineId = newRequestId(), sourceId = newRequestId();
  const source = create(ResourceSchema, { id: sourceId, sessionId: sourceId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 8n, documentJson: encode({ name: "Original parent", machine_id: machineId, workspace: "general-chat", outcome: "succeeded", archive: "active", dispatch: "paused", recovery: "none", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: newRequestId(), cleanup_verified: true } }) });
  const machine = create(ResourceSchema, { id: machineId, kind: EntityKind.MACHINE, schemaVersion: 1, revision: 1n, documentJson: encode({ worker_capabilities: ["codex-read-only-sidechat-v1"] }) });
  const childId = newRequestId();
  const child = create(ResourceSchema, { id: childId, sessionId: childId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Verified child", machine_id: machineId, workspace: "general-chat", outcome: "idle", archive: "active", dispatch: "paused", recovery: "none", initial_execution: { configuration: { harness: "codex" } }, fork: { source_session_id: source.id, sidechat_parent_snapshot: { original: true } } }) });
  const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ state: "claimed", input: { source_session_id: source.id } }) });
  let finish!: (value: { job: typeof job; session?: typeof child }) => void;
  const gate = new Promise<{ job: typeof job; session?: typeof child }>(resolve => { finish = resolve; });
  const fork = vi.fn(async (_request: unknown) => ({ job }));
  if (uncertain) fork.mockRejectedValueOnce(new ConnectError("Lost original receipt", Code.Unavailable));
  const inspect = vi.fn(async () => gate), enqueue = vi.fn(), control = vi.fn(), findings = vi.fn();
  if (options.readFailure) inspect.mockRejectedValueOnce(new ConnectError("Original job read failed", Code.Unavailable));
  const reads: string[] = [];
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SIDECHAT_V1] }) });
    router.service(ResourceService, { getResource: request => { reads.push(request.id); return { resource: request.kind === EntityKind.MACHINE ? machine : request.id === source.id ? source : child }; }, getSnapshot: request => ({ resources: [request.filter?.sessionId === child.id ? child : source], cursor: "fixture" }), listResources: () => ({ resources: [] }), async *watchEvents(_request, context) { if (!context.signal.aborted) await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true })); } });
    router.service(SessionService, { forkSession: fork, getSessionFork: inspect, listQueue: () => ({ inputs: [] }), getSessionBudget: request => ({ view: { session: request.sessionId === child.id ? child : source, state: BudgetState.ALLOW_INCOMPLETE } }), enqueueInput: enqueue, controlSession: control, sendSidechatFindings: findings });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const navigate = vi.fn();
  let tabs!: SessionTabsStore, readyText = "";
  function Probe() { tabs = useSessionTabsStore(); const direct = useDirectSidechat(); readyText = direct?.ready.get(child.id)?.draft.text ?? ""; return null; }
  const tree = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><SessionTabsProvider><MutationIntents><SessionForkProvider openSession={navigate}><Probe/><SessionView id={source.id} active={active} draft="Parent draft" setDraft={vi.fn()}/><button>Unrelated destination</button></SessionForkProvider></MutationIntents></SessionTabsProvider></QueryClientProvider></TransportProvider>;
  const view = render(tree());
  const open = async () => { fireEvent.click(await screen.findByRole("button", { name: "Open tool" })); fireEvent.click(await screen.findByRole("menuitem", { name: "Open Sidechat" })); };
  return { ...view, source, child, job, fork, inspect, enqueue, control, findings, reads, navigate, open, tabs: () => tabs, readyText: () => readyText, deactivate: () => view.rerender(tree(false)), complete: async (value = child) => act(async () => finish({ job: create(ResourceSchema, { ...job, revision: 2n, documentJson: encode({ state: options.terminal ?? "succeeded", input: { source_session_id: source.id } }) }), session: value })) };
}

it("opens and focuses one editable pending tab, then atomically transfers its unsent draft/caret to the verified child", async () => {
  const value = fixture(); await value.open();
  const draft = screen.getByRole("textbox", { name: "Sidechat message" }) as HTMLTextAreaElement;
  expect(document.activeElement).toBe(draft); expect(draft.disabled).toBe(false);
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.change(draft, { target: { value: "Retained unsent question" } }); draft.setSelectionRange(3, 7); fireEvent.select(draft);
  expect(screen.getByRole("button", { name: "Send (available after preparation)" })).toHaveProperty("disabled", true);
  expect(screen.getByRole("button", { name: "Attach image (available after preparation)" })).toHaveProperty("disabled", true);
  await waitFor(() => expect(value.fork).toHaveBeenCalledTimes(1));
  const pending = value.tabs().snapshot(value.source.id).tabs[1]!;
  expect(pending.kind).toBe(SessionTabKind.PendingSidechat);
  expect(value.fork.mock.calls[0]?.[0]).toMatchObject({ purpose: ForkPurpose.SIDECHAT, workspace: ForkWorkspace.UNSPECIFIED, mutation: { id: value.source.id, expectedRevision: 8n } });
  expect(value.reads).not.toContain((pending as {requestId: string}).requestId);
  await value.complete();
  await waitFor(() => expect(value.tabs().snapshot(value.source.id).selected).toBe(sessionTabKey({ kind: SessionTabKind.Sidechat, id: value.child.id, name: "Verified child" })));
  const field = await screen.findByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
  await waitFor(() => expect(document.activeElement).toBe(field));
  expect(field.value).toBe("Retained unsent question"); expect([field.selectionStart, field.selectionEnd]).toEqual([3,7]);
  expect(value.tabs().snapshot(value.source.id).tabs).toHaveLength(2);
  expect(value.enqueue).not.toHaveBeenCalled(); expect(value.control).not.toHaveBeenCalled(); expect(value.findings).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Finish fork operation" })).toBeNull();
});

it.each(["closed", "away"])("retains background completion and draft without reopening or focus theft (%s)", async disposition => {
  const value = fixture(); await value.open();
  fireEvent.change(screen.getByRole("textbox", { name: "Sidechat message" }), { target: { value: "Hidden retained draft" } });
  await waitFor(() => expect(value.inspect).toHaveBeenCalledTimes(1));
  if (disposition === "closed") act(() => value.tabs().close(value.source.id, value.tabs().snapshot(value.source.id).selected)); else value.deactivate();
  const destination = screen.getByRole("button", { name: "Unrelated destination" }); destination.focus();
  const navigation = value.navigate.mock.calls.length;
  await value.complete();
  await waitFor(() => expect(value.readyText()).toBe("Hidden retained draft"));
  expect(document.activeElement).toBe(destination); expect(value.navigate).toHaveBeenCalledTimes(navigation);
  if (disposition === "closed") { expect(value.tabs().snapshot(value.source.id).tabs).toHaveLength(1); expect(value.tabs().parent(value.child.id)).toBe(value.source.id); }
  expect(value.enqueue).not.toHaveBeenCalled(); expect(value.control).not.toHaveBeenCalled(); expect(value.findings).not.toHaveBeenCalled();
});

it("reopens the retained uncertain request without creating a second receipt and retries only its exact bytes", async () => {
  const value = fixture(true); await value.open();
  const retry = await screen.findByRole("button", { name: "Retry the same Sidechat request" });
  fireEvent.change(screen.getByRole("textbox", { name: "Sidechat message" }), { target: { value: "Uncertain draft" } });
  act(() => value.tabs().select(value.source.id, SessionTabKind.Conversation)); await value.open();
  expect(value.fork).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("textbox", { name: "Sidechat message" })).toHaveProperty("value", "Uncertain draft");
  fireEvent.click(retry); await waitFor(() => expect(value.fork).toHaveBeenCalledTimes(2));
  expect(value.fork.mock.calls[0]?.[0]).toEqual(value.fork.mock.calls[1]?.[0]);
});


it.each(["foreign", "wrong-kind", "missing-proof"])("retains draft and original-job reinspection when succeeded child is unverified (%s)", async invalid => {
  const value=fixture(); await value.open();
  fireEvent.change(screen.getByRole("textbox",{name:"Sidechat message"}),{target:{value:"Unverified child draft"}});
  await waitFor(()=>expect(value.inspect).toHaveBeenCalledTimes(1));
  const child=create(ResourceSchema,{...value.child,...(invalid==="wrong-kind"?{kind:EntityKind.JOB}:{}),documentJson:encode({name:"Unverified",fork:{source_session_id:invalid==="foreign"?newRequestId():value.source.id,...(invalid!=="missing-proof"?{sidechat_parent_snapshot:{original:true}}:{})}})});
  await value.complete(child);
  expect(await screen.findByRole("button",{name:"Recheck original Sidechat operation"})).toBeTruthy();
  expect(screen.getByRole("textbox",{name:"Sidechat message"})).toHaveProperty("value","Unverified child draft");
  expect(value.tabs().snapshot(value.source.id).tabs[1]?.kind).toBe(SessionTabKind.PendingSidechat);
  expect(value.tabs().parent(child.id)).toBeUndefined();
  fireEvent.click(screen.getByRole("button",{name:"Recheck original Sidechat operation"}));
  await waitFor(()=>expect(value.inspect).toHaveBeenCalledTimes(2));
  expect(value.fork).toHaveBeenCalledTimes(1);expect(value.enqueue).not.toHaveBeenCalled();
  expect(screen.queryByRole("button",{name:"Discard completed or rejected Sidechat preparation"})).toBeNull();
});


it("keeps failed status recovery on the original job without replaying creation",async()=>{
 const value=fixture(false,{readFailure:true});await value.open();
 fireEvent.change(screen.getByRole("textbox",{name:"Sidechat message"}),{target:{value:"Failed read draft"}});
 const retry=await screen.findByRole("button",{name:"Recheck original Sidechat operation"});
 expect(value.fork).toHaveBeenCalledTimes(1);fireEvent.click(retry);
 await waitFor(()=>expect(value.inspect).toHaveBeenCalledTimes(2));
 await value.complete();await waitFor(()=>expect(value.readyText()).toBe("Failed read draft"));
 expect(value.fork).toHaveBeenCalledTimes(1);expect(value.enqueue).not.toHaveBeenCalled();
});
it.each(["failed","canceled"])("hides confirmed terminal preparation on close and releases it only after explicit discard (%s)",async terminal=>{
 const value=fixture(false,{terminal});await value.open();
 fireEvent.change(screen.getByRole("textbox",{name:"Sidechat message"}),{target:{value:"Terminal retained draft"}});
 await waitFor(()=>expect(value.inspect).toHaveBeenCalledTimes(1));await value.complete();
 await screen.findByRole("button",{name:"Discard completed or rejected Sidechat preparation"});
 act(()=>value.tabs().close(value.source.id,value.tabs().snapshot(value.source.id).selected));
 await value.open();expect(value.fork).toHaveBeenCalledTimes(1);
 expect(screen.getByRole("textbox",{name:"Sidechat message"})).toHaveProperty("value","Terminal retained draft");
 fireEvent.click(screen.getByRole("button",{name:"Discard completed or rejected Sidechat preparation"}));
 expect(value.tabs().snapshot(value.source.id).tabs).toHaveLength(1);
 expect(value.enqueue).not.toHaveBeenCalled();expect(value.control).not.toHaveBeenCalled();
});
