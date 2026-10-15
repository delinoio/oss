import { MutationIntents } from "./mutation";
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type Transport,
} from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi, type Mock } from "vitest";
import {
  EntityKind,
  ErrorDetailSchema,
  FailureCode,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Surface } from "./views";
import { Sidebar } from "./sidebar";
import type { ComponentProps } from "react";
import { i18n, SupportedLanguage } from "./localization";
import { fakeDestinationWindow, primaryClick } from "./destination-window-test-support";
import {
  NavigationProvider,
  memoryNavigationBridge,
  type NavigationBridge,
} from "./navigation-preference";

type StatusResponse = {
  version: string;
  protocolVersion: number;
  stopping?: boolean;
};
type StatusReader = () => StatusResponse | Promise<StatusResponse>;

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function resource(
  kind: EntityKind,
  name: string,
  projectId = "",
  values: Record<string, unknown> = {},
): Resource {
  const id = newRequestId();
  const row = create(ResourceSchema, {
    id,
    sessionId: kind === EntityKind.SESSION ? id : "",
    projectId,
    kind,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ name, ...values }),
  });
  return row;
}

function mountSidebar({
  projects,
  sessions,
  props = {},
  stateful = false,
  withApp = false,
  navigation,
  destination,
  status = vi.fn<StatusReader>(() => ({
    version: "0.1.0",
    protocolVersion: 1,
  })),
}: {
  projects: (
    pageToken: string,
  ) =>
    | { resources: Resource[]; nextPageToken?: string }
    | Promise<{ resources: Resource[]; nextPageToken?: string }>;
  sessions: (request: {
    projectId: string;
    includeArchived: boolean;
    pageToken: string;
  }) =>
    | { sessions: Resource[]; nextPageToken?: string }
    | Promise<{ sessions: Resource[]; nextPageToken?: string }>;
  status?: Mock<StatusReader>;
  stateful?: boolean;
  withApp?: boolean;
  navigation?: NavigationBridge;
  destination?: ReturnType<typeof fakeDestinationWindow>;
  props?: Partial<ComponentProps<typeof Sidebar>>;
}) {
  const projectRequests: string[] = [];
  const sessionRequests: {
    projectId: string;
    includeArchived: boolean;
    pageToken: string;
  }[] = [];
  const transport: Transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: status,
    });
    router.service(ResourceService, {
      listResources: (request) => {
        if (request.filter?.kind !== EntityKind.PROJECT)
          return { resources: [] };
        const token = request.filter.pageToken;
        projectRequests.push(token);
        return projects(token);
      },
    });
    router.service(SessionService, {
      listSessions: (request) => {
        const value = {
          projectId: request.projectId,
          includeArchived: request.includeArchived,
          pageToken: request.pageToken,
        };
        sessionRequests.push(value);
        return sessions(value);
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 0,
        gcTime: 60000,
        refetchOnWindowFocus: false,
      },
    },
  });
  const openSession = vi.fn();
  const openSettings = vi.fn();
  const newProject = vi.fn();
  let currentProps = props;
  let selectSurface!: (surface: Surface) => void;
  const newSession = vi.fn(() => {
    if (stateful) selectSurface(Surface.NewSession);
  });
  const newGeneralChat = vi.fn(() => {
    if (stateful) selectSurface(Surface.NewGeneralChat);
  });
  const navigate = vi.fn((surface: Surface) => {
    if (stateful) selectSurface(surface);
  });
  function Harness() {
    const [surface, setSurface] = useState(Surface.Sessions);
    selectSurface = setSurface;
    return (
      <Sidebar
        surface={surface}
        selectedSessionId=""
        navigate={navigate}
        openSession={openSession}
        newSession={newSession}
        newGeneralChat={newGeneralChat}
        newProject={newProject}
        openSettings={openSettings}
        {...currentProps}
      />
    );
  }
  const tree = () => {
    const sidebar = withApp ? (
      <div className="app">
        <Harness />
      </div>
    ) : (
      <Harness />
    );
    const content = navigation ? (
      <NavigationProvider bridge={navigation}>{sidebar}</NavigationProvider>
    ) : (
      sidebar
    );
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            {destination ? destination.wrap(content) : content}
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    );
  };
  const view = render(tree());
  const setProps = (next: Partial<ComponentProps<typeof Sidebar>>) => {
    currentProps = { ...currentProps, ...next };
    view.rerender(tree());
  };
  return {
    ...view,
    setProps,
    client,
    navigate,
    openSession,
    openSettings,
    newSession,
    newGeneralChat,
    newProject,
    projectRequests,
    sessionRequests,
    setSurface: (surface: Surface) => act(() => selectSurface(surface)),
  };
}

it("keeps equal-name projects separate, includes empty projects, and only reads expanded project pages", async () => {
  const first = resource(EntityKind.PROJECT, "Same name");
  const second = resource(EntityKind.PROJECT, "Same name");
  const empty = resource(EntityKind.PROJECT, "Empty project");
  const firstSession = resource(EntityKind.SESSION, "First session", first.id, {
    workspace: "worktree",
    outcome: "running",
    archive: "active",
  });
  const secondSession = resource(
    EntityKind.SESSION,
    "Second session",
    second.id,
    { workspace: "local", outcome: "failed", archive: "archiving" },
  );
  const value = mountSidebar({
    projects: () => ({ resources: [first, second, empty] }),
    sessions: (request) =>
      request.projectId === first.id
        ? { sessions: [firstSession] }
        : request.projectId === second.id
          ? { sessions: [secondSession] }
          : { sessions: [] },
  });

  const firstGroup = await screen.findByRole("button", {
    name: `Same name · 1`,
  });
  const secondGroup = screen.getByRole("button", {
    name: `Same name · 2`,
  });
  expect(firstGroup).not.toBe(secondGroup);
  expect(firstGroup.getAttribute("aria-expanded")).toBe("false");
  expect(
    value.sessionRequests.filter((request) => request.projectId === first.id),
  ).toHaveLength(0);

  fireEvent.click(firstGroup);
  expect(
    await screen.findByRole("button", { name: /Worktree First session/ }),
  ).toBeTruthy();
  fireEvent.click(secondGroup);
  expect(
    await screen.findByRole("button", {
      name: /Local computer Second session/,
    }),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", {
      name: `Empty project`,
    }),
  );
  await waitFor(() =>
    expect(screen.getAllByText("No conversations loaded.")).toHaveLength(1),
  );

  expect(
    value.sessionRequests.filter((request) => request.projectId === first.id),
  ).toHaveLength(1);
  expect(
    value.sessionRequests.filter((request) => request.projectId === second.id),
  ).toHaveLength(1);
  expect(
    value.sessionRequests.filter((request) => request.projectId === empty.id),
  ).toHaveLength(1);
  expect(value.openSession).not.toHaveBeenCalled();
});

it("opens project creation shortcuts without toggling groups or reading collapsed sessions", async () => {
  const first = resource(EntityKind.PROJECT, "Same name");
  const second = resource(EntityKind.PROJECT, "Same name");
  const empty = resource(EntityKind.PROJECT, "Empty project");
  const value = mountSidebar({
    projects: () => ({ resources: [first, second, empty] }),
    sessions: () => ({ sessions: [] }),
  });
  for (const project of [first, second, empty]) {
    const originalName = JSON.parse(
      new TextDecoder().decode(project.documentJson),
    ).name as string;
    const name =
      project === empty
        ? originalName
        : `${originalName} · ${project === first ? 1 : 2}`;
    const toggle = await screen.findByRole("button", {
      name: `${name}`,
    });
    const shortcut = screen.getByRole("button", {
      name: `New session in ${name}`,
    });
    expect(shortcut.parentElement).toBe(toggle.parentElement);
    expect(toggle.contains(shortcut)).toBe(false);
    expect(shortcut.getAttribute("title")).toBe(`New session in ${name}`);
    fireEvent.click(shortcut);
    expect(value.newSession).toHaveBeenLastCalledWith(project.id);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(
      value.sessionRequests.some((request) => request.projectId === project.id),
    ).toBe(false);
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(shortcut);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
  }
  value.setProps({ projectSelectionBlocked: true });
  const disabled = screen.getByRole("button", {
    name: `New session in Same name · 1`,
  });
  expect(disabled).toHaveProperty("disabled", true);
  const before = value.newSession.mock.calls.length;
  fireEvent.click(disabled);
  expect(value.newSession).toHaveBeenCalledTimes(before);
  await act(() => i18n.changeLanguage("ko"));
  expect(
    screen.getByRole("button", {
      name: `Same name · 1 프로젝트의 새 세션`,
    }),
  ).toBe(disabled);
  expect(disabled.getAttribute("title")).toBe(
    "Same name · 1 프로젝트의 새 세션",
  );
});

it("keeps project creation shortcuts out of unknown-project and General Chat groups", async () => {
  const unknown = resource(
    EntityKind.SESSION,
    "Unknown parent",
    newRequestId(),
  );
  const general = resource(EntityKind.SESSION, "Projectless");
  mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [unknown, general] }),
  });
  await screen.findByRole("button", { name: /Unknown parent/ });
  await screen.findByRole("button", { name: /Projectless/ });
  expect(screen.queryByRole("button", { name: /^New session in / })).toBeNull();
});

it("includes the safe title reason in sidebar text and its accessible description", async () => {
  const project = resource(EntityKind.PROJECT, "Title project");
  const session = resource(EntityKind.SESSION, "New session", project.id, {
    workspace: "worktree",
    outcome: "succeeded",
    archive: "active",
    name_mode: "automatic",
    title_state: "skipped",
    title_reason: "budget-reached",
  });
  mountSidebar({
    projects: () => ({ resources: [project] }),
    sessions: () => ({ sessions: [session] }),
  });
  fireEvent.click(
    await screen.findByRole("button", {
      name: `Title project`,
    }),
  );
  const row = await screen.findByRole("button", {
    name: /Title skipped\. The session budget did not allow another request\./,
  });
  expect(within(row).getByText("Skipped · Budget reached")).toBeTruthy();
});

it("keeps sparse global paging reachable and groups retained sessions by their original unknown project ID", async () => {
  const listedProject = resource(EntityKind.PROJECT, "Listed project");
  const missingParent = newRequestId();
  const owned = resource(
    EntityKind.SESSION,
    "Listed session",
    listedProject.id,
    { workspace: "worktree", outcome: "succeeded", archive: "active" },
  );
  const general = resource(EntityKind.SESSION, "General session", "", {
    workspace: "general-chat",
    outcome: "not-started",
    archive: "active",
  });
  const retained = resource(
    EntityKind.SESSION,
    "Retained missing-parent session",
    missingParent,
    { workspace: "local", outcome: "stopped", archive: "archived" },
  );
  const value = mountSidebar({
    projects: () => ({ resources: [listedProject] }),
    sessions: (request) =>
      request.projectId
        ? { sessions: [] }
        : request.pageToken === ""
          ? { sessions: [owned], nextPageToken: "global-next" }
          : { sessions: [general, retained] },
  });

  await screen.findByRole("button", {
    name: `Listed project`,
  });
  await waitFor(() => expect(value.sessionRequests).toHaveLength(1));
  reach("sessions");

  expect(
    await screen.findByRole("button", { name: /General Chat General session/ }),
  ).toBeTruthy();
  const fallback = await screen.findByRole("button", {
    name: `Project unavailable · 2`,
  });
  expect(fallback.getAttribute("aria-expanded")).toBe("true");
  expect(
    screen.getByText("Project details are not in the loaded project catalog."),
  ).toBeTruthy();
  const retainedRow = screen.getByRole("button", {
    name: /Local computer Retained missing-parent session/,
  });
  expect(
    within(fallback.parentElement!).getByRole("button", {
      name: /Local computer Retained missing-parent session/,
    }),
  ).toBe(retainedRow);
  expect(
    value.sessionRequests.some(
      (request) => request.projectId === missingParent,
    ),
  ).toBe(false);
  expect(
    value.sessionRequests.some(
      (request) =>
        request.projectId === "" && request.pageToken === "global-next",
    ),
  ).toBe(true);
  expect(value.openSession).not.toHaveBeenCalled();
});

