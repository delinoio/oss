import { i18n } from "./localization";
import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, InboxService, InboxSource, IntegrationService, InteractionService, SearchArchiveState, SearchService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

// Full Settings mounts and sequential navigation share this aggregate fixture
// budget. Per-observation waits and product RPC/native deadlines remain unchanged.
const fullShellTimeoutMs = 60_000;

function fixture(interactions: Resource[] = [], repositories: Resource[] = [], projects: Resource[] = [], paginated = false, automaticTitles = false, selectorFailure?: Code, emptyAgents = false, agentGate?: Promise<void>) {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Retained session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "none" }) });
  const message = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role: "assistant", text: '<script>window.invalid = true</script>', state: "completed" }) });
  const other = create(ResourceSchema, { ...session, id: newRequestId(), documentJson: encode({ name: "Other session", workspace: "general-chat", outcome: "idle", archive: "active", dispatch: "paused", recovery: "none" }) });
  other.sessionId = other.id;
  const enqueues = vi.fn(async () => ({ change: { session } }));
  const controls = vi.fn(async () => ({ change: { session } }));
  const creates = vi.fn(async () => ({ change: { session } }));
  const agent = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Agent One" }) });
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Worker One" }) });
  const status = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 2, capabilities: automaticTitles ? [SystemCapability.AUTOMATIC_TITLES_V1] : [] }));
  const githubQuery = vi.fn(async (_request: { repositoryId: string; schemaVersion: number; queryJson: Uint8Array }) => ({ schemaVersion: 1, documentJson: encode({}) }));
  const saveConfiguration = vi.fn(async (request: { kind: EntityKind; documentJson: Uint8Array }) => ({ resource: create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: request.documentJson }) }));
  const projectRequests: string[] = [];
  const sessionRequests: { projectId: string; includeArchived: boolean; pageToken: string }[] = [];
  const searches = vi.fn((request: { query: string; archive: SearchArchiveState; pageToken: string }) => ({ hits: [], nextPageToken: request.pageToken ? undefined : "search-next" }));
  const inboxReads = vi.fn((_request: unknown) => ({ entries: [] }));
  const readStates = vi.fn(() => ({}));
  const responses = vi.fn(() => ({}));
  const preferences = create(NotificationPreferencesSchema, { revision: 1n, interactions: true, terminals: false });
  const saveNotificationPreferences = vi.fn(async () => ({ preferences }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(SessionService, { listSessions: (request) => {
      if (!paginated) return { sessions: [session, other] };
      sessionRequests.push({ projectId: request.projectId, includeArchived: request.includeArchived, pageToken: request.pageToken });
      if (request.projectId) return { sessions: [], ...(request.pageToken ? {} : { nextPageToken: "project-session-next" }) };
      return request.pageToken ? { sessions: [] } : { sessions: [session, other], nextPageToken: "global-next" };
    }, listQueue: () => ({ inputs: [] }), enqueueInput: enqueues, controlSession: controls, createSession: creates });
    router.service(ResourceService, {
      getSnapshot: (request) => ({ resources: [request.filter?.sessionId === other.id ? other : session], cursor: "snapshot" }),
      listResources: async (request) => {
        if (request.filter?.kind === EntityKind.AGENT && selectorFailure) throw new ConnectError("Selector request failed", selectorFailure);
        if (request.filter?.kind === EntityKind.AGENT && agentGate) await agentGate;
        if (request.filter?.kind === EntityKind.PROJECT && paginated) {
          projectRequests.push(request.filter.pageToken);
          return { resources: projects, ...(request.filter.pageToken ? {} : { nextPageToken: "project-next" }) };
        }
        return { resources: request.filter?.kind === EntityKind.MESSAGE ? [message] : request.filter?.kind === EntityKind.INTERACTION ? interactions : request.filter?.kind === EntityKind.REPOSITORY ? repositories : request.filter?.kind === EntityKind.PROJECT ? projects : request.filter?.kind === EntityKind.AGENT ? emptyAgents ? [] : [agent] : request.filter?.kind === EntityKind.MACHINE ? [machine] : [] };
      },
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); });
      },
    });
    router.service(InboxService, { listInbox: inboxReads, setInboxReadState: readStates, getNotificationPreferences: () => ({ preferences }), setNotificationPreferences: saveNotificationPreferences });
    router.service(SearchService, { searchConversations: searches });
    router.service(InteractionService, { respondQuestion: responses, respondApproval: responses });
    router.service(IntegrationService, { queryRepositoryIntegration: githubQuery });
    router.service(ConfigurationService, { saveConfiguration });
  });
  return { transport, session, message, enqueues, controls, creates, status, githubQuery, saveConfiguration, saveNotificationPreferences, projectRequests, sessionRequests, agent, machine, searches, inboxReads, readStates, responses };
}

