// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  EntityKind,
  InboxService,
  InboxViewSchema,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  ScheduleService,
  SessionService,
  SystemService,
  newRequestId,
  type InboxView,
  type Resource,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import { resetProductRouteClaim } from "./product-route";
import { ReloadShortcutOwner } from "./reload-shortcut";
import { ShortcutPreferenceProvider } from "./shortcut-preference-controller";
import { SettingsCategory } from "./settings-category";
import { Surface } from "./surface";
import { UsageTab } from "./usage-tab";

const retainedSession = (name: string, fork?: Record<string, unknown>) => {
  const id = newRequestId();
  return create(ResourceSchema, {
    id,
    sessionId: id,
    kind: EntityKind.SESSION,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      name,
      workspace: "general-chat",
      outcome: "stopped",
      archive: "active",
      dispatch: "paused",
      recovery: "none",
      ...(fork ? { fork } : {}),
    }),
  });
};

function fixture() {
  const session = retainedSession("Retained session");
  const other = retainedSession("Other session");
  const sidechat = retainedSession("Side question", {
    source_session_id: session.id,
    sidechat_parent_snapshot: { revision: "1" },
  });
  const repository = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.REPOSITORY,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ name: "Restored repository" }),
  });
  const schedule = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SCHEDULE,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      definition: {
        name: "Restored schedule",
        enabled: false,
        prompt: "Retained prompt",
        project_id: newRequestId(),
        agent_id: newRequestId(),
        machine_id: newRequestId(),
        workspace: "worktree",
        mode: "plan",
        cron: "0 9 * * 1-5",
        timezone: "Asia/Seoul",
        overlap: "wait",
      },
      configuration_revision: 2,
      created_by: newRequestId(),
      last_occurrence: 9,
      next_run_at: "2026-09-28T00:00:00Z",
    }),
  });
  const executionId = newRequestId();
  const inboxEntry = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: session.id,
    kind: EntityKind.INBOX,
    revision: 1n,
    schemaVersion: 1,
    createdAt: "2026-09-29T00:15:00Z",
    documentJson: encode({
      source: "execution-terminal",
      source_id: executionId,
      read_state: "read",
      terminal: { outcome: "succeeded" },
    }),
  });
  const inboxView: InboxView = create(InboxViewSchema, {
    entry: inboxEntry,
    session,
  });
  const resources = [session, other, sidechat, repository];
  const mutated = vi.fn();
  const reads = {
    resource: vi.fn(),
    schedule: vi.fn(),
    inbox: vi.fn(),
  };
  const failure: { code?: Code } = {};
  const preferences = create(NotificationPreferencesSchema, {
    revision: 1n,
    interactions: true,
    terminals: false,
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        version: "0.1.0",
        protocolVersion: 2,
        capabilities: [],
      }),
    });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session, other] }),
      listQueue: () => ({ inputs: [] }),
      enqueueInput: (request) => {
        mutated("enqueueInput", request);
        throw new ConnectError("unexpected", Code.Internal);
      },
      createSession: (request) => {
        mutated("createSession", request);
        throw new ConnectError("unexpected", Code.Internal);
      },
      controlSession: (request) => {
        mutated("controlSession", request);
        throw new ConnectError("unexpected", Code.Internal);
      },
    });
    router.service(ResourceService, {
      getResource: (request) => {
        reads.resource(request);
        if (failure.code)
          throw new ConnectError("read failed", failure.code);
        return {
          resource: resources.find(
            (row) => row.kind === request.kind && row.id === request.id,
          ),
        };
      },
      getSnapshot: (request) => ({
        resources: [
          resources.find((row) => row.id === request.filter?.sessionId) ??
            session,
        ],
        cursor: "snapshot",
      }),
      listResources: (request) => ({
        resources:
          request.filter?.kind === EntityKind.REPOSITORY ? [repository] : [],
      }),
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) => {
          if (context.signal.aborted) resolve();
          else
            context.signal.addEventListener("abort", () => resolve(), {
              once: true,
            });
        });
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [inboxView] }),
      getInboxEntry: (request) => {
        reads.inbox(request);
        if (failure.code)
          throw new ConnectError("read failed", failure.code);
        if (request.id !== inboxEntry.id)
          throw new ConnectError("missing", Code.NotFound);
        return { view: inboxView };
      },
      getNotificationPreferences: () => ({ preferences }),
    });
    router.service(ScheduleService, {
      listSchedules: () => ({ schedules: [schedule] }),
      getSchedule: (request) => {
        reads.schedule(request);
        if (failure.code)
          throw new ConnectError("read failed", failure.code);
        if (request.id !== schedule.id)
          throw new ConnectError("missing", Code.NotFound);
        return { schedule };
      },
      listScheduleOccurrences: () => ({ occurrences: [] }),
    });
  });
  return {
    transport,
    session,
    other,
    sidechat,
    repository,
    schedule,
    inboxEntry,
    mutated,
    reads,
    failure,
  };
}

