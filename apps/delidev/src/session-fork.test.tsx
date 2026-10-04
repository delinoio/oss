// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ForkWorkspace, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { document, encode, object } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionForkAction, SessionForkProvider } from "./session-fork";

it("retains exact fork retry and accepted job while navigation changes, publishing only the verified child", async () => {
 const turn = newRequestId();
 const source = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Original", workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: turn, cleanup_verified: true } }) });
 const job = create(ResourceSchema, { kind: EntityKind.JOB, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ state: "claimed", input: { source_session_id: source.id } }) });
 const child = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Child", fork: { source_session_id: source.id } }) });
 const sent: unknown[] = [];
 const fork = vi.fn(async (request) => { sent.push(request); if (sent.length === 1) throw new ConnectError("lost reply", Code.Unavailable); return { job }; });
 let completed = false;
 const observe = vi.fn(async () => ({ job: completed ? { ...job, documentJson: encode({ state: "succeeded" }) } : job, session: completed ? child : undefined }));
 const open = vi.fn();
 const transport = createRouterTransport((router) => { router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) }); router.service(SessionService, { forkSession: fork, getSessionFork: observe }); router.service(ResourceService, { getResource: () => ({ resource: source }) }); });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const view = (active: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={open}>{active ? <SessionForkAction source={source} /> : <p>Other conversation</p>}</SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view(true));
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 expect((screen.getByRole("textbox", { name: "Fork name" }) as HTMLInputElement).value).toBe("Original fork");
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await screen.findByRole("button", { name: "Retry the same fork request" });
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 rendered.rerender(view(false));
 fireEvent.click(screen.getByRole("button", { name: "Return to retained fork operation" }));
 fireEvent.click(screen.getByRole("button", { name: "Retry the same fork request" }));
 await screen.findByRole("region", { name: "Fork operation" });
 expect(sent[1]).toEqual(sent[0]);
 expect(sent[0]).toMatchObject({ mutation: { id: source.id, expectedRevision: 8n }, expectedTurnId: turn, workspace: ForkWorkspace.UNSPECIFIED });
 expect(screen.queryByRole("button", { name: "Open forked session" })).toBeNull();
 completed = true;
	await waitFor(() => expect((screen.getByRole("button", { name: "Refresh fork operation" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Refresh fork operation" }));
 fireEvent.click(await screen.findByRole("button", { name: "Open forked session" }));
 await waitFor(() => expect(open).toHaveBeenCalledWith(child.id));
 expect(fork).toHaveBeenCalledTimes(2);
});


it("offers a completed root but refuses a completed forked child", async () => {
  const completed = { name: "Completed", workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: newRequestId(), cleanup_verified: true } };
  const root = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode(completed) });
  const child = create(ResourceSchema, { ...root, id: newRequestId(), documentJson: encode({ ...completed, fork: { source_session_id: root.id, native_thread_id: newRequestId() } }) });
  const fork = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) });
    router.service(SessionService, { forkSession: fork });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (source: typeof root) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>;
  const rendered = render(view(root));
  await screen.findByRole("button", { name: "Fork session" });
  rendered.rerender(view(child));
  expect(screen.queryByRole("button", { name: "Fork session" })).toBeNull();
  expect(fork).not.toHaveBeenCalled();
});