it("creates an automatically named session from the first message and explicit Workers", async () => {
  const value = fixture([], [], [], false, true);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  const firstMessage = await screen.findByRole("textbox", { name: "First message" });
  const composer = within(firstMessage.closest("form")!);
  expect(window.document.activeElement).toBe(firstMessage);
  fireEvent.change(firstMessage, { target: { value: "Fix the startup crash" } });
  fireEvent.change(composer.getByLabelText("Agent Worker"), { target: { value: value.agent.id } });
  fireEvent.change(composer.getByLabelText("Runs on"), { target: { value: value.machine.id } });
  expect(screen.getByRole("heading", { name: "What would you like to work on?" })).toBeTruthy();
  expect(screen.getByText("General Chat · isolated projectless directory on the selected Worker")).toBeTruthy();
  fireEvent.keyDown(firstMessage, { key: "Enter", code: "Enter" });
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(1));
  const request = (value.creates.mock.calls as unknown as [{ documentJson: Uint8Array }][])[0][0];
  const document = JSON.parse(new TextDecoder().decode(request.documentJson));
  expect(document).toMatchObject({ name_mode: "automatic", prompt: "Fix the startup crash", agent_id: value.agent.id, machine_id: value.machine.id, workspace: "general-chat", mode: "execute", source: "MANUAL" });
  expect(document).not.toHaveProperty("name");
});

it("keeps first-message drafts when title support is absent and preserves valid UTF-8 at the limit", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  const firstMessage = await screen.findByRole("textbox", { name: "First message" });
  fireEvent.change(firstMessage, { target: { value: "Previous valid draft" } });
  expect(screen.getByText(/Automatic session titles are unavailable/)).toBeTruthy();
  fireEvent.change(firstMessage, { target: { value: "x".repeat(256 << 10) } });
  expect((firstMessage as HTMLTextAreaElement).value).toBe("x".repeat(256 << 10));
  fireEvent.change(firstMessage, { target: { value: "x".repeat((256 << 10) + 1) } });
  expect((firstMessage as HTMLTextAreaElement).value).toBe("x".repeat(256 << 10));
  expect(screen.getByText(/exceeds 256 KiB/)).toBeTruthy();
  fireEvent.keyDown(firstMessage, { key: "Enter", code: "Enter", isComposing: true, keyCode: 229 });
  fireEvent.keyDown(firstMessage, { key: "Enter", code: "Enter", shiftKey: true });
  expect(value.creates).not.toHaveBeenCalled();
});

it.each([
  [Code.PermissionDenied, /The server denied access to these choices/],
  [Code.Unavailable, /The server connection failed while loading these choices/],
])("explains Agent Worker selector failure %s without implying an empty inventory", async (code, message) => {
  const value = fixture([], [], [], false, true, code);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  expect(await screen.findByText(message)).toBeTruthy();
  expect(screen.queryByText("No selectable Agent Worker choices are on this page.")).toBeNull();
});

it("distinguishes an empty current Agent Worker page from a loading selector", async () => {
  const value = fixture([], [], [], false, true, undefined, true);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  expect(await screen.findByText("No selectable Agent Worker choices are on this page.")).toBeTruthy();
});

it("shows selector loading while the current page has not returned", async () => {
  let finish!: () => void;
  const gate = new Promise<void>((resolve) => { finish = resolve; });
  const value = fixture([], [], [], false, true, undefined, false, gate);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  expect(await screen.findByText("Loading Agent Worker choices…")).toBeTruthy();
  finish();
  expect(await screen.findByRole("option", { name: "Agent One" })).toBeTruthy();
});

