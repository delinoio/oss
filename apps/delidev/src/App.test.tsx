import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, InboxService, IntegrationService, InteractionService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SearchArchiveState, SearchService, SessionService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

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
  const status = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 1, capabilities: automaticTitles ? [SystemCapability.AUTOMATIC_TITLES_V1] : [] }));
  const githubQuery = vi.fn(async (_request: { repositoryId: string; schemaVersion: number; queryJson: Uint8Array }) => ({ schemaVersion: 1, documentJson: encode({}) }));
  const saveConfiguration = vi.fn(async (request: { kind: EntityKind; documentJson: Uint8Array }) => ({ resource: create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: request.documentJson }) }));
  const projectRequests: string[] = [];
  const sessionRequests: { projectId: string; includeArchived: boolean; pageToken: string }[] = [];
  const preferences = create(NotificationPreferencesSchema, { revision: 1n, interactions: true, terminals: false });
  const searches = vi.fn(async (_request: { query: string; archive: SearchArchiveState; pageToken: string }) => ({ hits: [], ...(_request.pageToken ? {} : { nextPageToken: "search-next" }) }));
  const inboxReads = vi.fn(async (_request: { pageToken: string }) => ({ entries: [], ...(paginated && !_request.pageToken ? { nextPageToken: "inbox-next" } : {}) }));
  const markRead = vi.fn(async () => ({}));
  const respond = vi.fn(async () => ({}));
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
    router.service(InboxService, { listInbox: inboxReads, setInboxReadState: markRead, getNotificationPreferences: () => ({ preferences }), setNotificationPreferences: async () => ({ preferences }) });
    router.service(SearchService, { searchConversations: searches });
    router.service(InteractionService, { respondQuestion: respond });
    router.service(IntegrationService, { queryRepositoryIntegration: githubQuery });
    router.service(ConfigurationService, { saveConfiguration });
  });
  return { transport, session, message, enqueues, controls, creates, status, githubQuery, saveConfiguration, projectRequests, sessionRequests, agent, machine, searches, inboxReads, markRead, respond };
}

afterEach(() => vi.unstubAllGlobals());

function viewport(initialWidth: number) {
  let width = initialWidth;
  const listeners = new Set<() => void>();
  vi.stubGlobal("matchMedia", vi.fn(() => ({ get matches() { return width <= 759; }, addEventListener: (_event: string, listener: () => void) => listeners.add(listener), removeEventListener: (_event: string, listener: () => void) => listeners.delete(listener) })));
  return (nextWidth: number) => { width = nextWidth; for (const listener of listeners) listener(); };
}

function headerAction(name: "Inbox" | "Search") {
  return within(window.document.querySelector<HTMLElement>(".sidebar-header")!).getByRole("button", { name });
}

function expectNoBusinessWrites(value: ReturnType<typeof fixture>) {
  for (const mutation of [value.enqueues, value.controls, value.creates, value.saveConfiguration, value.markRead, value.respond, value.githubQuery]) expect(mutation).not.toHaveBeenCalled();
}

it.each(["Inbox", "Search"] as const)("hands wide header %s focus to committed content before first Search autofocus", async (destination) => {
  viewport(960);
  const value = fixture();
  const view = render(<StrictMode><App transport={value.transport} /></StrictMode>);
  await screen.findByText("Server 0.1.0");
  const button = headerAction(destination);
  button.focus();
  const main = screen.getByRole("main");
  const focus = vi.spyOn(main, "focus");
  fireEvent.click(button);
  expect(focus).toHaveBeenCalledExactlyOnceWith({ preventScroll: true });
  expect(window.document.activeElement).toBe(main);
  expect(window.document.querySelector(".sidebar-header-actions")).toBeNull();
  if (destination === "Search") {
    const query = await screen.findByRole("textbox", { name: "Search conversations" });
    await waitFor(() => expect(window.document.activeElement).toBe(query));
    fireEvent.change(query, { target: { value: "keep this search" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Archive" }), { target: { value: SearchArchiveState.ARCHIVED } });
  }
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(headerAction(destination));
  expect(window.document.activeElement).toBe(main);
  await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });
  if (destination === "Search") {
    expect((screen.getByRole("textbox", { name: "Search conversations" }) as HTMLInputElement).value).toBe("keep this search");
    expect((screen.getByRole("combobox", { name: "Archive" }) as HTMLSelectElement).value).toBe(String(SearchArchiveState.ARCHIVED));
    expect(value.searches).not.toHaveBeenCalled();
  }
  view.rerender(<StrictMode><App transport={value.transport} connectionEpoch={1} /></StrictMode>);
  await waitFor(() => expect(value.status.mock.calls.length).toBeGreaterThan(1));
  expect(window.document.activeElement).toBe(main);
  expectNoBusinessWrites(value);
});

it.each(["Inbox", "Search"] as const)("closes the compact header %s drawer before focusing the current opener", async (destination) => {
  viewport(759);
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  const opener = screen.getByRole("button", { name: "Open session navigation" });
  opener.focus();
  fireEvent.click(opener);
  const drawer = screen.getByRole("dialog", { name: "DeliDev navigation" });
  const button = headerAction(destination);
  button.focus();
  fireEvent.click(button);
  expect(drawer.hasAttribute("open")).toBe(false);
  expect(screen.getByRole("button", { name: `Open ${destination.toLowerCase()} filters` })).toBe(opener);
  expect(opener.getAttribute("aria-expanded")).toBe("false");
  expect(window.document.activeElement).toBe(opener);
  await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });
  expect(window.document.activeElement).toBe(opener);
  fireEvent.click(opener);
  if (destination === "Search") await waitFor(() => expect(window.document.activeElement).toBe(screen.getByRole("textbox", { name: "Search conversations" })));
  fireEvent(drawer, new Event("cancel", { bubbles: true, cancelable: true }));
  expect(drawer.hasAttribute("open")).toBe(false);
  // Actual modal inertness, keyboard trapping and browser opener restoration
  // are verified separately; jsdom only models the dialog lifecycle.
  expectNoBusinessWrites(value);
});

