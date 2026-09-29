import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, InboxService, IntegrationService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

function fixture(interactions: Resource[] = [], repositories: Resource[] = [], projects: Resource[] = [], paginated = false) {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Retained session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "none" }) });
  const message = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role: "assistant", text: '<script>window.invalid = true</script>', state: "completed" }) });
  const other = create(ResourceSchema, { ...session, id: newRequestId(), documentJson: encode({ name: "Other session", workspace: "general-chat", outcome: "idle", archive: "active", dispatch: "paused", recovery: "none" }) });
  other.sessionId = other.id;
  const enqueues = vi.fn(async () => ({ change: { session } }));
  const controls = vi.fn(async () => ({ change: { session } }));
  const status = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 1 }));
  const githubQuery = vi.fn(async () => ({ schemaVersion: 1, documentJson: encode({}) }));
  const saveConfiguration = vi.fn(async (request: { kind: EntityKind; documentJson: Uint8Array }) => ({ resource: create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: request.documentJson }) }));
  const projectRequests: string[] = [];
  const sessionRequests: { projectId: string; includeArchived: boolean; pageToken: string }[] = [];
  const preferences = create(NotificationPreferencesSchema, { revision: 1n, interactions: true, terminals: false });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(SessionService, { listSessions: (request) => {
      if (!paginated) return { sessions: [session, other] };
      sessionRequests.push({ projectId: request.projectId, includeArchived: request.includeArchived, pageToken: request.pageToken });
      if (request.projectId) return { sessions: [], ...(request.pageToken ? {} : { nextPageToken: "project-session-next" }) };
      return request.pageToken ? { sessions: [] } : { sessions: [session, other], nextPageToken: "global-next" };
    }, listQueue: () => ({ inputs: [] }), enqueueInput: enqueues, controlSession: controls });
    router.service(ResourceService, {
      getSnapshot: (request) => ({ resources: [request.filter?.sessionId === other.id ? other : session], cursor: "snapshot" }),
      listResources: (request) => {
        if (request.filter?.kind === EntityKind.PROJECT && paginated) {
          projectRequests.push(request.filter.pageToken);
          return { resources: projects, ...(request.filter.pageToken ? {} : { nextPageToken: "project-next" }) };
        }
        return { resources: request.filter?.kind === EntityKind.MESSAGE ? [message] : request.filter?.kind === EntityKind.INTERACTION ? interactions : request.filter?.kind === EntityKind.REPOSITORY ? repositories : request.filter?.kind === EntityKind.PROJECT ? projects : [] };
      },
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); });
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences }), setNotificationPreferences: async () => ({ preferences }) });
    router.service(IntegrationService, { queryRepositoryIntegration: githubQuery });
    router.service(ConfigurationService, { saveConfiguration });
  });
  return { transport, session, message, enqueues, controls, status, githubQuery, saveConfiguration, projectRequests, sessionRequests };
}

