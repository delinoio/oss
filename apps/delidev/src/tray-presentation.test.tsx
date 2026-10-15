import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  InboxService,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  UsageService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import {
  PresentationOutcome,
  PresentationPhase,
} from "./presentation-diagnostics";

const native = vi.hoisted(() => ({
  callbacks: new Set<() => void>(),
  pending: null as {
    id: string;
    destination: string;
    inbox_id?: string;
  } | null,
  calls: [] as string[],
  failing: new Set<string>(),
  hanging: new Set<string>(),
  listenFails: false,
}));
vi.mock("@tauri-apps/api/core", () => ({
  isTauri: () => true,
  invoke: async (name: string, args?: { id: string }) => {
    native.calls.push(name);
    if (native.failing.has(name)) throw new Error(`${name} failed`);
    if (native.hanging.has(name)) return new Promise(() => {});
    if (name === "read_sidebar_preference")
      return { revision: 1, sidebar_preference: "expanded", problem: null };
    if (name === "read_navigation_preference")
      return { revision: 1, usage_pinned: false, problem: null };
    if (name === "notification_permission")
      return { permission: "unavailable", problem: "os-unavailable" };
    if (name === "begin_tray") return "scope";
    if (name === "read_tray_action") return native.pending;
    if (name === "acknowledge_tray_action" && native.pending?.id === args?.id)
      native.pending = null;
  },
}));
vi.mock("@tauri-apps/api/event", () => ({
  listen: async (event: string, callback: () => void) => {
    if (native.listenFails && event === "tray-activate")
      throw new Error("listen failed");
    if (event !== "tray-activate") return () => {};
    native.callbacks.add(callback);
    return () => native.callbacks.delete(callback);
  },
}));

it("opens the native-selected view without sending work or losing the session draft", async () => {
  const id = newRequestId();
  const session = create(ResourceSchema, {
    id,
    sessionId: id,
    kind: EntityKind.SESSION,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Tray fixture",
      workspace: "general-chat",
      outcome: "stopped",
      archive: "active",
      dispatch: "paused",
      recovery: "none",
    }),
  });
  const inboxId = newRequestId();
  const entry = create(ResourceSchema, {
    id: inboxId,
    kind: EntityKind.INBOX,
    sessionId: id,
    revision: 1n,
    schemaVersion: 1,
    createdAt: "2026-09-27T00:01:00Z",
    documentJson: encode({
      source: "execution-terminal",
      source_id: newRequestId(),
      read_state: "unread",
      terminal: { outcome: "stopped" },
    }),
  });
  const getInbox = vi.fn(() => ({ view: { entry, session } })),
    markRead = vi.fn(() => ({}));
  const send = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ version: "0.1.0" }),
      getOverview: () => ({
        observedAt: "2026-09-27T00:01:00Z",
        todayFromUnixMs: 1790467200000n,
        todayUntilUnixMs: 1790467260000n,
      }),
    });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [session] }),
      listQueue: () => ({ inputs: [] }),
      enqueueInput: send,
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getSnapshot: () => ({ resources: [session], cursor: "fixture" }),
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getInboxEntry: getInbox,
      setInboxReadState: markRead,
    });
    router.service(UsageService, { getUsageSummary: () => ({}) });
  });
  const view = render(
    <StrictMode>
      <App transport={transport} />
    </StrictMode>,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: /General Chat Tray fixture/ }),
  );
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep this unsent" } });
  await waitFor(() => expect(native.callbacks.size).toBe(1));
  native.pending = { id: newRequestId(), destination: "inbox" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  await screen.findByText("No retained requests or execution results.");
  await waitFor(() => expect(native.pending).toBeNull());
  native.pending = {
    id: newRequestId(),
    destination: "inbox",
    inbox_id: inboxId,
  };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  await screen.findByRole("heading", { name: "Tray fixture", level: 3 });
  expect(view.container.querySelector(".inbox-source-badge")?.textContent).toBe(
    "Execution stopped",
  );
  await screen.findByText("Original terminal observation");
  expect(getInbox).toHaveBeenCalledWith(
    expect.objectContaining({ id: inboxId }),
    expect.anything(),
  );
  expect(markRead).not.toHaveBeenCalled();
  await waitFor(() =>
    expect(
      (screen.getByRole("button", { name: "Refresh" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false),
  );
  const beforeReactivation = getInbox.mock.calls.length;
  native.pending = {
    id: newRequestId(),
    destination: "inbox",
    inbox_id: inboxId,
  };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  await waitFor(() =>
    expect(getInbox).toHaveBeenCalledTimes(beforeReactivation + 1),
  );
  expect(markRead).not.toHaveBeenCalled();
  native.pending = { id: newRequestId(), destination: "sessions" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep this unsent");
  // Tray Usage navigation works while Usage is unpinned and never writes the
  // device-local navigation preference.
  native.pending = { id: newRequestId(), destination: "usage" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  expect(
    screen.getByRole("button", { name: "More" }).getAttribute("aria-current"),
  ).toBe("page");
  expect(native.calls).not.toContain("update_navigation_preference");
  native.pending = { id: newRequestId(), destination: "sessions" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  native.pending = { id: newRequestId(), destination: "settings" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  expect(
    screen
      .getByRole("button", { name: "AI Subscription" })
      .getAttribute("aria-current"),
  ).toBe("page");
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "New Instructions" }),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
    target: { value: "Tray-abandoned draft" },
  });
  // Tray navigation away also closes the opening, even while an editor is active.
  native.pending = { id: newRequestId(), destination: "sessions" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  native.pending = { id: newRequestId(), destination: "settings" };
  await act(async () => {
    for (const callback of native.callbacks) callback();
  });
  expect(
    screen
      .getByRole("button", { name: "AI Subscription" })
      .getAttribute("aria-current"),
  ).toBe("page");
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(
    (screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement)
      .value,
  ).toBe("Keep this unsent");
  expect(send).not.toHaveBeenCalled();
  view.unmount();
  expect(native.callbacks.size).toBe(0);
}, 15000);

function diagnosticsTransport() {
  return createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ version: "0.1.0" }),
      getOverview: () => ({
        observedAt: "2026-09-27T00:01:00Z",
        todayFromUnixMs: 1790467200000n,
        todayUntilUnixMs: 1790467260000n,
      }),
    });
    router.service(SessionService, {
      listSessions: () => ({ sessions: [] }),
      listQueue: () => ({ inputs: [] }),
    });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getSnapshot: () => ({ resources: [], cursor: "fixture" }),
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) =>
          context.signal.addEventListener("abort", () => resolve(), {
            once: true,
          }),
        );
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }) });
    router.service(UsageService, { getUsageSummary: () => ({}) });
  });
}

