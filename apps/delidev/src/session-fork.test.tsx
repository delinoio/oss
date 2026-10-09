import { i18n } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ForkPurpose, ForkWorkspace, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionForkAction, SessionForkProvider } from "./session-fork";

function deferred<T>() {
 let resolve!: (value: T) => void;
 const promise = new Promise<T>(done => { resolve = done; });
 return { promise, resolve };
}

async function openCodePreflightFixture() {
 const machineId = newRequestId();
 const sources = ["Source A", "Source B"].map(name => create(ResourceSchema, {
  kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1,
  documentJson: encode({ name, machine_id: machineId, workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "opencode" } }, execution: { native_thread_id: "ses_01960dcbe1faABCDEFGHIJKLMN", native_turn_id: "msg_01960dcbe1fcABCDEFGHIJKLMN", cleanup_verified: true, observed: { opencode_agent: "build" } } }),
 }));
 const machine = create(ResourceSchema, { kind: EntityKind.MACHINE, id: machineId, revision: 1n, schemaVersion: 1, documentJson: encode({ os: "linux", worker_capabilities: ["opencode-general-chat-fork-v1"] }) });
 const gate = deferred<void>();
 const waiting = vi.fn();
 let hold = false;
 const jobs = new Map<string, Resource>();
 const fork = vi.fn(async request => {
  const job = create(ResourceSchema, { kind: EntityKind.JOB, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ state: "claimed", input: { source_session_id: request.mutation?.id } }) });
  jobs.set(job.id, job);
  return { job };
 });
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.OPENCODE_GENERAL_CHAT_FORK_V1] }) });
  router.service(ResourceService, {
   getResource: request => ({ resource: request.kind === EntityKind.MACHINE ? machine : sources.find(source => source.id === request.id) }),
   listResources: async request => {
    const source = sources.find(source => source.id === request.filter?.sessionId)!;
    if (hold && source.id === sources[0]!.id) { waiting(); await gate.promise; }
    return { resources: openCodePlainMessages(source) };
   },
  });
  router.service(SessionService, { forkSession: fork, getSessionFork: request => ({ job: jobs.get(request.jobId) }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const view = (source = sources[0]!) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view());
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 await waitFor(() => expect((screen.getByRole("button", { name: "Create fork" }) as HTMLButtonElement).disabled).toBe(false));
 hold = true;
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await waitFor(() => expect(waiting).toHaveBeenCalledTimes(1));
 return { sources, fork, client, gate, view, rendered, waiting };
}

it("does not admit a discarded Fork after its deferred profile check completes", async () => {
 const fixture = await openCodePreflightFixture();
 fireEvent.click(screen.getByRole("button", { name: "Discard fork draft" }));
 await act(async () => { fixture.gate.resolve(); });
 await waitFor(() => expect(fixture.client.isFetching()).toBe(0));
 expect(fixture.fork).not.toHaveBeenCalled();
 expect(screen.queryByRole("dialog", { name: "Fork session" })).toBeNull();
 expect(screen.queryByRole("button", { name: "Return to retained fork operation" })).toBeNull();
});

it("keeps a replacement draft and its accepted job independent of the old profile check", async () => {
 const fixture = await openCodePreflightFixture();
 fixture.rendered.rerender(fixture.view(fixture.sources[1]!));
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 expect((screen.getByRole("textbox", { name: "Fork name" }) as HTMLInputElement).value).toBe("Source B fork");
 await waitFor(() => expect((screen.getByRole("button", { name: "Create fork" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await screen.findByText("Accepted by the server. Waiting for the selected Worker to finish.");
 await act(async () => { fixture.gate.resolve(); });
 await waitFor(() => expect(fixture.client.isFetching()).toBe(0));
 expect(fixture.fork).toHaveBeenCalledTimes(1);
 expect(fixture.fork.mock.calls[0]?.[0]).toMatchObject({ mutation: { id: fixture.sources[1]!.id, expectedRevision: 8n }, name: "Source B fork" });
 expect(screen.getByText(`Source: ${fixture.sources[1]!.id} · Turn: msg_01960dcbe1fcABCDEFGHIJKLMN`)).not.toBeNull();
});

it("requires a new submission when the same source is reopened after discard", async () => {
 const fixture = await openCodePreflightFixture();
 fireEvent.click(screen.getByRole("button", { name: "Discard fork draft" }));
 fireEvent.click(screen.getByRole("button", { name: "Fork session" }));
 fireEvent.change(screen.getByRole("textbox", { name: "Fork name" }), { target: { value: "Replacement name" } });
 await act(async () => { fixture.gate.resolve(); });
 await waitFor(() => expect((screen.getByRole("button", { name: "Create fork" }) as HTMLButtonElement).disabled).toBe(false));
 expect(fixture.fork).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await screen.findByText("Accepted by the server. Waiting for the selected Worker to finish.");
 expect(fixture.fork).toHaveBeenCalledTimes(1);
 expect(fixture.fork.mock.calls[0]?.[0]).toMatchObject({ mutation: { id: fixture.sources[0]!.id, expectedRevision: 8n }, name: "Replacement name" });
});

it("submits once and retains the original Fork when hidden during profile validation", async () => {
 const fixture = await openCodePreflightFixture();
 const form = screen.getByRole("textbox", { name: "Fork name" }).closest("form")!;
 fireEvent.submit(form);
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 await act(async () => { fixture.gate.resolve(); });
 await waitFor(() => expect(fixture.fork).toHaveBeenCalledTimes(1));
 expect(fixture.waiting).toHaveBeenCalledTimes(1);
 expect(fixture.fork.mock.calls[0]?.[0]).toMatchObject({ mutation: { id: fixture.sources[0]!.id, expectedRevision: 8n }, name: "Source A fork" });
 expect(screen.queryByRole("dialog", { name: "Fork session" })).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Return to retained fork operation" }));
 await screen.findByText("Accepted by the server. Waiting for the selected Worker to finish.");
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 fireEvent.click(screen.getByRole("button", { name: "Return to retained fork operation" }));
 expect(screen.getByText("Accepted by the server. Waiting for the selected Worker to finish.")).not.toBeNull();
 expect(fixture.fork).toHaveBeenCalledTimes(1);
});

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
 const input = screen.getByRole("textbox", { name: "Fork name" }); input.focus();
 fireEvent.change(input, { target: { value: "Original confirmation draft" } });
 await act(() => i18n.changeLanguage("ko"));
 expect(screen.getByRole("textbox", { name: "포크 이름" })).toBe(input);
 expect(window.document.activeElement).toBe(input); expect(input).toHaveProperty("value", "Original confirmation draft");
 expect(fork).not.toHaveBeenCalled(); expect(observe).not.toHaveBeenCalled();
 await act(() => i18n.changeLanguage("en"));
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await screen.findByRole("button", { name: "Retry the same fork request" });
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 rendered.rerender(view(false));
 fireEvent.click(screen.getByRole("button", { name: "Return to retained fork operation" }));
 fireEvent.click(screen.getByRole("button", { name: "Retry the same fork request" }));
 await screen.findByText("Accepted by the server. Waiting for the selected Worker to finish.");
 expect(sent[1]).toEqual(sent[0]);
 expect(sent[0]).toMatchObject({ mutation: { id: source.id, expectedRevision: 8n }, expectedTurnId: turn, workspace: ForkWorkspace.UNSPECIFIED });
 expect(screen.queryByRole("button", { name: "Open forked session" })).toBeNull();
 completed = true;
 await act(async () => { await client.invalidateQueries(); });
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

it.each([true, false])("gates native Sidechat on the original Runner Device and sends no workspace override (%s)", async (supported) => {
 const machineId = newRequestId();
 const source = create(ResourceSchema, {schemaVersion:1,kind:EntityKind.SESSION,id:newRequestId(),revision:8n,documentJson:encode({name:"Original",machine_id:machineId,workspace:"worktree",archive:"active",recovery:"none",outcome:"succeeded",initial_execution:{configuration:{harness:"codex",account_type:"api"}},execution:{native_turn_id:newRequestId(),cleanup_verified:true}})});
 const machine=create(ResourceSchema,{schemaVersion:1,kind:EntityKind.MACHINE,id:machineId,revision:1n,documentJson:encode({worker_capabilities:supported?["codex-read-only-sidechat-v1"]:[]})});
 const job=create(ResourceSchema,{schemaVersion:1,kind:EntityKind.JOB,id:newRequestId(),revision:1n,documentJson:encode({state:"claimed",input:{source_session_id:source.id}})});
 const fork=vi.fn(async(_request:unknown)=>({job}));
 const transport=createRouterTransport((router)=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.NATIVE_SIDECHAT_V1]})});router.service(ResourceService,{getResource:(request)=>({resource:request.kind===EntityKind.MACHINE?machine:source})});router.service(SessionService,{forkSession:fork,getSessionFork:()=>({job})});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source}/></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 if(!supported){await waitFor(()=>expect(client.isFetching()).toBe(0));expect(screen.queryByRole("button",{name:"Open Sidechat"})).toBeNull();expect(fork).not.toHaveBeenCalled();return;}
 fireEvent.click(await screen.findByRole("button",{name:"Open Sidechat"}));
 expect(screen.queryByRole("combobox",{name:"Workspace"})).toBeNull();
 expect(screen.getByText(/permanently deletes dependent Sidechats/)).not.toBeNull();
 fireEvent.click(screen.getByRole("button",{name:"Create Sidechat"}));
 await waitFor(()=>expect(fork).toHaveBeenCalledTimes(1));
 expect(fork.mock.calls[0]?.[0]).toMatchObject({purpose:ForkPurpose.SIDECHAT,workspace:ForkWorkspace.UNSPECIFIED,mutation:{id:source.id,expectedRevision:8n}});
});

it.each(["pending", "uncertain", "stored"])("hides native fork and Sidechat while the parent workspace is %s", async (state) => {
 const source=create(ResourceSchema,{kind:EntityKind.SESSION,id:newRequestId(),revision:8n,schemaVersion:1,documentJson:encode({name:"Parent",machine_id:newRequestId(),workspace:"general-chat",archive:"active",recovery:"none",outcome:"succeeded",storage:{state},initial_execution:{configuration:{harness:"codex"}},execution:{native_turn_id:newRequestId(),cleanup_verified:true}})});
 const machine=create(ResourceSchema,{kind:EntityKind.MACHINE,id:newRequestId(),revision:1n,schemaVersion:1,documentJson:encode({worker_capabilities:["codex-read-only-sidechat-v1"]})});
 const fork=vi.fn();
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.CODEX_SESSION_FORK_V1,SystemCapability.NATIVE_SIDECHAT_V1]})});router.service(ResourceService,{getResource:()=>({resource:machine})});router.service(SessionService,{forkSession:fork});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source}/></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 await waitFor(()=>expect(client.isFetching()).toBe(0));
 expect(screen.queryByRole("button",{name:"Open Sidechat"})).toBeNull();expect(screen.queryByRole("button",{name:"Fork session"})).toBeNull();expect(fork).not.toHaveBeenCalled();
});


