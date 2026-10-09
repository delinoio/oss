import { SidebarProvider, memorySidebarBridge } from "./sidebar-preference";
import { sessionInputReceipt } from "./test-session-input";
import { chooseScrollOption, scrollChoiceValue, waitScrollChoices } from "./test-scroll-picker";
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

it("screen shortcuts reuse guarded forms and preserve drafts across keyboard navigation", async () => {
  const value = fixture([], [], [], false, true);
  render(<App transport={value.transport} />);
  fireEvent.keyDown(document.body, { key: "N", ctrlKey: true, shiftKey: true });
  const firstMessage = await screen.findByRole("textbox", { name: "First message" });
  fireEvent.change(firstMessage, { target: { value: "Keyboard-created session" } });
  fireEvent.keyDown(firstMessage, { key: "Enter", ctrlKey: true }); expect(value.creates).not.toHaveBeenCalled();
  await waitScrollChoices(screen.getByRole("combobox", { name: "Agent Worker" }));
  const form = within(firstMessage.closest("form")!);
  await chooseScrollOption(form.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(form.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.keyDown(firstMessage, { key: "Enter", ctrlKey: true, isComposing: true }); expect(value.creates).not.toHaveBeenCalled();
  fireEvent.keyDown(firstMessage, { key: "Enter", ctrlKey: true, repeat: true }); expect(value.creates).not.toHaveBeenCalled();
  fireEvent.keyDown(firstMessage, { key: "Enter", ctrlKey: true }); await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(1));
  const message = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.keyDown(message, { key: "?" }); expect(screen.queryByRole("dialog", { name: "Keyboard shortcuts" })).toBeNull();
  fireEvent.keyDown(message, { key: "Enter", ctrlKey: true }); expect(value.enqueues).not.toHaveBeenCalled();
  fireEvent.change(message, { target: { value: "Retained keyboard draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  const search = await screen.findByRole("textbox", { name: "Search conversations" });
  await waitFor(() => expect(document.activeElement).toBe(search));
  fireEvent.change(search, { target: { value: "original query" } });
  fireEvent.submit(search.closest("form")!); await waitFor(() => expect(value.searches).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("Retained keyboard draft");
  fireEvent.keyDown(document.body, { key: "i", ctrlKey: true }); expect(document.activeElement).toBe(message);
  fireEvent.keyDown(document.body, { key: "?" });
  expect(screen.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeTruthy();
  fireEvent.keyDown(message, { key: "Enter", ctrlKey: true }); expect(value.enqueues).not.toHaveBeenCalled();
  fireEvent.keyDown(screen.getByRole("button", { name: "Close keyboard shortcuts" }), { key: "Escape" });
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve; });
  value.enqueues.mockImplementationOnce(async request => { await gate; return sessionInputReceipt(value.session,request); });
  fireEvent.keyDown(message, { key: "Enter", ctrlKey: true }); await waitFor(() => expect(value.enqueues).toHaveBeenCalledTimes(1));
  fireEvent.keyDown(message, { key: "Enter", ctrlKey: true }); expect(value.enqueues).toHaveBeenCalledTimes(1);
  await act(async () => { release(); await gate; });
}, fullShellTimeoutMs);

it("Search input shortcut opens the compact drawer and refocuses retained drafts", async () => {
  viewport(true);
  render(<App transport={fixture().transport} />);
  fireEvent.click(screen.getByRole("button", { name: "Open session navigation" }));
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  fireEvent.keyDown(document.body, { key: "i", ctrlKey: true });
  const query = await screen.findByRole("textbox", { name: "Search conversations" });
  await waitFor(() => expect(document.activeElement).toBe(query));
  fireEvent.change(query, { target: { value: "Retained compact query" } });
  const drawer = document.querySelector<HTMLDialogElement>(".sidebar-pane-dialog")!;
  expect(drawer.open).toBe(true);
  fireEvent.keyDown(query, { key: "?" }); expect(screen.queryByRole("dialog", { name: "Keyboard shortcuts" })).toBeNull();
  fireEvent(drawer, new Event("cancel", { bubbles: true, cancelable: true }));
  await waitFor(() => expect(drawer.open).toBe(false));
  fireEvent.keyDown(document.body, { key: "i", ctrlKey: true });
  await waitFor(() => expect(drawer.open).toBe(true)); expect(document.activeElement).toBe(query);
  fireEvent(drawer, new Event("cancel", { bubbles: true, cancelable: true }));
  await waitFor(() => expect(drawer.open).toBe(false));
  fireEvent.keyDown(document.body, { key: "i", ctrlKey: true });
  await waitFor(() => expect(document.activeElement).toBe(query)); expect((query as HTMLInputElement).value).toBe("Retained compact query");
});

it.each(["MacIntel", "Win32", "Linux x86_64"])("opens and dismisses the fixed palette on %s without Search navigation, writes or draft loss", async platform => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
  // jsdom has no native layout observer; host-browser fixtures verify geometry.
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const value = fixture();
  render(<App transport={value.transport} />);
  await screen.findByRole("button", { name: "New session" });
  const chord = { key: "k", metaKey: platform === "MacIntel", ctrlKey: platform !== "MacIntel" };
  const main = document.querySelector<HTMLElement>("#main")!;
  const drawer = document.querySelector<HTMLDialogElement>(".sidebar-pane-dialog")!;
  const drawerOpen = drawer.open;
  main.focus();
  expect(fireEvent.keyDown(main, chord)).toBe(false);
  const menu = screen.getByRole("dialog", { name: "Command menu" });
  expect(document.activeElement).toBe(within(menu).getByRole("combobox"));
  fireEvent.keyDown(document.activeElement!, chord);
  expect(screen.queryByRole("dialog", { name: "Command menu" })).toBeNull();
  expect(document.activeElement).toBe(main);
  expect(screen.queryByRole("textbox", { name: "Search conversations" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  const input = await screen.findByRole("textbox", { name: "First message" });
  fireEvent.change(input, { target: { value: "Retained draft" } });
  input.focus();
  expect(fireEvent.keyDown(input, chord)).toBe(false);
  expect(document.activeElement).toBe(within(screen.getByRole("dialog", { name: "Command menu" })).getByRole("combobox"));
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(document.activeElement).toBe(input);
  expect((input as HTMLTextAreaElement).value).toBe("Retained draft");
  expect(screen.queryByRole("textbox", { name: "Search conversations" })).toBeNull();
  expect(drawer.open).toBe(drawerOpen);
  expect(value.searches).not.toHaveBeenCalled();
  expect(value.creates).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "Search" }).hasAttribute("aria-keyshortcuts")).toBe(false);
});

it.each(["en", "ko"])("shows fixed command menu guidance while omitting global Search guidance in %s help", async locale => {
  await act(() => i18n.changeLanguage(locale));
  render(<App transport={fixture().transport} />);
  const newSession = locale === "en" ? "New session" : "새 세션";
  const search = locale === "en" ? "Search" : "검색";
  await screen.findByRole("button", { name: newSession });
  for (const navigate of [undefined, newSession, search]) {
    if (navigate) fireEvent.click(screen.getByRole("button", { name: navigate }));
    fireEvent.keyDown(document.body, { key: "?" });
    const dialog = document.querySelector<HTMLDialogElement>(".shortcut-help")!;
    expect(dialog.open).toBe(true);
    expect(dialog.textContent).not.toContain(locale === "en" ? "Open search" : "검색 열기");
    expect([...dialog.querySelectorAll("kbd")].map(node => node.textContent)).toContain("K");
    expect(dialog.textContent).toContain(locale === "en" ? "Command menu" : "명령 메뉴");
    fireEvent.keyDown(dialog, { key: "Escape" });
    fireEvent.keyUp(document, { key: "?" });
  }
});

it("help shortcuts reuse existing navigation and screen focus without sending background input", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const message = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(message, { target: { value: "Retained help draft" } });
  for (const destination of ["session", "new-session", "search"]) {
    if (destination === "new-session") fireEvent.click(screen.getByRole("button", { name: "New session" }));
    if (destination === "search") fireEvent.click(screen.getByRole("button", { name: "Search" }));
    const input = screen.getByRole("textbox", { name: destination === "session" ? "Message" : destination === "new-session" ? "First message" : "Search conversations" });
    const opener = screen.getByRole("button", { name: "Keyboard shortcuts" }); opener.focus(); fireEvent.click(opener);
    const close = screen.getByRole("button", { name: "Close keyboard shortcuts" });
    for (const options of [{}, { ctrlKey: true }, { shiftKey: true }]) fireEvent.keyDown(close, { key: "Enter", ...options });
    expect(value.creates).not.toHaveBeenCalled(); expect(value.enqueues).not.toHaveBeenCalled(); expect(value.searches).not.toHaveBeenCalled();
    fireEvent.keyDown(close, { key: "i", ctrlKey: true });
    expect(screen.queryByRole("dialog", { name: "Keyboard shortcuts" })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(input));
  }
  fireEvent.click(screen.getByRole("button", { name: "Keyboard shortcuts" }));
  fireEvent.keyDown(screen.getByRole("button", { name: "Close keyboard shortcuts" }), { key: "n", ctrlKey: true, shiftKey: true });
  expect(screen.queryByRole("dialog", { name: "Keyboard shortcuts" })).toBeNull();
  expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "First message" }));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("Retained help draft");
  expect(value.creates).not.toHaveBeenCalled(); expect(value.enqueues).not.toHaveBeenCalled();
});