it("keeps catalog and session cursors independent and resets every session scope when archive visibility changes", async () => {
  const first = resource(EntityKind.PROJECT, "Project one");
  const second = resource(EntityKind.PROJECT, "Project two");
  const one = resource(EntityKind.SESSION, "One", first.id, {
    workspace: "worktree",
    outcome: "not-started",
    archive: "active",
  });
  const two = resource(EntityKind.SESSION, "Two", second.id, {
    workspace: "local",
    outcome: "running",
    archive: "active",
  });
  const value = mountSidebar({
    projects: (page) =>
      page
        ? { resources: [second] }
        : { resources: [first], nextPageToken: "project-next" },
    sessions: (request) => {
      if (request.projectId === first.id)
        return request.pageToken
          ? { sessions: [one] }
          : { sessions: [one], nextPageToken: "one-next" };
      if (request.projectId === second.id) return { sessions: [two] };
      return request.pageToken
        ? { sessions: [], nextPageToken: "global-last" }
        : { sessions: [], nextPageToken: "global-next" };
    },
  });

  const oneGroup = await screen.findByRole("button", {
    name: `Project one`,
  });
  fireEvent.click(oneGroup);
  reach("Project one sessions");
  await waitFor(() =>
    expect(
      value.sessionRequests.some(
        (request) =>
          request.projectId === first.id && request.pageToken === "one-next",
      ),
    ).toBe(true),
  );
  reach("sessions");
  await waitFor(() =>
    expect(
      value.sessionRequests.some(
        (request) =>
          request.projectId === "" && request.pageToken === "global-next",
      ),
    ).toBe(true),
  );
  reach("projects");
  expect(
    await screen.findByRole("button", {
      name: `Project two`,
    }),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", {
      name: `Project two`,
    }),
  );
  await screen.findByRole("button", { name: /Local computer Two/ });

  const catalogReadsBeforeArchive = value.projectRequests.length;
  fireEvent.click(
    screen.getByRole("button", { name: "Project and conversation options" }),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Include archived" }));
  await waitFor(() =>
    expect(
      value.sessionRequests.some(
        (request) =>
          request.includeArchived &&
          request.projectId === "" &&
          request.pageToken === "",
      ),
    ).toBe(true),
  );
  expect(
    value.sessionRequests.some(
      (request) =>
        request.includeArchived &&
        request.projectId === second.id &&
        request.pageToken === "",
    ),
  ).toBe(true);
  expect(value.projectRequests).toHaveLength(catalogReadsBeforeArchive);
  expect(
    screen.getByRole("button", {
      name: `Project one`,
    }),
  ).toBeTruthy();
  await waitFor(() =>
    expect(
      value.sessionRequests.some(
        (request) =>
          request.includeArchived &&
          request.projectId === first.id &&
          request.pageToken === "",
      ),
    ).toBe(true),
  );
});

it("shows unknown execution and archive states without inventing successful or active indicators", async () => {
  const unknown = resource(EntityKind.SESSION, "Unknown state", "", {
    workspace: "mystery-workspace",
    outcome: "waiting-for-oracle",
    archive: "suspended",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [unknown] }),
  });
  const row = await screen.findByRole("button", {
    name: /Unknown workspace \(mystery-workspace\) Unknown state\. Execution state: waiting-for-oracle\. Archive state: suspended\./,
  });
  expect(row.querySelectorAll(".sidebar-status-unknown")).toHaveLength(2);
  expect(row.querySelector(".sidebar-status-running")).toBeNull();
  expect(row.querySelector("button")).toBeNull();
  expect(row.getAttribute("aria-describedby")).toBeTruthy();
  fireEvent.focus(row);
  const card = await screen.findByRole("tooltip");
  expect(within(card).getByText("Unknown state")).toBeTruthy();
  expect(
    within(card).getByText("Unknown workspace (mystery-workspace)"),
  ).toBeTruthy();
  expect(within(card).getByText("waiting-for-oracle")).toBeTruthy();
  expect(within(card).getByText("suspended")).toBeTruthy();
  expect(card.querySelector(".sidebar-session-card-title-state")).toBeNull();
  expect(
    window.document.getElementById(row.getAttribute("aria-describedby")!)
      ?.textContent,
  ).toContain("Execution state: waiting-for-oracle. Archive state: suspended.");
  expect(value.openSession).not.toHaveBeenCalled();
});

function advanceHover(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

it("delays pointer entry, bridges the card gap and keeps card scrolling independent", async () => {
  const session = resource(EntityKind.SESSION, "Hover session", "", {
    workspace: "worktree",
    outcome: "not-started",
    archive: "active",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Worktree Hover session/,
  });
  const reads = value.sessionRequests.length;
  vi.useFakeTimers();
  fireEvent.pointerEnter(row);
  advanceHover(299);
  expect(screen.queryByRole("tooltip")).toBeNull();
  advanceHover(1);
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Not started")).toBeTruthy();
  expect(within(card).getByText("Active")).toBeTruthy();
  fireEvent.pointerLeave(row);
  advanceHover(149);
  fireEvent.pointerEnter(card);
  advanceHover(1000);
  fireEvent.scroll(card);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.pointerLeave(card);
  advanceHover(149);
  expect(screen.getByRole("tooltip")).toBe(card);
  advanceHover(1);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(value.sessionRequests).toHaveLength(reads);
  expect(value.openSession).not.toHaveBeenCalled();
});

it("opens immediately on focus, retains focus across pointer departure and preserves activation", async () => {
  const session = resource(EntityKind.SESSION, "Focused session", "", {
    workspace: "local",
    outcome: "failed",
    archive: "archived",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Local computer Focused session/,
  });
  vi.useFakeTimers();
  act(() => row.focus());
  const card = screen.getByRole("tooltip");
  expect(document.activeElement).toBe(row);
  fireEvent.pointerEnter(row);
  fireEvent.pointerLeave(row);
  advanceHover(300);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.click(row);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(value.openSession).toHaveBeenCalledExactlyOnceWith(session.id);
  expect(document.activeElement).toBe(row);
});

it("cancels pending hover on Escape and waits for a new entry after dismissal", async () => {
  const session = resource(EntityKind.SESSION, "Escape session", "", {
    workspace: "general-chat",
    outcome: "running",
    archive: "active",
  });
  mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /General Chat Escape session/,
  });
  vi.useFakeTimers();
  fireEvent.pointerEnter(row);
  advanceHover(100);
  fireEvent.keyDown(document, { key: "Escape" });
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerMove(row);
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerLeave(row);
  fireEvent.pointerEnter(row);
  advanceHover(300);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(row, { key: "Escape" });
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  act(() => row.focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(row, { key: "Escape" });
  expect(document.activeElement).toBe(row);
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

it("has one card and clears active or pending presentation on scrolling and surface changes", async () => {
  const first = resource(EntityKind.SESSION, "First hover", "", {
    workspace: "worktree",
    outcome: "succeeded",
    archive: "active",
  });
  const second = resource(EntityKind.SESSION, "Second hover", "", {
    workspace: "general-chat",
    outcome: "stopped",
    archive: "archiving",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [first, second] }),
  });
  const one = await screen.findByRole("button", {
    name: /Worktree First hover/,
  });
  const two = screen.getByRole("button", { name: /General Chat Second hover/ });
  vi.useFakeTimers();
  act(() => one.focus());
  act(() => two.focus());
  expect(screen.getAllByRole("tooltip")).toHaveLength(1);
  expect(
    within(screen.getByRole("tooltip")).getByText("Second hover"),
  ).toBeTruthy();
  fireEvent.scroll(screen.getByLabelText("Project and session navigation"));
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(one);
  value.setSurface(Surface.NewSession);
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerLeave(one);
  fireEvent.pointerEnter(one);
  advanceHover(300);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  value.setSurface(Surface.Usage);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

it("clears pending presentation when navigation is inactive or its source disappears", async () => {
  const session = resource(EntityKind.SESSION, "Disposed hover", "", {
    workspace: "local",
    outcome: "running",
    archive: "active",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Local computer Disposed hover/,
  });
  vi.useFakeTimers();
  fireEvent.pointerEnter(row);
  value.setProps({ homeActive: false });
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  value.setProps({ homeActive: true });
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerLeave(row);
  fireEvent.pointerEnter(row);
  fireEvent.click(screen.getByRole("button", { name: "General Chat" }));
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(row.isConnected).toBe(false);
});

it("places measured cards below or above when the right side cannot fit and clears on resize", async () => {
  const session = resource(EntityKind.SESSION, "Edge hover", "", {
    workspace: "local",
    outcome: "running",
    archive: "active",
  });
  mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Local computer Edge hover/,
  });
  vi.stubGlobal("innerWidth", 960);
  vi.stubGlobal("innerHeight", 640);
  const rect = (left: number, top: number, width: number, height: number) => ({
    x: left,
    y: top,
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    toJSON: () => ({}),
  });
  let sourceTop = 100;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return this === row
        ? rect(700, sourceTop, 100, 34)
        : this.classList.contains("sidebar-session-tooltip")
          ? rect(0, 0, 320, 220)
          : rect(0, 0, 0, 0);
    },
  );
  fireEvent.focus(row);
  let card = screen.getByRole("tooltip");
  expect(card.style.left).toBe("632px");
  expect(card.style.top).toBe("142px");
  fireEvent(window, new Event("resize"));
  expect(screen.queryByRole("tooltip")).toBeNull();
  sourceTop = 500;
  fireEvent.blur(row);
  fireEvent.focus(row);
  card = screen.getByRole("tooltip");
  expect(card.style.left).toBe("632px");
  expect(card.style.top).toBe("272px");
});

it.each([
  ["not-started", "active", "Not started", "Active"],
  ["running", "archiving", "Running", "Archiving"],
  ["succeeded", "archived", "Succeeded", "Archived"],
  ["failed", "active", "Failed", "Active"],
  ["stopped", "archived", "Stopped", "Archived"],
])(
  "keeps execution %s and archive %s as separate labeled observations",
  async (outcome, archive, executionText, archiveText) => {
    const session = resource(EntityKind.SESSION, "State hover", "", {
      workspace: "worktree",
      outcome,
      archive,
    });
    mountSidebar({
      projects: () => ({ resources: [] }),
      sessions: () => ({ sessions: [session] }),
    });
    fireEvent.focus(
      await screen.findByRole("button", { name: /Worktree State hover/ }),
    );
    const card = screen.getByRole("tooltip");
    expect(
      within(card).getByText("Execution").nextElementSibling?.textContent,
    ).toBe(executionText);
    expect(
      within(card).getByText("Archive").nextElementSibling?.textContent,
    ).toBe(archiveText);
    expect(card.querySelector("button, a, input, [tabindex]")).toBeNull();
  },
);

