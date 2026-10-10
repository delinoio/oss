// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ForkPurpose, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { PendingSidechatPane, SessionCreationAction, SessionForkAction, SessionForkProvider } from "./session-fork";
import { SessionTabsProvider, SessionTabKind, sessionTabKey, useSessionTabs, type SessionTabsStore } from "./session-tabs";
import { SessionTabBar } from "./session-tab-bar";
import { SidechatAuthoring } from "./sidechat-authoring";
import { SessionToolMenu } from "./session-tool-menu";

async function fixture(options: { lost?: boolean; hidden?: boolean; stale?: boolean; runnerError?: boolean } = {}) {
 const machineId = newRequestId(), sourceId = newRequestId();
 const source = create(ResourceSchema, { kind: EntityKind.SESSION, id: sourceId, revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Original", machine_id: machineId, workspace: "worktree", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex", subscription: true } }, execution: { native_turn_id: "original-turn", cleanup_verified: true } }) });
 const machine = create(ResourceSchema, { kind: EntityKind.MACHINE, id: machineId, revision: 1n, schemaVersion: 1, documentJson: encode({ worker_capabilities: ["codex-read-only-sidechat-v1", "managed-codex-sidechat-v1", "managed-codex-subscriptions-v1"] }) });
 const jobId = newRequestId(), childId = newRequestId();
 const job = (state: string, id = jobId) => create(ResourceSchema, { kind: EntityKind.JOB, id, revision: 1n, schemaVersion: 1, documentJson: encode({ state, input: { source_session_id: sourceId } }) });
 const child = (parent = sourceId, overlay = true) => create(ResourceSchema, { kind: EntityKind.SESSION, id: childId, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Verified Sidechat", fork: { source_session_id: parent, ...(overlay ? { sidechat_parent_snapshot: { original: true } } : {}) } }) });
 let result: { job: ReturnType<typeof job>; session?: ReturnType<typeof child> } = { job: job("claimed") };
 const fork = vi.fn(async request => { if (options.lost && fork.mock.calls.length === 1) throw new ConnectError("Lost original response", Code.Unavailable); return { job: job("claimed") }; });
 let runnerError = options.runnerError;
 const observe = vi.fn((_request: { jobId: string }) => result), read = vi.fn(request => { if (runnerError && request.kind === EntityKind.MACHINE) throw new ConnectError("Original Runner read unavailable", Code.Unavailable); return { resource: request.kind === EntityKind.MACHINE ? machine : options.stale ? { ...source, revision: 9n } : source }; });
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SIDECHAT_V1, SystemCapability.MANAGED_CODEX_SIDECHAT_V1] }) });
  router.service(ResourceService, { getResource: read }); router.service(SessionService, { forkSession: fork, getSessionFork: observe });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const navigate = vi.fn(); let store!: SessionTabsStore;
 function Workspace({ active = true }: { active?: boolean }) {
  const tabs = useSessionTabs(sourceId); store = tabs.store;
  return <><SessionToolMenu active={active}>{["Diff", "Files", "Terminals", "Browser", "Diagnostics"].map(label => <button key={label} role="menuitem">{label}</button>)}{tabs.tab.kind !== SessionTabKind.PendingSidechat && tabs.tab.kind !== SessionTabKind.Sidechat ? <SessionForkAction source={source} action={SessionCreationAction.Sidechat}/> : null}</SessionToolMenu>
   <SessionTabBar id={sourceId} tabs={tabs.tabs} selected={tabs.selected} select={key => tabs.store.select(sourceId, key)} close={key => tabs.store.close(sourceId, key)}/>
   {tabs.tabs.map(tab => tab.kind === SessionTabKind.PendingSidechat ? <div hidden={!active || tabs.selected !== sessionTabKey(tab)} key={tab.requestId}><PendingSidechatPane requestId={tab.requestId} active={active && tabs.selected === sessionTabKey(tab)}/></div> : null)}
   {tabs.store.sidechats(sourceId).map(tab => <div key={tab.id} hidden={!active || tabs.selected !== sessionTabKey(tab)}><SidechatAuthoring id={tab.id} active={active && tabs.selected === sessionTabKey(tab)}>{({draft,setDraft}) => <textarea aria-label="Verified child draft" id={`prompt-${tab.id}`} value={draft} onChange={event => setDraft(event.target.value)}/>}</SidechatAuthoring></div>)}
  </>;
 }
 const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionTabsProvider><SessionForkProvider openSession={navigate}><Workspace active={active}/><SessionForkAction source={source} action={SessionCreationAction.Sidechat}/></SessionForkProvider></SessionTabsProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view());
 await waitFor(() => expect(client.isFetching()).toBe(0));
 return { sourceId, childId, fork, observe, read, client, navigate, store: () => store, rendered, view, job, child, setRunnerHealthy: () => { runnerError = false; }, setResult: (value: typeof result) => { result = value; } };
}