function fixture(interactions: Resource[] = [], repositories: Resource[] = [], projects: Resource[] = [], paginated = false, automaticTitles = false, selectorFailure?: Code, emptyAgents = false, agentGate?: Promise<void>) {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Retained session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "none" }) });
  const message = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role: "assistant", text: '<script>window.invalid = true</script>', state: "completed" }) });
  const other = create(ResourceSchema, { ...session, id: newRequestId(), documentJson: encode({ name: "Other session", workspace: "general-chat", outcome: "idle", archive: "active", dispatch: "paused", recovery: "none" }) });
  other.sessionId = other.id;
  const enqueues = vi.fn(async (request: { requestId: string; sessionId: string; documentJson: Uint8Array }) => sessionInputReceipt(session, request));
  const controls = vi.fn(async () => ({ change: { session } }));
  const creates = vi.fn(async (_request: { requestId: string; documentJson: Uint8Array }) => ({ change: { session } }));
  const agent = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Agent One" }) });
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Worker One" }) });
  const status = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 1, capabilities: automaticTitles ? [SystemCapability.AUTOMATIC_TITLES_V1] : [] }));
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
      getResource: (request) => ({ resource: [...projects, ...repositories, agent, machine].find(row => row.kind === request.kind && row.id === request.id) }),
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
  await chooseScrollOption(composer.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(composer.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  expect(composer.queryByRole("button", { name: "Inspect this Runner" })).toBeNull();
  expect(screen.getByRole("heading", { name: "What would you like to work on?" })).toBeTruthy();
  expect(screen.getByText("General Chat · isolated projectless directory on the selected Worker")).toBeTruthy();
  fireEvent.keyDown(firstMessage, { key: "Enter", code: "Enter" });
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(1));
  const request = (value.creates.mock.calls as unknown as [{ documentJson: Uint8Array }][])[0][0];
  const document = JSON.parse(new TextDecoder().decode(request.documentJson));
  expect(document).toMatchObject({ name_mode: "automatic", prompt: "Fix the startup crash", agent_id: value.agent.id, machine_id: value.machine.id, workspace: "general-chat", mode: "execute", source: "MANUAL" });
  expect(document).not.toHaveProperty("name");
});

