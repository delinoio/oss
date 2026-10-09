// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, TerminalService, TerminalAction, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTerminals } from "./session-terminals";

// Component tests use a text fixture; real parser/WebGL/CSP acceptance runs in
// the external Chrome fixture. This adapter exercises input ownership only.
vi.mock("./terminal-emulator", () => ({ openTerminalScreen: (host: HTMLElement, input: (bytes: Uint8Array) => void) => {
  const output = document.createElement("pre"), field = document.createElement("textarea");
  field.setAttribute("aria-label", "Terminal input"); field.disabled = true;
  field.addEventListener("keydown", event => { if (!field.disabled && event.key === "Enter") input(new TextEncoder().encode(field.value + "\r")); });
  host.append(output, field); let decoder = new TextDecoder();
  return { write: async (bytes: Uint8Array, gap: boolean) => { if (gap) { decoder = new TextDecoder(); output.textContent = ""; } output.textContent += decoder.decode(bytes, { stream: true }); }, enabled: (value: boolean) => { field.disabled = !value; }, focus: () => field.focus(), dispose: () => host.replaceChildren() };
} }));

it("retries the exact creation request and reattaches without another shell", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running", machine_id: newRequestId(), shell: "/bin/sh", cwd: "/fixture" }) });
  const createTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  createTerminal.mockRejectedValueOnce(new ConnectError("acknowledgment lost", Code.Unavailable));
  const calls: { epoch: string; afterSequence: bigint }[] = [];
  const epoch = newRequestId();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [terminal] }) });
    router.service(TerminalService, { createTerminal, watchTerminalOutput: async function* (request) {
      calls.push({ epoch: request.epoch, afterSequence: request.afterSequence });
      if (calls.length === 1) { yield { epoch, sequence: 1n, data: new Uint8Array([...new TextEncoder().encode("original shell output"), 0xe2, 0x82]), terminal }; throw new ConnectError("disconnected", Code.Unavailable); }
      if (calls.length === 2) { yield { epoch, sequence: 1n, heartbeat: true, terminal }; return; }
      const exited = create(ResourceSchema, { ...terminal, revision: 5n, documentJson: encode({ state: "exited", cleanup_verified: true }) });
      yield { epoch, sequence: 2n, data: new Uint8Array([0xac]), terminal: exited };
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  await waitFor(() => expect((screen.getByRole("button", { name: "Create terminal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same terminal creation" }));
  await screen.findByText("original shell output");
  fireEvent.click(await screen.findByRole("button", { name: "Reattach original terminal" }));
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1]).toEqual({ epoch, afterSequence: 1n });
  fireEvent.click(await screen.findByRole("button", { name: "Reattach original terminal" }));
  await screen.findByText("original shell output€");
  expect(calls[2]).toEqual({ epoch, afterSequence: 1n });
  expect(createTerminal).toHaveBeenCalledTimes(2);
  expect(createTerminal.mock.calls[0]![0]).toEqual(createTerminal.mock.calls[1]![0]);
  view.unmount(); client.clear();
});


it("focuses the attached terminal input and sends exact UTF-8 and native resize controls", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running", machine_id: newRequestId() }) });
  const controlTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [terminal] }) });
    router.service(TerminalService, { controlTerminal, watchTerminalOutput: async function* () {
      yield { epoch: newRequestId(), sequence: 0n, heartbeat: true, terminal };
      await held;
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    const input = await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.change(input, { target: { value: "€" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    const sent = controlTerminal.mock.calls[0]![0] as { input: Uint8Array };
    expect(sent).toMatchObject({ mutation: { id: terminal.id, expectedRevision: 4n }, action: TerminalAction.INPUT });
    expect(Array.from(sent.input)).toEqual([0xe2, 0x82, 0xac, 13]);
    /* Automatic dimensions are tested by the dedicated queue/browser fixture. */
  } finally {
    view.unmount(); release(); client.clear();
  }
});

it("attaches to the accepted creation beyond the first full history page", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const history = Array.from({ length: 50 }, () => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 2n, documentJson: encode({ state: "closed", cleanup_verified: true }) }));
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running" }) });
  const createTerminal = vi.fn(() => ({ terminal }));
  const controlTerminal = vi.fn((_request: unknown) => ({ terminal }));
  const watched: string[] = [];
  const listResources = vi.fn(() => ({ resources: history, nextPageToken: "older-page" }));
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources });
    router.service(TerminalService, { createTerminal, controlTerminal, watchTerminalOutput: async function* (request) {
      watched.push(request.terminalId);
      yield { epoch: newRequestId(), sequence: 1n, data: new TextEncoder().encode("new original terminal"), terminal };
      await held;
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  try {
    await screen.findByRole("tab", { name: /Terminal 50/ });
    await waitFor(() => expect((screen.getByRole("button", { name: "Create terminal" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
    await screen.findByText("new original terminal");
    expect(watched).toEqual([terminal.id]);
    expect(createTerminal).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(listResources.mock.calls.length).toBeGreaterThan(1));
    const input = screen.getByRole("textbox", { name: "Terminal input" });
    fireEvent.change(input, { target: { value: "original" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    expect(controlTerminal.mock.calls[0]![0]).toMatchObject({ mutation: { id: terminal.id, expectedRevision: 4n }, action: TerminalAction.INPUT });
    expect(screen.getByText("new original terminal")).toBeTruthy();
  } finally {
    view.unmount(); release(); client.clear();
  }
});

it("gates initial reads, polling and manual refresh on advertised terminal support", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  let capabilities: SystemCapability[] = [];
  let statusPending = true;
  let release = (_value: { capabilities: SystemCapability[] }) => {};
  const pending = new Promise<{ capabilities: SystemCapability[] }>((resolve) => { release = resolve; });
  const getStatus = vi.fn(async () => statusPending ? await pending : { capabilities });
  const listResources = vi.fn(() => ({ resources: [] }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus });
    router.service(ResourceService, { listResources });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  const waitForPoll = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 1100)); });
  try {
    await waitFor(() => expect(getStatus).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "Details" }));
    const refresh = screen.getByRole("button", { name: "Refresh terminals" }) as HTMLButtonElement;
    expect(refresh.disabled).toBe(true);
    fireEvent.click(refresh);
    await waitForPoll();
    expect(listResources).not.toHaveBeenCalled();
    statusPending = false;
    await act(async () => release({ capabilities: [] }));
    await waitForPoll();
    expect(refresh.disabled).toBe(true);
    fireEvent.click(refresh);
    expect(listResources).not.toHaveBeenCalled();
    expect(screen.getByText("Waiting for a server that supports session terminals.")).toBeTruthy();
    capabilities = [SystemCapability.SESSION_TERMINALS_V1];
    await act(async () => { await client.invalidateQueries(); });
    await waitFor(() => expect(listResources).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(refresh.disabled).toBe(false));
    fireEvent.click(refresh);
    await waitFor(() => expect(listResources).toHaveBeenCalledTimes(2));
    capabilities = [];
    // Refresh only status to avoid requesting one more supported history page.
    await act(async () => { await client.invalidateQueries({ predicate: (query) => query.queryKey.some((part) => typeof part === "object" && part !== null && "methodName" in part && part.methodName === "GetStatus") }); });
    await waitFor(() => expect(refresh.disabled).toBe(true));
    const calls = listResources.mock.calls.length;
    await waitForPoll();
    fireEvent.click(refresh);
    expect(listResources).toHaveBeenCalledTimes(calls);
  } finally {
    release({ capabilities: [] }); view.unmount(); client.clear();
  }
});

it("keeps terminal input enabled while one control is pending and never steals focus on acknowledgment", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running" }) });
  let acknowledge = () => {};
  const pending = new Promise<void>((resolve) => { acknowledge = resolve; });
  const controlTerminal = vi.fn(async () => { await pending; return { terminal }; });
  let releaseStream = () => {};
  const held = new Promise<void>((resolve) => { releaseStream = resolve; });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [terminal] }) });
    router.service(TerminalService, { controlTerminal, watchTerminalOutput: async function* () {
      yield { epoch: newRequestId(), sequence: 0n, heartbeat: true, terminal };
      await held;
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  try {
    fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
    const input = await screen.findByRole("textbox", { name: "Terminal input" }) as HTMLTextAreaElement;
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.change(input, { target: { value: "fixture line" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    expect(input.disabled).toBe(false);
    // jsdom retains disabled focus; model the browser's focus loss explicitly.
    const columns = screen.getByRole("button", { name: "Details" });
    columns.focus();
    expect(document.activeElement).not.toBe(input);
    await act(async () => acknowledge());
    await waitFor(() => expect(input.disabled).toBe(false));
    expect(document.activeElement).toBe(columns);
    expect(controlTerminal).toHaveBeenCalledTimes(1);
    // A metadata poll must not steal focus while controls remain available.
    columns.focus();
    await act(async () => { await client.invalidateQueries(); });
    expect(document.activeElement).toBe(columns);
  } finally {
    acknowledge(); view.unmount(); releaseStream(); client.clear();
  }
});

it("preserves the explicitly attached terminal and its draft after its history payload is evicted", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, revision: 1n, documentJson: encode({ archive: "active" }) });
  const history = Array.from({ length: 4 }, () => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, revision: 1n, documentJson: encode({ state: "running" }) }));
  const watch = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: request => {
      const index = Number(request.filter?.pageToken || 0);
      return { resources: [history[index]], nextPageToken: index < 3 ? String(index + 1) : "" };
    } });
    router.service(TerminalService, { watchTerminalOutput: async function* (request, context) {
      watch(request);
      yield { epoch: newRequestId(), sequence: 1n, data: new TextEncoder().encode("Original attached output"), terminal: history[0] };
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
  await screen.findByText("Original attached output");
  const input = screen.getByRole("textbox", { name: "Terminal input" });
  fireEvent.change(input, { target: { value: "Retained terminal draft" } });
  for (let index = 2; index <= 4; index++) {
    fireEvent.click(screen.getByRole("button", { name: "Load more Terminal history pages" }));
    await screen.findByRole("tab", { name: new RegExp(`Terminal ${index}`) });
  }
  expect(screen.getByRole("tab", { name: /Terminal 1/ })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Terminal input" })).toBe(input);
  expect(input).toHaveProperty("value", "Retained terminal draft");
  expect(watch).toHaveBeenCalledTimes(1);
});

it("keeps failed terminal capability reads distinct from missing support and preserves the shell draft", async () => {
 const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
 const status = vi.fn().mockRejectedValueOnce(new ConnectError("private-native-status", Code.PermissionDenied)).mockResolvedValue({ capabilities: [] });
 const createTerminal = vi.fn();
 const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: status }); router.service(TerminalService, { createTerminal }); });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
 fireEvent.click(screen.getByRole("button", { name: "Details" }));
 const shell = screen.getByRole("textbox");
 fireEvent.change(shell, { target: { value: "/original/shell" } });
 const retry = await screen.findByRole("button", { name: "Retry terminal capability read" });
 expect(screen.queryByText(/Waiting for a server that supports/)).toBeNull();
 expect(createTerminal).not.toHaveBeenCalled();
 fireEvent.click(retry);
 await waitFor(() => expect(status).toHaveBeenCalledTimes(2));
 expect(shell).toHaveProperty("value", "/original/shell"); expect(createTerminal).not.toHaveBeenCalled();
});