it("only explicit flat menu activation creates one original request and focuses the editable draft", async () => {
 const f = await fixture(); const trigger = screen.getByRole("button", { name: "Open tool" });
 fireEvent.click(trigger);
 const menu = screen.getByRole("menu"); expect([...menu.querySelectorAll('[role="menuitem"]')].map(item => item.textContent)).toEqual(["Diff", "Files", "Terminals", "Browser", "Diagnostics", "Open Sidechat"]);
 expect(f.fork).not.toHaveBeenCalled(); fireEvent.keyDown(menu, { key: "Escape" }); expect(document.activeElement).toBe(trigger); expect(f.fork).not.toHaveBeenCalled();
 fireEvent.click(trigger); fireEvent.click(menu.querySelector('[role="menuitem"]:last-child')!);
 const input = await screen.findByRole("textbox", { name: "Message" }); expect(document.activeElement).toBe(input); expect(input).toHaveProperty("disabled", false); expect(screen.queryByRole("dialog")).toBeNull();
 fireEvent.change(input, { target: { value: "Retained question" } });
 await waitFor(() => expect(f.fork).toHaveBeenCalledTimes(1));
 fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" })); await act(async () => {}); expect(f.fork).toHaveBeenCalledTimes(1);
 expect(f.fork.mock.calls[0]![0]).toMatchObject({ mutation: { id: f.sourceId, expectedRevision: 8n }, expectedTurnId: "original-turn", purpose: ForkPurpose.SIDECHAT, name: "Original Sidechat" });
 expect(screen.getByRole("button", { name: "Queue message" })).toHaveProperty("disabled", true);
 expect(f.read.mock.calls.every(call => [EntityKind.SESSION, EntityKind.MACHINE].includes(call[0].kind))).toBe(true);
});

it.each(["selected", "background", "closed", "hidden"])("publishes only the verified child in place, retaining draft and original caret without navigation: %s", async mode => {
 const f = await fixture(); fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
 const input = await screen.findByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;
 fireEvent.change(input, { target: { value: "My original question" } }); input.setSelectionRange(3, 8); fireEvent.select(input);
 await waitFor(() => expect(f.observe).toHaveBeenCalled());
 const pending = f.store().snapshot(f.sourceId).tabs.find(tab => tab.kind === SessionTabKind.PendingSidechat)!;
 if (mode === "background") { act(() => f.store().open(f.sourceId, { kind: SessionTabKind.Files })); screen.getByRole("tab", { name: "Files" }).focus(); }
 if (mode === "closed") fireEvent.click(screen.getByRole("button", { name: "Close Original Sidechat tab" }));
 if (mode === "hidden") f.rendered.rerender(f.view(false));
 const previousFocus = document.activeElement;
 f.setResult({ job: f.job("succeeded"), session: f.child() }); await act(async () => { await f.client.invalidateQueries(); });
 await waitFor(() => expect(f.store().parent(f.childId)).toBe(f.sourceId));
 expect(f.navigate).not.toHaveBeenCalled(); expect(f.fork).toHaveBeenCalledTimes(1);
 const snapshot = f.store().snapshot(f.sourceId); expect(snapshot.tabs.some(tab => tab.kind === SessionTabKind.PendingSidechat)).toBe(false);
 if (mode === "closed") {
  expect(f.store().childDraft(f.childId)).toMatchObject({ text: "My original question", start: 3, end: 8, focus: false });
  expect(snapshot.tabs.some(tab => tab.kind === SessionTabKind.Sidechat)).toBe(false);
  act(() => f.store().open(f.sourceId, { kind: SessionTabKind.Sidechat, id: f.childId, name: "Verified Sidechat" }));
 }
 const childInput = document.getElementById(`prompt-${f.childId}`) as HTMLTextAreaElement;
 expect(childInput.value).toBe("My original question"); expect(f.store().childDraft(f.childId)).toBeUndefined();
 if (mode === "selected") { expect(document.activeElement).toBe(childInput); expect(childInput.selectionStart).toBe(3); expect(childInput.selectionEnd).toBe(8); }
 if (mode === "hidden" || mode === "closed") expect(document.activeElement).not.toBe(childInput);

 if (mode === "background") { expect(snapshot.selected).toBe(SessionTabKind.Files); expect(document.activeElement).toBe(previousFocus); }
 if (mode === "selected") expect(snapshot.tabs[1]).toMatchObject({ kind: SessionTabKind.Sidechat, id: f.childId });
});