it("starts General Chat with explicit execution selections and no project or Local authority", async () => {
  const value = fixture([], [], [], false, true);
  const proof = vi.fn();
  render(<App transport={value.transport} readLocalWorker={proof} />);
  await screen.findByRole("button", { name: "General Chat" });
  fireEvent.click(within(document.querySelector(".sidebar-general-chat") as HTMLElement).getByRole("button", { name: "New Chat" }));
  const page = within(screen.getByRole("region", { name: "What would you like to talk about?" }));
  const firstMessage = page.getByRole("textbox", { name: "First message" });
  expect(document.activeElement).toBe(firstMessage);
  expect(page.queryByLabelText("Project")).toBeNull();
  expect(page.getByText("Ask questions or share ideas without a project.")).toBeTruthy();
  expect(scrollChoiceValue(page.getByRole("combobox", { name: "Agent Worker" }))).toBe("");
  expect(scrollChoiceValue(page.getByRole("combobox", { name: "Runs on" }))).toBe("");
  expect((page.getByRole("button", { name: "Start general chat" }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getAllByRole("button", { name: "New Chat" })[0].getAttribute("aria-current")).toBe("page");
  expect(screen.getByRole("button", { name: "New session" }).getAttribute("aria-current")).toBeNull();
  const ids = [...document.querySelectorAll(".new-session-page [id]")].map((element) => element.id);
  expect(new Set(ids).size).toBe(ids.length);
  fireEvent.click(page.getByRole("button", { name: "Options" }));
  expect(page.getByRole("heading", { name: "Optional estimated-cost budget" })).toBeTruthy();
  expect(page.getByRole("checkbox", { name: "Enable estimated-cost budget" })).toBeTruthy();
  expect(page.getByRole("heading", { name: "Optional estimated-cost budget" }).closest("details")).toBeNull();
  expect(page.queryByText("Use separate Worktrees")).toBeNull();
  expect(page.queryByText("Use this computer's Local checkouts")).toBeNull();
  expect(page.queryByText(/private projectless directory/)).toBeNull();
  await waitScrollChoices(page.getByRole("combobox", { name: "Agent Worker" }));
  await chooseScrollOption(page.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(page.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.click(page.getByRole("checkbox", { name: "Plan Mode" }));
  expect(page.queryByRole("button", { name: "Inspect this Runner" })).toBeNull();
  fireEvent.change(firstMessage, { target: { value: "Help me think through an idea" } });
  fireEvent.keyDown(firstMessage, { key: "Enter", shiftKey: true });
  fireEvent.keyDown(firstMessage, { key: "Enter", isComposing: true, keyCode: 229 });
  expect(value.creates).not.toHaveBeenCalled();
  fireEvent.keyDown(firstMessage, { key: "Enter" });
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(1));
  const request = (value.creates.mock.calls as unknown as [{ documentJson: Uint8Array; localWorkerToken: string }][])[0][0];
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({ name_mode: "automatic", prompt: "Help me think through an idea", agent_id: value.agent.id, machine_id: value.machine.id, workspace: "general-chat", mode: "plan", source: "MANUAL" });
  expect(request.localWorkerToken).toBe("");
  expect(proof).not.toHaveBeenCalled();
  expect(await screen.findByRole("heading", { name: "Retained session" })).toBeTruthy();
}, fullShellTimeoutMs);

it.each([false, true])("submits unchecked Execute, checked Plan and unchecked Execute from creation (General Chat %s)", async generalChat => {
  const value = fixture([], [], [], false, true); render(<App transport={value.transport} />);
  for (const [index, expected] of ["execute", "plan", "execute"].entries()) {
    fireEvent.click((await screen.findAllByRole("button", { name: generalChat ? "New Chat" : "New session" }))[0]);
    const page = within(screen.getByRole("region", { name: generalChat ? "What would you like to talk about?" : "What would you like to work on?" }));
    const mode = page.getByRole("checkbox", { name: "Plan Mode" });
    if (!index) expect(mode).toHaveProperty("checked", false);
    else { fireEvent.click(mode); expect(value.creates).toHaveBeenCalledTimes(index); }
    await waitScrollChoices(page.getByRole("combobox", { name: "Agent Worker" }));
    if (!index) { await chooseScrollOption(page.getByRole("combobox", { name: "Agent Worker" }), value.agent.id); await chooseScrollOption(page.getByRole("combobox", { name: "Runs on" }), value.machine.id); }
    fireEvent.change(page.getByLabelText("First message"), { target: { value: `Explicit draft ${index}` } });
    fireEvent.click(page.getByRole("button", { name: generalChat ? "Start general chat" : "Create session" }));
    await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(index + 1));
    const request = (value.creates.mock.calls as unknown as [{ documentJson: Uint8Array }][])[index][0];
    expect(JSON.parse(new TextDecoder().decode(request.documentJson)).mode).toBe(expected);
    await screen.findByRole("heading", { name: "Retained session" });
  }
}, fullShellTimeoutMs);

it("retains separate Local and General Chat drafts through Settings, language and same-identity reconnect", async () => {
  const project = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Draft project" }) });
  const value = fixture([], [], [project], false, true);
  const authority = { endpoint: "http://127.0.0.1:46399", serverId: newRequestId() };
  const device = newRequestId();
  const proof = vi.fn(async () => ({ machineId: value.machine.id, token: "A".repeat(43) }));
  const props = { pairingAuthority: authority, currentDeviceId: device, readLocalWorker: proof };
  const view = render(<App transport={value.transport} {...props} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  const original = within(screen.getByRole("region", { name: "What would you like to work on?" }));
  await waitScrollChoices(original.getByRole("combobox", { name: "Project" }));
  await chooseScrollOption(original.getByRole("combobox", { name: "Project" }), project.id);
  fireEvent.change(original.getByLabelText("First message"), { target: { value: "Keep this Local task" } });
  fireEvent.click(original.getByRole("button", { name: "Options" }));
  fireEvent.click(original.getByRole("radio", { name: "Local" }));
  await waitFor(() => expect((original.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement).disabled).toBe(true));
  fireEvent.click(screen.getAllByRole("button", { name: "New Chat" })[0]);
  const general = within(screen.getByRole("region", { name: "What would you like to talk about?" }));
  const message = general.getByRole("textbox", { name: "First message" });
  fireEvent.change(message, { target: { value: "Keep my conversation idea" } });
  await waitScrollChoices(general.getByRole("combobox", { name: "Agent Worker" }));
  await chooseScrollOption(general.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(general.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.click(general.getByRole("checkbox", { name: "Plan Mode" }));
  fireEvent.click(general.getByRole("button", { name: "Options" }));
  fireEvent.click(general.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  fireEvent.change(general.getByLabelText("Budget currency"), { target: { value: "USD" } });
  fireEvent.change(general.getByLabelText("Estimated-cost threshold"), { target: { value: "2" } });
  fireEvent.click(general.getByRole("button", { name: "Options" }));
  expect(general.queryByRole("checkbox", { name: "Enable estimated-cost budget" })).toBeNull();
  fireEvent.click(general.getByRole("button", { name: "Options" }));
  expect(general.getByRole("checkbox", { name: "Enable estimated-cost budget" })).toHaveProperty("checked", true);
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  view.rerender(<App transport={{ ...value.transport }} {...props} connectionEpoch={1} />);
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getAllByRole("button", { name: "New Chat" })[1]);
  expect(general.getByRole("textbox", { name: "First message" })).toBe(message);
  expect((message as HTMLTextAreaElement).value).toBe("Keep my conversation idea");
  expect(scrollChoiceValue(general.getByRole("combobox", { name: "Agent Worker" }))).toBe(value.agent.id);
  expect(general.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", true);
  expect((general.getByLabelText("Estimated-cost threshold") as HTMLInputElement).value).toBe("2");
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.getByRole("heading", { name: "어떤 이야기를 나누고 싶으신가요?" })).toBeTruthy();
  expect(general.getByRole("checkbox", { name: "계획 모드" })).toHaveProperty("checked", true);
  expect((general.getByRole("textbox", { name: "첫 메시지" }) as HTMLTextAreaElement).value).toBe("Keep my conversation idea");
  await act(async () => { await i18n.changeLanguage("en"); });
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expect(scrollChoiceValue(original.getByRole("combobox", { name: "Project" }))).toBe(project.id);
  expect((original.getByLabelText("First message") as HTMLTextAreaElement).value).toBe("Keep this Local task");
  expect(original.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", false);
  expect((original.getByRole("combobox", { name: "Runs on" }) as HTMLSelectElement).disabled).toBe(true);
  expect(value.creates).not.toHaveBeenCalled();
  view.rerender(<App transport={value.transport} {...props} currentDeviceId={newRequestId()} />);
  fireEvent.click((await screen.findAllByRole("button", { name: "New Chat" }))[0]);
  const fresh = within(screen.getByRole("region", { name: "What would you like to talk about?" }));
  expect((fresh.getByLabelText("First message") as HTMLTextAreaElement).value).toBe("");
  expect(scrollChoiceValue(fresh.getByRole("combobox", { name: "Agent Worker" }))).toBe("");
  expect(fresh.getByRole("button", { name: "Options" }).getAttribute("aria-expanded")).toBe("false");
  expect(fresh.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", false);
}, fullShellTimeoutMs);

it("isolates pending General Chat from an uncertain project request and never steals its activation", async () => {
  const value = fixture([], [], [], false, true);
  let finish!: (result: { change: { session: Resource } }) => void;
  value.creates.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; })).mockRejectedValueOnce(new ConnectError("Lost response", Code.Unavailable));
  render(<App transport={value.transport} />);
  fireEvent.click((await screen.findAllByRole("button", { name: "New Chat" }))[0]);
  const general = within(screen.getByRole("region", { name: "What would you like to talk about?" }));
  await waitScrollChoices(general.getByRole("combobox", { name: "Agent Worker" }));
  await chooseScrollOption(general.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(general.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.click(general.getByRole("button", { name: "Options" }));
  fireEvent.click(general.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  fireEvent.change(general.getByLabelText("Budget currency"), { target: { value: "USD" } });
  fireEvent.change(general.getByLabelText("Estimated-cost threshold"), { target: { value: "2" } });
  fireEvent.click(general.getByRole("checkbox", { name: "Plan Mode" }));
  fireEvent.change(general.getByLabelText("First message"), { target: { value: "Original general conversation" } });
  fireEvent.click(general.getByRole("button", { name: "Start general chat" }));
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(1));
  expect((general.getByLabelText("First message").closest("fieldset") as HTMLFieldSetElement).disabled).toBe(true);
  expect(general.getByRole("checkbox", { name: "Enable estimated-cost budget" }).matches(":disabled")).toBe(true);
  expect(general.getByLabelText("Budget currency").matches(":disabled")).toBe(true);
  expect(general.getByLabelText("Estimated-cost threshold").matches(":disabled")).toBe(true);
  expect(general.getByRole("button", { name: "Options" }).matches(":disabled")).toBe(true);
  expect(general.getByRole("checkbox", { name: "Plan Mode" }).matches(":disabled")).toBe(true);
  expect(general.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", true);
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  const original = within(screen.getByRole("region", { name: "What would you like to work on?" }));
  await chooseScrollOption(original.getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(original.getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.change(original.getByLabelText("First message"), { target: { value: "Independent task request" } });
  fireEvent.click(original.getByRole("button", { name: "Create session" }));
  await original.findByRole("button", { name: "Retry the same session creation" });
  await act(async () => finish({ change: { session: value.session } }));
  expect(screen.getByRole("heading", { name: "What would you like to work on?" })).toBeTruthy();
  expect((original.getByLabelText("First message") as HTMLTextAreaElement).value).toBe("Independent task request");
  fireEvent.click(original.getByRole("button", { name: "Retry the same session creation" }));
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(3));
  const requests = value.creates.mock.calls as unknown as [unknown][];
  expect(requests[1][0]).toEqual(requests[2][0]);
  expect(requests[0][0]).not.toEqual(requests[1][0]);
  fireEvent.click((await screen.findAllByRole("button", { name: "New Chat" }))[0]);
  expect((general.getByLabelText("First message") as HTMLTextAreaElement).value).toBe("");
  expect(scrollChoiceValue(general.getByRole("combobox", { name: "Agent Worker" }))).toBe(value.agent.id);
  expect(general.getByRole("button", { name: "Open conversation" })).toBeTruthy();
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

function shortcutProject(name: string) {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name, repositories: [], agents: { configured: false, ids: [] } }) });
}

it.each([false, true])("selects a project once, retains the draft, and focuses the composer (compact %s)", async compact => {
  viewport(compact);
  const first = shortcutProject("First project"), second = shortcutProject("Second project");
  const value = fixture([], [], [first, second], false, true);
  const currentDeviceId = newRequestId(), pairingAuthority = { endpoint: "http://127.0.0.1:46310", serverId: newRequestId() };
  const rendered = render(<StrictMode><App transport={value.transport} currentDeviceId={currentDeviceId} pairingAuthority={pairingAuthority} /></StrictMode>);
  const showHome = async () => {
    if (compact) fireEvent.click(screen.getByRole("button", { name: "Open session navigation" }));
    await screen.findByRole("button", { name: `New session in First project. Project ID: ${first.id}` });
  };
  await showHome();
  const fold = screen.getByRole("button", { name: `First project. Project ID: ${first.id}` });
  fireEvent.click(screen.getByRole("button", { name: `New session in First project. Project ID: ${first.id}` }));
  const prompt = screen.getByRole("textbox", { name: "First message" });
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(first.id);
  expect(fold.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(prompt);
  if (compact) expect(screen.queryByRole("dialog", { name: "DeliDev navigation" })).toBeNull();
  fireEvent.change(prompt, { target: { value: "Retain this task" } });
  await waitScrollChoices(screen.getByRole("combobox", { name: "Agent Worker" }));
  await chooseScrollOption(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.click(within(document.querySelector(".new-session-page")!).getByRole("checkbox", { name: "Plan Mode" }));
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByText("Optional estimated-cost budget"));
  fireEvent.click(screen.getByRole("checkbox", { name: "Enable estimated-cost budget" }));
  fireEvent.change(within(document.querySelector(".new-session-page")!).getByLabelText("Budget currency"), { target: { value: "USD" } });
  fireEvent.change(within(document.querySelector(".new-session-page")!).getByLabelText("Estimated-cost threshold"), { target: { value: "1.25" } });
  await showHome();
  fireEvent.click(screen.getByRole("button", { name: `New session in First project. Project ID: ${first.id}` }));
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Agent Worker" }))).toBe(value.agent.id);
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Runs on" }))).toBe(value.machine.id);
  await showHome();
  fireEvent.click(screen.getByRole("button", { name: `New session in Second project. Project ID: ${second.id}` }));
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(second.id);
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Agent Worker" }))).toBe("");
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Runs on" }))).toBe("");
  expect(screen.getByRole("radio", { name: "Worktree" })).toHaveProperty("checked", true);
  expect(within(document.querySelector(".new-session-page")!).getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", true);
  expect(within(document.querySelector(".new-session-page")!).getByLabelText("Estimated-cost threshold")).toHaveProperty("value", "1.25");
  expect(screen.getByRole("button", { name: "Options" }).getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByRole("textbox", { name: "First message" })).toBe(prompt);
  expect(prompt).toHaveProperty("value", "Retain this task");
  await chooseScrollOption(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }), first.id);
  await act(() => i18n.changeLanguage("ko"));
  expect(document.querySelector<HTMLElement>(".new-session-content > .resource-choice [role=combobox]")!.dataset.value).toBe(first.id);
  await act(() => i18n.changeLanguage("en"));
  rendered.rerender(<StrictMode><App transport={{ ...value.transport }} currentDeviceId={currentDeviceId} pairingAuthority={pairingAuthority} /></StrictMode>);
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(first.id);
  fireEvent.click(screen.getByRole("button", { name: "Back to sessions" }));
  await showHome();
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(first.id);
  expect(prompt).toHaveProperty("value", "Retain this task");
  expectNoNavigationWrites(value);
}, fullShellTimeoutMs);

it("locks shortcuts for pending and uncertain creates and retries the unchanged request", async () => {
  viewport();
  const first = shortcutProject("First project"), second = shortcutProject("Second project");
  const value = fixture([], [], [first, second], false, true);
  let reject!: (reason: unknown) => void;
  value.creates.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: `New session in First project. Project ID: ${first.id}` }));
  await waitScrollChoices(screen.getByRole("combobox", { name: "Agent Worker" }));
  await chooseScrollOption(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Agent Worker" }), value.agent.id);
  await chooseScrollOption(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Runs on" }), value.machine.id);
  fireEvent.change(within(document.querySelector(".new-session-page")!).getByLabelText("First message"), { target: { value: "Original request" } });
  fireEvent.click(screen.getByRole("button", { name: "Create session" }));
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(1));
  const shortcut = screen.getByRole("button", { name: `New session in Second project. Project ID: ${second.id}` });
  expect(shortcut).toHaveProperty("disabled", true);
  fireEvent.click(shortcut);
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(first.id);
  await act(async () => reject(new ConnectError("ack lost", Code.Unavailable)));
  await screen.findByRole("button", { name: "Retry the same session creation" });
  expect(shortcut).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByRole("button", { name: "Back to sessions" }));
  fireEvent.click(shortcut);
  expect(screen.queryByRole("textbox", { name: "First message" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same session creation" }));
  await waitFor(() => expect(value.creates).toHaveBeenCalledTimes(2));
  expect(value.creates.mock.calls[1][0]).toEqual(value.creates.mock.calls[0][0]);
  await waitFor(() => expect(shortcut).toHaveProperty("disabled", false));
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(first.id);
});

it("locks shortcuts during Local proof and preserves the original project on completion", async () => {
  viewport();
  const first = shortcutProject("First project"), second = shortcutProject("Second project");
  const value = fixture([], [], [first, second], false, true);
  let release!: (proof: { machineId: string; token: string }) => void;
  const readLocalWorker = vi.fn(() => new Promise<{ machineId: string; token: string }>(resolve => { release = resolve; }));
  render(<App transport={value.transport} readLocalWorker={readLocalWorker} />);
  fireEvent.click(await screen.findByRole("button", { name: `New session in First project. Project ID: ${first.id}` }));
  fireEvent.click(screen.getByRole("button", { name: "Options" }));
  fireEvent.click(screen.getByRole("radio", { name: "Local" }));
  const shortcut = screen.getByRole("button", { name: `New session in Second project. Project ID: ${second.id}` });
  expect(shortcut).toHaveProperty("disabled", true);
  fireEvent.click(shortcut);
  await act(async () => release({ machineId: value.machine.id, token: "A".repeat(43) }));
  expect(shortcut).toHaveProperty("disabled", false);
  expect(scrollChoiceValue(within(document.querySelector(".new-session-page")!).getByRole("combobox", { name: "Project" }))).toBe(first.id);
  expect(screen.getByRole("radio", { name: "Local" })).toHaveProperty("checked", true);
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
  await waitScrollChoices(screen.getByRole("combobox", { name: "Agent Worker" }));
});

it.each(["New project", "Create a project"])("opens a fresh New Project dialog from %s without visiting Settings", async (entry) => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const opener = await screen.findByRole("button", { name: entry });
  const welcome = screen.getByRole("heading", { name: "Your sessions, in one place" });
  if (entry === "New project") {
    expect(opener.textContent).toBe("");
    expect(opener.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  }
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "New Project" });
  expect(dialog.getAttribute("data-size")).toBe("form");
  expect(screen.getByRole("heading", { name: "Your sessions, in one place" })).toBe(welcome);
  expect(screen.queryByRole("region", { name: "Settings content" })).toBeNull();
  expect(screen.queryByRole("navigation", { name: "Settings categories" })).toBeNull();
  expect(screen.getByRole("button", { name: "Sessions" }).getAttribute("aria-current")).toBe("page");
  expect(screen.getByRole("button", { name: "Settings" }).getAttribute("aria-current")).toBeNull();
  expect(window.document.querySelector(".sidebar-action-tooltip")).toBeNull();
  const name = screen.getByRole("searchbox", { name: "Search repository names" });
  await waitFor(() => expect(window.document.activeElement).toBe(name));
  fireEvent.change(name, { target: { value: "Retained project" } });
  const addRepository = within(dialog).getByRole("button", { name: "Add repository" }); fireEvent.click(addRepository);
  const registration = await screen.findByRole("dialog", { name: "Add repository" });
  expect(screen.queryByRole("region", { name: "Settings content" })).toBeNull();
  fireEvent(registration, new Event("cancel", { cancelable: true }));
  await waitFor(() => expect(document.activeElement).toBe(addRepository));
  expect((name as HTMLInputElement).value).toBe("Retained project");
  expect(value.saveConfiguration).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
  expect(value.controls).not.toHaveBeenCalled();

  fireEvent(dialog, new Event("cancel", { cancelable: true }));
  await waitFor(() => expect(window.document.activeElement).toBe(opener));
  fireEvent.click(opener);
  expect(screen.getByRole("searchbox", { name: "Search repository names" })).not.toBe(name);
  expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe("");
  expect(screen.getAllByRole("heading", { name: "New Project" })).toHaveLength(1);
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

function projectRepository() {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Creation repository" }) });
}

async function fillProject(repository: Resource, name = "Direct project") {
  fireEvent.click(await screen.findByRole("checkbox", { name: "Creation repository" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  const projectName = await screen.findByRole("textbox", { name: "Project name" });
  fireEvent.change(projectName, { target: { value: name } });
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  return projectName;
}

async function chooseProjectRepository() {
  fireEvent.click(await screen.findByRole("checkbox", { name: "Creation repository" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
}

it.each([false, true])("preserves the mounted conversation or New session draft while creating a project (new session %s)", async newSession => {
  const repository = projectRepository(), value = fixture([], [repository], [], false, true);
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: newSession ? "New session" : /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: newSession ? "First message" : "Message" });
  fireEvent.change(composer, { target: { value: "Keep this unsent draft" } });
  const opener = screen.getByRole("button", { name: "New project" });
  fireEvent.click(opener);
  await fillProject(repository);
  expect(screen.getByRole("textbox", { name: newSession ? "First message" : "Message" })).toBe(composer);
  fireEvent.click(screen.getByRole("button", { name: "Close New Project" }));
  await waitFor(() => expect(document.activeElement).toBe(opener));
  expect((composer as HTMLTextAreaElement).value).toBe("Keep this unsent draft");
  fireEvent.click(opener);
  await fillProject(repository);
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "New Project" })).toBeNull());
  expect(screen.getByRole("textbox", { name: newSession ? "First message" : "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep this unsent draft");
  expect(value.saveConfiguration).toHaveBeenCalledOnce();
  expect(value.creates).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
}, fullShellTimeoutMs);

it("returns successful wide project creation to the persistent Home fallback", async () => {
  const repository = projectRepository(), value = fixture([], [repository]);
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: "Create a project" }));
  await fillProject(repository);
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "New Project" })).toBeNull());
  expect(document.activeElement).toBe(screen.getByRole("main"));
}, fullShellTimeoutMs);

it.each([false, true])("closes the compact drawer before creation and returns focus to the persistent Home opener (pending %s)", async pending => {
  viewport(true);
  const repository = projectRepository(), value = fixture([], [repository]);
  if (pending) value.saveConfiguration.mockImplementationOnce(() => new Promise(() => undefined));
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const opener = screen.getByRole("button", { name: "Open session navigation" });
  fireEvent.click(opener);
  const drawer = screen.getByRole("dialog", { name: "DeliDev navigation" });
  fireEvent.click(await within(drawer).findByRole("button", { name: "New project" }));
  const dialog = await screen.findByRole("dialog", { name: "New Project" });
  expect(drawer.hasAttribute("open")).toBe(false);
  expect(document.querySelectorAll("dialog[open]")).toHaveLength(1);
  await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole("searchbox", { name: "Search repository names" })));
  if (pending) {
    await fillProject(repository);
    fireEvent.click(within(dialog).getByRole("button", { name: "Save Project" }));
    await waitFor(() => expect(value.saveConfiguration).toHaveBeenCalledOnce());
  }
  fireEvent.click(within(dialog).getByRole("button", { name: "Close New Project" }));
  await waitFor(() => expect(document.activeElement).toBe(opener));
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByRole("navigation", { name: "Settings categories" })).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
}, fullShellTimeoutMs);

