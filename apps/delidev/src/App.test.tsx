import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, InboxService, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

function fixture(interactions: Resource[] = []) {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Retained session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "none" }) });
  const message = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role: "assistant", text: '<script>window.invalid = true</script>', state: "completed" }) });
  const other = create(ResourceSchema, { ...session, id: newRequestId(), documentJson: encode({ name: "Other session", workspace: "general-chat", outcome: "idle", archive: "active", dispatch: "paused", recovery: "none" }) });
  other.sessionId = other.id;
  const enqueues = vi.fn(async () => ({ change: { session } }));
  const controls = vi.fn(async () => ({ change: { session } }));
  const status = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 1 }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(SessionService, { listSessions: () => ({ sessions: [session, other] }), listQueue: () => ({ inputs: [] }), enqueueInput: enqueues, controlSession: controls });
    router.service(ResourceService, {
      getSnapshot: (request) => ({ resources: [request.filter?.sessionId === other.id ? other : session], cursor: "snapshot" }),
      listResources: (request) => ({ resources: request.filter?.kind === EntityKind.MESSAGE ? [message] : request.filter?.kind === EntityKind.INTERACTION ? interactions : [] }),
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); });
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }) });
    router.service(ConfigurationService, {});
  });
  return { transport, session, message, enqueues, controls, status };
}

it("keeps the draft and session mounted across settings and navigation, and renders native text inertly", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep my unsent input" } });
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  expect(window.document.activeElement).toBe(screen.getByRole("button", { name: "Settings" }));
  expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  await screen.findByText("No retained requests or completions.");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  expect(await screen.findByText('<script>window.invalid = true</script>')).toBeTruthy();
  expect(window.document.querySelector("script")).toBeNull();
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("refreshes reads after recovery without replacing the connection's session draft", async () => {
  const value = fixture();
  value.status.mockRejectedValueOnce(new ConnectError("Server is stopped", Code.Unavailable));
  const view = render(<App transport={value.transport} connectionReady={false} />);
  await screen.findByText("Server unavailable");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Retain this across server restart" } });
  view.rerender(<App transport={value.transport} connectionReady connectionEpoch={1} />);
  await screen.findByText("Server 0.1.0");
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Retain this across server restart");
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("retries the exact accepted message identity after uncertainty instead of sending a new message", async () => {
  const value = fixture();
  value.enqueues.mockRejectedValueOnce(new ConnectError("Lost response.", Code.Unavailable));
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "One logical message" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Queue message" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same message" }));
  await waitFor(() => expect((composer as HTMLTextAreaElement).value).toBe(""));
  expect(value.enqueues).toHaveBeenCalledTimes(2);
  const calls = value.enqueues.mock.calls as unknown as [unknown][];
  expect(calls[0][0]).toEqual(calls[1][0]);
});

