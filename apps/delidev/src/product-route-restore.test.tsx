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
  SearchService,
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
import { NavigationIntentSource } from "./navigation-intent";
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
  const otherRepository = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.REPOSITORY,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ name: "Chosen repository" }),
  });
  const scheduleOf = (name: string) =>
    create(ResourceSchema, {
      id: newRequestId(),
      kind: EntityKind.SCHEDULE,
      revision: 3n,
      schemaVersion: 1,
      documentJson: encode({
        definition: {
          name,
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
  const schedule = scheduleOf("Restored schedule");
  const otherSchedule = scheduleOf("Chosen schedule");
  const inboxOf = (owner: Resource) => {
    const entry = create(ResourceSchema, {
      id: newRequestId(),
      sessionId: owner.id,
      kind: EntityKind.INBOX,
      revision: 1n,
      schemaVersion: 1,
      createdAt: "2026-09-29T00:15:00Z",
      documentJson: encode({
        source: "execution-terminal",
        source_id: newRequestId(),
        read_state: "read",
        terminal: { outcome: "succeeded" },
      }),
    });
    const view: InboxView = create(InboxViewSchema, {
      entry,
      session: owner,
    });
    return { entry, view };
  };
  const { entry: inboxEntry, view: inboxView } = inboxOf(session);
  const { entry: otherInboxEntry, view: otherInboxView } = inboxOf(other);
  const resources = [session, other, sidechat, repository, otherRepository];
  // Reads of a held ID wait until `hold` releases them, so a test can answer a
  // restore read after the user acted.
  const held = new Map<string, Promise<void>>();
  const hold = (id: string) => {
    let release = () => {};
    held.set(
      id,
      new Promise<void>((resolve) => {
        release = resolve;
      }),
    );
    return release;
  };
  const mutated = vi.fn();
  const reads = {
    resource: vi.fn(),
    schedule: vi.fn(),
    inbox: vi.fn(),
  };
  // `id` limits the injected failure to reads of one ID.
  const failure: { code?: Code; id?: string } = {};
  const failing = (id: string) =>
    failure.code !== undefined && (!failure.id || failure.id === id);
  // Like the server, a read of an ID that belongs to another entity kind is an
  // invalid argument, not a missing resource.
  const kindOf = new Map<string, EntityKind>([
    ...resources.map((row) => [row.id, row.kind] as const),
    [schedule.id, EntityKind.SCHEDULE],
    [otherSchedule.id, EntityKind.SCHEDULE],
    [inboxEntry.id, EntityKind.INBOX],
    [otherInboxEntry.id, EntityKind.INBOX],
  ]);
  const rejectWrongKind = (id: string, kind: EntityKind) => {
    const actual = kindOf.get(id);
    if (actual !== undefined && actual !== kind)
      throw new ConnectError("wrong kind", Code.InvalidArgument);
  };
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
      getResource: async (request) => {
        reads.resource(request);
        await held.get(request.id);
        if (failing(request.id))
          throw new ConnectError("read failed", failure.code);
        rejectWrongKind(request.id, request.kind);
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
          request.filter?.kind === EntityKind.REPOSITORY
            ? [repository, otherRepository]
            : [],
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
      listInbox: () => ({ entries: [inboxView, otherInboxView] }),
      getInboxEntry: async (request) => {
        reads.inbox(request);
        await held.get(request.id);
        if (failing(request.id))
          throw new ConnectError("read failed", failure.code);
        rejectWrongKind(request.id, EntityKind.INBOX);
        const view = [inboxView, otherInboxView].find(
          (row) => row.entry?.id === request.id,
        );
        if (!view) throw new ConnectError("missing", Code.NotFound);
        return { view };
      },
      getNotificationPreferences: () => ({ preferences }),
    });
    router.service(SearchService, {
      searchConversations: () => ({
        hits: [
          {
            sessionName: "Searched session",
            message: create(ResourceSchema, {
              id: newRequestId(),
              sessionId: other.id,
              kind: EntityKind.MESSAGE,
              revision: 1n,
              schemaVersion: 1,
              documentJson: encode({
                role: "assistant",
                text: "Match in Searched session",
                state: "completed",
              }),
            }),
          },
        ],
      }),
    });
    router.service(ScheduleService, {
      listSchedules: () => ({ schedules: [schedule, otherSchedule] }),
      getSchedule: async (request) => {
        reads.schedule(request);
        await held.get(request.id);
        if (failing(request.id))
          throw new ConnectError("read failed", failure.code);
        rejectWrongKind(request.id, EntityKind.SCHEDULE);
        const found = [schedule, otherSchedule].find(
          (row) => row.id === request.id,
        );
        if (!found) throw new ConnectError("missing", Code.NotFound);
        return { schedule: found };
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
    otherRepository,
    schedule,
    otherSchedule,
    inboxEntry,
    inboxView,
    otherInboxEntry,
    hold,
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
    const visit = async (open: () => unknown, expected: Surface) => {
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
      expect(screen.getAllByText("Retained session").length).toBeGreaterThan(0),
    );
    expect(screen.queryByText("Your sessions, in one place")).toBeNull();
    expect(value.reads.resource).toHaveBeenCalled();
    await waitFor(() => expect(search().get("session")).toBe(value.session.id));
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
    await waitFor(() => expect(search().get("item")).toBe(value.repository.id));
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
    expect(await screen.findByText("Your sessions, in one place")).toBeTruthy();
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
        await screen.findByText(
          "Select a schedule from the sidebar to inspect its details and retained history.",
        ),
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
    expect(await screen.findByText("Your sessions, in one place")).toBeTruthy();
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
      pairingAuthority: {
        endpoint: "http://127.0.0.1:46310",
        serverId: newRequestId(),
      },
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
      await screen.findByText(
        "Select a schedule from the sidebar to inspect its details and retained history.",
      ),
    ).toBeTruthy();
    await waitFor(() => expect(search().has("item")).toBe(false));
  });

  it("ignores another connection's route when StrictMode double-invokes the Shell initializer", async () => {
    const value = fixture();
    const identity = (endpoint: string) => ({
      pairingAuthority: { endpoint, serverId: newRequestId() },
      currentDeviceId: newRequestId(),
    });
    const first = identity("http://127.0.0.1:46310");
    const second = identity("http://127.0.0.1:46311");
    route(`?screen=schedules&item=${value.schedule.id}`);
    const view = render(
      <StrictMode>
        <App transport={value.transport} {...first} />
      </StrictMode>,
    );
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    // The URL still names the first connection's Schedule. The second
    // connection's Shell parses it at most as a rejected, side-effect-free read.
    value.reads.schedule.mockClear();
    view.rerender(
      <StrictMode>
        <App transport={value.transport} {...second} />
      </StrictMode>,
    );
    expect(
      await screen.findByText(
        "Select a schedule from the sidebar to inspect its details and retained history.",
      ),
    ).toBeTruthy();
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(value.reads.schedule).not.toHaveBeenCalled();
  });

  it("keeps the Repository selection for the screen's own retry after a retryable read failure", async () => {
    const value = fixture();
    value.failure.code = Code.Unavailable;
    route(`?screen=pull-requests&item=${value.repository.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    // The mounted screen owns its existing retry and error state.
    await waitFor(() => expect(search().get("item")).toBe(value.repository.id));
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("opens the Session so its own retry state shows after a retryable read failure", async () => {
    const value = fixture();
    value.failure.code = Code.Unavailable;
    route(`?screen=sessions&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await waitFor(() => expect(search().get("session")).toBe(value.session.id));
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

// Every failure a restore read can return, classified once. A definitive
// rejection clears the selection to the requested screen's list; a transient
// failure keeps it (or, for a Schedule, which needs the fresh resource, clears
// it so the list's own retry applies).
const definitiveCodes: Array<[string, Code]> = [
  ["invalid_argument", Code.InvalidArgument],
  ["not_found", Code.NotFound],
  ["permission_denied", Code.PermissionDenied],
  ["unauthenticated", Code.Unauthenticated],
];
const transientCodes: Array<[string, Code]> = [
  ["failed_precondition", Code.FailedPrecondition],
  ["unavailable", Code.Unavailable],
  ["deadline_exceeded", Code.DeadlineExceeded],
  ["canceled", Code.Canceled],
  ["internal", Code.Internal],
];

describe("route failure classification", () => {
  const cases = [
    ...definitiveCodes.map(([name, code]) => [name, code, true] as const),
    ...transientCodes.map(([name, code]) => [name, code, false] as const),
  ];

  it.each(cases)("%s on a Session read", async (_name, code, definitive) => {
    const value = fixture();
    value.failure.code = code;
    route(`?screen=sessions&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    if (definitive) {
      expect(
        await screen.findByText("Your sessions, in one place"),
      ).toBeTruthy();
      await waitFor(() => expect(search().has("session")).toBe(false));
    } else {
      await waitFor(() =>
        expect(search().get("session")).toBe(value.session.id),
      );
      expect(screen.queryByText("Your sessions, in one place")).toBeNull();
    }
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it.each(cases)("%s on a Sidechat read", async (_name, code, definitive) => {
    const value = fixture();
    value.failure.code = code;
    // Only the Sidechat child fails: the verified parent opens either way.
    value.failure.id = value.sidechat.id;
    route(
      `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
    );
    render(<App transport={value.transport} />);
    await waitFor(() =>
      expect(screen.getAllByText("Retained session").length).toBeGreaterThan(0),
    );
    if (definitive) {
      // A definitively rejected child is dropped in favor of its parent.
      expect(screen.queryByRole("tab", { name: /Side question/ })).toBeNull();
      await waitFor(() => expect(search().has("sidechat")).toBe(false));
    } else {
      // A retryable failure keeps the Sidechat selection: the child tab is
      // mounted so its own retry and error state runs, and the URL keeps it.
      await waitFor(() =>
        expect(search().get("sidechat")).toBe(value.sidechat.id),
      );
    }
    expect(search().get("session")).toBe(value.session.id);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  describe("a Sidechat restored during a transient outage", () => {
    // Collects every presentation string of every tab for the whole test, so a
    // brief flash of an internal ID cannot go unnoticed.
    const watchTabLabels = () => {
      const seen = new Set<string>();
      const collect = () => {
        for (const tab of screen.queryAllByRole("tab")) {
          seen.add(tab.textContent ?? "");
          seen.add(tab.getAttribute("title") ?? "");
          seen.add(tab.getAttribute("aria-label") ?? "");
        }
        for (const close of screen.queryAllByRole("button"))
          seen.add(close.getAttribute("aria-label") ?? "");
      };
      const observer = new MutationObserver(collect);
      observer.observe(window.document.body, {
        subtree: true,
        childList: true,
        attributes: true,
        characterData: true,
      });
      return { seen, collect, stop: () => observer.disconnect() };
    };

    it.each([
      ["the Sidechat read", "child"],
      ["the parent read", "parent"],
    ] as const)(
      "labels the tab with a product label instead of its ID when %s fails, then shows the loaded name",
      async (_name, failing) => {
        const value = fixture();
        value.failure.code = Code.Unavailable;
        if (failing === "child") value.failure.id = value.sidechat.id;
        route(
          `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
        );
        const labels = watchTabLabels();
        render(<App transport={value.transport} />);
        await waitFor(() =>
          expect(search().get("sidechat")).toBe(value.sidechat.id),
        );
        // The pane loads the Session through the stream and names the tab.
        expect(
          await screen.findByRole("tab", { name: /Side question/ }),
        ).toBeTruthy();
        labels.collect();
        labels.stop();
        expect(labels.seen.size).toBeGreaterThan(0);
        for (const label of labels.seen)
          expect(label).not.toContain(value.sidechat.id);
        expect(value.mutated).not.toHaveBeenCalled();
      },
    );

    it("ends a placeholder tab whose Session is not a Sidechat of the requested parent", async () => {
      const value = fixture();
      value.failure.code = Code.Unavailable;
      route(
        `?screen=sessions&session=${value.session.id}&sidechat=${value.other.id}`,
      );
      const labels = watchTabLabels();
      render(<App transport={value.transport} />);
      await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
      await waitFor(() =>
        expect(
          screen
            .queryAllByRole("tab")
            .filter((tab) => !/Conversation/.test(tab.textContent ?? "")),
        ).toHaveLength(0),
      );
      labels.stop();
      for (const label of labels.seen)
        expect(label).not.toContain(value.other.id);
      expect(value.mutated).not.toHaveBeenCalled();
    });
  });

  it.each(transientCodes)(
    "%s on the parent keeps the requested Sidechat too",
    async (_name, code) => {
      const value = fixture();
      value.failure.code = code;
      route(
        `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
      );
      render(<App transport={value.transport} />);
      await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
      await waitFor(() =>
        expect(search().get("sidechat")).toBe(value.sidechat.id),
      );
      expect(search().get("session")).toBe(value.session.id);
      expect(value.mutated).not.toHaveBeenCalled();
    },
  );

  it.each(cases)(
    "%s on an Inbox entry read",
    async (_name, code, definitive) => {
      const value = fixture();
      value.failure.code = code;
      route(`?screen=inbox&item=${value.inboxEntry.id}`);
      render(<App transport={value.transport} />);
      await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
      if (definitive)
        await waitFor(() => expect(search().has("item")).toBe(false));
      else
        await waitFor(() =>
          expect(search().get("item")).toBe(value.inboxEntry.id),
        );
      expect(search().get("screen")).toBe(Surface.Inbox);
      expect(value.mutated).not.toHaveBeenCalled();
    },
  );

  it.each(cases)(
    "%s on a Schedule read: the selection is cleared",
    async (_name, code) => {
      const value = fixture();
      value.failure.code = code;
      route(`?screen=schedules&item=${value.schedule.id}`);
      render(<App transport={value.transport} />);
      await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
      await waitFor(() => expect(search().has("item")).toBe(false));
      expect(search().get("screen")).toBe(Surface.Schedules);
    },
  );

  it.each(cases)("%s on a repository read", async (_name, code, definitive) => {
    const value = fixture();
    value.failure.code = code;
    route(`?screen=pull-requests&item=${value.repository.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    if (definitive)
      await waitFor(() => expect(search().has("item")).toBe(false));
    else
      await waitFor(() =>
        expect(search().get("item")).toBe(value.repository.id),
      );
    expect(search().get("screen")).toBe(Surface.PullRequests);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("restores the Settings category and Usage tab without any read", async () => {
    const value = fixture();
    value.failure.code = Code.InvalidArgument;
    route(`?screen=usage&tab=${UsageTab.Overview}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(search().get("screen")).toBe(Surface.Usage));
    expect(value.reads.resource).not.toHaveBeenCalled();
    expect(value.reads.schedule).not.toHaveBeenCalled();
    expect(value.reads.inbox).not.toHaveBeenCalled();
  });
});

describe("wrong-kind route IDs", () => {
  it("clears a Schedule ID supplied as a Pull requests repository", async () => {
    const value = fixture();
    route(`?screen=pull-requests&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(search().get("screen")).toBe(Surface.PullRequests);
    expect(screen.queryByRole("region", { name: "Query options" })).toBeNull();
  });

  it("clears a repository ID supplied as a Session", async () => {
    const value = fixture();
    route(`?screen=sessions&session=${value.repository.id}`);
    render(<App transport={value.transport} />);
    expect(await screen.findByText("Your sessions, in one place")).toBeTruthy();
    await waitFor(() => expect(search().has("session")).toBe(false));
    expect(search().get("screen")).toBe(Surface.Sessions);
  });

  it("clears a repository ID supplied as a Schedule", async () => {
    const value = fixture();
    route(`?screen=schedules&item=${value.repository.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(search().get("screen")).toBe(Surface.Schedules);
  });

  it("clears a Schedule ID supplied as an Inbox entry", async () => {
    const value = fixture();
    route(`?screen=inbox&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
    await waitFor(() => expect(search().has("item")).toBe(false));
    expect(search().get("screen")).toBe(Surface.Inbox);
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
    await screen.findByText(
      "Select a schedule from the sidebar to inspect its details and retained history.",
    );
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

  it("aborts and restarts a restore read when a reconnect bumps the epoch of a retained transport", async () => {
    const value = fixture();
    const release = value.hold(value.schedule.id);
    route(`?screen=schedules&item=${value.schedule.id}`);
    const view = render(
      <App transport={value.transport} connectionReady connectionEpoch={0} />,
    );
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalledTimes(1));
    // The same saved identity reverifies: the transport object is retained and
    // the connection stays ready, but the peer behind it may have changed.
    view.rerender(
      <App transport={value.transport} connectionReady connectionEpoch={1} />,
    );
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalledTimes(2));
    expect(value.reads.schedule).toHaveBeenCalledTimes(2);
    expect(
      screen.queryByRole("heading", { name: "Restored schedule" }),
    ).toBeNull();
    await act(async () => {
      release();
    });
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    // Only the read that started in the current epoch restored the selection.
    expect(search().get("item")).toBe(value.schedule.id);
    expect(value.mutated).not.toHaveBeenCalled();
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

const invalidEnvelopes: [string, (resource: Resource) => void][] = [
  [
    "a zero revision",
    (resource) => {
      resource.revision = 0n;
    },
  ],
  [
    "an unsupported schema version",
    (resource) => {
      resource.schemaVersion = 99;
    },
  ],
];

describe("route envelope validation", () => {
  it.each(invalidEnvelopes)(
    "clears a Session whose read has %s and shows Sessions Home",
    async (_name, corrupt) => {
      const value = fixture();
      corrupt(value.session);
      route(`?screen=sessions&session=${value.session.id}`);
      render(<App transport={value.transport} />);
      expect(
        await screen.findByText("Your sessions, in one place"),
      ).toBeTruthy();
      await waitFor(() => expect(search().has("session")).toBe(false));
      expect(search().get("screen")).toBe(Surface.Sessions);
    },
  );

  it.each(invalidEnvelopes)(
    "opens only the parent when the Sidechat read has %s",
    async (_name, corrupt) => {
      const value = fixture();
      corrupt(value.sidechat);
      route(
        `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
      );
      render(<App transport={value.transport} />);
      await waitFor(() =>
        expect(screen.getAllByText("Retained session").length).toBeGreaterThan(
          0,
        ),
      );
      expect(screen.queryByRole("tab", { name: /Side question/ })).toBeNull();
      await waitFor(() => expect(search().has("sidechat")).toBe(false));
      expect(search().get("session")).toBe(value.session.id);
    },
  );

  it.each(invalidEnvelopes)(
    "clears an Inbox entry whose read has %s",
    async (_name, corrupt) => {
      const value = fixture();
      corrupt(value.inboxView.entry!);
      route(`?screen=inbox&item=${value.inboxEntry.id}`);
      render(<App transport={value.transport} />);
      await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
      await waitFor(() => expect(search().has("item")).toBe(false));
      expect(search().get("screen")).toBe(Surface.Inbox);
      expect(value.mutated).not.toHaveBeenCalled();
    },
  );

  it.each(invalidEnvelopes)(
    "clears a Schedule whose read has %s",
    async (_name, corrupt) => {
      const value = fixture();
      corrupt(value.schedule);
      route(`?screen=schedules&item=${value.schedule.id}`);
      render(<App transport={value.transport} />);
      expect(
        await screen.findByText(
          "Select a schedule from the sidebar to inspect its details and retained history.",
        ),
      ).toBeTruthy();
      await waitFor(() => expect(search().has("item")).toBe(false));
      expect(search().get("screen")).toBe(Surface.Schedules);
    },
  );

  it.each(invalidEnvelopes)(
    "clears a Pull requests repository whose read has %s",
    async (_name, corrupt) => {
      const value = fixture();
      corrupt(value.repository);
      route(`?screen=pull-requests&item=${value.repository.id}`);
      render(<App transport={value.transport} />);
      await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
      await waitFor(() => expect(search().has("item")).toBe(false));
      expect(search().get("screen")).toBe(Surface.PullRequests);
      expect(
        screen.queryByRole("region", { name: "Query options" }),
      ).toBeNull();
    },
  );
});

describe("selection supersedes a slow route restore", () => {
  it("keeps a Schedule chosen while the restore read is slow", async () => {
    const value = fixture();
    const release = value.hold(value.schedule.id);
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    fireEvent.click(await screen.findByText("Chosen schedule"));
    expect(
      await screen.findByRole("heading", { name: "Chosen schedule" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherSchedule.id),
    );
    await act(async () => {
      release();
    });
    await act(async () => {});
    expect(
      screen.getByRole("heading", { name: "Chosen schedule" }),
    ).toBeTruthy();
    expect(
      screen.queryByRole("heading", { name: "Restored schedule" }),
    ).toBeNull();
    expect(search().get("item")).toBe(value.otherSchedule.id);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("keeps a repository chosen while the restore read is slow", async () => {
    const value = fixture();
    const release = value.hold(value.repository.id);
    route(`?screen=pull-requests&item=${value.repository.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    fireEvent.click(await screen.findByText("Chosen repository"));
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherRepository.id),
    );
    await act(async () => {
      release();
    });
    await act(async () => {});
    expect(search().get("item")).toBe(value.otherRepository.id);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("keeps an Inbox entry chosen while the restore read is slow", async () => {
    const value = fixture();
    const release = value.hold(value.inboxEntry.id);
    route(`?screen=inbox&item=${value.inboxEntry.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
    fireEvent.click(await screen.findByText(/Other session/));
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherInboxEntry.id),
    );
    await act(async () => {
      release();
    });
    await act(async () => {});
    expect(search().get("item")).toBe(value.otherInboxEntry.id);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("keeps a Session chosen while the restore read is slow", async () => {
    const value = fixture();
    const release = value.hold(value.session.id);
    route(`?screen=sessions&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    fireEvent.click(await screen.findByText("Other session"));
    await waitFor(() => expect(search().get("session")).toBe(value.other.id));
    await act(async () => {
      release();
    });
    await act(async () => {});
    expect(search().get("session")).toBe(value.other.id);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("keeps a Session chosen while the Sidechat restore read is slow", async () => {
    const value = fixture();
    const release = value.hold(value.sidechat.id);
    route(
      `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
    );
    render(<App transport={value.transport} />);
    await waitFor(() =>
      expect(
        value.reads.resource.mock.calls.some(
          ([request]) => request.id === value.sidechat.id,
        ),
      ).toBe(true),
    );
    fireEvent.click(await screen.findByText("Other session"));
    await waitFor(() => expect(search().get("session")).toBe(value.other.id));
    await act(async () => {
      release();
    });
    await act(async () => {});
    expect(search().get("session")).toBe(value.other.id);
    expect(search().has("sidechat")).toBe(false);
    expect(screen.queryByRole("tab", { name: /Side question/ })).toBeNull();
  });

  it("keeps a Usage tab chosen right after the restored tab opens", async () => {
    const value = fixture();
    route(`?screen=usage&tab=${UsageTab.Prices}`);
    render(<App transport={value.transport} />);
    await screen.findByRole("tab", { name: "Model prices" });
    fireEvent.click(await screen.findByRole("tab", { name: "Overview" }));
    await waitFor(() => expect(search().get("tab")).toBe(UsageTab.Overview));
  });
});

// A read that settles in the same tick as a click, before React has rendered
// the choice or flushed any passive effect, must not override that choice.
const clickAndSettle = async (target: HTMLElement, release: () => void) => {
  await act(async () => {
    fireEvent.click(target);
    release();
  });
  await act(async () => {});
};

describe("navigation intent supersedes a restore settling in the same tick", () => {
  it("keeps a Session chosen as the Session restore read settles", async () => {
    const value = fixture();
    const release = value.hold(value.session.id);
    route(`?screen=sessions&session=${value.session.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await clickAndSettle(await screen.findByText("Other session"), release);
    await waitFor(() => expect(search().get("session")).toBe(value.other.id));
    expect(search().has("sidechat")).toBe(false);
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("keeps a Session chosen as the parent read of a Sidechat restore settles", async () => {
    const value = fixture();
    const release = value.hold(value.session.id);
    route(
      `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
    );
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await clickAndSettle(await screen.findByText("Other session"), release);
    await waitFor(() => expect(search().get("session")).toBe(value.other.id));
    expect(search().has("sidechat")).toBe(false);
    expect(screen.queryByRole("tab", { name: /Side question/ })).toBeNull();
  });

  it("keeps a Session chosen as the child read of a Sidechat restore settles", async () => {
    const value = fixture();
    const release = value.hold(value.sidechat.id);
    route(
      `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
    );
    render(<App transport={value.transport} />);
    await waitFor(() =>
      expect(
        value.reads.resource.mock.calls.some(
          ([request]) => request.id === value.sidechat.id,
        ),
      ).toBe(true),
    );
    await clickAndSettle(await screen.findByText("Other session"), release);
    await waitFor(() => expect(search().get("session")).toBe(value.other.id));
    expect(search().has("sidechat")).toBe(false);
    expect(screen.queryByRole("tab", { name: /Side question/ })).toBeNull();
  });

  it("keeps an Inbox entry chosen as the Inbox restore read settles", async () => {
    const value = fixture();
    const release = value.hold(value.inboxEntry.id);
    route(`?screen=inbox&item=${value.inboxEntry.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
    await clickAndSettle(await screen.findByText(/Other session/), release);
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherInboxEntry.id),
    );
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("keeps a Schedule chosen as the Schedule restore read settles", async () => {
    const value = fixture();
    const release = value.hold(value.schedule.id);
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    await clickAndSettle(await screen.findByText("Chosen schedule"), release);
    expect(
      await screen.findByRole("heading", { name: "Chosen schedule" }),
    ).toBeTruthy();
    expect(
      screen.queryByRole("heading", { name: "Restored schedule" }),
    ).toBeNull();
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherSchedule.id),
    );
  });

  it("keeps a repository chosen as the repository restore read settles", async () => {
    const value = fixture();
    const release = value.hold(value.repository.id);
    route(`?screen=pull-requests&item=${value.repository.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await clickAndSettle(await screen.findByText("Chosen repository"), release);
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherRepository.id),
    );
    expect(value.mutated).not.toHaveBeenCalled();
  });

  it("does not restore a Schedule after New schedule is opened, and canceling the editor shows the list state", async () => {
    const value = fixture();
    const release = value.hold(value.schedule.id);
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    await clickAndSettle(
      await screen.findByRole("button", { name: "New schedule" }),
      release,
    );
    // The creation editor stays open and the stale Schedule never reaches the URL.
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    expect(search().has("item")).toBe(false);
    expect(
      screen.queryByRole("heading", { name: "Restored schedule" }),
    ).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(
      await screen.findByText(
        "Select a schedule from the sidebar to inspect its details and retained history.",
      ),
    ).toBeTruthy();
    expect(
      screen.queryByRole("heading", { name: "Restored schedule" }),
    ).toBeNull();
    expect(search().has("item")).toBe(false);
  });

  it("does not restore a Schedule after the surface changes in the same tick", async () => {
    const value = fixture();
    const release = value.hold(value.schedule.id);
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    await clickAndSettle(
      await rail().findByRole("button", { name: "Pull requests" }),
      release,
    );
    await waitFor(() =>
      expect(search().get("screen")).toBe(Surface.PullRequests),
    );
    expect(search().has("item")).toBe(false);
  });

  it("treats a keyboard activation of a row before the read settles as navigation", async () => {
    const value = fixture();
    const release = value.hold(value.inboxEntry.id);
    route(`?screen=inbox&item=${value.inboxEntry.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
    // A keyboard activation reaches the row handler as a click with no pointer.
    await clickAndSettle(await screen.findByText(/Other session/), release);
    await waitFor(() =>
      expect(search().get("item")).toBe(value.otherInboxEntry.id),
    );
  });

  it("still restores when nothing is chosen while the read is slow", async () => {
    const value = fixture();
    const release = value.hold(value.schedule.id);
    route(`?screen=schedules&item=${value.schedule.id}`);
    render(<App transport={value.transport} />);
    await waitFor(() => expect(value.reads.schedule).toHaveBeenCalled());
    await act(async () => {
      release();
    });
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    expect(search().get("item")).toBe(value.schedule.id);
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

const intentLog = "delidev.product_route.navigation_intent";
const registeredSources = (info: { mock: { calls: unknown[][] } }) =>
  info.mock.calls
    .filter(([message]) => message === intentLog)
    .map(([, detail]) => (detail as { source: NavigationIntentSource }).source);

describe("controls that are not navigation keep a pending selection", () => {
  it("keeps a waiting Schedule selection after a refresh or an unrelated click and key, then restores it when the connection is ready", async () => {
    // jsdom has no native layout observer; the paginated list reads it on refresh.
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    const info = vi.spyOn(console, "info");
    const value = fixture();
    route(`?screen=schedules&item=${value.schedule.id}`);
    const view = render(
      <App transport={value.transport} connectionReady={false} />,
    );
    await act(async () => {});
    await screen.findByText(
      "Select a schedule from the sidebar to inspect its details and retained history.",
    );
    // A retry-style refresh, a click on inert content and selection keys are not
    // navigation: none of them registers an intent or cancels the restore.
    fireEvent.click(await screen.findByRole("button", { name: "Refresh" }));
    fireEvent.click(document.body);
    fireEvent.keyDown(document.body, { key: "Enter" });
    fireEvent.keyDown(document.body, { key: "ArrowDown" });
    fireEvent.keyDown(document.body, { key: "k", ctrlKey: true });
    await act(async () => {});
    expect(registeredSources(info)).toEqual([]);
    expect(search().get("item")).toBe(value.schedule.id);
    expect(value.reads.schedule).not.toHaveBeenCalled();
    view.rerender(<App transport={value.transport} connectionReady />);
    expect(
      await screen.findByRole("heading", { name: "Restored schedule" }),
    ).toBeTruthy();
    expect(search().get("item")).toBe(value.schedule.id);
    expect(registeredSources(info)).toEqual([]);
    expect(value.mutated).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it("keeps a waiting Inbox selection after a refresh click and restores it when ready", async () => {
    const info = vi.spyOn(console, "info");
    const value = fixture();
    route(`?screen=inbox&item=${value.inboxEntry.id}`);
    const view = render(
      <App transport={value.transport} connectionReady={false} />,
    );
    await act(async () => {});
    fireEvent.click(await screen.findByRole("button", { name: "Refresh" }));
    await act(async () => {});
    expect(registeredSources(info)).toEqual([]);
    expect(search().get("item")).toBe(value.inboxEntry.id);
    expect(value.reads.inbox).not.toHaveBeenCalled();
    view.rerender(<App transport={value.transport} connectionReady />);
    await waitFor(() => expect(value.reads.inbox).toHaveBeenCalled());
    await act(async () => {});
    expect(search().get("item")).toBe(value.inboxEntry.id);
    expect(registeredSources(info)).toEqual([]);
  });

  it("keeps a waiting repository selection after a click on the page and restores it when ready", async () => {
    const value = fixture();
    route(`?screen=pull-requests&item=${value.repository.id}`);
    const view = render(
      <App transport={value.transport} connectionReady={false} />,
    );
    await act(async () => {});
    fireEvent.click(document.body);
    await act(async () => {});
    expect(search().get("item")).toBe(value.repository.id);
    view.rerender(<App transport={value.transport} connectionReady />);
    await waitFor(() => expect(value.reads.resource).toHaveBeenCalled());
    await act(async () => {});
    expect(search().get("item")).toBe(value.repository.id);
  });
});

describe("every navigation handler registers an intent and cancels a pending restore", () => {
  // Each driver renders the real application and performs the user action of
  // one handler. The record is keyed by the closed enum, so adding a handler
  // source without a driver fails typecheck and cannot silently skip the token.
  const drivers: Record<
    NavigationIntentSource,
    (value: ReturnType<typeof fixture>) => Promise<void>
  > = {
    [NavigationIntentSource.Surface]: async () => {
      fireEvent.click(
        await rail().findByRole("button", { name: "Pull requests" }),
      );
    },
    [NavigationIntentSource.SessionOpen]: async () => {
      fireEvent.click(await screen.findByText("Other session"));
    },
    [NavigationIntentSource.SessionTab]: async (value) => {
      await screen.findByRole("tab", { name: /Side question/ });
      const unselected = screen
        .getAllByRole("tab")
        .find((tab) => tab.getAttribute("aria-selected") === "false");
      expect(unselected).toBeTruthy();
      fireEvent.click(unselected!);
    },
    [NavigationIntentSource.SearchResult]: async () => {
      fireEvent.click(await screen.findByRole("button", { name: "Search" }));
      const input = await screen.findByRole("textbox", {
        name: "Search conversations",
      });
      fireEvent.change(input, { target: { value: "match" } });
      fireEvent.submit(input.closest("form")!);
      fireEvent.click(
        await screen.findByRole("button", { name: "Searched session" }),
      );
    },
    [NavigationIntentSource.ProjectCreation]: async () => {
      fireEvent.click(
        await screen.findByRole("button", { name: "New project" }),
      );
    },
    [NavigationIntentSource.InboxSelect]: async () => {
      fireEvent.click(await screen.findByText(/Other session/));
    },
    [NavigationIntentSource.InboxClose]: async () => {
      fireEvent.click(await screen.findByText(/Other session/));
      fireEvent.click(
        await screen.findByRole("button", { name: /back to inbox/i }),
      );
    },
    [NavigationIntentSource.ScheduleSelect]: async () => {
      fireEvent.click(await screen.findByText("Chosen schedule"));
    },
    [NavigationIntentSource.ScheduleNew]: async () => {
      fireEvent.click(
        await screen.findByRole("button", { name: "New schedule" }),
      );
    },
    [NavigationIntentSource.ScheduleEdit]: async () => {
      fireEvent.click(await screen.findByText("Chosen schedule"));
      fireEvent.click(
        await screen.findByRole("button", { name: "Edit schedule" }),
      );
    },
    [NavigationIntentSource.ScheduleClose]: async () => {
      fireEvent.click(await screen.findByText("Chosen schedule"));
      fireEvent.click(
        await screen.findByRole("button", { name: "Back to schedules" }),
      );
    },
    [NavigationIntentSource.RepositorySelect]: async () => {
      fireEvent.click(await screen.findByText("Chosen repository"));
    },
    [NavigationIntentSource.SettingsCategory]: async () => {
      await screen.findByRole("navigation", { name: "Settings categories" });
      const categories = [
        ...document.querySelectorAll<HTMLButtonElement>(
          "[data-settings-category]",
        ),
      ];
      const next = categories.find(
        (button) => button.getAttribute("aria-current") !== "page",
      );
      expect(next).toBeTruthy();
      fireEvent.click(next!);
    },
    [NavigationIntentSource.UsageTab]: async () => {
      fireEvent.click(await screen.findByRole("tab", { name: "Model prices" }));
    },
  };
  // The screen each driver starts from.
  const startRoutes: Partial<
    Record<
      NavigationIntentSource,
      (value: ReturnType<typeof fixture>) => string
    >
  > = {
    [NavigationIntentSource.SessionOpen]: (value) =>
      `?screen=sessions&session=${value.session.id}`,
    [NavigationIntentSource.SessionTab]: (value) =>
      `?screen=sessions&session=${value.session.id}&sidechat=${value.sidechat.id}`,
    [NavigationIntentSource.InboxSelect]: () => "?screen=inbox",
    [NavigationIntentSource.InboxClose]: () => "?screen=inbox",
    [NavigationIntentSource.ScheduleSelect]: () => "?screen=schedules",
    [NavigationIntentSource.ScheduleNew]: () => "?screen=schedules",
    [NavigationIntentSource.ScheduleEdit]: () => "?screen=schedules",
    [NavigationIntentSource.ScheduleClose]: () => "?screen=schedules",
    [NavigationIntentSource.RepositorySelect]: () => "?screen=pull-requests",
    [NavigationIntentSource.SettingsCategory]: () => "?screen=settings",
    [NavigationIntentSource.UsageTab]: () => "?screen=usage",
  };

  it("has a driver for every handler source", () => {
    expect(Object.keys(drivers).sort()).toEqual(
      Object.values(NavigationIntentSource).sort(),
    );
  });

  it.each(Object.values(NavigationIntentSource))(
    "registers the %s intent",
    async (source) => {
      const info = vi.spyOn(console, "info");
      const value = fixture();
      const start = startRoutes[source];
      if (start) route(start(value));
      render(<App transport={value.transport} />);
      // A restore or initial read may settle first; only the user action counts.
      await act(async () => {});
      await screen.findByRole("navigation", { name: "Primary navigation" });
      info.mockClear();
      await drivers[source](value);
      await act(async () => {});
      expect(registeredSources(info)).toContain(source);
    },
  );

  it.each([
    [
      "an Inbox row",
      NavigationIntentSource.InboxSelect,
      (value: ReturnType<typeof fixture>) =>
        `?screen=inbox&item=${value.inboxEntry.id}`,
      async () => {
        fireEvent.click(await screen.findByText(/Other session/));
      },
      (value: ReturnType<typeof fixture>) => value.otherInboxEntry.id,
    ],
    [
      "a Schedule row",
      NavigationIntentSource.ScheduleSelect,
      (value: ReturnType<typeof fixture>) =>
        `?screen=schedules&item=${value.schedule.id}`,
      async () => {
        fireEvent.click(await screen.findByText("Chosen schedule"));
      },
      (value: ReturnType<typeof fixture>) => value.otherSchedule.id,
    ],
    [
      "New schedule",
      NavigationIntentSource.ScheduleNew,
      (value: ReturnType<typeof fixture>) =>
        `?screen=schedules&item=${value.schedule.id}`,
      async () => {
        fireEvent.click(
          await screen.findByRole("button", { name: "New schedule" }),
        );
      },
      () => undefined,
    ],
    [
      "a repository row",
      NavigationIntentSource.RepositorySelect,
      (value: ReturnType<typeof fixture>) =>
        `?screen=pull-requests&item=${value.repository.id}`,
      async () => {
        fireEvent.click(await screen.findByText("Chosen repository"));
      },
      (value: ReturnType<typeof fixture>) => value.otherRepository.id,
    ],
  ])(
    "cancels a pending restore when %s is activated while the connection is not ready",
    async (_name, source, startRoute, activate, expectedItem) => {
      const info = vi.spyOn(console, "info");
      const value = fixture();
      route(startRoute(value));
      const view = render(
        <App transport={value.transport} connectionReady={false} />,
      );
      await act(async () => {});
      await activate();
      expect(registeredSources(info)).toContain(source);
      expect(info).toHaveBeenCalledWith("delidev.product_route.restore", {
        outcome: "superseded",
      });
      // The cancelled restore never resumes once the connection is ready.
      view.rerender(<App transport={value.transport} connectionReady />);
      await act(async () => {});
      expect(
        screen.queryByRole("heading", { name: "Restored schedule" }),
      ).toBeNull();
      expect(search().get("item") ?? undefined).toBe(expectedItem(value));
      expect(value.mutated).not.toHaveBeenCalled();
    },
  );
});