it("opens a fresh New Project form from the plus button with visible destination focus", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const opener = await screen.findByRole("button", { name: "New project" });
  expect(opener.textContent).toBe("");
  expect(opener.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  fireEvent.click(opener);
  expect(await screen.findByRole("heading", { name: "New Project" })).toBeTruthy();
  expect(window.document.querySelector(".sidebar-action-tooltip")).toBeNull();
  const name = screen.getByRole("textbox", { name: "Name" });
  await waitFor(() => expect(window.document.activeElement).toBe(name));
  fireEvent.change(name, { target: { value: "Retained project" } });
  expect(value.saveConfiguration).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(window.document.activeElement).toBe(screen.getByRole("main"));
  fireEvent.click(opener);
  expect(screen.getByRole("textbox", { name: "Name" })).not.toBe(name);
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(screen.getAllByRole("heading", { name: "New Project" })).toHaveLength(1);
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

it("preserves a protected draft on active Settings reselection and discards it on departure", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Instructions" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Instructions" }));
  const providerName = screen.getByRole("textbox", { name: "Name" });
  fireEvent.change(providerName, { target: { value: "Retained instructions draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(providerName);
  expect((providerName as HTMLInputElement).value).toBe("Retained instructions draft");
  expect(screen.getByRole("button", { name: "Save Instructions" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

it("abandons an uncertain New Project save without replay when reopening", async () => {
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
  await screen.findByRole("button", { name: "Retry the same configuration" });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(opener);
  expect(screen.getByRole("textbox", { name: "Name" })).not.toBe(name);
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(value.saveConfiguration).toHaveBeenCalledTimes(1);
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();
}, fullShellTimeoutMs);

it("invalidates the loaded sidebar pages after saving without resetting their cursors or archive filter", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository" }) });
  const project = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Existing project" }) });
  const value = fixture([], [repository], [project], true);
  render(<App transport={value.transport} />);
  await screen.findByRole("button", { name: `Existing project. Project ID: ${project.id}` });
  reach("projects");
  await waitFor(() => expect(value.projectRequests).toContain("project-next"));
  reach("sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.pageToken === "global-next")).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: `Existing project. Project ID: ${project.id}` }));
  await waitFor(() => expect(window.document.querySelector('[data-continuation="Existing project sessions"]')).toBeTruthy());
  reach("Existing project sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === project.id && request.pageToken === "project-session-next")).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Project and conversation options" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Include archived" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.includeArchived && request.pageToken === "")).toBe(true));
  reach("sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.includeArchived && request.pageToken === "global-next")).toBe(true));
  await waitFor(() => expect(window.document.querySelector('[data-continuation="Existing project sessions"]')).toBeTruthy());
  reach("Existing project sessions");
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
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  await waitFor(() => expect(value.projectRequests.filter((page) => page === "project-next")).toHaveLength(currentProjectReads + 1));
  await waitFor(() => expect(value.sessionRequests.filter((request) => request.projectId === "" && request.includeArchived && request.pageToken === "global-next")).toHaveLength(currentGlobalReads + 1));
  await waitFor(() => expect(value.sessionRequests.filter((request) => request.projectId === project.id && request.includeArchived && request.pageToken === "project-session-next")).toHaveLength(currentProjectSessionReads + 1));
  const refreshed = value.sessionRequests.slice(sessionRequestsBeforeSave);
  expect(refreshed).toContainEqual({ projectId: "", includeArchived: true, pageToken: "global-next" });
  expect(refreshed).toContainEqual({ projectId: project.id, includeArchived: true, pageToken: "project-session-next" });
// This full-shell scenario performs several sequential pagination and settings
// interactions. Bound its aggregate CI duration separately from the unchanged
// per-observation deadlines; the default five seconds is not a product SLA.
}, fullShellTimeoutMs);

it("keeps the draft and session mounted across settings and navigation, and renders native text inertly", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep my unsent input" } });
  fireEvent.click(screen.getByRole("button", { name: "Browser" }));
  expect(screen.getByRole("region", { name: "Session browser" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  fireEvent.click(screen.getByRole("button", { name: "Close browser" }));
  expect(window.document.activeElement).toBe(screen.getByRole("button", { name: "Browser" }));

  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect(screen.queryByRole("dialog", { name: "Settings" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(window.document.activeElement).toBe(screen.getByRole("main"));
  expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  await screen.findByText("No retained requests or execution results.");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  expect(await screen.findByText('<script>window.invalid = true</script>')).toBeTruthy();
  expect(window.document.querySelector("script")).toBeNull();
  expect(value.enqueues).not.toHaveBeenCalled();
}, fullShellTimeoutMs);

it("refreshes reads after recovery without replacing the connection's session draft", async () => {
  const value = fixture();
  value.status.mockRejectedValueOnce(new ConnectError("Server is stopped", Code.Unavailable));
  const view = render(<App transport={value.transport} connectionReady={false} />);
  await screen.findByText("This computer · Disconnected · previous data may be stale");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Retain this across server restart" } });
  view.rerender(<App transport={value.transport} connectionReady connectionEpoch={1} />);
  await screen.findByText("This computer · Connected");
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
  await screen.findByText("This computer · Connected");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep while disconnected" } });
  value.status.mockRejectedValue(new ConnectError("Server disconnected", Code.Unavailable));
  view.rerender(<App transport={value.transport} connectionEpoch={1} />);
  await screen.findByText("This computer · Disconnected · previous data may be stale");
  expect(within(screen.getByLabelText("Application sidebar")).queryByText("This computer · Connected")).toBeNull();
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

it("opens a dedicated PR workspace and reads GitHub only after Load", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository", integration_id: newRequestId(), github_owner: "owner", github_name: "repo" }) });
  const value = fixture([], [repository]);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Pull requests" }));
  fireEvent.click(await screen.findByRole("button", { name: `Fixture repository. Repository ID: ${repository.id}` }));
  expect(screen.getByRole("button", { name: "Load pull requests" })).toBeTruthy();
  expect(value.githubQuery).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Load pull requests" }));
  await waitFor(() => expect(value.githubQuery).toHaveBeenCalledTimes(1));
  const request = JSON.parse(new TextDecoder().decode(value.githubQuery.mock.calls[0][0].queryJson));
  expect(request).toMatchObject({ kind: "pull-request", operation: "list", state: "open", page: 1, page_size: 20 });
});

it("discards an Instructions draft when navigating away and preserves targeted repository entry", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Instructions" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Instructions" }));
  const name = await screen.findByRole("textbox", { name: "Name" });
  fireEvent.change(name, { target: { value: "Retained instructions draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect(await within(screen.getByRole("main")).findByRole("heading", { name: "Pull requests" })).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();
});

it("discards a nested integration profile draft on close before targeted repository entry", async () => {
  const value = fixture();
  value.status.mockResolvedValue({ version: "0.1.0", protocolVersion: 2, capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] });
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Git Profiles" }));
  // The initial empty read moves the action from the toolbar into setup guidance.
  // Wait for that read so the test clicks the current button, not a detached node.
  await screen.findByRole("heading", { name: "Add your first GitHub profile" });
  fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
  const name = await screen.findByLabelText("GitHub personal access token");
  fireEvent.change(name, { target: { value: "fixture-unsent-token" } });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.queryByLabelText("GitHub personal access token")).toBeNull();
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.githubQuery).not.toHaveBeenCalled();
}, fullShellTimeoutMs);

// Each independent draft family owns its complete close/reopen lifecycle check.
// Keep them separate so unrelated navigation does not consume one test deadline.
it("discards a notification draft on close without saving", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Questions and approval requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.queryByRole("button", { name: "Cancel notification edit" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  await screen.findByRole("button", { name: "Edit notification preferences" });
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(within(screen.getByRole("group", { name: "Notify this client about" })).getByText("Enabled")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.saveNotificationPreferences).not.toHaveBeenCalled();
  expect(value.saveConfiguration).not.toHaveBeenCalled();
}, fullShellTimeoutMs);

it("discards an import draft on close before targeted repository entry without saving", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const importDraft = screen.getByRole("textbox", { name: "Configuration JSON" });
  fireEvent.change(importDraft, { target: { value: "{\"version\":1" } });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  expect((screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement).value).toBe("");
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

function reach(label: string, remaining = 96) {
  const root = window.document.querySelector<HTMLDivElement>(".sidebar-list")!;
  Object.defineProperty(root, "clientHeight", { configurable: true, value: 400 });
  const rect = (top: number) => ({ top, bottom: top + 1, left: 0, right: 200, width: 200, height: 1, x: 0, y: top, toJSON: () => ({}) });
  vi.spyOn(root, "getBoundingClientRect").mockReturnValue({ ...rect(0), bottom: 400, height: 400 });
  for (const anchor of root.querySelectorAll<HTMLElement>("[data-continuation]")) {
    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(rect(600));
  }
  const anchor = root.querySelector<HTMLElement>(`[data-continuation="${label}"]`)!;
  vi.mocked(anchor.getBoundingClientRect).mockReturnValueOnce(rect(400 + remaining));
  fireEvent.scroll(root);
}


afterEach(() => vi.unstubAllGlobals());

function viewport(compact = false) {
  const listeners = new Set<() => void>();
  const media = { matches: compact, media: "(max-width: 759px)", addEventListener: (_name: string, listener: () => void) => listeners.add(listener), removeEventListener: (_name: string, listener: () => void) => listeners.delete(listener) };
  vi.stubGlobal("matchMedia", vi.fn(() => media));
  return (matches: boolean) => { media.matches = matches; listeners.forEach((listener) => listener()); };
}

function animationFrames() {
  let id = 0;
  const frames = new Map<number, FrameRequestCallback>();
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { frames.set(++id, callback); return id; });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => { frames.delete(id); });
  return () => act(() => { const current = [...frames.values()]; frames.clear(); current.forEach((callback) => callback(0)); });
}

function headerAction(name: "Inbox" | "Search") {
  return within(window.document.querySelector<HTMLElement>(".sidebar-header")!).getByRole("button", { name });
}

function expectNoNavigationWrites(value: ReturnType<typeof fixture>) {
  for (const mutation of [value.enqueues, value.controls, value.creates, value.readStates, value.responses, value.githubQuery, value.saveConfiguration]) expect(mutation).not.toHaveBeenCalled();
}

it("hands wide header focus to main before first Search autofocus and preserves search/filter pages", async () => {
  viewport();
  const flushFrames = animationFrames();
  const value = fixture();
  const view = render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const inbox = headerAction("Inbox");
  inbox.focus();
  fireEvent.click(inbox);
  expect(document.activeElement).toBe(screen.getByRole("main"));
  expect(window.document.querySelector(".sidebar-header-actions")).toBeNull();
  await screen.findByText("No retained requests or execution results.");
  fireEvent.change(screen.getByRole("combobox", { name: "Source" }), { target: { value: InboxSource.INTERACTION } });
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await screen.findByText("No items match these filters.");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  const search = headerAction("Search");
  search.focus();
  fireEvent.click(search);
  expect(document.activeElement).toBe(screen.getByRole("main"));
  const input = screen.getByRole("textbox", { name: "Search conversations" });
  flushFrames();
  expect(document.activeElement).toBe(input);
  fireEvent.change(input, { target: { value: "keep this search" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Archive" }), { target: { value: SearchArchiveState.ARCHIVED } });
  fireEvent.click(within(input.closest("form")!).getByRole("button", { name: "Search" }));
  await screen.findByText("No retained conversation matches.");
  fireEvent.click(within(screen.getByRole("main")).getByRole("button", { name: "Next page" }));
  await screen.findByText("No further conversations on this page.");
  expect(value.searches.mock.calls.at(-1)?.[0]).toMatchObject({ query: "keep this search", archive: SearchArchiveState.ARCHIVED, pageToken: "search-next" });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(headerAction("Search"));
  expect(document.activeElement).toBe(screen.getByRole("main"));
  flushFrames();
  expect(document.activeElement).toBe(screen.getByRole("main"));
  expect(screen.getByRole("textbox", { name: "Search conversations" })).toBe(input);
  expect((input as HTMLInputElement).value).toBe("keep this search");
  expect((screen.getByRole("combobox", { name: "Archive" }) as HTMLSelectElement).value).toBe(String(SearchArchiveState.ARCHIVED));
  await waitFor(() => expect(value.searches.mock.calls.at(-1)?.[0].pageToken).toBe("search-next"));
  view.rerender(<StrictMode><App transport={value.transport} connectionEpoch={1} /></StrictMode>);
  await waitFor(() => expect(value.searches.mock.calls.filter(([request]) => request.pageToken === "search-next").length).toBeGreaterThan(1));
  flushFrames();
  expect(document.activeElement).toBe(screen.getByRole("main"));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(headerAction("Inbox"));
  expect((screen.getByRole("combobox", { name: "Source" }) as HTMLSelectElement).value).toBe(String(InboxSource.INTERACTION));
  await waitFor(() => expect(value.inboxReads.mock.calls.at(-1)?.[0]).toMatchObject({ source: InboxSource.INTERACTION }));
  expectNoNavigationWrites(value);
});

it.each(["Inbox", "Search"] as const)("closes the compact drawer and focuses the persistent %s opener", async (name) => {
  viewport(true);
  const flushFrames = animationFrames();
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(screen.getByRole("button", { name: "Open session navigation" }));
  const action = headerAction(name);
  action.focus();
  fireEvent.click(action);
  const opener = screen.getByRole("button", { name: `Open ${name.toLowerCase()} filters` });
  expect(document.activeElement).toBe(opener);
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expect(document.querySelector(".sidebar-pane-dialog")?.hasAttribute("open")).toBe(false);
  flushFrames();
  expect(document.activeElement).toBe(opener);
  fireEvent.click(opener);
  expect(document.querySelector(".sidebar-pane-dialog")?.hasAttribute("open")).toBe(true);
  if (name === "Search") {
    flushFrames();
    expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Search conversations" }));
  }
  // jsdom emulates dialog lifecycle only; native focus containment and return
  // require independent rendered evidence in a browser or desktop host.
  fireEvent(document.querySelector(".sidebar-pane-dialog")!, new Event("cancel", { cancelable: true }));
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Close navigation" }));
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expectNoNavigationWrites(value);
});

it.each(["replacement", "settings", "resize"])("consumes or discards header focus intent during %s", async (mode) => {
  const resize = viewport(mode === "resize");
  const flushFrames = animationFrames();
  const value = fixture();
  render(<App transport={value.transport} />);
  if (mode === "resize") fireEvent.click(screen.getByRole("button", { name: "Open session navigation" }));
  act(() => {
    fireEvent.click(headerAction("Inbox"));
    if (mode === "replacement") {
      const replacement = screen.getByRole("button", { name: "Pull requests" });
      replacement.focus();
      fireEvent.click(replacement);
    } else if (mode === "settings") fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    else resize(false);
  });
  flushFrames();
  if (mode === "replacement") expect(document.activeElement).toBe(screen.getByRole("button", { name: "Pull requests" }));
  else if (mode === "settings") {
    expect(document.activeElement?.closest("dialog")?.classList.contains("sidebar-pane-dialog")).not.toBe(true);
    expect(document.activeElement).toBe(screen.getByRole("main"));
    fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
    flushFrames();
    expect(document.activeElement).toBe(screen.getByRole("main"));
  } else expect(document.activeElement).toBe(screen.getByRole("main"));
  expectNoNavigationWrites(value);
});

it("keeps a New session draft and a selected conversation across both header destinations", async () => {
  viewport();
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const message = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(message, { target: { value: "Keep my unsent input" } });
  for (const name of ["Inbox", "Search"] as const) {
    fireEvent.click(headerAction(name));
    fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
    expect(screen.getByRole("textbox", { name: "Message" })).toBe(message);
    expect((message as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  }
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  const firstMessage = screen.getByRole("textbox", { name: "First message" });
  fireEvent.change(firstMessage, { target: { value: "Keep my creation draft" } });
  for (const name of ["Inbox", "Search"] as const) {
    fireEvent.click(headerAction(name));
    fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
    fireEvent.click(screen.getByRole("button", { name: "New session" }));
    expect(screen.getByRole("textbox", { name: "First message" })).toBe(firstMessage);
    expect((firstMessage as HTMLTextAreaElement).value).toBe("Keep my creation draft");
  }
  expectNoNavigationWrites(value);
});


it.each([false, true])("uses shared Settings navigation and preserves a visit through Escape, reselection and reflow (compact %s)", async (compact) => {
  const resize = viewport(compact);
  const value = fixture();
  const view = render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const rail = within(screen.getByRole("navigation", { name: "Primary navigation" }));
  fireEvent.click(rail.getByRole("button", { name: "Settings" }));
  expect(rail.getByRole("button", { name: "Settings" }).getAttribute("aria-current")).toBe("page");
  expect(rail.getByRole("button", { name: "Sessions" }).getAttribute("aria-current")).toBeNull();
  const main = screen.getByRole("main");
  expect(within(main).getByRole("region", { name: "Settings content" })).toBeTruthy();
  expect(screen.queryByRole("dialog", { name: "Settings" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Close Settings" })).toBeNull();
  expect(screen.queryByRole("combobox", { name: "Settings category" })).toBeNull();
  expect(document.activeElement).toBe(compact ? screen.getByRole("button", { name: "Open settings categories" }) : main);
  if (compact) fireEvent.click(screen.getByRole("button", { name: "Open settings categories" }));
  const categories = screen.getByRole("navigation", { name: "Settings categories" });
  expect(categories.closest(".sidebar-surface-outlet")).toBeTruthy();
  expect(categories.closest("main")).toBeNull();
  fireEvent.click(within(categories).getByRole("button", { name: "Instructions" }));
  if (compact) expect(document.querySelector(".sidebar-pane-dialog")?.hasAttribute("open")).toBe(false);
  fireEvent.click(await within(main).findByRole("button", { name: "New Instructions" }));
  const name = screen.getByRole("textbox", { name: "Name" });
  name.focus();
  fireEvent.change(name, { target: { value: "Visit draft" } });
  fireEvent.keyDown(name, { key: "Escape" });
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  expect(rail.getByRole("button", { name: "Usage" }).hasAttribute("disabled")).toBe(false);
  fireEvent.click(rail.getByRole("button", { name: "Settings" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  act(() => resize(!compact));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(name);
  view.rerender(<StrictMode><App transport={value.transport} connectionEpoch={1} /></StrictMode>);
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Visit draft");
  fireEvent.click(rail.getByRole("button", { name: "Usage" }));
  expect(screen.queryByRole("region", { name: "Settings content" })).toBeNull();
  expect(document.activeElement).toBe(!compact ? screen.getByRole("button", { name: "Open usage filters" }) : main);
  fireEvent.click(rail.getByRole("button", { name: "Settings" }));
  expect(within(main).getByRole("heading", { name: "AI Subscription", level: 1 })).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expectNoNavigationWrites(value);
});

it("changing language retains the mounted conversation draft, focus and read identities", async () => {
  const value = fixture([], [], [], true);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const input = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Unsent original 한국어 draft <b>inert</b>" } }); input.focus();
  await screen.findByText('<script>window.invalid = true</script>');
  const sessionRequests = value.sessionRequests.length, statusRequests = value.status.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("textbox", { name: "메시지" })).toBe(input);
  expect(document.activeElement).toBe(input);
  expect(input).toHaveProperty("value", "Unsent original 한국어 draft <b>inert</b>");
  expect(screen.getByText('<script>window.invalid = true</script>')).toBeTruthy();
  expect(value.sessionRequests).toHaveLength(sessionRequests); expect(value.status).toHaveBeenCalledTimes(statusRequests);
  expect(value.enqueues).not.toHaveBeenCalled(); expect(value.controls).not.toHaveBeenCalled(); expect(value.creates).not.toHaveBeenCalled();
}, fullShellTimeoutMs);

it("language changes keep an uncertain conversation operation and never replay its RPC", async () => {
  const value = fixture();
  value.enqueues.mockRejectedValueOnce(new ConnectError("Original response lost", Code.Unavailable));
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const input = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Original queued input" } });
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await screen.findByRole("button", { name: "Retry the same message" });
  const original = value.enqueues.mock.calls[0];
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByRole("textbox", { name: "메시지" })).toBe(input);
  expect(input).toHaveProperty("value", "Original queued input");
  expect(value.enqueues).toHaveBeenCalledTimes(1); expect(value.enqueues.mock.calls[0]).toBe(original);
  expect(value.controls).not.toHaveBeenCalled(); expect(value.creates).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: /같은 메시지/ })).toBeTruthy();
}, fullShellTimeoutMs);