it.each([
  ["waiting", "", "Title waits for the first completed turn", ""],
  ["queued", "", "Title queued", ""],
  ["running", "", "Generating title", ""],
  ["succeeded", "", "Title generated", ""],
  [
    "skipped",
    "budget-reached",
    "Title skipped",
    "The session budget did not allow another request.",
  ],
  [
    "failed",
    "inference-failed",
    "Title generation failed",
    "The title request failed; the conversation remains available.",
  ],
  [
    "unsupported",
    "unsupported-agent-profile",
    "Title generation unsupported",
    "The original Agent profile does not support title generation.",
  ],
  [
    "unsupported",
    "worker-capability-absent",
    "Title generation unsupported",
    "The original Worker did not prove the required title capability.",
  ],
  [
    "skipped",
    "canceled",
    "Title skipped",
    "Title generation was canceled with the session operation.",
  ],
  [
    "failed",
    "authority-lost",
    "Title generation failed",
    "The original account or session permission is no longer available.",
  ],
  [
    "failed",
    "invalid-output",
    "Title generation failed",
    "The title result did not pass validation.",
  ],
  [
    "uncertain",
    "cleanup-uncertain",
    "Title outcome uncertain",
    "Worker cleanup is uncertain; inspect the retained operation.",
  ],
  [
    "skipped",
    "manual-rename",
    "Title skipped",
    "A manual rename now owns this title.",
  ],
  ["future-title", "", "Unknown title state (future-title)", ""],
  ["", "", "Title state unavailable", ""],
])(
  "retains automatic title state %s and its safe reason",
  async (state, reason, label, detail) => {
    const session = resource(EntityKind.SESSION, "Title hover", "", {
      workspace: "worktree",
      outcome: "succeeded",
      archive: "active",
      name_mode: "automatic",
      title_state: state,
      title_reason: reason,
    });
    mountSidebar({
      projects: () => ({ resources: [] }),
      sessions: () => ({ sessions: [session] }),
    });
    const row = await screen.findByRole("button", {
      name: /Worktree Title hover/,
    });
    fireEvent.focus(row);
    const card = screen.getByRole("tooltip");
    expect(
      card.querySelector(".sidebar-session-card-title-state")?.textContent,
    ).toContain(label);
    if (detail)
      expect(
        card.querySelector(".sidebar-session-card-title-state")?.textContent,
      ).toContain(detail);
    expect(
      document.getElementById(row.getAttribute("aria-describedby")!)
        ?.textContent,
    ).toContain(label);
  },
);

it("omits automatic title information and its divider after a manual rename", async () => {
  const session = resource(EntityKind.SESSION, "My chosen title", "", {
    workspace: "worktree",
    outcome: "succeeded",
    archive: "active",
    name_mode: "manual",
    title_state: "skipped",
    title_reason: "manual-rename",
  });
  mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Worktree My chosen title/,
  });
  fireEvent.focus(row);
  const card = screen.getByRole("tooltip");
  expect(card.querySelector(".sidebar-session-card-title-state")).toBeNull();
  expect(card.textContent).not.toContain("Title skipped");
  expect(row.querySelector(".sidebar-session-title-state")).toBeNull();
  expect(
    document.getElementById(row.getAttribute("aria-describedby")!)?.textContent,
  ).not.toContain("Title skipped");
});

it("updates an open card in Korean without losing focus or adding reads and preserves the full name", async () => {
  const name = "A complete long session name ".repeat(8);
  const session = resource(EntityKind.SESSION, name, "", {
    workspace: "local",
    outcome: "failed",
    archive: "archived",
    name_mode: "automatic",
    title_state: "failed",
    title_reason: "future-reason",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Local computer A complete long session name/,
  });
  act(() => row.focus());
  const card = screen.getByRole("tooltip");
  expect(card.querySelector(".sidebar-session-card-title")?.textContent).toBe(
    name,
  );
  expect(card.textContent).toContain("future-reason");
  const reads = value.sessionRequests.length;
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(screen.getByRole("tooltip")).toBe(card);
  expect(within(card).getByText("실행")).toBeTruthy();
  expect(within(card).getByText("실패")).toBeTruthy();
  expect(within(card).getByText("보관됨")).toBeTruthy();
  expect(card.querySelector(".sidebar-session-card-title")?.textContent).toBe(
    name,
  );
  expect(document.activeElement).toBe(row);
  expect(value.sessionRequests).toHaveLength(reads);
});

it("distinguishes permission and connection failures from successful empty pages", async () => {
  mountSidebar({
    projects: () => {
      throw new ConnectError("Denied", Code.PermissionDenied);
    },
    sessions: () => {
      throw new ConnectError("Offline", Code.Unavailable);
    },
  });
  expect(
    await screen.findByText("You do not have permission to view projects."),
  ).toBeTruthy();
  expect(
    await screen.findByText("Could not connect to load sessions."),
  ).toBeTruthy();
  expect(screen.queryByText("No projects loaded.")).toBeNull();
  expect(screen.queryByText("No conversations loaded.")).toBeNull();
});

it("keeps cached rows visible and labels them as previous data after a failed refresh", async () => {
  const project = resource(EntityKind.PROJECT, "Cached project");
  let projectReads = 0;
  const value = mountSidebar({
    projects: () => {
      projectReads++;
      if (projectReads > 1) throw new ConnectError("Offline", Code.Unavailable);
      return { resources: [project] };
    },
    sessions: () => ({ sessions: [] }),
  });
  const row = await screen.findByRole("button", {
    name: `Cached project`,
  });
  await act(async () => {
    await value.client.invalidateQueries({ refetchType: "active" });
  });
  expect(
    await screen.findByText(
      "Could not refresh projects. Previous data is shown.",
    ),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", {
      name: `Cached project`,
    }),
  ).toBe(row);
  expect(
    screen.queryByRole("button", { name: "Refresh projects and sessions" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry project catalog" }),
  );
  await waitFor(() => expect(value.projectRequests).toHaveLength(3));
  expect(value.projectRequests).toEqual(["", "", ""]);
  expect(
    screen.getByRole("button", {
      name: `Cached project`,
    }),
  ).toBe(row);
});

it("retries only the failed catalog and global-session pages", async () => {
  const first = resource(EntityKind.PROJECT, "First project");
  const second = resource(EntityKind.PROJECT, "Second project");
  const global = resource(EntityKind.SESSION, "First global session");
  let catalogNextReads = 0,
    globalNextReads = 0;
  const value = mountSidebar({
    projects: (page) => {
      if (!page) return { resources: [first], nextPageToken: "project-next" };
      if (++catalogNextReads === 1)
        throw new ConnectError("Catalog unavailable", Code.Unavailable);
      return { resources: [second] };
    },
    sessions: (request) => {
      if (request.projectId) return { sessions: [] };
      if (!request.pageToken)
        return { sessions: [global], nextPageToken: "global-next" };
      if (++globalNextReads === 1)
        throw new ConnectError("Sessions unavailable", Code.Unavailable);
      return { sessions: [] };
    },
  });

  await screen.findByRole("button", {
    name: `First project`,
  });
  reach("projects");
  expect(
    await screen.findByRole("button", { name: "Retry project catalog" }),
  ).toBeTruthy();
  reach("sessions");
  expect(
    await screen.findByRole("button", { name: "Retry global sessions" }),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry project catalog" }),
  );
  expect(
    await screen.findByRole("button", {
      name: `Second project`,
    }),
  ).toBeTruthy();
  expect(value.projectRequests).toEqual(["", "project-next", "project-next"]);
  expect(
    value.sessionRequests.map(({ projectId, pageToken }) => [
      projectId,
      pageToken,
    ]),
  ).toEqual([
    ["", ""],
    ["", "global-next"],
  ]);
  expect(
    screen.getByRole("button", { name: "Retry global sessions" }),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry global sessions" }),
  );
  await waitFor(() => expect(value.sessionRequests).toHaveLength(3));
  expect(value.sessionRequests[2]).toMatchObject({
    projectId: "",
    pageToken: "global-next",
    includeArchived: false,
  });
  expect(value.projectRequests).toEqual(["", "project-next", "project-next"]);
});

it("retries an expanded project's exact session page without refetching other scopes", async () => {
  const project = resource(EntityKind.PROJECT, "Named project");
  const row = resource(EntityKind.SESSION, "Project session", project.id);
  let nextReads = 0;
  const value = mountSidebar({
    projects: () => ({ resources: [project] }),
    sessions: (request) => {
      if (!request.projectId) return { sessions: [] };
      if (!request.pageToken)
        return { sessions: [row], nextPageToken: "project-session-next" };
      if (++nextReads === 1)
        throw new ConnectError(
          "Project sessions unavailable",
          Code.Unavailable,
        );
      return { sessions: [row] };
    },
  });
  fireEvent.click(
    await screen.findByRole("button", {
      name: `Named project`,
    }),
  );
  reach("Named project sessions");
  expect(
    await screen.findByRole("button", { name: "Retry Named project sessions" }),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry Named project sessions" }),
  );
  await waitFor(() =>
    expect(
      value.sessionRequests.filter(
        (request) => request.projectId === project.id,
      ),
    ).toHaveLength(3),
  );
  expect(
    value.sessionRequests
      .filter((request) => request.projectId === project.id)
      .map((request) => request.pageToken),
  ).toEqual(["", "project-session-next", "project-session-next"]);
  expect(value.projectRequests).toEqual([""]);
  expect(
    value.sessionRequests.filter((request) => !request.projectId),
  ).toHaveLength(1);
});

it("routes the icon rail to the matching surface and opens New project independently of Settings", async () => {
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
  });
  await screen.findByRole("button", { name: "Sessions" });
  const newProject = screen.getByRole("button", { name: "New project" });
  expect(newProject.querySelector("svg")?.getAttribute("aria-hidden")).toBe(
    "true",
  );
  expect(newProject.textContent).toBe("");
  fireEvent.focus(newProject);
  expect(
    window.document.querySelector(".sidebar-action-tooltip")?.textContent,
  ).toBe("New project");
  fireEvent.click(newProject);
  expect(value.newProject).toHaveBeenCalledOnce();
  expect(value.openSettings).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect(value.navigate).toHaveBeenCalledWith(Surface.PullRequests);
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expect(value.newSession).toHaveBeenCalledOnce();
  expect(value.sessionRequests).toHaveLength(1);
});