it("lost creation response retains draft and replays only the exact original request explicitly", async () => {
 const f = await fixture({ lost: true }); fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
 const input = await screen.findByRole("textbox", { name: "Message" }); fireEvent.change(input, { target: { value: "Keep this draft" } });
 const retry = await screen.findByRole("button", { name: "Retry original Sidechat request" }); expect(f.fork).toHaveBeenCalledTimes(1);
 expect(screen.queryByRole("button", { name: "Discard Sidechat draft" })).toBeNull(); fireEvent.click(retry);
 await waitFor(() => expect(f.observe).toHaveBeenCalled()); expect(f.fork.mock.calls[1]![0]).toEqual(f.fork.mock.calls[0]![0]); expect(input).toHaveProperty("value", "Keep this draft");
});

it.each(["uncertain", "foreign-child", "missing-overlay", "foreign-job"])("unsettled publication permits original status reads without replay: %s", async mode => {
 const f = await fixture(); fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" })); await waitFor(() => expect(f.observe).toHaveBeenCalled());
 const input = screen.getByRole("textbox", { name: "Message" }); fireEvent.change(input, { target: { value: "Still mine" } });
 f.setResult({ job: f.job(mode === "uncertain" ? "uncertain" : "succeeded", mode === "foreign-job" ? newRequestId() : undefined), session: mode === "uncertain" ? undefined : f.child(mode === "foreign-child" ? newRequestId() : undefined, mode !== "missing-overlay") });
 await act(async () => { await f.client.invalidateQueries(); });
 const retry = await screen.findByRole("button", { name: "Retry original status read" }); fireEvent.click(retry);
 await waitFor(() => expect(f.observe.mock.calls.length).toBeGreaterThanOrEqual(3)); expect(f.fork).toHaveBeenCalledTimes(1); expect(input).toHaveProperty("value", "Still mine"); expect(f.store().parent(f.childId)).toBeUndefined();
 expect(f.observe.mock.calls.every(call => call[0].jobId === f.job("claimed").id)).toBe(true);
});

it("fresh source revision mismatch retains draft without admitting creation", async () => {
 const f = await fixture({ stale: true }); fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
 await screen.findByText(/The original turn changed/);
 expect(f.fork).not.toHaveBeenCalled(); expect(screen.getByRole("textbox", { name: "Message" })).toHaveProperty("disabled", false);
});


it("Runner read retry only reinspects original metadata without creating Sidechat", async () => {
 const f = await fixture({ runnerError: true }); expect(f.fork).not.toHaveBeenCalled();
 f.setRunnerHealthy(); fireEvent.click(screen.getByRole("menuitem", { name: "Recheck original fork Runner" }));
 await screen.findByRole("menuitem", { name: "Open Sidechat" }); expect(f.fork).not.toHaveBeenCalled(); expect(f.observe).not.toHaveBeenCalled(); expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
});

it("confirmed failure retains the editable draft until explicit guarded discard", async () => {
 const f = await fixture(); fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" })); await waitFor(() => expect(f.observe).toHaveBeenCalled());
 const input = screen.getByRole("textbox", { name: "Message" }); fireEvent.change(input, { target: { value: "Failed but retained" } });
 f.setResult({ job: f.job("failed") }); await act(async () => { await f.client.invalidateQueries(); });
 await screen.findByText(/Sidechat preparation did not complete/); expect(input).toHaveProperty("value", "Failed but retained"); expect(input).toHaveProperty("disabled", false);
 fireEvent.click(screen.getByRole("button", { name: "Discard Sidechat draft" })); expect(f.store().snapshot(f.sourceId).tabs.some(tab => tab.kind === SessionTabKind.PendingSidechat)).toBe(false); expect(f.fork).toHaveBeenCalledTimes(1);
});
