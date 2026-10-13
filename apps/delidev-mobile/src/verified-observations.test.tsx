// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  InboxService,
  SystemService,
} from "@delinoio/delidev-api-client";
import { App } from "./app";
import { Connection, Status } from "./connection";
import { documentBytes, Operation, ProtectedState } from "./state";
import { en } from "./localization";
const platform = vi.hoisted(() => ({ foreground: (_active: boolean) => {} }));
vi.mock("./platform", () => ({
  storage: { read: async () => null, write: async () => {} },
  observeForeground: async (change: (active: boolean) => void) => {
    platform.foreground = change;
    return () => {};
  },
  permission: async () => false,
  notify: async () => false,
  tls: async () => "ok",
}));
// Isolate the verified connection lifecycle from the independent event decoder.
vi.mock("@delinoio/delidev-api-client", async (original) => {
  const actual =
    await original<typeof import("@delinoio/delidev-api-client")>();
  return {
    ...actual,
    synchronizeResources: async function* (
      _client: unknown,
      _scope: unknown,
      options: { signal: AbortSignal },
    ) {
      yield {
        kind: actual.SyncKind.Connection,
        state: actual.ConnectionState.Live,
      };
      await new Promise<void>((resolve) =>
        options.signal.addEventListener("abort", () => resolve(), {
          once: true,
        }),
      );
    },
  };
});
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
});
afterEach(cleanup);
const row = (
  kind: EntityKind,
  id: string,
  document: Record<string, unknown>,
  sessionId = "",
) =>
  create(ResourceSchema, {
    kind,
    id,
    sessionId,
    revision: 7n,
    schemaVersion: 1,
    documentJson: documentBytes(document),
  });