it("opens the existing New Project form from the plus button, retains its draft, and restores opener focus", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const opener = await screen.findByRole("button", { name: "New project" });
  expect(opener.textContent).toBe("");
  expect(opener.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  fireEvent.click(opener);
  expect(await screen.findByRole("heading", { name: "New Project" })).toBeTruthy();
  const name = screen.getByRole("textbox", { name: "Name" });
  await waitFor(() => expect(window.document.activeElement).toBe(name));
  fireEvent.change(name, { target: { value: "Retained project" } });
  expect(value.saveConfiguration).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  expect(window.document.activeElement).toBe(opener);
  fireEvent.click(opener);
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  expect((name as HTMLInputElement).value).toBe("Retained project");
  expect(screen.getAllByRole("heading", { name: "New Project" })).toHaveLength(1);
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

it("defers a New Project entry behind a retained parent editor", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Provider" }));
  const providerName = screen.getByRole("textbox", { name: "Name" });
  fireEvent.change(providerName, { target: { value: "Retained provider draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "New project" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(providerName);
  expect((providerName as HTMLInputElement).value).toBe("Retained provider draft");
  expect(screen.getByRole("button", { name: "Save Provider" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel edit" }));
  expect(await screen.findByRole("heading", { name: "New Project" })).toBeTruthy();
  await waitFor(() => expect(window.document.activeElement).toBe(screen.getByRole("textbox", { name: "Name" })));
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

it("resumes an uncertain New Project save with the same editor and immutable request", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository" }) });
  const value = fixture([], [repository]);
  value.saveConfiguration.mockRejectedValueOnce(new ConnectError("The save response was lost.", Code.Unavailable));
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const opener = await screen.findByRole("button", { name: "New project" });
  fireEvent.click(opener);
  const name = await screen.findByRole("textbox", { name: "Name" });
  fireEvent.change(name, { target: { value: "Sidebar project" } });
  fireEvent.change(await screen.findByRole("combobox", { name: "Add Repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Add selected" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  const retry = await screen.findByRole("button", { name: "Retry the same configuration" });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(opener);
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  expect((name as HTMLInputElement).value).toBe("Sidebar project");
  expect(screen.getByRole("button", { name: "Retry the same configuration" })).toBe(retry);
  fireEvent.click(retry);
  await waitFor(() => expect(value.saveConfiguration).toHaveBeenCalledTimes(2));
  expect(value.saveConfiguration.mock.calls[0][0]).toEqual(value.saveConfiguration.mock.calls[1][0]);
  expect(await screen.findByRole("button", { name: "New Project" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Projects" }).getAttribute("aria-pressed")).toBe("true");
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();
});

it("invalidates the loaded sidebar pages after saving without resetting their cursors or archive filter", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository" }) });
  const project = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Existing project" }) });
  const value = fixture([], [repository], [project], true);
  render(<App transport={value.transport} />);
  await screen.findByRole("button", { name: `Existing project. Project ID: ${project.id}` });
  fireEvent.click(screen.getByRole("button", { name: "Next project page" }));
  await waitFor(() => expect(value.projectRequests).toContain("project-next"));
  fireEvent.click(screen.getByRole("button", { name: "Next session page" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.pageToken === "global-next")).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: `Existing project. Project ID: ${project.id}` }));
  fireEvent.click(await screen.findByRole("button", { name: "Next page of Existing project sessions" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === project.id && request.pageToken === "project-session-next")).toBe(true));
  fireEvent.click(screen.getByRole("checkbox", { name: "Include archived" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.includeArchived && request.pageToken === "")).toBe(true));
  fireEvent.click(await screen.findByRole("button", { name: "Next session page" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.includeArchived && request.pageToken === "global-next")).toBe(true));
  fireEvent.click(await screen.findByRole("button", { name: "Next page of Existing project sessions" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.includeArchived && request.pageToken === "global-next")).toBe(true));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === project.id && request.includeArchived && request.pageToken === "project-session-next")).toBe(true));

  const currentProjectReads = value.projectRequests.filter((page) => page === "project-next").length;
  const currentGlobalReads = value.sessionRequests.filter((request) => request.projectId === "" && request.includeArchived && request.pageToken === "global-next").length;
  const currentProjectSessionReads = value.sessionRequests.filter((request) => request.projectId === project.id && request.includeArchived && request.pageToken === "project-session-next").length;
  const sessionRequestsBeforeSave = value.sessionRequests.length;
  fireEvent.click(screen.getByRole("button", { name: "New project" }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "New bounded project" } });
  fireEvent.change(await screen.findByRole("combobox", { name: "Add Repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Add selected" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  expect(await screen.findByRole("button", { name: "New Project" })).toBeTruthy();
  await waitFor(() => expect(value.projectRequests.filter((page) => page === "project-next")).toHaveLength(currentProjectReads + 1));
  await waitFor(() => expect(value.sessionRequests.filter((request) => request.projectId === "" && request.includeArchived && request.pageToken === "global-next")).toHaveLength(currentGlobalReads + 1));
  await waitFor(() => expect(value.sessionRequests.filter((request) => request.projectId === project.id && request.includeArchived && request.pageToken === "project-session-next")).toHaveLength(currentProjectSessionReads + 1));
  const refreshed = value.sessionRequests.slice(sessionRequestsBeforeSave);
  expect(refreshed).toContainEqual({ projectId: "", includeArchived: true, pageToken: "global-next" });
  expect(refreshed).toContainEqual({ projectId: project.id, includeArchived: true, pageToken: "project-session-next" });
});

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

it.each(["Files", "Diff"])("opens and closes workspace %s without replacing or sending the composer draft", async (panel) => {
  const f = fixture(); render(<App transport={f.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep while browsing files" } });
  const files = screen.getByRole("button", { name: panel });
  fireEvent.click(files);
  expect(await screen.findByRole("complementary", { name: panel === "Files" ? "Session files" : "Session Git diff" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  fireEvent.click(screen.getByRole("button", { name: panel === "Files" ? "Close session files" : "Close session diff" }));
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

it.each(["valid", "mixed", "null"])("renders closed Grok user history through session RPC (%s)", async change => {
 const value=fixture(), thread=newRequestId();
 value.message.documentJson=encode({execution_id:newRequestId(),input_id:newRequestId(),native_thread_id:thread,native_turn_id:"526452fa-1956-42dd-b5f4-60e2b23dfe92",native_id:`${thread}-2`,role:"user",text:"Original verified Grok input",state:"complete",first_sequence:5,last_sequence:5,grok_user:change==="null"?null:{source:"closed-first-text",native_event_id:`${thread}-2`,timestamp_ms:"1",prompt_index:"0",model:"Original model",input_digest:"ab".repeat(32)},...(change==="mixed"?{grok_text:{}}:{})});
 render(<App transport={value.transport}/>);
 fireEvent.click(await screen.findByRole("button",{name:/General Chat Retained session/}));
 if(change==="valid") {
  expect(await screen.findByText("Original verified Grok input")).toBeTruthy();
  expect(screen.getByText("Verified from closed native history")).toBeTruthy();
 } else {
  expect(await screen.findByLabelText("Grok user input unavailable")).toBeTruthy();
  expect(screen.queryByText("Original verified Grok input")).toBeNull();
 }
 expect(value.enqueues).not.toHaveBeenCalled(); expect(value.controls).not.toHaveBeenCalled();
});

it("opens the PR shortcut in Repositories without reading GitHub until the repository browser is activated", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository", integration_id: newRequestId(), github_owner: "owner", github_name: "repo" }) });
  const value = fixture([], [repository]);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Pull requests" }));
  await screen.findByRole("button", { name: "Browse GitHub items" });
  expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true");
  expect(value.githubQuery).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Browse GitHub items" }));
  await waitFor(() => expect(value.githubQuery).toHaveBeenCalledTimes(1));
});

it("defers the PR entry while a parent configuration editor draft is open", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Provider" }));
  const name = await screen.findByRole("textbox", { name: "Name" });
  fireEvent.change(name, { target: { value: "Retained provider draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Retained provider draft");
  expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("false");
  fireEvent.click(screen.getByRole("button", { name: "Cancel edit" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();
});

it("defers the PR entry until a nested integration profile draft is canceled", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Integrations" }));
  fireEvent.click(await screen.findByRole("button", { name: "New GitHub profile" }));
  const name = screen.getByRole("textbox", { name: "Profile name" });
  fireEvent.change(name, { target: { value: "Retained GitHub profile draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect((screen.getByRole("textbox", { name: "Profile name" }) as HTMLInputElement).value).toBe("Retained GitHub profile draft");
  expect(screen.getByRole("button", { name: "Integrations" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Cancel edit" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.githubQuery).not.toHaveBeenCalled();
});

it("retains and defers around notification and import drafts until their explicit cancel path", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Questions and approval requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect((screen.getByRole("checkbox", { name: "Questions and approval requests" }) as HTMLInputElement).checked).toBe(false);
  expect(screen.getByRole("button", { name: "Notifications" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Cancel notification edit" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));

  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const importDraft = screen.getByRole("textbox", { name: "Configuration JSON" });
  fireEvent.change(importDraft, { target: { value: "{\"version\":1" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect((screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement).value).toBe("{\"version\":1");
  expect(screen.getByRole("button", { name: "Import / Export" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.change(importDraft, { target: { value: "" } });
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
});