it("retires Activity while retaining the four rail destinations and header navigation in both languages", async () => {
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
  });
  await screen.findByRole("button", { name: "Sessions" });
  for (const language of [
    SupportedLanguage.English,
    SupportedLanguage.Korean,
  ]) {
    await act(async () => {
      await i18n.changeLanguage(language);
    });
    const rail = document.querySelector(".sidebar-rail")!;
    const names = [...rail.querySelectorAll("button")].map((button) =>
      button.getAttribute("aria-label"),
    );
    const expected =
      language === SupportedLanguage.English
        ? ["Sessions", "Pull requests", "Schedules", "More"]
        : ["세션", "풀 리퀘스트", "예약 작업", "더보기"];
    expect(names.slice(0, 4)).toEqual(expected);
    expect(names).not.toContain(
      language === SupportedLanguage.English ? "Activity" : "활동",
    );
    expect(
      screen.queryByRole("button", {
        name: language === SupportedLanguage.English ? "Activity" : "활동",
      }),
    ).toBeNull();
    expect(
      document.querySelectorAll(".sidebar-header-actions button"),
    ).toHaveLength(2);
  }
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.English);
  });
  expect(value.sessionRequests).toHaveLength(1);
});

function reach(label: string, remaining = 96) {
  const root = window.document.querySelector<HTMLDivElement>(".sidebar-list")!;
  Object.defineProperty(root, "clientHeight", {
    configurable: true,
    value: 400,
  });
  const rect = (top: number) => ({
    top,
    bottom: top + 1,
    left: 0,
    right: 200,
    width: 200,
    height: 1,
    x: 0,
    y: top,
    toJSON: () => ({}),
  });
  vi.spyOn(root, "getBoundingClientRect").mockReturnValue({
    ...rect(0),
    bottom: 400,
    height: 400,
  });
  for (const anchor of root.querySelectorAll<HTMLElement>(
    "[data-continuation]",
  )) {
    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(rect(600));
  }
  const anchor = root.querySelector<HTMLElement>(
    `[data-continuation="${label}"]`,
  )!;
  vi.mocked(anchor.getBoundingClientRect).mockReturnValueOnce(
    rect(400 + remaining),
  );
  fireEvent.scroll(root);
}

it("requires the visible anchor to be within 96px and retains focus and scroll on append", async () => {
  const first = resource(EntityKind.SESSION, "First visible", "", {
    workspace: "general-chat",
  });
  const second = resource(EntityKind.SESSION, "Additional visible", "", {
    workspace: "general-chat",
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: ({ pageToken }) => ({
      sessions: pageToken ? [first, second] : [first],
      nextPageToken: pageToken ? "" : "next",
    }),
  });
  const row = await screen.findByRole("button", { name: /First visible/ });
  row.focus();
  const list = window.document.querySelector<HTMLDivElement>(".sidebar-list")!;
  list.scrollTop = 42;
  reach("sessions", 97);
  expect(value.sessionRequests).toHaveLength(1);
  reach("sessions", 96);
  await screen.findByRole("button", { name: /Additional visible/ });
  expect(value.sessionRequests.map(({ pageToken }) => pageToken)).toEqual([
    "",
    "next",
  ]);
  expect(window.document.activeElement).toBe(row);
  expect(list.scrollTop).toBe(42);
  expect(screen.getAllByRole("button", { name: /First visible/ })).toHaveLength(
    1,
  );
  expect(
    screen.queryByRole("button", {
      name: /First page|Next page|Load more|Next session|Next project/,
    }),
  ).toBeNull();
});

it("discards delayed named continuations on collapse and preserves accepted rows across Home navigation", async () => {
  const project = resource(EntityKind.PROJECT, "Delayed");
  const first = resource(EntityKind.SESSION, "Accepted", project.id);
  const late = resource(EntityKind.SESSION, "Late", project.id);
  let release!: (result: {
    sessions: Resource[];
    nextPageToken: string;
  }) => void;
  const value = mountSidebar({
    projects: () => ({ resources: [project] }),
    sessions: ({ projectId, pageToken }) =>
      !projectId
        ? { sessions: [] }
        : !pageToken
          ? { sessions: [first], nextPageToken: "next" }
          : new Promise((resolve) => {
              release = resolve;
            }),
  });
  const group = await screen.findByRole("button", {
    name: `Delayed`,
  });
  fireEvent.click(group);
  await screen.findByRole("button", { name: /Accepted/ });
  reach("Delayed sessions");
  await waitFor(() =>
    expect(
      value.sessionRequests.some(({ pageToken }) => pageToken === "next"),
    ).toBe(true),
  );
  reach("Delayed sessions");
  expect(
    value.sessionRequests.filter(({ pageToken }) => pageToken === "next"),
  ).toHaveLength(1);
  fireEvent.click(group);
  await act(async () => release({ sessions: [late], nextPageToken: "unseen" }));
  fireEvent.click(group);
  await screen.findByRole("button", { name: /Accepted/ });
  expect(screen.queryByRole("button", { name: /Late/ })).toBeNull();
  value.setProps({ surface: Surface.NewSession });
  expect(screen.getByRole("button", { name: /Accepted/ })).toBeTruthy();
  value.setProps({ surface: Surface.NewGeneralChat });
  expect(screen.getByRole("button", { name: /Accepted/ })).toBeTruthy();
  expect(
    screen
      .getAllByRole("button", { name: "New Chat" })[0]
      .getAttribute("aria-current"),
  ).toBe("page");
  expect(screen.getByRole("button", { name: "Inbox" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Search" })).toBeTruthy();
  value.setProps({ surface: Surface.Usage });
  expect(screen.queryByRole("button", { name: /Accepted/ })).toBeNull();
  value.setProps({ surface: Surface.Sessions });
  expect(screen.getByRole("button", { name: /Accepted/ })).toBeTruthy();
});

it("transfers expanded fallback rows until the authoritative named read accepts success", async () => {
  const project = resource(EntityKind.PROJECT, "Resolved parent");
  const retained = resource(
    EntityKind.SESSION,
    "Retained fallback",
    project.id,
    { workspace: "local" },
  );
  let accept!: (result: { sessions: Resource[] }) => void;
  const value = mountSidebar({
    projects: (token) =>
      token
        ? { resources: [project] }
        : { resources: [], nextPageToken: "catalog-next" },
    sessions: ({ projectId }) =>
      projectId
        ? new Promise((resolve) => {
            accept = resolve;
          })
        : { sessions: [retained] },
  });
  fireEvent.click(
    await screen.findByRole("button", { name: /Retained fallback/ }),
  );
  value.setProps({ selectedSessionId: retained.id });
  const originalRow = screen.getByRole("button", { name: /Retained fallback/ });
  originalRow.focus();
  reach("projects");
  const named = await screen.findByRole("button", {
    name: `Resolved parent`,
  });
  expect(named.getAttribute("aria-expanded")).toBe("true");
  const row = screen.getByRole("button", { name: /Retained fallback/ });
  expect(row).toBe(originalRow);
  expect(window.document.activeElement).toBe(originalRow);
  expect(row.getAttribute("aria-current")).toBe("true");
  expect(
    within(named.parentElement!).getByRole("button", {
      name: /Retained fallback/,
    }),
  ).toBe(row);
  await waitFor(() => expect(accept).toBeTypeOf("function"));
  await act(async () => accept({ sessions: [retained] }));
  expect(
    screen
      .getByRole("button", { name: /Retained fallback/ })
      .getAttribute("aria-current"),
  ).toBe("true");
  expect(value.openSession).toHaveBeenCalledOnce();
});

it("keeps the archive popup open for changes and restores its opener on Escape", async () => {
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
  });
  await screen.findByText("No projects loaded.");
  fireEvent.click(screen.getByRole("button", { name: "Create a project" }));
  expect(value.newProject).toHaveBeenCalledOnce();
  expect(value.openSettings).not.toHaveBeenCalled();
  const opener = screen.getByRole("button", {
    name: "Project and conversation options",
  });
  fireEvent.click(opener);
  const popup = screen.getByRole("dialog", {
    name: "Project and conversation options",
  });
  fireEvent.click(
    within(popup).getByRole("checkbox", { name: "Include archived" }),
  );
  await screen.findByText("Archived included");
  expect(
    screen.getByRole("dialog", { name: "Project and conversation options" }),
  ).toBe(popup);
  fireEvent.keyDown(popup, { key: "Escape" });
  expect(
    screen.queryByRole("dialog", { name: "Project and conversation options" }),
  ).toBeNull();
  expect(window.document.activeElement).toBe(opener);
});

for (const language of [SupportedLanguage.English, SupportedLanguage.Korean]) {
  for (const compact of [false, true]) {
    it(`omits the connection footer on every surface (${language}, compact=${compact})`, async () => {
      await act(() => i18n.changeLanguage(language));
      vi.stubGlobal("matchMedia", () => ({
        matches: compact,
        addEventListener: () => {},
        removeEventListener: () => {},
      }));
      const status = vi.fn<StatusReader>();
      const value = mountSidebar({
        projects: () => ({ resources: [] }),
        sessions: () => ({ sessions: [] }),
        status,
        props: { drawerOpen: compact },
      });
      const pane = value.container.querySelector(".sidebar-pane")!;
      const list = pane.querySelector(".sidebar-list");
      for (const theme of ["light", "dark"]) {
        value.container.setAttribute("data-theme", theme);
        // The shared sidebar has no status observer in any connection state.
        for (const state of [
          "pending",
          "connected",
          "failed",
          "disconnected",
          "stale",
          "stopping",
        ]) {
          status.mockImplementation(() => {
            if (state === "pending") return new Promise(() => {});
            if (state === "failed" || state === "disconnected")
              throw new ConnectError("Fixture disconnected", Code.Unavailable);
            return {
              version: "0.1.0",
              protocolVersion: 1,
              stopping: state === "stopping",
            };
          });
          value.setProps({ connectionReady: state === "connected" });
          for (const surface of Object.values(Surface)) {
            value.setSurface(surface);
            expect(pane.querySelector(".sidebar-list")).toBe(list);
            expect(
              pane.querySelector(".sidebar-footer, .sidebar-server-summary"),
            ).toBeNull();
            expect(
              pane.querySelector(
                ":scope > [role='status'], :scope > footer [role='status']",
              ),
            ).toBeNull();
            expect(pane.lastElementChild).toBe(list);
          }
        }
      }
      expect(status).not.toHaveBeenCalled();
      value.unmount();
      await act(() => i18n.changeLanguage(SupportedLanguage.English));
    });
  }
}

function expectHeaderActions(home: boolean) {
  const header = window.document.querySelector<HTMLElement>(".sidebar-header")!;
  expect(within(header).getByRole("heading", { name: "DeliDev" })).toBeTruthy();
  expect(
    [...header.querySelectorAll("button")].map((button) =>
      button.getAttribute("aria-label"),
    ),
  ).toEqual(home ? ["Inbox", "Search"] : []);
  expect(header.querySelector(".sidebar-header-actions") === null).toBe(!home);
  for (const label of ["Inbox", "Search"]) {
    const action = within(header).queryByRole("button", { name: label });
    if (home)
      expect(action?.querySelector("svg")?.getAttribute("aria-hidden")).toBe(
        "true",
      );
    else expect(action).toBeNull();
  }
}

it.each(["loading", "empty", "denied", "unavailable", "cached-refresh"])(
  "shows home-only header actions independently of %s reads",
  async (mode) => {
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => {
      finish = resolve;
    });
    let failedRefresh = false;
    const value = mountSidebar({
      stateful: true,
      projects: async () => {
        if (mode === "loading") await gate;
        if (mode === "denied" || mode === "unavailable" || failedRefresh)
          throw new ConnectError(
            "Fixture read failure",
            mode === "denied" ? Code.PermissionDenied : Code.Unavailable,
          );
        return { resources: [] };
      },
      sessions: async () => {
        if (mode === "loading") await gate;
        return { sessions: [] };
      },
    });
    if (mode === "loading") await screen.findByText("Loading projects…");
    else if (mode === "denied")
      await screen.findByText("You do not have permission to view projects.");
    else if (mode === "unavailable")
      await screen.findByText("Could not connect to load projects.");
    else await screen.findByText("No projects loaded.");
    if (mode === "cached-refresh") {
      failedRefresh = true;
      await act(async () => {
        await value.client.invalidateQueries({ refetchType: "active" });
      });
      await screen.findByText(
        "Could not refresh projects. Previous data is shown.",
      );
    }
    expectHeaderActions(true);
    fireEvent.click(screen.getByRole("button", { name: "New session" }));
    expectHeaderActions(true);
    const reads = [value.projectRequests.length, value.sessionRequests.length];
    for (const surface of [
      Surface.PullRequests,
      Surface.Usage,
      Surface.Schedules,
      Surface.Inbox,
      Surface.Search,
    ]) {
      value.setSurface(surface);
      expectHeaderActions(false);
      expect([
        value.projectRequests.length,
        value.sessionRequests.length,
      ]).toEqual(reads);
    }
    fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
    expectHeaderActions(true);
    expect(value.openSession).not.toHaveBeenCalled();
    expect(value.openSettings).not.toHaveBeenCalled();
    finish();
  },
);