// The window renders no global status for tray publication, activation listener
// or retained action failures, and the tray effects keep running. Each failure
// leaves one bounded structured warning with only its stable phase and outcome.
it.each([
  ["begin_tray", PresentationPhase.TrayBegin],
  ["publish_tray", PresentationPhase.TrayPublish],
  ["read_tray_action", PresentationPhase.TrayActionRead],
  ["listen", PresentationPhase.TrayActivationListener],
] as const)(
  "renders no global status after a tray %s failure and warns once with its phase",
  async (failure, phase) => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    native.failing.clear();
    native.hanging.clear();
    native.listenFails = failure === "listen";
    if (failure !== "listen") native.failing.add(failure);
    // A successful publication would otherwise hide a later listener or action
    // failure again, so only the publication case lets it run.
    if (failure !== "publish_tray") native.hanging.add("publish_tray");
    native.calls.length = 0;
    const transport = diagnosticsTransport();
    try {
      // A transient status counts: later healthy work must not hide a regression.
      let everShown = false;
      const observer = new MutationObserver(() => {
        if (/Tray status is unavailable/.test(document.body.textContent ?? ""))
          everShown = true;
      });
      observer.observe(document.body, {
        childList: true,
        subtree: true,
        characterData: true,
      });
      const view = render(<App transport={transport} />);
      await waitFor(() => expect(native.calls).toContain("begin_tray"));
      if (failure === "publish_tray")
        await waitFor(() => expect(native.calls).toContain("publish_tray"));
      if (failure === "read_tray_action")
        await waitFor(() => expect(native.calls).toContain("read_tray_action"));
      // Let the rejected native call and its state updates settle.
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 50));
      });
      observer.disconnect();
      const records = warn.mock.calls.filter(
        ([name]) => name === "delidev.presentation.outcome",
      );
      expect(records).toEqual([
        [
          "delidev.presentation.outcome",
          { phase, outcome: PresentationOutcome.Failed },
        ],
      ]);
      expect(JSON.stringify(warn.mock.calls)).not.toContain(
        `${failure} failed`,
      );
      expect(everShown).toBe(false);
      expect(screen.queryByText(/Tray status is unavailable/)).toBeNull();
      expect(screen.queryByRole("status")).toBeNull();
      expect(view.container.querySelector("main [role='alert']")).toBeNull();
      view.unmount();
    } finally {
      warn.mockRestore();
      native.failing.clear();
      native.hanging.clear();
      native.listenFails = false;
    }
  },
  15000,
);

// Recovery and disposal cells of the tray rows in the diagnostics table of the
// desktop contract: a failing phase records one recovery on its next success, a
// repeated failure adds no record, and a disposed tray records nothing.
it("records one recovery when a retained tray action read succeeds after failing and nothing after unmount", async () => {
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  const info = vi.spyOn(console, "info").mockImplementation(() => {});
  native.failing.clear();
  native.hanging.clear();
  native.listenFails = false;
  native.calls.length = 0;
  native.failing.add("read_tray_action");
  const outcomes = (spy: { mock: { calls: unknown[][] } }) =>
    spy.mock.calls
      .filter(([name]) => name === "delidev.presentation.outcome")
      .map(([, record]) => record);
  try {
    const view = render(<App transport={diagnosticsTransport()} />);
    await waitFor(() =>
      expect(native.calls.filter((c) => c === "read_tray_action").length).toBe(
        2,
      ),
    );
    expect(outcomes(warn)).toEqual([
      {
        phase: PresentationPhase.TrayActionRead,
        outcome: PresentationOutcome.Failed,
      },
    ]);
    native.failing.delete("read_tray_action");
    await waitFor(() => expect(outcomes(info)).toHaveLength(1));
    expect(outcomes(info)).toEqual([
      {
        phase: PresentationPhase.TrayActionRead,
        outcome: PresentationOutcome.Recovered,
      },
    ]);
    expect(outcomes(warn)).toHaveLength(1);
    native.failing.add("read_tray_action");
    view.unmount();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 400));
    });
    expect(outcomes(warn)).toHaveLength(1);
  } finally {
    warn.mockRestore();
    info.mockRestore();
    native.failing.clear();
  }
}, 15000);