function fixture() {
  const state = new ProtectedState({
    read: async () => null,
    write: async () => {},
  });
  state.state = {
    version: 1,
    language: "en",
    theme: "system",
    notifications: false,
    selectedProfile: "profile",
    profiles: [
      {
        id: "profile",
        name: "Original",
        origin: "https://fixture.invalid",
        serverId: "server",
        deviceId: "device",
        token: "fixture",
      },
    ],
  };
  vi.spyOn(state, "load").mockResolvedValue(undefined);
  let serverId = "server",
    protocolVersion = 2,
    label = "old";
  const calls: string[] = [];
  let statusWait: Promise<void> | undefined;
  let preferenceWait: Promise<void> | undefined;
  const session = (id = "session") =>
    row(EntityKind.SESSION, id, {
      name: `${label} conversation`,
      archive: "active",
      recovery: "none",
    });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: async () => {
        calls.push("status");
        await statusWait;
        return { serverId, protocolVersion };
      },
    });
    router.service(SessionService, {
      listSessions: (request) => {
        calls.push(`sessions:${request.pageToken}`);
        return {
          sessions: [
            row(EntityKind.SESSION, request.pageToken || "first", {
              name: `${label} session ${request.pageToken || "first"}`,
            }),
          ],
          nextPageToken: request.pageToken ? "" : `${label}-sessions`,
        };
      },
      listQueue: () => ({ inputs: [] }),
    });
    router.service(ResourceService, {
      getResource: (request) => ({ resource: session(request.id) }),
      listResources: (request) => {
        const filter = request.filter!;
        calls.push(`resources:${filter.kind}:${filter.pageToken}`);
        if (filter.kind === EntityKind.MESSAGE)
          return {
            resources: [
              row(
                EntityKind.MESSAGE,
                filter.pageToken || "message",
                { text: `${label} message ${filter.pageToken || "first"}` },
                "session",
              ),
            ],
            nextPageToken: filter.pageToken ? "" : `${label}-history`,
          };
        if (filter.kind === EntityKind.INTERACTION)
          return {
            resources: [
              row(
                EntityKind.INTERACTION,
                "question",
                {
                  type: "user-question",
                  closure: "open",
                  questions: {
                    questions: [
                      { id: "q", text: "Original question", options: [] },
                    ],
                  },
                },
                filter.sessionId,
              ),
            ],
          };
        return { resources: [] };
      },
    });
    router.service(InboxService, {
      getNotificationPreferences: async () => {
        calls.push("preferences");
        await preferenceWait;
        return {
          preferences: { revision: 9n, interactions: true, terminals: false },
        };
      },
      listInbox: (request) => {
        calls.push(`inbox:${request.pageToken}`);
        return {
          entries: [
            {
              entry: row(EntityKind.INBOX, request.pageToken || "entry", {
                source: `${label} inbox ${request.pageToken || "first"}`,
              }),
            },
          ],
          nextPageToken: request.pageToken ? "" : `${label}-inbox`,
        };
      },
      getInboxEntry: () => ({
        view: {
          entry: row(EntityKind.INBOX, "entry", {
            source: `${label} selected`,
          }),
          session: session(),
        },
      }),
    });
  });
  vi.spyOn(state, "transport").mockReturnValue(transport);
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  const retry = vi.spyOn(state, "retry").mockResolvedValue(undefined);
  return {
    state,
    calls,
    perform,
    retry,
    mismatch: (protocol = false) => {
      if (protocol) protocolVersion = 3;
      else serverId = "replacement";
    },
    restore: () => {
      serverId = "server";
      protocolVersion = 2;
      label = "fresh";
    },
    waitStatus: (wait?: Promise<void>) => {
      statusWait = wait;
    },
    waitPreferences: (wait?: Promise<void>) => {
      preferenceWait = wait;
    },
  };
}
function tab(name: string) {
  fireEvent.click(
    within(screen.getByRole("navigation")).getByRole("button", { name }),
  );
}
function refreshConnection() {
  fireEvent.click(document.querySelector(".connection button")!);
}
it.each([false, true])(
  "verifies Settings before preference access and denies mismatch (protocol=%s)",
  async (protocol) => {
    const f = fixture();
    let verified!: () => void;
    f.waitStatus(
      new Promise((resolve) => {
        verified = resolve;
      }),
    );
    render(<App state={f.state} />);
    await waitFor(() => expect(f.calls).toEqual(["status"]));
    tab(en.settings);
    expect(f.calls).not.toContain("preferences");
    await act(async () => {
      verified();
    });
    await waitFor(() => expect(f.calls).toContain("preferences"));
    const preferences = screen.getByRole("group", {
      name: en.serverNotifications,
    });
    expect((preferences as HTMLFieldSetElement).disabled).toBe(false);
    f.mismatch(protocol);
    act(() => platform.foreground(false));
    expect((preferences as HTMLFieldSetElement).disabled).toBe(true);
    act(() => platform.foreground(true));
    await screen.findByText(en.version);
    expect(f.calls.filter((call) => call === "preferences")).toHaveLength(1);
    fireEvent.click(within(preferences).getByLabelText(en.interactions));
    expect(f.perform).not.toHaveBeenCalled();
    expect(f.retry).not.toHaveBeenCalled();
  },
);
it("retires reads synchronously and awaits cancellation before mismatch presentation", async () => {
  const f = fixture();
  const pending = {
    operation: Operation.Send,
    target: "session",
    request: '{"requestId":"original","documentJson":"exact-bytes"}',
  };
  f.state.state.profiles[0]!.pending = pending;
  const snapshots: { status: Status; size: number; epoch: number }[] = [];
  const connection = new Connection(f.state, "profile", (status) =>
    snapshots.push({
      status,
      size: connection.cache.getQueryCache().getAll().length,
      epoch: connection.observationEpoch,
    }),
  );
  void connection.resume();
  await waitFor(() => expect(connection.status).toBe(Status.Live));
  const originalEpoch = connection.observationEpoch;
  let signal!: AbortSignal, finish!: (value: string) => void;
  const read = connection.cache
    .fetchQuery({
      queryKey: ["old-read"],
      queryFn: ({ signal: original }) => {
        signal = original;
        return new Promise<string>((resolve) => {
          finish = resolve;
        });
      },
    })
    .catch(() => undefined);
  await waitFor(() => expect(signal).toBeDefined());
  let canceled!: () => void;
  const cancelQueries = connection.cache.cancelQueries.bind(connection.cache);
  vi.spyOn(connection.cache, "cancelQueries").mockImplementation(async () => {
    await cancelQueries();
    await new Promise<void>((resolve) => {
      canceled = resolve;
    });
  });
  f.mismatch();
  const refresh = connection.resume();
  expect(connection.observationEpoch).toBeGreaterThan(originalEpoch);
  expect(connection.isCurrent(originalEpoch)).toBe(false);
  expect(signal.aborted).toBe(true);
  expect(connection.cache.getQueryCache().getAll()).toEqual([]);
  expect(connection.status).toBe(Status.Connecting);
  await waitFor(() => expect(canceled).toBeDefined());
  expect(f.calls.filter((call) => call === "status")).toHaveLength(1);
  canceled();
  await refresh;
  expect(snapshots.at(-1)).toMatchObject({ status: Status.Version, size: 0 });
  finish("late-old-data");
  await read;
  expect(connection.cache.getQueryCache().getAll()).toEqual([]);
  expect(f.state.profile("profile").pending).toEqual(pending);
  connection.suspend();
});
it.each([en.sessions, en.inbox])(
  "purges accumulated %s pages and restarts with fresh cursors",
  async (destination) => {
    const f = fixture();
    render(<App state={f.state} />);
    await screen.findByText(en.live);
    if (destination === en.inbox) tab(destination);
    await screen.findByRole("button", {
      name: new RegExp(
        `old ${destination === en.inbox ? "inbox" : "session"} first`,
      ),
    });
    fireEvent.click(screen.getByRole("button", { name: en.more }));
    await screen.findByRole("button", {
      name: new RegExp(
        destination === en.inbox
          ? "old inbox old-inbox"
          : "old session old-sessions",
      ),
    });
    if (destination === en.inbox) {
      fireEvent.click(screen.getByRole("button", { name: /old inbox first/ }));
      await screen.findByText("old selected");
    }
    f.mismatch();
    refreshConnection();
    await screen.findByText(en.version);
    expect(document.body.textContent).not.toMatch(
      /old (inbox|session|selected)/,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    f.restore();
    refreshConnection();
    await screen.findByRole("button", {
      name: new RegExp(
        `fresh ${destination === en.inbox ? "inbox" : "session"} first`,
      ),
    });
    expect(document.body.textContent).not.toContain("old-");
    expect(f.calls.at(-1)).not.toContain("old-");
    expect(f.perform).not.toHaveBeenCalled();
  },
);
it("purges history and detail observations while preserving composer and original-revision response drafts", async () => {
  const f = fixture();
  render(<App state={f.state} />);
  fireEvent.click(
    await screen.findByRole("button", { name: /old session first/ }),
  );
  await screen.findByRole("heading", { name: "old conversation" });
  fireEvent.change(screen.getByLabelText(en.prompt), {
    target: { value: "Retained composer" },
  });
  await screen.findByLabelText(en.custom);
  fireEvent.change(screen.getByLabelText(en.custom), {
    target: { value: "Retained answer" },
  });
  fireEvent.click(screen.getByRole("button", { name: en.more }));
  await screen.findByText("old message old-history");
  f.mismatch();
  refreshConnection();
  await screen.findByText(en.version);
  expect(document.body.textContent).not.toMatch(
    /old conversation|old message|Original question/,
  );
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe(
    "Retained composer",
  );
  f.restore();
  refreshConnection();
  await screen.findByRole("heading", { name: "fresh conversation" });
  expect(
    ((await screen.findByLabelText(en.custom)) as HTMLTextAreaElement).value,
  ).toBe("Retained answer");
  expect(document.body.textContent).not.toContain("old-history");
  expect(f.perform).not.toHaveBeenCalled();
});

it("verifies a replacement profile before Settings reads and preserves original pending bytes", async () => {
  const f = fixture();
  const pending = {
    operation: Operation.Preferences,
    target: "",
    request:
      '{"requestId":"original","expectedRevision":"9","changes":{"questions":false}}',
  };
  f.state.state.profiles[0]!.pending = pending;
  f.state.state.profiles.push({
    ...f.state.state.profiles[0]!,
    id: "replacement",
    name: "Replacement",
    pending: undefined,
  });
  vi.spyOn(f.state, "select").mockImplementation(async (id) => {
    f.state.state.selectedProfile = id;
  });
  render(<App state={f.state} />);
  await screen.findByText(en.live);
  tab(en.settings);
  await waitFor(() =>
    expect(f.calls.filter((call) => call === "preferences")).toHaveLength(1),
  );
  let verified!: () => void;
  f.waitStatus(
    new Promise((resolve) => {
      verified = resolve;
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Replacement" }));
  await waitFor(() =>
    expect(f.calls.filter((call) => call === "status")).toHaveLength(2),
  );
  expect(f.calls.filter((call) => call === "preferences")).toHaveLength(1);
  await act(async () => {
    verified();
  });
  await waitFor(() =>
    expect(f.calls.filter((call) => call === "preferences")).toHaveLength(2),
  );
  expect(f.state.profile("profile").pending).toEqual(pending);
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});

it("retains mounted creation fields through mismatch and fresh verification", async () => {
  const f = fixture();
  render(<App state={f.state} />);
  fireEvent.click(await screen.findByRole("button", { name: en.newSession }));
  const dialog = within(screen.getByRole("dialog", { name: en.newSession }));
  fireEvent.change(dialog.getByLabelText(en.title), {
    target: { value: "Retained title" },
  });
  fireEvent.change(dialog.getByLabelText(en.prompt), {
    target: { value: "Retained creation" },
  });
  f.mismatch();
  refreshConnection();
  await screen.findByText(en.version);
  expect((dialog.getByLabelText(en.title) as HTMLInputElement).value).toBe(
    "Retained title",
  );
  expect((dialog.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe(
    "Retained creation",
  );
  f.restore();
  refreshConnection();
  await screen.findByText(en.live);
  expect((dialog.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe(
    "Retained creation",
  );
  expect(f.perform).not.toHaveBeenCalled();
});

it("fences a delayed Settings read on background without replaying an uncertain request", async () => {
  const f = fixture();
  const pending = {
    operation: Operation.Preferences,
    target: "",
    request:
      '{"requestId":"original","expectedRevision":"9","preferences":{"interactions":false}}',
  };
  f.state.state.profiles[0]!.pending = pending;
  let finish!: () => void;
  f.waitPreferences(
    new Promise((resolve) => {
      finish = resolve;
    }),
  );
  render(<App state={f.state} />);
  await screen.findByText(en.live);
  tab(en.settings);
  await waitFor(() => expect(f.calls).toContain("preferences"));
  act(() => platform.foreground(false));
  f.mismatch();
  act(() => platform.foreground(true));
  await screen.findByText(en.version);
  await act(async () => {
    finish();
  });
  expect(
    (
      screen.getByRole("group", {
        name: en.serverNotifications,
      }) as HTMLFieldSetElement
    ).disabled,
  ).toBe(true);
  expect(
    (screen.getByLabelText(en.interactions) as HTMLInputElement).checked,
  ).toBe(false);
  expect(f.calls.filter((call) => call === "preferences")).toHaveLength(1);
  expect(f.state.profile("profile").pending).toEqual(pending);
  expect(f.perform).not.toHaveBeenCalled();
  expect(f.retry).not.toHaveBeenCalled();
});