it.each(["worktree", "local"] as const)("offers Local sharing only for a user-owned Local source (%s)", async (workspace) => {
  const source = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Original", workspace, machine_id: newRequestId(), archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: newRequestId(), cleanup_verified: true } }) });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) });
    router.service(ResourceService, { getResource: () => ({ resource: source }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()} readLocalWorker={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
  const option = screen.queryByRole("option", { name: "Share this computer's Local checkouts" });
  if (workspace === "local") expect(option).not.toBeNull();
  else expect(option).toBeNull();
  expect(screen.getByRole("option", { name: "Independent workspace" })).not.toBeNull();
});

it.each([true, false])("requires independent OpenCode server and Unix Runner Device support (%s)", async (supported) => {
  const machineId = newRequestId();
  const source = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Original OpenCode", machine_id: machineId, workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "opencode" } }, execution: { native_thread_id: "ses_01960dcbe1faABCDEFGHIJKLMN", native_turn_id: "msg_01960dcbe1fcABCDEFGHIJKLMN", cleanup_verified: true, observed: { opencode_agent: "build" } } }) });
  const machine = create(ResourceSchema, { kind: EntityKind.MACHINE, id: machineId, revision: 1n, schemaVersion: 1, documentJson: encode({ os: supported ? "darwin" : "windows", worker_capabilities: ["opencode-general-chat-fork-v1"] }) });
  const fork = vi.fn();
  const transport = createRouterTransport((router) => { router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1] }) }); router.service(ResourceService, { getResource: (request) => ({ resource: request.kind === EntityKind.MACHINE ? machine : source }), listResources: () => ({resources: openCodePlainMessages(source)}) }); router.service(SessionService, { forkSession: fork }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
  if (supported) { fireEvent.click(await screen.findByRole("button", { name: "Fork session" })); expect(screen.getByText(/child keeps a separate copy/)).not.toBeNull(); }
  else { await waitFor(() => expect(client.isFetching()).toBe(0)); expect(screen.queryByRole("button", { name: "Fork session" })).toBeNull(); }
  expect(fork).not.toHaveBeenCalled();
});

function openCodePlainMessages(source: import("@delinoio/delidev-api-client").Resource) {
 const execution = object(document(source).execution);
 return ["user", "assistant"].map((role, index) => create(ResourceSchema, {kind:EntityKind.MESSAGE,id:newRequestId(),sessionId:source.id,schemaVersion:1,revision:1n,documentJson:encode({execution_id:newRequestId(),native_thread_id:execution.native_thread_id,native_turn_id:execution.native_turn_id,native_id:index ? "prt_01960dcbe1fbABCDEFGHIJKLMN" : "prt_01960dcbe1faABCDEFGHIJKLMN",native_parent_id:role === "user" ? execution.native_turn_id : "msg_01960dcbe1fdABCDEFGHIJKLMN",...(role === "user" ? {input_id:newRequestId()} : {}),role,text:"Original plain text",state:"complete",first_sequence:index+1,last_sequence:index+1})}));
}

it.each(["valid", "tool", "artifact", "changes", "empty-changes", "empty-summary", "missing-change-event", "invalid-change-event", "late-tool", "repeated-cursor", "changed-source"])("offers OpenCode Fork only after complete retained plain-text history: %s", async mode => {
 const machineId = newRequestId();
 const source = create(ResourceSchema,{kind:EntityKind.SESSION,id:newRequestId(),revision:8n,schemaVersion:1,documentJson:encode({name:"OpenCode",machine_id:machineId,workspace:"general-chat",archive:"active",recovery:"none",outcome:"succeeded",initial_execution:{configuration:{harness:"opencode"}},execution:{native_thread_id:"ses_01960dcbe1faABCDEFGHIJKLMN",native_turn_id:"msg_01960dcbe1fcABCDEFGHIJKLMN",cleanup_verified:true,observed:{opencode_agent:"build"}}})});
 const machine = create(ResourceSchema,{kind:EntityKind.MACHINE,id:machineId,revision:1n,schemaVersion:1,documentJson:encode({os:"linux",worker_capabilities:["opencode-general-chat-fork-v1"]})});
 const messages = openCodePlainMessages(source);
 const bad = document(messages[1]);
 if (mode === "tool" || mode === "late-tool") bad.tool = {name:"fixture"};
 if (mode === "artifact") bad.artifact = {kind:"fixture"};
 messages[1]!.documentJson = encode(bad);
 if (["changes", "empty-changes", "empty-summary", "missing-change-event", "invalid-change-event"].includes(mode)) {
  const changes: Record<string, unknown> = {...bad,role:"progress",text:"",native_id:"",first_sequence:3,last_sequence:3,progress:{kind:"opencode-changes",changes:{source:mode === "empty-summary" ? "input-summary" : "session-diff",native_event_id:mode === "missing-change-event" ? undefined : mode === "invalid-change-event" ? "msg_01960dcbe1fdABCDEFGHIJKLMN" : "evt_01960dcbe1fdABCDEFGHIJKLMN",...(mode === "empty-summary" ? {native_message_id:object(document(source).execution).native_turn_id} : {}),diffs:mode === "changes" ? [{file:"original.txt",additions:1,deletions:0}] : []}}};
  delete changes.native_parent_id;
  messages.push(create(ResourceSchema,{kind:EntityKind.MESSAGE,id:newRequestId(),sessionId:source.id,revision:1n,schemaVersion:1,documentJson:encode(changes)}));
 }
 const fork = vi.fn(); let reads = 0;
 const list = vi.fn((request) => request.filter?.pageToken ? {resources:messages.slice(1),nextPageToken:mode === "repeated-cursor" ? "later" : ""} : {resources:messages.slice(0,1),nextPageToken:"later"});
 const transport = createRouterTransport(router => {router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1]})});router.service(SessionService,{forkSession:fork});router.service(ResourceService,{getResource:request=>({resource:request.kind === EntityKind.MACHINE ? machine : mode === "changed-source" && ++reads > 1 ? {...source,revision:9n} : source}),listResources:list});});
 const client = new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source}/></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 const valid = ["valid", "empty-changes", "empty-summary"].includes(mode);
 if(valid) await screen.findByRole("button",{name:"Fork session"});
 else {await waitFor(()=>expect(list).toHaveBeenCalled());await waitFor(()=>expect(client.isFetching()).toBe(0));expect(screen.queryByRole("button",{name:"Fork session"})).toBeNull();}
 expect(fork).not.toHaveBeenCalled();
});