it("reinspects the retained uncertain Fork without resending creation", async () => {
 const source = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Recovery source", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { cleanup_verified: true, native_turn_id: "original-turn" } }) });
 const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, revision: 1n, schemaVersion: 1, documentJson: encode({ state: "uncertain", input: { source_session_id: source.id } }) });
 const forkSession = vi.fn(() => ({ job }));
 const getSessionFork = vi.fn((_request: { jobId: string }) => ({ job }));
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) });
  router.service(ResourceService, { getResource: () => ({ resource: source }) });
  router.service(SessionService, { forkSession, getSessionFork });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 fireEvent.click(await screen.findByRole("button", { name: "Create fork" }));
 const retry = await screen.findByRole("button", { name: "Retry original status read" });
 await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(retry);
 await waitFor(() => expect(getSessionFork).toHaveBeenCalledTimes(2));
 expect(getSessionFork.mock.calls.every(call => (call[0] as {jobId: string}).jobId === job.id)).toBe(true);
 expect(forkSession).toHaveBeenCalledTimes(1);
 expect(screen.queryByRole("button", { name: "Finish fork" })).toBeNull();
});


it.each(["missing", "foreign", "wrong-kind"])("reinspects a successful Fork with an unverified %s child", async mode => {
 const source = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Recovery source", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { cleanup_verified: true, native_turn_id: "original-turn" } }) });
 const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, revision: 1n, schemaVersion: 1, documentJson: encode({ state: "succeeded", input: { source_session_id: source.id } }) });
 const forkSession = vi.fn(() => ({ job }));
 const child = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ fork: { source_session_id: source.id } }) });
 const getSessionFork = vi.fn((_request: { jobId: string }) => ({ job, session: child }));
 getSessionFork.mockReturnValueOnce({ job, session: mode === "missing" ? undefined! : { ...child, ...(mode === "wrong-kind" ? { kind: EntityKind.JOB } : { documentJson: encode({ fork: { source_session_id: newRequestId() } }) }) } });
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) });
  router.service(ResourceService, { getResource: () => ({ resource: source }) });
  router.service(SessionService, { forkSession, getSessionFork });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 fireEvent.click(await screen.findByRole("button", { name: "Create fork" }));
 const retry = await screen.findByRole("button", { name: "Retry original status read" });
 await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(retry);
 await waitFor(() => expect(getSessionFork).toHaveBeenCalledTimes(2));
 expect(getSessionFork.mock.calls.every(call => (call[0] as {jobId: string}).jobId === job.id)).toBe(true);
 expect(forkSession).toHaveBeenCalledTimes(1);
 expect(await screen.findByRole("button", { name: "Open forked session" })).toBeTruthy();
});