it("retains sidebar page scopes, group expansion and per-surface scroll through header navigation", async () => {
  const project = resource(EntityKind.PROJECT, "Retained project");
  const value = mountSidebar({
    stateful: true,
    projects: (page) => ({
      resources: [project],
      nextPageToken: page ? undefined : "project-next",
    }),
    sessions: (request) => ({
      sessions: [],
      nextPageToken: request.pageToken
        ? undefined
        : request.projectId
          ? "group-next"
          : "global-next",
    }),
  });
  fireEvent.click(
    await screen.findByRole("button", {
      name: `Retained project`,
    }),
  );
  await waitFor(() =>
    expect(screen.queryByText("Loading Retained project sessions…")).toBeNull(),
  );
  reach("Retained project sessions");
  await waitFor(() =>
    expect(
      value.sessionRequests.some(
        (request) =>
          request.projectId === project.id &&
          request.pageToken === "group-next",
      ),
    ).toBe(true),
  );
  reach("projects");
  await waitFor(() => expect(value.projectRequests).toContain("project-next"));
  reach("sessions");
  await waitFor(() =>
    expect(
      value.sessionRequests.some(
        (request) => request.pageToken === "global-next",
      ),
    ).toBe(true),
  );
  const list = window.document.querySelector<HTMLElement>(".sidebar-list")!;
  list.scrollTop = 180;
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  expectHeaderActions(false);
  expect(list.scrollTop).toBe(0);
  list.scrollTop = 45;
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expectHeaderActions(true);
  expect(list.scrollTop).toBe(180);
  expect(
    screen
      .getByRole("button", {
        name: `Retained project`,
      })
      .getAttribute("aria-expanded"),
  ).toBe("true");
  expect(
    screen.queryByRole("button", {
      name: /First project page|First session page|First page of Retained project sessions|Next page/,
    }),
  ).toBeNull();
  await waitFor(() => {
    expect(value.projectRequests).toContain("project-next");
    expect(
      value.sessionRequests.some(
        (request) => !request.projectId && request.pageToken === "global-next",
      ),
    ).toBe(true);
    expect(
      value.sessionRequests.some(
        (request) =>
          request.projectId === project.id &&
          request.pageToken === "group-next",
      ),
    ).toBe(true);
  });
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  expect(list.scrollTop).toBe(45);
  expect(value.openSession).not.toHaveBeenCalled();
});

it.each([false, true])(
  "keeps global recovery visible with General Chat collapsed (expired=%s)",
  async (expired) => {
    const parent = newRequestId();
    const retained = resource(
      EntityKind.SESSION,
      "Visible fallback after failure",
      parent,
      { workspace: "local" },
    );
    let failed = false;
    const value = mountSidebar({
      projects: () => ({ resources: [] }),
      sessions: ({ projectId }) => {
        expect(projectId).toBe("");
        if (failed)
          throw new ConnectError(
            "Fixture read failure",
            expired ? Code.OutOfRange : Code.Unavailable,
            undefined,
            expired
              ? [
                  {
                    desc: ErrorDetailSchema,
                    value: create(ErrorDetailSchema, {
                      code: FailureCode.CursorExpired,
                    }),
                  },
                ]
              : [],
          );
        return { sessions: [retained] };
      },
    });
    const row = await screen.findByRole("button", {
      name: /Visible fallback after failure/,
    });
    value.setProps({ selectedSessionId: retained.id });
    row.focus();
    const general = screen.getByRole("button", { name: "General Chat" });
    fireEvent.click(general);
    expect(general.getAttribute("aria-expanded")).toBe("false");
    failed = true;
    await act(async () => {
      await value.client.invalidateQueries({ refetchType: "active" });
    });
    expect(
      await screen.findByText(
        expired
          ? "sessions: The list cursor expired."
          : "Could not refresh sessions. Previous data is shown.",
      ),
    ).toBeTruthy();
    const recovery = screen.getByRole("button", {
      name: expired ? "Reload sessions list" : "Retry global sessions",
    });
    expect(recovery.closest(".sidebar-general-chat")).toBeNull();
    expect(
      screen.getByRole("button", { name: /Visible fallback after failure/ }),
    ).toBe(row);
    expect(row.getAttribute("aria-current")).toBe("true");
    expect(document.activeElement).toBe(row);
    const catalogReads = [...value.projectRequests];
    const globalReads = value.sessionRequests.length;
    failed = false;
    fireEvent.click(recovery);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", {
          name: /Retry global sessions|Reload sessions list/,
        }),
      ).toBeNull(),
    );
    expect(value.sessionRequests).toHaveLength(globalReads + 1);
    expect(value.sessionRequests.at(-1)?.pageToken).toBe("");
    expect(value.projectRequests).toEqual(catalogReads);
    expect(general.getAttribute("aria-expanded")).toBe("false");
    expect(
      screen.getByRole("button", { name: /Visible fallback after failure/ }),
    ).toBe(row);
    expect(value.openSession).not.toHaveBeenCalled();
  },
);

it("keeps the General Chat header shortcut independent of disclosure and project locks", async () => {
  const setDrawerOpen = vi.fn();
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
    props: { projectSelectionBlocked: true, setDrawerOpen },
  });
  const toggle = await screen.findByRole("button", { name: "General Chat" });
  const group = toggle.parentElement!;
  const shortcut = within(group).getByRole("button", { name: "New Chat" });
  expect(toggle.contains(shortcut)).toBe(false);
  expect(shortcut.getAttribute("title")).toBe("New Chat");
  expect(shortcut).toHaveProperty("disabled", false);
  for (const expanded of [true, false, true]) {
    expect(toggle.getAttribute("aria-expanded")).toBe(String(expanded));
    fireEvent.click(shortcut);
    expect(toggle.getAttribute("aria-expanded")).toBe(String(expanded));
    expect(setDrawerOpen).toHaveBeenLastCalledWith(false);
    fireEvent.click(toggle);
  }
  expect(value.newGeneralChat).toHaveBeenCalledTimes(3);
  expect(value.newSession).not.toHaveBeenCalled();
  await act(() => i18n.changeLanguage("ko"));
  expect(within(group).getByRole("button", { name: "새 일반 대화" })).toBe(
    shortcut,
  );
  expect(shortcut.getAttribute("title")).toBe("새 일반 대화");
});

it("routes a retained Sidechat through its original parent hint without independent Fork adoption", async () => {
  const parent = newRequestId();
  const child = resource(EntityKind.SESSION, "Retained child", "", {
    workspace: "local",
    outcome: "stopped",
    archive: "active",
    fork: {
      source_session_id: parent,
      sidechat_parent_snapshot: { configuration: {} },
    },
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [child] }),
  });
  fireEvent.click(
    await screen.findByRole("button", {
      name: /Local computer Retained child/,
    }),
  );
  expect(value.openSession).toHaveBeenCalledExactlyOnceWith(
    child.id,
    parent,
    "Retained child",
  );
});