it("disposes the original uncertain project request when either external creation entry reopens it", async () => {
  const repository = projectRepository(), value = fixture([], [repository]);
  value.saveConfiguration.mockRejectedValueOnce(new ConnectError("The original response was lost.", Code.Unavailable));
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: "New project" }));
  const name = await fillProject(repository);
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await screen.findByRole("button", { name: "Retry the same configuration" });
  const original = value.saveConfiguration.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Close New Project" }));
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "New project" })));
  expect(screen.queryByRole("dialog", { name: "New Project" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Create a project" }));
  const reopenedSearch = await screen.findByRole("searchbox", { name: "Search repository names" });
  expect(reopenedSearch).not.toBe(name);
  expect((reopenedSearch as HTMLInputElement).value).toBe("");
  expect(value.saveConfiguration).toHaveBeenCalledOnce();
  expect(value.saveConfiguration.mock.calls[0][0]).toEqual(original);
  expect(screen.queryByRole("region", { name: "Settings content" })).toBeNull();
}, fullShellTimeoutMs);

it.each(["surface", "conversation", "connection"] as const)("disposes a hidden pending creation without letting its late success affect a replacement (%s change)", async departure => {
  const repository = projectRepository(), value = fixture([], [repository], [], false, true);
  let resolve!: (result: { resource: Resource }) => void;
  value.saveConfiguration.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  const authority = { endpoint: "http://127.0.0.1:9090", serverId: newRequestId() }, deviceId = newRequestId();
  const tree = (serverId = authority.serverId) => <StrictMode><App transport={value.transport} pairingAuthority={{ ...authority, serverId }} currentDeviceId={deviceId} /></StrictMode>;
  const view = render(tree());
  fireEvent.click(await screen.findByRole("button", { name: "New project" }));
  await fillProject(repository);
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(value.saveConfiguration).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "Close New Project" }));
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  if (departure === "connection") view.rerender(tree(newRequestId()));
  else if (departure === "conversation") {
    fireEvent.click(screen.getByRole("button", { name: /General Chat Retained session/ }));
    await screen.findByRole("textbox", { name: "Message" });
  }
  else {
    fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
    fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  }
  fireEvent.click(await screen.findByRole("button", { name: "New project" }));
  await chooseProjectRepository();
  const name = await screen.findByRole("textbox", { name: "Project name" });
  fireEvent.change(name, { target: { value: "Replacement draft" } });
  await waitFor(() => expect(document.activeElement).toBe(name));
  await act(async () => resolve({ resource: create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Original accepted project" }) }) }));
  expect(screen.getByRole("textbox", { name: "Project name" })).toBe(name);
  expect((name as HTMLInputElement).value).toBe("Replacement draft");
  expect(document.activeElement).toBe(name);
  expect(value.saveConfiguration).toHaveBeenCalledOnce();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
}, fullShellTimeoutMs);

it("retains the creation draft through same-identity reconnect and preserves a denied save for correction", async () => {
  const repository = projectRepository(), value = fixture([], [repository]), replacement = fixture([], [repository]);
  replacement.saveConfiguration.mockRejectedValueOnce(new ConnectError("Saving this project was denied.", Code.PermissionDenied));
  const authority = { endpoint: "http://127.0.0.1:9090", serverId: newRequestId() }, deviceId = newRequestId();
  const view = render(<App transport={value.transport} pairingAuthority={authority} currentDeviceId={deviceId} />);
  fireEvent.click(await screen.findByRole("button", { name: "New project" }));
  const name = await fillProject(repository);
  view.rerender(<App transport={replacement.transport} pairingAuthority={authority} currentDeviceId={deviceId} connectionEpoch={1} />);
  expect(name.isConnected).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect((name as HTMLInputElement).value).toBe("Direct project");
  expect(value.saveConfiguration).not.toHaveBeenCalled();
  expect(replacement.saveConfiguration).toHaveBeenCalledOnce();
  expect((screen.getByRole("button", { name: "Save Project" }) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Close New Project" }));
  expect(screen.queryByRole("dialog", { name: "New Project" })).toBeNull();
}, fullShellTimeoutMs);

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
  fireEvent.click(await screen.findByRole("checkbox", { name: "Fixture repository" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  const name = await screen.findByRole("textbox", { name: "Project name" });
  fireEvent.change(name, { target: { value: "Sidebar project" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await screen.findByRole("button", { name: "Retry the same configuration" });
  fireEvent.click(screen.getByRole("button", { name: "Close New Project" }));
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(opener);
  expect(screen.getByRole("searchbox", { name: "Search repository names" })).not.toBe(name);
  expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe("");
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
  fireEvent.click(await screen.findByRole("checkbox", { name: "Fixture repository" }));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Project name" }), { target: { value: "New bounded project" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: repository.id } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "New Project" })).toBeNull());
  expect(screen.queryByRole("region", { name: "Settings content" })).toBeNull();
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
  fireEvent.click(screen.getByRole("button", { name: "Open tool" })); fireEvent.click(screen.getByRole("menuitem", { name: "Browser" }));
  expect(screen.getByRole("region", { name: "Session browser" })).toBeTruthy();
  expect(composer.isConnected).toBe(true);
  expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
  expect((composer as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  fireEvent.click(screen.getByRole("button", { name: "Close Browser tab" }));
  expect(window.document.activeElement).toBe(screen.getByRole("tab", { name: "Conversation" }));

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

it("shows expanded execution configuration without changing the unsent session draft or dispatching work", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep the original unsent draft" } });
  expect(screen.getByText("Execution settings").closest("details")?.open).toBe(true);
  expect(screen.getByText(/No accepted execution configuration/)).toBeTruthy();
  expect((composer as HTMLTextAreaElement).value).toBe("Keep the original unsent draft");
  expect(value.controls).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("sends explicit Restore with the original revision and never sends Resume on its behalf", async () => {
  const value = fixture();
  value.session.documentJson = encode({ name: "Retained session", workspace: "general-chat", outcome: "failed", archive: "archived", dispatch: "paused", recovery: "none" });
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.click(screen.getByRole("button", { name: "Session actions" }));
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
  fireEvent.click(screen.getByRole("button", { name: "Open tool" }));
  fireEvent.click(screen.getByRole("menuitem", { name: panel }));
  expect(screen.getByRole("button", { name: "Open tool" }).getAttribute("aria-expanded")).toBe("false");
  expect(await screen.findByRole("complementary", { name: panel === "Files" ? "Session files" : "Session Git diff" })).toBeTruthy();
  expect(composer.isConnected).toBe(true);
  expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: `Close ${panel} tab` }));
  expect(document.activeElement).toBe(screen.getByRole("tab", { name: "Conversation" }));
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

it("opens a dedicated PR workspace and reads GitHub automatically after valid selection", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture repository", integration_id: newRequestId(), github_owner: "owner", github_name: "repo" }) });
  const value = fixture([], [repository]);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Pull requests" }));
  const select = await screen.findByRole("button", { name: `Fixture repository. Repository ID: ${repository.id}` });
  expect(value.githubQuery).not.toHaveBeenCalled();
  fireEvent.click(select);
  expect(screen.queryByRole("button", { name: "Load pull requests" })).toBeNull();
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
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Git Profiles" }));
  // The initial empty read moves the action from the toolbar into setup guidance.
  // Wait for that read so the test clicks the current button, not a detached node.
  await screen.findByRole("heading", { name: "Add your first GitHub profile" });
  fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
  const name = await screen.findByRole("textbox", { name: "Profile name" });
  fireEvent.change(name, { target: { value: "Retained GitHub profile draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.queryByRole("textbox", { name: "Profile name" })).toBeNull();
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.githubQuery).not.toHaveBeenCalled();
});

it("opens a fresh targeted Git Profiles visit after abandoning an unsaved profile", async () => {
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Unconfigured repository" }) });
  const value = fixture([], [repository]); render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" })); fireEvent.click(screen.getByRole("button", { name: "Git Profiles" }));
  await screen.findByRole("heading", { name: "Add your first GitHub profile" }); fireEvent.click(screen.getByRole("button", { name: "New GitHub profile" }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Profile name" }), { target: { value: "Discarded profile draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(await screen.findByRole("button", { name: `Unconfigured repository. Repository ID: ${repository.id}` }));
  fireEvent.click(await screen.findByRole("button", { name: "GitHub profiles" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Git Profiles" }).getAttribute("aria-pressed")).toBe("true"));
  expect(screen.queryByRole("textbox", { name: "Profile name" })).toBeNull(); expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(screen.getByRole("main")); expect(value.githubQuery).not.toHaveBeenCalled(); expect(value.saveConfiguration).not.toHaveBeenCalled();
});

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
  fireEvent.click(within(screen.getByRole("main")).getByRole("button", { name: "Load more Search conversations" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Search conversations" })).toBeNull());
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

it("retains one original Runner inspection draft through same-identity connection readiness changes", async () => {
  const value = fixture();
  value.machine.documentJson = encode({ name: "Worker One", disabled: false, installations: [{ harness: "claude-code", state: "missing", explicit_path: "" }] });
  const device = newRequestId(), authority = { endpoint: "http://127.0.0.1:46310", serverId: newRequestId() };
  const view = (ready: boolean) => <App transport={value.transport} currentDeviceId={device} pairingAuthority={authority} connectionReady={ready} />;
  const mounted = render(view(true));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Runner Devices" }));
  fireEvent.click(await screen.findByRole("button", { name: "Inspect installed harnesses" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit executable paths" }));
  const path = screen.getByLabelText("claude-code executable path");
  fireEvent.change(path, { target: { value: "/chosen/unchanged-claude" } });
  mounted.rerender(view(false));
  expect(screen.getByLabelText("claude-code executable path")).toBe(path);
  expect(path).toHaveProperty("value", "/chosen/unchanged-claude");
  mounted.rerender(view(true));
  expect(screen.getByLabelText("claude-code executable path")).toBe(path);
  expectNoNavigationWrites(value);
}, fullShellTimeoutMs);


it("rechecks the original welcome status failure without creating or controlling a session", async () => {
  const value = fixture();
  value.status.mockRejectedValueOnce(new ConnectError("Fixture status failure", Code.PermissionDenied));
  render(<App transport={value.transport} />);
  const retry = await screen.findByRole("button", { name: "Retry current read" });
  expect(value.status).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await waitFor(() => expect(value.status).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry current read" })).toBeNull());
  expect(value.creates).not.toHaveBeenCalled(); expect(value.controls).not.toHaveBeenCalled();
});

it("wide sidebar collapse retains composer selection and mounted navigation while moving only hidden focus", async () => {
  viewport(); const value = fixture(); render(<SidebarProvider bridge={memorySidebarBridge()}><App transport={value.transport} /></SidebarProvider>);
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  const draft = await screen.findByRole("textbox", { name: "First message" }) as HTMLTextAreaElement;
  fireEvent.change(draft, { target: { value: "original draft" } }); act(() => { draft.focus(); draft.setSelectionRange(2, 7); });
  const pane = document.querySelector<HTMLDialogElement>(".sidebar-pane-dialog")!;
  const initialPane = pane, initialOutlet = pane.querySelector(".sidebar-surface-outlet");
  await waitFor(() => expect(screen.getByRole("button", { name: "Collapse sidebar" }).getAttribute("aria-disabled")).toBe("false"));
  fireEvent.keyDown(draft, { key: "b", ctrlKey: true });
  await screen.findByRole("button", { name: "Expand sidebar" });
  expect(pane.hidden).toBe(true); expect(pane.inert).toBe(true); expect(document.activeElement).toBe(draft);
  expect([draft.selectionStart, draft.selectionEnd, draft.value]).toEqual([2, 7, "original draft"]);
  fireEvent.keyDown(draft, { key: "b", ctrlKey: true }); await screen.findByRole("button", { name: "Collapse sidebar" });
  expect(document.querySelector(".sidebar-pane-dialog")).toBe(initialPane); expect(pane.querySelector(".sidebar-surface-outlet")).toBe(initialOutlet);
  expect(pane.hidden).toBe(false); expect(document.activeElement).toBe(draft);
  const source = pane.querySelector<HTMLButtonElement>("button")!; act(() => source.focus());
  fireEvent.keyDown(source, { key: "b", ctrlKey: true }); const toggle = await screen.findByRole("button", { name: "Expand sidebar" });
  expect(document.activeElement).toBe(toggle); expectNoNavigationWrites(value);
}, fullShellTimeoutMs);

it("compact navigation never changes the retained wide collapse choice or dispatches its shortcut", async () => {
  const resize = viewport(); const value = fixture(); render(<SidebarProvider bridge={memorySidebarBridge()}><App transport={value.transport} /></SidebarProvider>);
  await waitFor(() => expect(screen.getByRole("button", { name: "Collapse sidebar" }).getAttribute("aria-disabled")).toBe("false"));
  fireEvent.click(screen.getByRole("button", { name: "Collapse sidebar" })); await screen.findByRole("button", { name: "Expand sidebar" });
  act(() => resize(true)); fireEvent.keyDown(document.body, { key: "b", ctrlKey: true });
  const opener = document.querySelector<HTMLButtonElement>(".sidebar-context-trigger")!; fireEvent.click(opener);
  expect(document.querySelector<HTMLDialogElement>(".sidebar-pane-dialog")!.open).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Close navigation" }));
  act(() => resize(false)); expect(screen.getByRole("button", { name: "Expand sidebar" })).toBeTruthy();
  expect(document.querySelector<HTMLDialogElement>(".sidebar-pane-dialog")!.hidden).toBe(true); expectNoNavigationWrites(value);
}, fullShellTimeoutMs);