it.each(["eligible", "missing-server", "missing-worker", "missing-auth", "locked"])("negotiates managed ChatGPT Sidechat with its original Runner (%s)", async (profile) => {
 const machineId = newRequestId();
 const source = create(ResourceSchema, { schemaVersion: 1, kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, documentJson: encode({ name: "Managed parent", machine_id: machineId, workspace: "worktree", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex", subscription: true, subscription_service: "chatgpt" } }, execution: { native_turn_id: newRequestId(), cleanup_verified: true } }) });
 const workerCapabilities = ["codex-read-only-sidechat-v1", ...(profile !== "missing-worker" ? ["managed-codex-sidechat-v1"] : []), ...(profile !== "missing-auth" ? ["managed-codex-subscriptions-v1"] : [])];
 const machine = create(ResourceSchema, { schemaVersion: 1, kind: EntityKind.MACHINE, id: machineId, revision: 1n, documentJson: encode({ worker_capabilities: workerCapabilities }) });
 const job = create(ResourceSchema, { schemaVersion: 1, kind: EntityKind.JOB, id: newRequestId(), revision: 1n, documentJson: encode({ state: "claimed", input: { source_session_id: source.id } }) });
 const fork = vi.fn(async (_request: unknown) => ({ job }));
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.NATIVE_SIDECHAT_V1, ...(profile !== "missing-server" ? [SystemCapability.MANAGED_CODEX_SIDECHAT_V1] : [])] }) });
  router.service(ResourceService, { getResource: request => ({ resource: request.kind === EntityKind.MACHINE ? machine : source }) });
  router.service(SessionService, { forkSession: fork, getSessionFork: () => ({ job }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} disabled={profile === "locked"} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 if (profile.startsWith("missing")) {
  await waitFor(() => expect(client.isFetching()).toBe(0));
  expect(screen.queryByRole("button", { name: "Open Sidechat" })).toBeNull();
 } else {
  const action = await screen.findByRole("button", { name: "Open Sidechat" });
  if (profile === "locked") expect((action as HTMLButtonElement).disabled).toBe(true);
  else {
   fireEvent.click(action);
   fireEvent.click(await screen.findByRole("button", { name: "Create Sidechat" }));
   await waitFor(() => expect(fork).toHaveBeenCalledTimes(1));
   expect(fork.mock.calls[0]?.[0]).toMatchObject({ purpose: ForkPurpose.SIDECHAT, workspace: ForkWorkspace.UNSPECIFIED });
  }
 }
 if (profile !== "eligible") expect(fork).not.toHaveBeenCalled();
});

it.each(["all", "server", "worker", "subscription"])("negotiates managed independent Fork separately: %s", async missing => {
 const machineId = newRequestId();
 const source = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1, documentJson: encode({ name: "Managed source", machine_id: machineId, workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex", subscription: true } }, execution: { native_thread_id: newRequestId(), native_turn_id: newRequestId(), cleanup_verified: true } }) });
 const workerCapabilities = ["managed-codex-sidechat-v1", "codex-read-only-sidechat-v1", ...(missing === "worker" ? [] : ["managed-codex-fork-v1"]), ...(missing === "subscription" ? [] : ["managed-codex-subscriptions-v1"])];
 const machine = create(ResourceSchema, { kind: EntityKind.MACHINE, id: machineId, revision: 1n, schemaVersion: 1, documentJson: encode({ os: "linux", worker_capabilities: workerCapabilities }) });
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1, SystemCapability.MANAGED_CODEX_SIDECHAT_V1, ...(missing === "server" ? [] : [SystemCapability.MANAGED_CODEX_FORK_V1])] }) });
  router.service(ResourceService, { getResource: () => ({ resource: machine }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const rendered = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={vi.fn()}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 await waitFor(() => expect(client.getQueryCache().getAll().filter(query => query.state.status === "success").length).toBe(2));
 expect(Boolean(screen.queryByRole("button", { name: "Fork session" }))).toBe(missing === "all");
 rendered.unmount();client.clear();
});

async function rejectedDraftFixture(code = Code.FailedPrecondition, hold = false) {
 const sources = ["Rejected source", "Fresh source"].map(name => create(ResourceSchema, {
  kind: EntityKind.SESSION, id: newRequestId(), revision: 8n, schemaVersion: 1,
  documentJson: encode({ name, workspace: "general-chat", archive: "active", recovery: "none", outcome: "succeeded", initial_execution: { configuration: { harness: "codex" } }, execution: { native_turn_id: newRequestId(), cleanup_verified: true } }),
 }));
 const gate = deferred<void>();
 const jobs = new Map<string, Resource>();
 const children = new Map<string, Resource>();
 const open = vi.fn();
 const fork = vi.fn(async request => {
  if (fork.mock.calls.length === 1) {
   if (hold) await gate.promise;
   throw new ConnectError("Original Fork rejection", code);
  }
  const job = create(ResourceSchema, { kind: EntityKind.JOB, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ state: "succeeded", input: { source_session_id: request.mutation?.id } }) });
  const child = create(ResourceSchema, { kind: EntityKind.SESSION, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Verified child", fork: { source_session_id: request.mutation?.id } }) });
  jobs.set(job.id, job); children.set(job.id, child);
  return { job };
 });
 const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.CODEX_SESSION_FORK_V1] }) });
  router.service(ResourceService, { getResource: request => ({ resource: sources.find(source => source.id === request.id) }) });
  router.service(SessionService, { forkSession: fork, getSessionFork: request => ({ job: jobs.get(request.jobId), session: children.get(request.jobId) }) });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 const view = (source = sources[0]!) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionForkProvider openSession={open}><SessionForkAction source={source} /></SessionForkProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered = render(view());
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 await waitFor(() => expect(fork).toHaveBeenCalledTimes(1));
 if (!hold) await screen.findByText("recovery_required");
 return { sources, fork, gate, open, children, rendered, view };
}