it("nests loaded Fork/Sidechat generations in accepted order and preserves typed activation", async () => {
  const p = resource(EntityKind.SESSION, "Parent", "", {
    workspace: "general-chat",
    outcome: "running",
    archive: "active",
  });
  const f = resource(EntityKind.SESSION, "Fork child", "", {
    workspace: "general-chat",
    outcome: "stopped",
    archive: "active",
    fork: { source_session_id: p.id },
  });
  const child = resource(EntityKind.SESSION, "Side child", "", {
    workspace: "general-chat",
    outcome: "stopped",
    archive: "active",
    fork: {
      source_session_id: p.id,
      sidechat_parent_snapshot: { configuration: {} },
    },
  });
  const g = resource(EntityKind.SESSION, "Grandchild", "", {
    workspace: "general-chat",
    outcome: "stopped",
    archive: "active",
    fork: { source_session_id: f.id },
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [g, child, p, f] }),
  });
  const grand = await screen.findByRole("button", {
    name: /General Chat Grandchild/,
  });
  const parent = value.container.querySelector(
    `[data-conversation-node="${p.id}"]`,
  )!;
  expect(
    [
      ...[...parent.children].find((node) => node.tagName === "UL")!.children,
    ].map((n) => (n as HTMLElement).dataset.conversationNode),
  ).toEqual([child.id, f.id]);
  expect(
    parent
      .querySelector(`[data-conversation-node="${f.id}"] > ul > li`)
      ?.getAttribute("data-conversation-node"),
  ).toBe(g.id);
  expect(
    value.container.querySelectorAll(`[data-session-id="${g.id}"]`),
  ).toHaveLength(1);
  const disclosure = screen.getByRole("button", {
    name: "Conversations beneath Parent",
  });
  grand.focus();
  fireEvent.click(disclosure);
  expect(disclosure.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(disclosure);
  expect(
    screen.queryByRole("button", { name: /General Chat Grandchild/ }),
  ).toBeNull();
  expect(value.openSession).not.toHaveBeenCalled();
  value.setProps({ selectedSessionId: g.id });
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: /General Chat Grandchild/ }),
    ).toBeTruthy(),
  );
  expect(disclosure.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(disclosure);
  value.setProps({ selectedSessionId: g.id, selectedSessionActivation: 1 });
  await waitFor(() =>
    expect(disclosure.getAttribute("aria-expanded")).toBe("true"),
  );
  fireEvent.click(
    screen.getByRole("button", { name: /Side Chat.*Side child/ }),
  );
  expect(value.openSession).toHaveBeenLastCalledWith(
    child.id,
    p.id,
    "Side child",
  );
  fireEvent.click(
    screen.getByRole("button", { name: /Independent Fork.*Fork child/ }),
  );
  expect(value.openSession).toHaveBeenLastCalledWith(f.id);
});
it("regroups an orphan when its accepted parent arrives, retaining focus and collapse across refresh", async () => {
  const p = resource(EntityKind.SESSION, "Later parent", "", {
    workspace: "general-chat",
    outcome: "running",
    archive: "active",
  });
  const f = resource(EntityKind.SESSION, "Retained orphan", "", {
    workspace: "general-chat",
    outcome: "running",
    archive: "active",
    fork: { source_session_id: p.id },
  });
  let rows = [f];
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: rows }),
  });
  const orphan = await screen.findByRole("button", { name: /Retained orphan/ });
  orphan.focus();
  rows = [f, p];
  await act(() => value.client.invalidateQueries());
  await screen.findByRole("button", {
    name: "Conversations beneath Later parent",
  });
  const moved = screen.getByRole("button", {
    name: /General Chat Retained orphan/,
  });
  expect(document.activeElement).toBe(moved);
  expect(
    moved
      .closest("li")
      ?.parentElement?.parentElement?.getAttribute("data-conversation-node"),
  ).toBe(p.id);
  expect(value.openSession).not.toHaveBeenCalled();
  const disclosure = screen.getByRole("button", {
    name: "Conversations beneath Later parent",
  });
  fireEvent.click(disclosure);
  await act(() => value.client.invalidateQueries());
  expect(disclosure.getAttribute("aria-expanded")).toBe("false");
  rows = [f];
  await act(() => value.client.invalidateQueries());
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: /General Chat Retained orphan/ }),
    ).toBeTruthy(),
  );
  expect(
    value.container.querySelectorAll(`[data-session-id="${f.id}"]`),
  ).toHaveLength(1);
});
it("keeps distinct conversation glyphs, localized type descriptions and independent workspace/status", async () => {
  const parent = newRequestId();
  const rows = [
    resource(EntityKind.SESSION, "Same", "", {
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
    }),
    resource(EntityKind.SESSION, "Same", "", {
      workspace: "local",
      outcome: "running",
      archive: "active",
    }),
    resource(EntityKind.SESSION, "Same", "", {
      workspace: "worktree",
      outcome: "running",
      archive: "active",
    }),
    resource(EntityKind.SESSION, "Same", "", {
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
      fork: { source_session_id: parent },
    }),
    resource(EntityKind.SESSION, "Same", "", {
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
      fork: {
        source_session_id: parent,
        sidechat_parent_snapshot: { configuration: {} },
      },
    }),
  ];
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: rows }),
  });
  await screen.findByRole("button", {
    name: /Work session.*Local computer Same/,
  });
  const glyph = (id: string) =>
    value.container.querySelector(`[data-session-id="${id}"] > svg`)!.innerHTML;
  expect(glyph(rows[1]!.id)).toBe(glyph(rows[2]!.id));
  expect(
    new Set([rows[0], rows[1], rows[3], rows[4]].map((r) => glyph(r!.id))).size,
  ).toBe(4);
  const icons = rows.map((r) => glyph(r.id));
  await act(() => i18n.changeLanguage("ko"));
  expect(
    screen.getByRole("button", { name: /사이드 채팅.*Same/ }),
  ).toBeTruthy();
  expect(
    screen.getAllByRole("button", { name: /작업 세션.*Same/ }),
  ).toHaveLength(2);
  expect(rows.map((r) => glyph(r.id))).toEqual(icons);
  await act(() => i18n.changeLanguage("en"));
});

it("retains semantic deep nesting after indentation caps and roots every cyclic row", async () => {
  const rows: Resource[] = [];
  for (let i = 0; i < 8; i++)
    rows.push(
      resource(EntityKind.SESSION, `Level ${i}`, "", {
        workspace: "general-chat",
        outcome: "running",
        archive: "active",
        ...(i ? { fork: { source_session_id: rows[i - 1]!.id } } : {}),
      }),
    );
  const a = resource(EntityKind.SESSION, "Cycle A", "", {
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
    }),
    b = resource(EntityKind.SESSION, "Cycle B", "", {
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
    });
  a.documentJson = encode({
    name: "Cycle A",
    workspace: "general-chat",
    outcome: "running",
    archive: "active",
    fork: { source_session_id: b.id },
  });
  b.documentJson = encode({
    name: "Cycle B",
    workspace: "general-chat",
    outcome: "running",
    archive: "active",
    fork: { source_session_id: a.id },
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [...rows, a, b] }),
  });
  await screen.findByRole("button", { name: /General Chat Level 7/ });
  const last = value.container.querySelector(
    `[data-conversation-node="${rows[7]!.id}"]`,
  )!;
  expect((last.firstElementChild as HTMLElement).style.paddingLeft).toBe(
    "36px",
  );
  let depth = 0;
  for (
    let element = last.parentElement;
    element;
    element = element.parentElement
  )
    if (
      element.tagName === "UL" &&
      element.className !== "sidebar-conversation-forest"
    )
      depth++;
  expect(depth).toBe(7);
  for (const row of [a, b])
    expect(
      value.container.querySelector(`[data-conversation-node="${row.id}"]`)
        ?.parentElement?.className,
    ).toBe("sidebar-conversation-forest");
});

it.each(["project", "general-chat", "sidechat"])(
  "clears retained %s row selection on creation pages and restores it on return",
  async (kind) => {
    const project = resource(EntityKind.PROJECT, "Retained project");
    const parent = resource(EntityKind.SESSION, "Retained parent", "", {
      workspace: "general-chat",
      outcome: "running",
      archive: "active",
    });
    const retained = resource(
      EntityKind.SESSION,
      "Retained selection",
      kind === "project" ? project.id : "",
      {
        workspace: kind === "project" ? "worktree" : "general-chat",
        outcome: "running",
        archive: "active",
        ...(kind === "sidechat"
          ? {
              fork: {
                source_session_id: parent.id,
                sidechat_parent_snapshot: { configuration: {} },
              },
            }
          : {}),
      },
    );
    const rows = kind === "sidechat" ? [parent, retained] : [retained];
    const value = mountSidebar({
      projects: () => ({ resources: kind === "project" ? [project] : [] }),
      sessions: ({ projectId }) => ({
        sessions: projectId && projectId !== project.id ? [] : rows,
      }),
      props: { selectedSessionId: retained.id },
      stateful: true,
    });
    const row = await screen.findByRole("button", {
      name: /Retained selection/,
    });
    expect(row.getAttribute("aria-current")).toBe("true");
    const list = value.container.querySelector<HTMLElement>(".sidebar-list")!;
    list.scrollTop = 45;
    fireEvent.scroll(list);
    const action = value.container.querySelector<HTMLButtonElement>(
      ".sidebar-new-session",
    )!;
    fireEvent.click(action);
    expect(action.getAttribute("aria-current")).toBe("page");
    expect(
      value.container.querySelectorAll(
        '.sidebar-session-row[aria-current="true"]',
      ),
    ).toHaveLength(0);
    expect(screen.getByRole("button", { name: /Retained selection/ })).toBe(
      row,
    );
    fireEvent.click(screen.getAllByRole("button", { name: "New Chat" })[0]);
    expect(
      screen
        .getAllByRole("button", { name: "New Chat" })[0]
        .getAttribute("aria-current"),
    ).toBe("page");
    expect(
      value.container.querySelectorAll(
        '.sidebar-session-row[aria-current="true"]',
      ),
    ).toHaveLength(0);
    value.setSurface(Surface.Sessions);
    expect(screen.getByRole("button", { name: /Retained selection/ })).toBe(
      row,
    );
    expect(row.getAttribute("aria-current")).toBe("true");
    expect(list.scrollTop).toBe(45);
    expect(value.openSession).not.toHaveBeenCalled();
    expect(value.newSession).toHaveBeenCalledOnce();
    expect(value.newGeneralChat).toHaveBeenCalledOnce();
    if (kind === "project") {
      value.setProps({ projectSelectionBlocked: true });
      fireEvent.click(
        value.container.querySelector<HTMLButtonElement>(
          ".sidebar-project-group .sidebar-project-new-session",
        )!,
      );
      expect(value.newSession).toHaveBeenCalledOnce();
      expect(row.getAttribute("aria-current")).toBe("true");
    }
  },
);

it("keeps the drawer close affordance outside the compact Home header and creation block", () => {
  const setDrawerOpen = vi.fn();
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
    props: { setDrawerOpen },
  });
  for (const surface of [
    Surface.Sessions,
    Surface.NewSession,
    Surface.NewGeneralChat,
  ]) {
    value.setProps({ surface });
    const pane = value.container.querySelector(".sidebar-pane")!;
    const header = pane.querySelector(".sidebar-header")!;
    const session = pane.querySelector(".sidebar-new-session")!;
    const chat = pane.querySelector(".sidebar-new-general-chat")!;
    const close = pane.querySelector<HTMLButtonElement>(
      ".sidebar-drawer-close",
    )!;
    expect(header.nextElementSibling).toBe(session);
    expect(session.nextElementSibling).toBe(chat);
    expect(chat.nextElementSibling).toBe(close);
    expect(close.nextElementSibling).toBe(pane.querySelector(".sidebar-list"));
    fireEvent.click(close);
    expect(setDrawerOpen).toHaveBeenLastCalledWith(false);
    expect(value.newSession).not.toHaveBeenCalled();
    expect(value.newGeneralChat).not.toHaveBeenCalled();
  }
  value.setProps({ surface: Surface.Search });
  const header = value.container.querySelector(".sidebar-header")!;
  expect(header.nextElementSibling?.className).toBe("sidebar-drawer-close");
});

it("places one decorative response indicator before independent execution/archive glyphs and translates its description", async () => {
  const name = "A long response-waiting session ".repeat(8);
  const session = resource(EntityKind.SESSION, name, "", {
    workspace: "local",
    outcome: "running",
    archive: "active",
    awaiting_user_response: true,
  });
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [session] }),
  });
  const row = await screen.findByRole("button", {
    name: /Waiting for your response/,
  });
  const statuses = row.querySelector(".sidebar-statuses")!;
  expect(statuses.querySelectorAll(".sidebar-response-waiting")).toHaveLength(
    1,
  );
  expect(
    statuses.firstElementChild?.classList.contains("sidebar-response-waiting"),
  ).toBe(true);
  expect(statuses.querySelector(".sidebar-status-running")).toBeTruthy();
  const indicator = statuses.firstElementChild!;
  expect(indicator.getAttribute("aria-hidden")).toBe("true");
  expect(indicator.getAttribute("focusable")).toBe("false");
  expect(indicator.hasAttribute("tabindex")).toBe(false);
  expect(row.querySelector(".sidebar-session-title")?.textContent).toBe(name);
  act(() => row.focus());
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Waiting for your response")).toBeTruthy();
  expect(card.querySelectorAll(".sidebar-response-waiting")).toHaveLength(0);
  const reads = value.sessionRequests.length;
  try {
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.Korean);
    });
    expect(row.getAttribute("aria-label")).toContain("응답 대기 중");
    expect(screen.getByRole("tooltip")).toBe(card);
    expect(within(card).getByText("응답 대기 중")).toBeTruthy();
    expect(document.activeElement).toBe(row);
    expect(value.sessionRequests).toHaveLength(reads);
    fireEvent.click(row);
    expect(value.openSession).toHaveBeenCalledExactlyOnceWith(session.id);
  } finally {
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.English);
    });
  }
});