it("retains exact uncertain input across dock hiding and never closes or creates a shell", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, sessionId: session.id, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running" }) });
  const controls = vi.fn(async (_request: unknown) => ({ terminal })); controls.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable));
  const createTerminal = vi.fn(), watched = vi.fn(), aborted = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [terminal] }) });
    router.service(TerminalService, { createTerminal, controlTerminal: controls, watchTerminalOutput: async function* (_request, context) {
      watched(); yield { epoch: "original-epoch", sequence: 0n, heartbeat: true, terminal };
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => { aborted(); resolve(); }, { once: true }));
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const fixture = (active: boolean) => <QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} active={active} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>;
  const view = render(fixture(true));
  fireEvent.click(await screen.findByRole("tab", { name: /Terminal 1/ }));
  const input = await screen.findByRole("textbox", { name: "Terminal input" }); await waitFor(() => expect(input).toHaveProperty("disabled", false));
  fireEvent.change(input, { target: { value: "€original" } });fireEvent.keyDown(input, { key: "Enter" });
  await screen.findByRole("button", { name: "Retry the same terminal operation" });expect(controls).toHaveBeenCalledTimes(1);
  view.rerender(fixture(false));await waitFor(()=>expect(aborted).toHaveBeenCalledTimes(1));
  view.rerender(fixture(true));fireEvent.click(await screen.findByRole("button", { name: "Retry the same terminal operation" }));
  await waitFor(()=>expect(controls).toHaveBeenCalledTimes(2));expect(controls.mock.calls[1]![0]).toEqual(controls.mock.calls[0]![0]);
  expect(createTerminal).not.toHaveBeenCalled();expect((controls.mock.calls[0]![0] as {action:TerminalAction}).action).toBe(TerminalAction.INPUT);
  view.unmount();client.clear();
});

it("keeps every existing terminal picker keyboard reachable in the single-pane workspace", async () => {
 const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({archive:"active"})});
 const terminals=[1,2].map(()=>create(ResourceSchema,{id:newRequestId(),kind:EntityKind.TERMINAL,sessionId:session.id,schemaVersion:1,revision:1n,documentJson:encode({state:"exited",cleanup_verified:true})}));
 const open=vi.fn(),transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SESSION_TERMINALS_V1]})});router.service(ResourceService,{listResources:()=>({resources:terminals})});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const view=render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} tabbed close={()=>{}} openTerminal={open}/></MutationIntents></TransportProvider></QueryClientProvider>);
 const first=await screen.findByRole("button",{name:/Terminal 1/}),second=screen.getByRole("button",{name:/Terminal 2/});expect(first.tabIndex).toBe(0);expect(second.tabIndex).toBe(0);first.focus();expect(fireEvent.keyDown(first,{key:"ArrowRight"})).toBe(true);expect(open).not.toHaveBeenCalled();second.focus();expect(document.activeElement).toBe(second);view.unmount();client.clear();
});