it.each(["discard-other", "discard-same", "replace-other"])("clears only the rejected draft diagnostic before a fresh submission: %s", async mode => {
 const fixture = await rejectedDraftFixture();
 // Hiding and reopening the original draft must preserve its own diagnostic.
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 fireEvent.click(screen.getByRole("button", { name: "Return to retained fork operation" }));
 expect(screen.getByText("recovery_required")).toBeDefined();
 if (mode !== "replace-other") fireEvent.click(screen.getByRole("button", { name: "Discard fork draft" }));
 const next = mode === "discard-same" ? fixture.sources[0]! : fixture.sources[1]!;
 fixture.rendered.rerender(fixture.view(next));
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 expect(screen.queryByText("recovery_required")).toBeNull();
 expect(screen.getByRole("textbox", { name: "Fork name" })).toHaveProperty("value", `${document(next).name} fork`);
 expect(fixture.fork).toHaveBeenCalledTimes(1);
 fireEvent.click(screen.getByRole("button", { name: "Create fork" }));
 fireEvent.click(await screen.findByRole("button", { name: "Open forked session" }));
 await waitFor(() => expect(fixture.open).toHaveBeenCalledWith([...fixture.children.values()][0]!.id));
 expect(fixture.fork).toHaveBeenCalledTimes(2);
 expect(fixture.fork.mock.calls[1]![0]).toMatchObject({ mutation: { id: next.id, expectedRevision: next.revision }, expectedTurnId: object(document(next).execution).native_turn_id });
 expect(fixture.fork.mock.calls[1]![0].mutation?.requestId).not.toBe(fixture.fork.mock.calls[0]![0].mutation?.requestId);
});