it("keeps unknown execution/archive question marks independent and hides unsupported response metadata", async () => {
  const sessions = [true, false, "true", undefined].map((waiting, index) =>
    resource(EntityKind.SESSION, `Unknown status ${index}`, "", {
      workspace: "general-chat",
      awaiting_user_response: waiting,
    }),
  );
  mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions }),
  });
  for (let index = 0; index < sessions.length; index++) {
    const row = await screen.findByRole("button", {
      name: new RegExp(`Unknown status ${index}`),
    });
    expect(row.querySelectorAll(".sidebar-status-unknown")).toHaveLength(2);
    expect(row.querySelectorAll(".sidebar-response-waiting")).toHaveLength(
      index === 0 ? 1 : 0,
    );
  }
});

it.each([Surface.PullRequests, Surface.Inbox])(
  "returns pane focus to the current or first rail item before collapsing %s",
  (surface) => {
    const view = mountSidebar({
      projects: () => ({ resources: [] }),
      sessions: () => ({ sessions: [] }),
      props: { surface },
    });
    const pane = view.container.querySelector<HTMLDialogElement>(
      ".sidebar-pane-dialog",
    )!;
    const originalPane = pane;
    const control = pane.querySelector<HTMLButtonElement>("button")!;
    control.focus();
    const rail = view.container.querySelector(".sidebar-rail")!;
    const target =
      rail.querySelector("[aria-current='page']") ??
      rail.querySelector(".sidebar-rail-button");
    let hiddenAtFocus: boolean | undefined;
    target!.addEventListener("focus", () => {
      hiddenAtFocus = pane.hidden;
    });
    view.setProps({ collapsed: true });
    expect(document.activeElement).toBe(target);
    expect(hiddenAtFocus).toBe(false);
    expect(pane.hidden).toBe(true);
    expect(pane.inert).toBe(true);
    expect(pane.getAttribute("aria-hidden")).toBe("true");
    view.setProps({ collapsed: false });
    expect(view.container.querySelector(".sidebar-pane-dialog")).toBe(
      originalPane,
    );
    expect(document.activeElement).toBe(target);
  },
);

it("preserves an external composer selection through collapse and expansion", () => {
  const view = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
  });
  const composer = document.createElement("textarea");
  view.container.append(composer);
  composer.value = "Unsent draft";
  composer.focus();
  composer.setSelectionRange(2, 8);
  view.setProps({ collapsed: true });
  view.setProps({ collapsed: false });
  expect(document.activeElement).toBe(composer);
  expect(composer.value).toBe("Unsent draft");
  expect(composer.selectionStart).toBe(2);
  expect(composer.selectionEnd).toBe(8);
});

it("closes logical activity and focus before the visual interval, then settles once", () => {
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
  const original = getComputedStyle;
  let rendered = 288;
  vi.stubGlobal("getComputedStyle", (element: Element) =>
    element.classList.contains("app")
      ? {
          gridTemplateColumns: `${53 + rendered}px 1000px`,
          transitionDuration: "0.18s",
        }
      : original(element),
  );
  const changed = vi.fn();
  const view = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [] }),
    withApp: true,
    props: { motionReady: true, presentationChanged: changed },
  });
  const pane = view.container.querySelector<HTMLDialogElement>(
    ".sidebar-pane-dialog",
  )!;
  const content = pane.querySelector(".sidebar-pane");
  pane.querySelector<HTMLButtonElement>("button")!.focus();
  view.setProps({ collapsed: true });
  expect(pane.hidden).toBe(false);
  expect(pane.inert).toBe(true);
  expect(pane.getAttribute("aria-hidden")).toBe("true");
  expect(document.activeElement).toBe(
    view.container.querySelector(".sidebar-rail [aria-current='page']"),
  );
  act(() => vi.advanceTimersByTime(240));
  expect(pane.hidden).toBe(true);
  expect(pane.open).toBe(false);
  rendered = 0;
  view.setProps({ collapsed: false });
  expect(pane.hidden).toBe(false);
  expect(pane.inert).toBe(false);
  expect(changed.mock.calls.at(-1)![0].settled).toBe(false);
  act(() => vi.advanceTimersByTime(240));
  expect(pane.querySelector(".sidebar-pane")).toBe(content);
  expect(changed.mock.calls.at(-1)![0].settled).toBe(true);
  expect(document.activeElement).toBe(
    view.container.querySelector(".sidebar-rail [aria-current='page']"),
  );
  rendered = 288;
  view.setProps({ collapsed: true });
  expect(pane.hidden).toBe(false);
  view.setProps({ motionScope: {} });
  expect(pane.hidden).toBe(true);
  expect(changed.mock.calls.at(-1)![0]).toMatchObject({
    settled: true,
    focusAllowed: false,
  });
  rendered = 0;
  view.setProps({ collapsed: false });
  expect(changed.mock.calls.at(-1)![0].settled).toBe(false);
  view.setProps({ surface: Surface.Search });
  expect(pane.hidden).toBe(false);
  expect(changed.mock.calls.at(-1)![0]).toMatchObject({
    settled: true,
    focusAllowed: false,
  });
  view.unmount();
});

function railNames() {
  return [...document.querySelectorAll(".sidebar-rail > button")].map(
    (button) => button.getAttribute("aria-label"),
  );
}
const emptyLists = {
  projects: () => ({ resources: [] }),
  sessions: () => ({ sessions: [] }),
};

it("shows Usage only in More by default and opens the existing Usage destination from it", async () => {
  const value = mountSidebar({ ...emptyLists });
  await screen.findByRole("button", { name: "Sessions" });
  expect(railNames().slice(0, 4)).toEqual([
    "Sessions",
    "Pull requests",
    "Schedules",
    "More",
  ]);
  expect(railNames()).not.toContain("Usage");
  expect(railNames().at(-1)).toBe("Settings");
  const more = screen.getByRole("button", { name: "More" });
  expect(more.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(more);
  const popover = screen.getByRole("dialog", { name: "More" });
  expect(more.getAttribute("aria-expanded")).toBe("true");
  expect(
    within(popover).getByRole("button", { name: "Pin Usage" }),
  ).toBeTruthy();
  expect(document.activeElement).toBe(
    within(popover).getByRole("button", { name: "Usage" }),
  );
  fireEvent.click(within(popover).getByRole("button", { name: "Usage" }));
  expect(value.navigate).toHaveBeenCalledExactlyOnceWith(Surface.Usage);
  expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  expect(document.activeElement).toBe(more);
});

it("pins and unpins Usage without navigating and keeps the active destination named correctly", async () => {
  const bridge = memoryNavigationBridge();
  const update = vi.spyOn(bridge, "update");
  const value = mountSidebar({
    ...emptyLists,
    stateful: true,
    navigation: bridge,
  });
  await screen.findByRole("button", { name: "Sessions" });
  value.setSurface(Surface.Usage);
  const more = screen.getByRole("button", { name: "More" });
  expect(more.getAttribute("aria-current")).toBe("page");
  fireEvent.click(more);
  fireEvent.click(screen.getByRole("button", { name: "Pin Usage" }));
  await waitFor(() =>
    expect(railNames().slice(0, 5)).toEqual([
      "Sessions",
      "Pull requests",
      "Usage",
      "Schedules",
      "More",
    ]),
  );
  expect(update).toHaveBeenCalledExactlyOnceWith(true, 1);
  expect(value.navigate).not.toHaveBeenCalled();
  expect(
    document
      .querySelector(".sidebar-rail > button[aria-label='Usage']")
      ?.getAttribute("aria-current"),
  ).toBe("page");
  expect(more.getAttribute("aria-current")).toBeNull();
  // The row stays in More while pinned so the same control can unpin.
  const popover = screen.getByRole("dialog", { name: "More" });
  expect(within(popover).getByRole("button", { name: "Usage" })).toBeTruthy();
  fireEvent.click(within(popover).getByRole("button", { name: "Unpin Usage" }));
  await waitFor(() => expect(railNames()).not.toContain("Usage"));
  expect(value.navigate).not.toHaveBeenCalled();
  expect(more.getAttribute("aria-current")).toBe("page");
});

it("dismisses More with Escape or an outside press, restores opener focus and supports arrow keys", async () => {
  mountSidebar({ ...emptyLists });
  await screen.findByRole("button", { name: "Sessions" });
  const more = screen.getByRole("button", { name: "More" });
  fireEvent.click(more);
  const usage = screen.getByRole("button", { name: "Usage" });
  const pin = screen.getByRole("button", { name: "Pin Usage" });
  expect(document.activeElement).toBe(usage);
  fireEvent.keyDown(usage, { key: "ArrowRight" });
  expect(document.activeElement).toBe(pin);
  fireEvent.keyDown(pin, { key: "ArrowDown" });
  expect(document.activeElement).toBe(usage);
  fireEvent.keyDown(usage, { key: "End" });
  expect(document.activeElement).toBe(pin);
  fireEvent.keyDown(pin, { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  expect(document.activeElement).toBe(more);
  fireEvent.click(more);
  fireEvent.click(more);
  expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  fireEvent.click(more);
  expect(screen.getByRole("dialog", { name: "More" })).toBeTruthy();
  fireEvent.pointerDown(document.body);
  expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  expect(document.activeElement).toBe(more);
});

it("localizes More and the pin controls in Korean", async () => {
  mountSidebar({ ...emptyLists, navigation: memoryNavigationBridge() });
  await screen.findByRole("button", { name: "Sessions" });
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  try {
    fireEvent.click(screen.getByRole("button", { name: "더보기" }));
    const popover = screen.getByRole("dialog", { name: "더보기" });
    fireEvent.click(
      within(popover).getByRole("button", { name: "사용량 고정" }),
    );
    await within(popover).findByRole("button", { name: "사용량 고정 해제" });
  } finally {
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.English);
    });
  }
});