it.each(["replacement", "settings", "resize"])("discards or consumes header focus intent in the same commit as %s", async (change) => {
  const resize = viewport(change === "resize" ? 759 : 960);
  const value = fixture();
  render(<App transport={value.transport} />);
  const main = screen.getByRole("main");
  const focus = vi.spyOn(main, "focus");
  if (change === "resize") fireEvent.click(screen.getByRole("button", { name: "Open session navigation" }));
  const inbox = headerAction("Inbox");
  const replacement = screen.getByRole("button", { name: change === "settings" ? "Settings" : "Usage" });
  act(() => {
    fireEvent.click(inbox);
    if (change === "resize") resize(760);
    else fireEvent.click(replacement);
  });
  if (change === "resize") {
    expect(focus).toHaveBeenCalledExactlyOnceWith({ preventScroll: true });
    await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });
    expect(window.document.activeElement).toBe(main);
  } else {
    expect(focus).not.toHaveBeenCalled();
    if (change === "settings") {
      await screen.findByRole("button", { name: "Close Settings" });
      fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
      expect(focus).not.toHaveBeenCalled();
    }
  }
  expectNoBusinessWrites(value);
});

it("retains session, sidebar pages and scroll plus applied Search and Inbox filters through home navigation", async () => {
  viewport(960);
  const project = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Navigation fixture" }) });
  const value = fixture([], [], [project], true);
  const view = render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep my unsent input" } });
  fireEvent.click(screen.getByRole("button", { name: "Next project page" }));
  await waitFor(() => expect(value.projectRequests).toContain("project-next"));
  const projectGroup = await screen.findByRole("button", { name: `Navigation fixture. Project ID: ${project.id}` });
  fireEvent.click(projectGroup);
  fireEvent.click(await screen.findByRole("button", { name: "Next page of Navigation fixture sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "Next session page" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.pageToken === "global-next")).toBe(true));
  const list = window.document.querySelector<HTMLElement>(".sidebar-list")!;
  list.scrollTop = 120;
  fireEvent.click(headerAction("Search"));
  const query = await screen.findByRole("textbox", { name: "Search conversations" });
  fireEvent.change(query, { target: { value: "keep this search" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Archive" }), { target: { value: SearchArchiveState.ARCHIVED } });
  fireEvent.click(within(query.closest("form")!).getByRole("button", { name: "Search" }));
  await waitFor(() => expect(value.searches).toHaveBeenCalledTimes(1));
  await waitFor(() => expect((within(screen.getByRole("main")).getByRole("button", { name: "Next page" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(within(screen.getByRole("main")).getByRole("button", { name: "Next page" }));
  await waitFor(() => expect(value.searches.mock.calls.at(-1)?.[0].pageToken).toBe("search-next"));
  fireEvent.change(query, { target: { value: "unapplied search draft" } });
  list.scrollTop = 80;
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(list.scrollTop).toBe(120);
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  expect(screen.getByRole("button", { name: `Navigation fixture. Project ID: ${project.id}` }).getAttribute("aria-expanded")).toBe("true");
  // Re-entering an expired query scope legitimately refreshes its retained
  // page and temporarily disables paging; assert after that read commits.
  await waitFor(() => expect((screen.getByRole("button", { name: "First project page" }) as HTMLButtonElement).disabled).toBe(false));
  await waitFor(() => expect((screen.getByRole("button", { name: "First session page" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(headerAction("Inbox"));
  await screen.findByText("No retained requests or execution results.");
  fireEvent.click(screen.getByRole("button", { name: "Unread" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await waitFor(() => expect((within(screen.getByRole("main")).getByRole("button", { name: "Next page" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(within(screen.getByRole("main")).getByRole("button", { name: "Next page" }));
  await waitFor(() => expect(value.inboxReads.mock.calls.at(-1)?.[0].pageToken).toBe("inbox-next"));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(headerAction("Search"));
  expect(list.scrollTop).toBe(80);
  expect((screen.getByRole("textbox", { name: "Search conversations" }) as HTMLInputElement).value).toBe("unapplied search draft");
  await waitFor(() => expect((within(screen.getByRole("main")).getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false));
  expect(value.searches.mock.calls.at(-1)?.[0]).toMatchObject({ query: "keep this search", archive: SearchArchiveState.ARCHIVED, pageToken: "search-next" });
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(headerAction("Inbox"));
  expect(screen.getByRole("button", { name: "Unread" }).getAttribute("aria-pressed")).toBe("true");
  await waitFor(() => expect((within(screen.getByRole("main")).getByRole("button", { name: "First page" }) as HTMLButtonElement).disabled).toBe(false));
  view.rerender(<App transport={value.transport} connectionEpoch={1} />);
  expectNoBusinessWrites(value);
}, 15_000);

it("keeps the NewSession creation draft while header destinations are visited", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  const message = await screen.findByRole("textbox", { name: "First message" });
  fireEvent.change(message, { target: { value: "Keep my creation draft" } });
  fireEvent.click(headerAction("Search"));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(headerAction("Inbox"));
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expect(screen.getByRole("textbox", { name: "First message" })).toBe(message);
  expect((message as HTMLTextAreaElement).value).toBe("Keep my creation draft");
  expectNoBusinessWrites(value);
});

it("creates an automatically named session from the first message and explicit Workers", async () => {
  const value = fixture([], [], [], false, true);
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "New session" }));
  const firstMessage = await screen.findByRole("textbox", { name: "First message" });
  const composer = within(firstMessage.closest("form")!);
  expect(window.document.activeElement).toBe(firstMessage);
  fireEvent.change(firstMessage, { target: { value: "Fix the startup crash" } });
  fireEvent.change(composer.getByLabelText("Agent Worker"), { target: { value: value.agent.id } });
  fireEvent.change(composer.getByLabelText("Execution Worker"), { target: { value: value.machine.id } });
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

it("opens a fresh New Project form from the plus button and restores opener focus", async () => {
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
  expect(screen.getByRole("textbox", { name: "Name" })).not.toBe(name);
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(screen.getAllByRole("heading", { name: "New Project" })).toHaveLength(1);
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});

it("defers a targeted entry within an opening and clears it when that opening closes", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Instructions" }));
  fireEvent.click(await screen.findByRole("button", { name: "New Instructions" }));
  const providerName = screen.getByRole("textbox", { name: "Name" });
  fireEvent.change(providerName, { target: { value: "Retained instructions draft" } });
  fireEvent.click(screen.getByRole("button", { name: "New project" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toBe(providerName);
  expect((providerName as HTMLInputElement).value).toBe("Retained instructions draft");
  expect(screen.getByRole("button", { name: "Save Instructions" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect((screen.getByRole("combobox", { name: "Settings category" }) as HTMLSelectElement).value).toBe("subscription-accounts");
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
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(opener);
  expect(screen.getByRole("textbox", { name: "Name" })).not.toBe(name);
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(value.saveConfiguration).toHaveBeenCalledTimes(1);
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
// This full-shell scenario performs several sequential pagination and settings
// interactions. Bound its aggregate CI duration separately from the unchanged
// per-observation deadlines; the default five seconds is not a product SLA.
}, 15_000);

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
  await screen.findByText("No retained requests or execution results.");
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
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
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
  fireEvent.click(screen.getByRole("button", { name: "Integrations" }));
  fireEvent.click(await screen.findByRole("button", { name: "New GitHub profile" }));
  const name = screen.getByRole("textbox", { name: "Profile name" });
  fireEvent.change(name, { target: { value: "Retained GitHub profile draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.queryByRole("textbox", { name: "Profile name" })).toBeNull();
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));
  expect(value.githubQuery).not.toHaveBeenCalled();
});

it("discards notification and import drafts on close without saving", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit notification preferences" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Questions and approval requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.queryByRole("button", { name: "Cancel notification edit" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
  expect((await screen.findByRole("checkbox", { name: "Questions and approval requests" }) as HTMLInputElement).checked).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true"));

  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const importDraft = screen.getByRole("textbox", { name: "Configuration JSON" });
  fireEvent.change(importDraft, { target: { value: "{\"version\":1" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  fireEvent.click(screen.getByRole("button", { name: "Repository settings" }));
  expect(screen.getByRole("button", { name: "Repositories" }).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  expect((screen.getByRole("textbox", { name: "Configuration JSON" }) as HTMLTextAreaElement).value).toBe("");
  expect(value.saveConfiguration).not.toHaveBeenCalled();
});