it("preserves pending then uncertain original ownership through hiding and attempted source replacement", async () => {
 const fixture = await rejectedDraftFixture(Code.Unavailable, true);
 expect(screen.queryByRole("button", { name: "Discard fork draft" })).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 fixture.rendered.rerender(fixture.view(fixture.sources[1]!));
 fireEvent.click(await screen.findByRole("button", { name: "Fork session" }));
 expect(screen.getByRole("textbox", { name: "Fork name" })).toHaveProperty("value", "Rejected source fork");
 await act(async () => fixture.gate.resolve());
 await screen.findByRole("button", { name: "Retry the same fork request" });
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 fireEvent.click(screen.getByRole("button", { name: "Fork session" }));
 expect(screen.getByRole("textbox", { name: "Fork name" })).toHaveProperty("value", "Rejected source fork");
 expect(screen.queryByRole("button", { name: "Discard fork draft" })).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Retry the same fork request" }));
 await screen.findByRole("button", { name: "Open forked session" });
 expect(fixture.fork.mock.calls[1]![0]).toEqual(fixture.fork.mock.calls[0]![0]);
 fireEvent.click(screen.getByRole("button", { name: "Close Fork session" }));
 fireEvent.click(screen.getByRole("button", { name: "Fork session" }));
 expect(screen.getByText(new RegExp(fixture.sources[0]!.id))).toBeDefined();
 expect(screen.queryByRole("button", { name: "Create fork" })).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Open forked session" }));
 expect(fixture.open).toHaveBeenCalledWith([...fixture.children.values()][0]!.id);
 expect(fixture.fork).toHaveBeenCalledTimes(2);
});