it("keeps the confirmed pin state and offers explicit reload after read or uncertain save failures", async () => {
  const stored = memoryNavigationBridge();
  let failRead = true;
  let failUpdate = false;
  const bridge: NavigationBridge = {
    ...stored,
    read: vi.fn(async () => {
      if (failRead) throw new Error("storage detail");
      return stored.read();
    }),
    update: vi.fn(async (pinned, revision) => {
      if (failUpdate) throw new Error("storage detail");
      return stored.update(pinned, revision);
    }),
  };
  mountSidebar({ ...emptyLists, navigation: bridge });
  await screen.findByRole("button", { name: "Sessions" });
  fireEvent.click(screen.getByRole("button", { name: "More" }));
  const popover = await screen.findByRole("dialog", { name: "More" });
  await within(popover).findByRole("alert");
  const pin = within(popover).getByRole("button", { name: "Pin Usage" });
  expect(pin.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(pin);
  expect(bridge.update).not.toHaveBeenCalled();
  failRead = false;
  fireEvent.click(
    within(popover).getByRole("button", {
      name: "Reload navigation preference",
    }),
  );
  await waitFor(() => expect(within(popover).queryByRole("alert")).toBeNull());
  expect(document.activeElement).toBe(pin);
  expect(pin.getAttribute("aria-disabled")).toBeNull();
  failUpdate = true;
  fireEvent.click(pin);
  await within(popover).findByRole("alert");
  // An uncertain save never shows the requested pin as committed.
  expect(railNames()).not.toContain("Usage");
  expect(
    within(popover).getByRole("button", { name: "Pin Usage" }),
  ).toBeTruthy();
  fireEvent.click(pin);
  expect(bridge.update).toHaveBeenCalledTimes(1);
  failUpdate = false;
  fireEvent.click(
    within(popover).getByRole("button", {
      name: "Reload navigation preference",
    }),
  );
  await waitFor(() => expect(within(popover).queryByRole("alert")).toBeNull());
  expect(railNames()).not.toContain("Usage");
});

it("re-places an open More popover when a failure notice makes it taller", async () => {
  const stored = memoryNavigationBridge();
  let failUpdate = false;
  const bridge: NavigationBridge = {
    ...stored,
    update: vi.fn(async (pinned, revision) => {
      if (failUpdate) throw new Error("storage detail");
      return stored.update(pinned, revision);
    }),
  };
  // jsdom has no layout: model a 300px viewport, a 60px popover that grows to
  // 200px with the notice, and an opener whose top sits near the bottom edge.
  vi.stubGlobal("innerHeight", 300);
  const height = vi
    .spyOn(HTMLElement.prototype, "offsetHeight", "get")
    .mockImplementation(function (this: HTMLElement) {
      return this.matches(".sidebar-more-popover")
        ? this.querySelector(".navigation-preference-notice")
          ? 200
          : 60
        : 0;
    });
  try {
    mountSidebar({ ...emptyLists, navigation: bridge });
    await screen.findByRole("button", { name: "Sessions" });
    const more = screen.getByRole("button", { name: "More" });
    vi.spyOn(more, "getBoundingClientRect").mockReturnValue({
      top: 200,
      right: 44,
      bottom: 244,
      left: 0,
      width: 44,
      height: 44,
      x: 0,
      y: 200,
      toJSON: () => ({}),
    });
    fireEvent.click(more);
    const popover = await screen.findByRole("dialog", { name: "More" });
    expect(popover.style.top).toBe("200px");
    failUpdate = true;
    fireEvent.click(within(popover).getByRole("button", { name: "Pin Usage" }));
    await within(popover).findByRole("alert");
    // The notice must stay inside the viewport: 300 - 200 - 4.
    expect(popover.style.top).toBe("96px");
    failUpdate = false;
    fireEvent.click(
      within(popover).getByRole("button", {
        name: "Reload navigation preference",
      }),
    );
    await waitFor(() =>
      expect(within(popover).queryByRole("alert")).toBeNull(),
    );
    expect(popover.style.top).toBe("200px");
  } finally {
    height.mockRestore();
  }
});

it("dismisses More when Shift+Tab moves focus to the opener but not for a press on it", async () => {
  mountSidebar({ ...emptyLists });
  await screen.findByRole("button", { name: "Sessions" });
  const more = screen.getByRole("button", { name: "More" });
  fireEvent.click(more);
  const usage = screen.getByRole("button", { name: "Usage" });
  expect(document.activeElement).toBe(usage);
  // A press on the opener blurs the list, and the click that follows toggles it.
  fireEvent.pointerDown(more);
  fireEvent.focusOut(usage, { relatedTarget: more });
  expect(screen.getByRole("dialog", { name: "More" })).toBeTruthy();
  fireEvent.click(more);
  expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  // Keyboard focus movement from the first row to the opener is an exit.
  fireEvent.click(more);
  fireEvent.keyDown(screen.getByRole("button", { name: "Usage" }), {
    key: "Tab",
    shiftKey: true,
  });
  more.focus();
  fireEvent.focusOut(screen.getByRole("button", { name: "Usage" }), {
    relatedTarget: more,
  });
  expect(screen.queryByRole("dialog", { name: "More" })).toBeNull();
  expect(more.getAttribute("aria-expanded")).toBe("false");
  expect(document.activeElement).toBe(more);
});

it("exposes one current-page destination while Usage is active and More is open", async () => {
  const bridge = memoryNavigationBridge();
  const value = mountSidebar({
    ...emptyLists,
    stateful: true,
    navigation: bridge,
  });
  await screen.findByRole("button", { name: "Sessions" });
  value.setSurface(Surface.Usage);
  const current = () =>
    [
      ...document.querySelectorAll(
        ".sidebar-rail [aria-current='page'], .sidebar-more-popover [aria-current]",
      ),
    ].map((node) => node.getAttribute("aria-label"));
  const more = screen.getByRole("button", { name: "More" });
  fireEvent.click(more);
  const popover = screen.getByRole("dialog", { name: "More" });
  // Unpinned: the More button is the only current destination.
  expect(current()).toEqual(["More"]);
  expect(
    within(popover).getByRole("button", { name: "Usage" }).dataset.current,
  ).toBe("true");
  fireEvent.click(within(popover).getByRole("button", { name: "Pin Usage" }));
  await waitFor(() => expect(railNames()).toContain("Usage"));
  // Pinned: the Usage rail button is the only current destination.
  expect(current()).toEqual(["Usage"]);
});

// Destination windows: a platform-primary modified click on a destination
// control sends its typed route to the native host and changes nothing here.
it("opens each rail and header destination in a new window on a primary modified click and keeps plain clicks in place", async () => {
  const destination = fakeDestinationWindow();
  const setDrawerOpen = vi.fn();
  const value = mountSidebar({
    ...emptyLists,
    destination,
    props: { setDrawerOpen, navigateHeader: vi.fn() },
  });
  const header = vi.fn();
  value.setProps({ navigateHeader: header });
  const cases: [string, Surface][] = [
    ["Sessions", Surface.Sessions],
    ["Pull requests", Surface.PullRequests],
    ["Schedules", Surface.Schedules],
    ["Inbox", Surface.Inbox],
    ["Search", Surface.Search],
  ];
  for (const [name, surface] of cases) {
    const control = await screen.findByRole("button", { name });
    const event = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      ...primaryClick,
    });
    act(() => {
      control.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
    expect(destination.opened).toHaveBeenLastCalledWith({ surface });
  }
  expect(destination.opened).toHaveBeenCalledTimes(cases.length);
  expect(value.navigate).not.toHaveBeenCalled();
  expect(header).not.toHaveBeenCalled();
  expect(setDrawerOpen).not.toHaveBeenCalled();
  // Plain clicks keep the existing current-window navigation.
  for (const [name, surface] of cases) {
    fireEvent.click(screen.getByRole("button", { name }), { detail: 1 });
    const handler = surface === Surface.Inbox || surface === Surface.Search
      ? header
      : value.navigate;
    expect(handler).toHaveBeenLastCalledWith(surface);
  }
  expect(destination.opened).toHaveBeenCalledTimes(cases.length);
});

it("never opens a destination window for Alt, Control plus Meta or keyboard activation", async () => {
  const destination = fakeDestinationWindow();
  const value = mountSidebar({ ...emptyLists, destination });
  const control = await screen.findByRole("button", { name: "Schedules" });
  for (const init of [
    { ctrlKey: true, altKey: true, detail: 1 },
    { ctrlKey: true, metaKey: true, detail: 1 },
    { metaKey: true, detail: 1 },
    { ctrlKey: true, detail: 0 },
    { detail: 1 },
  ])
    fireEvent.click(control, init);
  expect(destination.opened).not.toHaveBeenCalled();
  expect(value.navigate).toHaveBeenCalledTimes(5);
});

it("uses Command as the primary modifier on macOS and Control elsewhere", async () => {
  const destination = fakeDestinationWindow();
  const value = mountSidebar({ ...emptyLists, destination });
  const control = await screen.findByRole("button", { name: "Schedules" });
  const platform = vi.spyOn(navigator, "platform", "get");
  platform.mockReturnValue("MacIntel");
  fireEvent.click(control, { ctrlKey: true, detail: 1 });
  expect(destination.opened).not.toHaveBeenCalled();
  fireEvent.click(control, { metaKey: true, shiftKey: true, detail: 1 });
  expect(destination.opened).toHaveBeenCalledTimes(1);
  platform.mockReturnValue("Win32");
  fireEvent.click(control, { metaKey: true, detail: 1 });
  expect(destination.opened).toHaveBeenCalledTimes(1);
  fireEvent.click(control, { ctrlKey: true, shiftKey: true, detail: 1 });
  expect(destination.opened).toHaveBeenCalledTimes(2);
  expect(value.navigate).toHaveBeenCalledTimes(2);
  platform.mockRestore();
});

it("opens Session and Sidechat rows at their typed destination without selecting them here", async () => {
  const destination = fakeDestinationWindow();
  const parent = resource(EntityKind.SESSION, "Parent session", "", {
    workspace: "local",
    outcome: "stopped",
    archive: "active",
  });
  const child = resource(EntityKind.SESSION, "Retained child", "", {
    workspace: "local",
    outcome: "stopped",
    archive: "active",
    fork: {
      source_session_id: parent.id,
      sidechat_parent_snapshot: { configuration: {} },
    },
  });
  const setDrawerOpen = vi.fn();
  const value = mountSidebar({
    projects: () => ({ resources: [] }),
    sessions: () => ({ sessions: [parent, child] }),
    destination,
    props: { setDrawerOpen },
  });
  fireEvent.click(
    await screen.findByRole("button", { name: /Local computer Parent session/ }),
    primaryClick,
  );
  expect(destination.opened).toHaveBeenLastCalledWith({
    surface: Surface.Sessions,
    session: parent.id,
  });
  fireEvent.click(
    screen.getByRole("button", { name: /Local computer Retained child/ }),
    { ...primaryClick, shiftKey: true },
  );
  expect(destination.opened).toHaveBeenLastCalledWith({
    surface: Surface.Sessions,
    session: parent.id,
    sidechat: child.id,
  });
  expect(value.openSession).not.toHaveBeenCalled();
  expect(setDrawerOpen).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: /Local computer Parent session/ }),
    { detail: 1 },
  );
  expect(value.openSession).toHaveBeenCalledExactlyOnceWith(parent.id);
});

it("opens Usage in a new window from More and leaves the menu open", async () => {
  const destination = fakeDestinationWindow();
  const value = mountSidebar({ ...emptyLists, destination });
  await screen.findByRole("button", { name: "Sessions" });
  fireEvent.click(screen.getByRole("button", { name: "More" }));
  const popover = screen.getByRole("dialog", { name: "More" });
  fireEvent.click(within(popover).getByRole("button", { name: "Usage" }), primaryClick);
  expect(destination.opened).toHaveBeenCalledExactlyOnceWith({
    surface: Surface.Usage,
  });
  expect(value.navigate).not.toHaveBeenCalled();
  expect(screen.getByRole("dialog", { name: "More" })).toBeTruthy();
  // Pinning is an action control and never opens a window.
  fireEvent.click(
    within(popover).getByRole("button", { name: "Pin Usage" }),
    primaryClick,
  );
  expect(destination.opened).toHaveBeenCalledTimes(1);
});

it("never opens a destination window from action controls", async () => {
  const destination = fakeDestinationWindow();
  const value = mountSidebar({ ...emptyLists, destination });
  await screen.findByRole("button", { name: "Sessions" });
  fireEvent.click(screen.getByRole("button", { name: "New session" }), primaryClick);
  fireEvent.click(screen.getByRole("button", { name: "New project" }), primaryClick);
  expect(destination.opened).not.toHaveBeenCalled();
  expect(value.newSession).toHaveBeenCalledOnce();
  expect(value.newProject).toHaveBeenCalledOnce();
});