const rail = () =>
  within(screen.getByRole("navigation", { name: "Primary navigation" }));
async function openUsage() {
  fireEvent.click(rail().getByRole("button", { name: "More" }));
  fireEvent.click(await rail().findByRole("button", { name: "Usage" }));
}
const route = (query: string) =>
  window.history.replaceState(null, "", `/index.html${query}#main`);
const search = () => new URLSearchParams(window.location.search);
// A simulated document reload: the URL survives, all renderer state is new.
const newDocument = () => resetProductRouteClaim();

afterEach(() => {
  vi.restoreAllMocks();
});

describe("route mirroring", () => {
  it("replaces the URL for every top-level screen without adding history entries or dropping the hash and native parameters", async () => {
    window.history.replaceState(null, "", "/index.html?native=1#main");
    const length = window.history.length;
    const push = vi.spyOn(window.history, "pushState");
    const value = fixture();
    render(<App transport={value.transport} />);
    await screen.findByRole("button", { name: "New session" });
    expect(search().get("screen")).toBe(Surface.Sessions);
    const visit = async (open: () => void | Promise<void>, expected: Surface) => {
      await open();
      await waitFor(() => expect(search().get("screen")).toBe(expected));
      expect(search().get("native")).toBe("1");
      expect(window.location.hash).toBe("#main");
    };
    const home = () =>
      fireEvent.click(rail().getByRole("button", { name: "Sessions" }));
    await visit(
      () => fireEvent.click(screen.getByRole("button", { name: "Search" })),
      Surface.Search,
    );
    await visit(home, Surface.Sessions);
    await visit(
      () => fireEvent.click(screen.getByRole("button", { name: "Inbox" })),
      Surface.Inbox,
    );
    await visit(home, Surface.Sessions);
    await visit(
      () =>
        fireEvent.click(rail().getByRole("button", { name: "Pull requests" })),
      Surface.PullRequests,
    );
    await visit(
      () => fireEvent.click(rail().getByRole("button", { name: "Schedules" })),
      Surface.Schedules,
    );
    await visit(openUsage, Surface.Usage);
    await visit(home, Surface.Sessions);
    fireEvent.click(screen.getByRole("button", { name: "New session" }));
    await waitFor(() =>
      expect(search().get("screen")).toBe(Surface.NewSession),
    );
    expect(window.history.length).toBe(length);
    expect(push).not.toHaveBeenCalled();
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("stores the Usage tab and no stale selection on selection-free screens", async () => {
    const value = fixture();
    render(<App transport={value.transport} />);
    await screen.findByRole("button", { name: "New session" });
    await openUsage();
    fireEvent.click(await screen.findByRole("tab", { name: "Model prices" }));
    await waitFor(() => expect(search().get("tab")).toBe(UsageTab.Prices));
    expect(search().get("screen")).toBe(Surface.Usage);
    expect(search().has("item")).toBe(false);
    expect(search().has("session")).toBe(false);
    fireEvent.click(rail().getByRole("button", { name: "Sessions" }));
    await waitFor(() => expect(search().get("screen")).toBe(Surface.Sessions));
    expect(search().has("tab")).toBe(false);
  });
});

describe("route restore after a document reload", () => {
  it("reopens the selected Session from a fresh read without flashing Home", async () => {
    const value = fixture();
    route(`?screen=sessions&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    // The pending route never renders the Home welcome content.
    expect(screen.queryByText("Your sessions, in one place")).toBeNull();
    await waitFor(() =>
      expect(
        screen.getAllByText("Retained session").length,
      ).toBeGreaterThan(0),
    );
    expect(screen.queryByText("Your sessions, in one place")).toBeNull();
    expect(value.reads.resource).toHaveBeenCalled();
    await waitFor(() =>
      expect(search().get("session")).toBe(value.session.id),
    );
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("reopens a Sidechat tab under its original parent", async () => {
    const value = fixture();
    route(
      `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
    );
    render(<App transport={value.transport} />);
    const tab = await screen.findByRole("tab", { name: /Side question/ });
    await waitFor(() => expect(tab.getAttribute("aria-selected")).toBe("true"));
    expect(tab.id.startsWith(`session-tab-${value.session.id}-`)).toBe(true);
    await waitFor(() =>
      expect(search().get("sidechat")).toBe(value.sidechat.id),
    );
    expect(search().get("session")).toBe(value.session.id);
  });

  it("opens the parent only when the Sidechat is not its child", async () => {
    const value = fixture();
    route(
      `?screen=sessions&session=${value.session.id}&sidechat=${value.other.id}`,
    );
    render(<App transport={value.transport} />);
    await waitFor(() =>
      expect(screen.getAllByText("Retained session").length).toBeGreaterThan(0),
    );
    expect(screen.queryByRole("tab", { name: /Side question/ })).toBeNull();
    await waitFor(() => expect(search().has("sidechat")).toBe(false));
    expect(search().get("session")).toBe(value.session.id);
  });

  it("reopens the Schedule, Pull requests repository, Inbox item, Settings category and Usage tab", async () => {
    const value = fixture();
    route(`?screen=schedules&item=${value.schedule.id}`);
    const view = render(<App transport={value.transport} />);
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    await waitFor(() => expect(search().get("item")).toBe(value.schedule.id));
    view.unmount();

    newDocument();
    route(`?screen=pull-requests&item=${value.repository.id}`);
    const prs = render(<App transport={value.transport} />);
    expect(
      await screen.findByRole("region", { name: "Query options" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(search().get("item")).toBe(value.repository.id),
    );
    prs.unmount();

    newDocument();
    route(`?screen=inbox&item=${value.inboxEntry.id}`);
    const inbox = render(<App transport={value.transport} />);
    await waitFor(() =>
      expect(value.reads.inbox.mock.calls.length).toBeGreaterThan(1),
    );
    await waitFor(() => expect(search().get("item")).toBe(value.inboxEntry.id));
    expect(search().get("screen")).toBe(Surface.Inbox);
    inbox.unmount();

    newDocument();
    route(`?screen=settings&category=${SettingsCategory.Backups}`);
    const settings = render(<App transport={value.transport} />);
    await waitFor(() =>
      expect(search().get("category")).toBe(SettingsCategory.Backups),
    );
    expect(search().get("screen")).toBe(Surface.Settings);
    settings.unmount();

    newDocument();
    route(`?screen=usage&tab=${UsageTab.Prices}`);
    render(<App transport={value.transport} />);
    expect(
      (await screen.findByRole("tab", { name: "Model prices" })).getAttribute(
        "aria-selected",
      ),
    ).toBe("true");
    expect(search().get("tab")).toBe(UsageTab.Prices);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it.each([
    [Surface.Search, "Search conversations"],
    [Surface.NewSession, "First message"],
  ])("returns to the selection-free %s screen", async (surface, name) => {
    const value = fixture();
    route(`?screen=${surface}&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    expect(await screen.findByRole("textbox", { name })).toBeTruthy();
    expect(search().get("screen")).toBe(surface);
    expect(search().has("item")).toBe(false);
  });

  it("returns to New General Chat", async () => {
    const value = fixture();
    route(`?screen=${Surface.NewGeneralChat}`);
    render(<App transport={value.transport} />);
    expect(
      await screen.findByRole("textbox", { name: "First message" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(search().get("screen")).toBe(Surface.NewGeneralChat),
    );
  });

  it("survives StrictMode double mounting", async () => {
    const value = fixture();
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(
      <StrictMode>
        <App transport={value.transport} />
      </StrictMode>,
    );
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
  });
});

describe("route fallbacks", () => {
  it("falls back to Sessions Home for an unknown screen", async () => {
    const value = fixture();
    route(`?screen=nowhere&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    expect(
      await screen.findByText("Your sessions, in one place"),
    ).toBeTruthy();
    await waitFor(() => expect(search().get("screen")).toBe(Surface.Sessions));
    expect(search().has("session")).toBe(false);
    expect(value.reads.resource).not.toHaveBeenCalled();
  });

  it.each([
    ["malformed", () => "?screen=schedules&item=not-an-id"],
    ["deleted", () => `?screen=schedules&item=${newRequestId()}`],
  ])(
    "clears a %s Schedule selection and keeps the Schedules list",
    async (_name, query) => {
      const value = fixture();
      route(query());
      render(<App transport={value.transport} />);
      expect(
        await screen.findByText("Select a schedule from the sidebar to inspect its details and retained history."),
      ).toBeTruthy();
      await waitFor(() =>
        expect(search().get("screen")).toBe(Surface.Schedules),
      );
      await waitFor(() => expect(search().has("item")).toBe(false));
    },
  );

  it("clears a deleted Session selection and shows Sessions Home", async () => {
    const value = fixture();
    route(`?screen=sessions&session=${newRequestId()}`);
    render(<App transport={value.transport} />);
    expect(
      await screen.findByText("Your sessions, in one place"),
    ).toBeTruthy();
    await waitFor(() => expect(search().has("session")).toBe(false));
    expect(search().get("screen")).toBe(Surface.Sessions);
  });

  it("clears an inaccessible Pull requests repository and Inbox entry", async () => {
    const value = fixture();
    route(`?screen=pull-requests&item=${newRequestId()}`);
    const first = render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(search().get("screen")).toBe(Surface.PullRequests);
    expect(screen.queryByRole("region", { name: "Query options" })).toBeNull();
    first.unmount();
    newDocument();
    route(`?screen=inbox&item=${newRequestId()}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(search().get("screen")).toBe(Surface.Inbox);
  });

  it("ignores a selection from another connection identity", async () => {
    const value = fixture();
    const first = {
      pairingAuthority: { endpoint: "http://127.0.0.1:46310", serverId: newRequestId() },
      currentDeviceId: newRequestId(),
    };
    route(`?screen=schedules&item=${value.schedule.id}`);
    const view = render(<App transport={value.transport} {...first} />);
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    // A different server/device identity gets a fresh shell and no inherited route.
    view.rerender(
      <App
        transport={value.transport}
        pairingAuthority={{
          endpoint: "http://127.0.0.1:46311",
          serverId: newRequestId(),
        }}
        currentDeviceId={newRequestId()}
      />,
    );
    expect(
      await screen.findByText("Select a schedule from the sidebar to inspect its details and retained history."),
    ).toBeTruthy();
    await waitFor(() => expect(search().has("item")).toBe(false));
  });

  it("keeps the Repository selection for the screen's own retry after a retryable read failure", async () => {
    const value = fixture();
    value.failure.code = Code.Unavailable;
    route(`?screen=pull-requests&item=${value.repository.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    // The mounted screen owns its existing retry and error state.
    await waitFor(() =>
      expect(search().get("item")).toBe(value.repository.id),
    );
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("opens the Session so its own retry state shows after a retryable read failure", async () => {
    const value = fixture();
    value.failure.code = Code.Unavailable;
    route(`?screen=sessions&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await waitFor(() =>
      expect(search().get("session")).toBe(value.session.id),
    );
    expect(screen.queryByText("Your sessions, in one place")).toBeNull();
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("does not reopen a Schedule whose read failed", async () => {
    const value = fixture();
    value.failure.code = Code.Unavailable;
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(search().get("screen")).toBe(Surface.Schedules);
  });
});

describe("pending routes", () => {
  it("keeps the route pending until the same connection becomes ready", async () => {
    const value = fixture();
    route(`?screen=schedules&item=${value.schedule.id}`);
    const view = render(
      <App transport={value.transport} connectionReady={false} />,
    );
    await act(async () => {});
    await screen.findByText("Select a schedule from the sidebar to inspect its details and retained history.");
    expect(value.reads.schedule).not.toHaveBeenCalled();
    // The original route stays in the URL while the connection is unavailable.
    expect(search().get("item")).toBe(value.schedule.id);
    view.rerender(<App transport={value.transport} connectionReady />);
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    expect(value.reads.schedule).toHaveBeenCalled();
    expect(search().get("item")).toBe(value.schedule.id);
  });

  it("lets deliberate navigation supersede a pending selection", async () => {
    const value = fixture();
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} connectionReady={false} />);
    fireEvent.click(
      await rail().findByRole("button", { name: "Pull requests" }),
    );
    await waitFor(() =>
      expect(search().get("screen")).toBe(Surface.PullRequests),
    );
    expect(search().has("item")).toBe(false);
    expect(value.reads.schedule).not.toHaveBeenCalled();
  });
});

describe("reload shortcut remount", () => {
  it("reloads once and the remounted renderer returns to the same screen and item", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Win32");
    const value = fixture();
    const reload = vi.fn();
    const bridge = {
      read: async () => ({ revision: 0, overrides: {}, problem: null }),
      update: vi.fn(async () => ({})),
      subscribe: async () => () => {},
    };
    route(`?screen=schedules&item=${value.schedule.id}`);
    const shell = () => (
      <ShortcutPreferenceProvider bridge={bridge}>
        <ReloadShortcutOwner reload={reload} />
        <App transport={value.transport} />
      </ShortcutPreferenceProvider>
    );
    const first = render(shell());
    await screen.findByRole("heading", { name: "Restored schedule" });
    await act(async () => {});
    const event = new KeyboardEvent("keydown", {
      key: "r",
      code: "KeyR",
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    act(() => {
      window.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
    expect(reload).toHaveBeenCalledTimes(1);
    const before = window.location.search;
    first.unmount();
    newDocument();
    expect(window.location.search).toBe(before);
    render(shell());
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    expect(search().get("screen")).toBe(Surface.Schedules);
    expect(search().get("item")).toBe(value.schedule.id);
    expect(value.mutated).not.toHaveBeenCalled();
  });
});