it("opens execution configuration without changing the unsent session draft or dispatching work", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep the original unsent draft" } });
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  expect(screen.getByText(/No accepted execution configuration/)).toBeTruthy();
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  expect((composer as HTMLTextAreaElement).value).toBe("Keep the original unsent draft");
  expect(value.controls).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("sends explicit Restore with the original revision and never sends Resume on its behalf", async () => {
  const value = fixture();
  value.session.documentJson = encode({ name: "Retained session", workspace: "general-chat", outcome: "failed", archive: "archived", dispatch: "paused", recovery: "none" });
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const restore = await screen.findByRole("button", { name: "Restore" });
  await act(async () => fireEvent.click(restore));
  expect(value.controls).toHaveBeenCalledTimes(1);
  const calls = value.controls.mock.calls as unknown as [{ mutation: { id: string; expectedRevision: bigint }; action: number }][];
  expect(calls[0][0]).toMatchObject({ mutation: { id: value.session.id, expectedRevision: 7n }, action: 3 });
});

it("retains an uncertain message across a switch to another session", async () => {
  const value = fixture();
  value.enqueues.mockRejectedValueOnce(new ConnectError("Lost response.", Code.Unavailable));
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Message" }), { target: { value: "Original request" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Queue message" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await screen.findByRole("button", { name: "Retry the same message" });
  fireEvent.click(screen.getByRole("button", { name: /General Chat Other session/ }));
  await screen.findByRole("heading", { name: "Other session" });
  fireEvent.click(screen.getByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same message" }));
  await waitFor(() => expect(value.enqueues).toHaveBeenCalledTimes(2));
  const calls = value.enqueues.mock.calls as unknown as [unknown][];
  expect(calls[0][0]).toEqual(calls[1][0]);
});

it("drops connection-scoped drafts and caches when the selected transport changes", async () => {
  const first = fixture();
  const second = fixture();
  const view = render(<App transport={first.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Message" }), { target: { value: "Private draft for first server" } });
  view.rerender(<App transport={second.transport} />);
  await screen.findByText("Your sessions, in one place");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  expect((await screen.findByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("");
});


it("does not present cached server status as current connectivity after a failed refresh", async () => {
  const value = fixture();
  const view = render(<App transport={value.transport} />);
  await screen.findByText("Server 0.1.0");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep while disconnected" } });
  value.status.mockRejectedValue(new ConnectError("Server disconnected", Code.Unavailable));
  view.rerender(<App transport={value.transport} connectionEpoch={1} />);
  await screen.findByText("Server unavailable");
  expect(screen.queryByText("Server 0.1.0")).toBeNull();
  expect((composer as HTMLTextAreaElement).value).toBe("Keep while disconnected");
  expect(value.enqueues).not.toHaveBeenCalled();
});

for (const mixed of [false, true]) {
  it(`renders original Claude blocks and rejects mixed message families (${mixed})`, async () => {
    const value = fixture();
    value.message.documentJson = encode({
      role: "assistant", text: "", state: "complete",
      claude: { model: "fixture", stop_reason: "end_turn", stop_sequence: null, blocks: [
        { index: 0, block: { kind: "thinking", text: "Original native reasoning" }, state: "stopped" },
        { index: 1, block: { kind: "text", text: "Original native answer" }, state: "stopped" },
      ] },
      ...(mixed ? { tool: { output: "Mixed tool output" } } : {}),
    });
    render(<App transport={value.transport} />);
    fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
    if (mixed) {
      expect(await screen.findByLabelText("Claude message unavailable")).toBeTruthy();
      expect(screen.queryByText("Original native answer")).toBeNull();
      expect(screen.queryByText("Mixed tool output")).toBeNull();
    } else {
      expect(await screen.findByText("Original native answer")).toBeTruthy();
      expect(screen.getByText("Original native reasoning")).toBeTruthy();
    }
    expect(value.enqueues).not.toHaveBeenCalled();
    expect(value.controls).not.toHaveBeenCalled();
  });
}

for (const mixed of [false, true]) {
  it(`renders original Claude tool ownership and rejects mixed records (${mixed})`, async () => {
    const value = fixture();
    value.message.documentJson = encode({ role: "tool", text: "", state: "complete", native_id: "tool_original", native_parent_id: "msg_original",
      claude_tool: { reference: { id: value.message.id, native_id: "tool_original", name: "Read" }, message_id: "01900000-0000-7000-8000-000000000002", native_message_id: "msg_original", index: 0, caller: null, initial_input: "{}", input_delta: null, proposal: { proposed: "{}", applied: "{}" }, result: { native_event_id: "123e4567-e89b-42d3-a456-426614174000", is_error: false, text: "Original Claude tool output", blocks: null, structured: null } },
      ...(mixed ? { claude: { model: "foreign" }, tool: { output: "Mixed output" } } : {}),
    });
    render(<App transport={value.transport} />);
    fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
    if (mixed) {
      expect(await screen.findByLabelText("Claude tool unavailable")).toBeTruthy();
      expect(screen.queryByText("Original Claude tool output")).toBeNull();
      expect(screen.queryByText("Mixed output")).toBeNull();
    } else expect(await screen.findByText("Original Claude tool output")).toBeTruthy();
    expect(value.enqueues).not.toHaveBeenCalled();
    expect(value.controls).not.toHaveBeenCalled();
  });
}

for (const mixed of [false, true]) it(`renders original Claude progress through session RPC with no implied input action (${mixed})`, async () => {
  const value = fixture(), native = newRequestId();
  value.message.documentJson = encode({
    execution_id: newRequestId(), native_thread_id: newRequestId(), native_turn_id: newRequestId(), native_id: native,
    role: "progress", text: "", state: "complete", first_sequence: 2, last_sequence: 2,
    claude_progress: { native_event_id: native, kind: "session-status", input_accepted: false, status: { status: "requesting", permission: null, compact_result: null, compact_error: null }, thinking: null },
    ...(mixed ? { claude_tool: {} } : {}),
  });
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  if (mixed) {
    expect(await screen.findByLabelText("Claude progress unavailable")).toBeTruthy();
    expect(screen.queryByText("Requesting")).toBeNull();
  } else {
    expect(await screen.findByText("Before input acceptance")).toBeTruthy();
    expect(screen.getByText("Requesting")).toBeTruthy();
  }
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();
});

for (const mixed of [false, true]) it(`renders Claude callbacks through the session RPC without other response controls (${mixed})`, async () => {
  const data = { type: "native-approval", closure: "open", native_item_id: "tool_original", native_request_id: { kind: "text", text: "request_original" }, claude: {
    version: "2.1.236", kind: "tool-permission", arrival_id: newRequestId(), tool: { id: newRequestId(), native_id: "tool_original", name: "Bash" }, message_id: newRequestId(), native_message_id: "msg_original", index: 0, caller: null, input_json: '{"command":"printf original"}', metadata: { permission_suggestions: null, blocked_path: null, decision_reason: null, decision_reason_type: null, requires_user_interaction: null, agent_id: null, title: null, display_name: null, description: "Original callback description" },
  }, ...(mixed ? { opencode: {} } : {}) };
  const interaction = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTERACTION, revision: 1n, schemaVersion: 1, documentJson: encode(data) });
  const value = fixture([interaction]); interaction.sessionId = value.session.id;
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  if (mixed) {
    expect(await screen.findByLabelText("Claude request unavailable")).toBeTruthy();
    expect(screen.queryByText("Original callback description")).toBeNull();
  } else expect(await screen.findByText("Original callback description")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Send decision" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  expect(value.enqueues).not.toHaveBeenCalled(); expect(value.controls).not.toHaveBeenCalled();
});

it("opens and closes workspace files without replacing or sending the composer draft", async () => {
  const f = fixture(); render(<App transport={f.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep while browsing files" } });
  const files = screen.getByRole("button", { name: "Files" });
  fireEvent.click(files);
  expect(await screen.findByRole("complementary", { name: "Session files" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  fireEvent.click(screen.getByRole("button", { name: "Close session files" }));
  expect(document.activeElement).toBe(files);
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep while browsing files");
  expect(f.enqueues).not.toHaveBeenCalled();
  expect(f.controls).not.toHaveBeenCalled();
});

for (const mixed of [false, true]) it(`renders original Grok text through session RPC without new input (${mixed})`, async () => {
 const value=fixture(),thread=newRequestId(),meta={event_id:`${thread}-10`,chunk_id:"1",context_tokens:"18446744073709551615",timestamp_ms:"1",stream_start_ms:"0",turn_start_ms:"0"};
 value.message.documentJson=encode({execution_id:newRequestId(),native_thread_id:thread,native_turn_id:"526452fa-1956-42dd-b5f4-60e2b23dfe92",native_id:meta.event_id,role:"assistant",text:"Original Grok text",state:"complete",first_sequence:3,last_sequence:4,grok_text:{response_ordinal:1,chunks:[meta]},...(mixed?{claude_progress:{}}:{})});
 render(<App transport={value.transport}/>);
 fireEvent.click(await screen.findByRole("button",{name:/General Chat Retained session/}));
 if(mixed){expect(await screen.findByLabelText("Grok text unavailable")).toBeTruthy();expect(screen.queryByText("Original Grok text")).toBeNull();}
 else expect(await screen.findByText("Original Grok text")).toBeTruthy();
 expect(value.enqueues).not.toHaveBeenCalled();expect(value.controls).not.toHaveBeenCalled();
});
