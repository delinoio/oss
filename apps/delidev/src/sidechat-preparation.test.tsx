// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
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
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ForkPurpose,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import {
  PendingSidechatPane,
  SessionCreationAction,
  SessionForkAction,
  SessionForkProvider,
} from "./session-fork";
import {
  SessionTabsProvider,
  SessionTabKind,
  sessionTabKey,
  useSessionTabs,
  type SessionTabsStore,
} from "./session-tabs";
import { SessionTabBar } from "./session-tab-bar";
import { SidechatAuthoring } from "./sidechat-authoring";
import { SessionToolMenu } from "./session-tool-menu";

async function fixture(
  options: {
    lost?: boolean;
    hidden?: boolean;
    stale?: boolean;
    runnerError?: boolean;
    rejected?: boolean;
    replayRejected?: boolean;
    second?: boolean;
  } = {},
) {
  const machineId = newRequestId(),
    sourceId = newRequestId();
  const source = create(ResourceSchema, {
    kind: EntityKind.SESSION,
    id: sourceId,
    revision: 8n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Original",
      machine_id: machineId,
      workspace: "worktree",
      archive: "active",
      recovery: "none",
      outcome: "succeeded",
      initial_execution: {
        configuration: { harness: "codex", subscription: true },
      },
      execution: { native_turn_id: "original-turn", cleanup_verified: true },
    }),
  });
  const machine = create(ResourceSchema, {
    kind: EntityKind.MACHINE,
    id: machineId,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      worker_capabilities: [
        "codex-read-only-sidechat-v1",
        "managed-codex-sidechat-v1",
        "managed-codex-subscriptions-v1",
      ],
    }),
  });
  const jobId = newRequestId(),
    childId = newRequestId();
  const job = (state: string, id = jobId) =>
    create(ResourceSchema, {
      kind: EntityKind.JOB,
      id,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({ state, input: { source_session_id: sourceId } }),
    });
  const child = (parent = sourceId, overlay = true, id = childId) =>
    create(ResourceSchema, {
      kind: EntityKind.SESSION,
      id,
      revision: 1n,
      schemaVersion: 1,
      documentJson: encode({
        name: "Verified Sidechat",
        fork: {
          source_session_id: parent,
          ...(overlay ? { sidechat_parent_snapshot: { original: true } } : {}),
        },
      }),
    });
  // A second completed Session B shares the same Runner and eligibility data.
  const other = create(ResourceSchema, {
    ...source,
    id: newRequestId(),
    documentJson: source.documentJson,
  });
  let result: {
    job: ReturnType<typeof job>;
    session?: ReturnType<typeof child>;
  } = { job: job("claimed") };
  const fork = vi.fn(async (request) => {
    if (options.rejected)
      throw new ConnectError("Original creation denied", Code.PermissionDenied);
    if (options.replayRejected && fork.mock.calls.length > 1)
      throw new ConnectError("Replay denied", Code.PermissionDenied);
    if (options.lost && fork.mock.calls.length === 1)
      throw new ConnectError("Lost original response", Code.Unavailable);
    return { job: job("claimed") };
  });
  let runnerError = options.runnerError;
  let sessionReadFailing = false;
  const observe = vi.fn((_request: { jobId: string }) => result),
    read = vi.fn((request) => {
      if (runnerError && request.kind === EntityKind.MACHINE)
        throw new ConnectError(
          "Original Runner read unavailable",
          Code.Unavailable,
        );
      if (sessionReadFailing && request.kind === EntityKind.SESSION)
        throw new ConnectError("Original parent read failed", Code.Unavailable);
      return {
        resource:
          request.kind === EntityKind.MACHINE
            ? machine
            : options.stale
              ? { ...source, revision: 9n }
              : source,
      };
    });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [
          SystemCapability.NATIVE_SIDECHAT_V1,
          SystemCapability.MANAGED_CODEX_SIDECHAT_V1,
        ],
      }),
    });
    router.service(ResourceService, { getResource: read });
    router.service(SessionService, {
      forkSession: fork,
      getSessionFork: observe,
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const sessionNavigationGeneration = { current: 0 };
  const navigate = vi.fn(() => {
    sessionNavigationGeneration.current += 1;
  });
  let store!: SessionTabsStore;
  function Workspace({ active = true }: { active?: boolean }) {
    const tabs = useSessionTabs(sourceId);
    store = tabs.store;
    return (
      <>
        <SessionToolMenu active={active}>
          {["Diff", "Files", "Terminals", "Browser", "Diagnostics"].map(
            (label) => (
              <button key={label} role="menuitem">
                {label}
              </button>
            ),
          )}
          {tabs.tab.kind !== SessionTabKind.PendingSidechat &&
          tabs.tab.kind !== SessionTabKind.Sidechat ? (
            <SessionForkAction
              source={source}
              action={SessionCreationAction.Sidechat}
            />
          ) : null}
        </SessionToolMenu>
        <SessionTabBar
          id={sourceId}
          tabs={tabs.tabs}
          selected={tabs.selected}
          select={(key) => tabs.store.select(sourceId, key)}
          close={(key) => tabs.store.close(sourceId, key)}
        />
        {tabs.tabs.map((tab) =>
          tab.kind === SessionTabKind.PendingSidechat ? (
            <div
              hidden={!active || tabs.selected !== sessionTabKey(tab)}
              key={tab.requestId}
            >
              <PendingSidechatPane
                requestId={tab.requestId}
                active={active && tabs.selected === sessionTabKey(tab)}
              />
            </div>
          ) : null,
        )}
        {tabs.store.sidechats(sourceId).map((tab) => (
          <div
            key={tab.id}
            hidden={!active || tabs.selected !== sessionTabKey(tab)}
          >
            <SidechatAuthoring
              id={tab.id}
              active={active && tabs.selected === sessionTabKey(tab)}
            >
              {({ draft, setDraft }) => (
                <textarea
                  aria-label="Verified child draft"
                  id={`prompt-${tab.id}`}
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                />
              )}
            </SidechatAuthoring>
          </div>
        ))}
      </>
    );
  }
  const view = (active = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionTabsProvider>
            <SessionForkProvider
              openSession={navigate}
              sessionNavigationGeneration={sessionNavigationGeneration}
            >
              <Workspace active={active} />
              <SessionForkAction
                source={source}
                action={SessionCreationAction.Sidechat}
              />
              {options.second ? (
                <section aria-label="Session B actions">
                  <SessionForkAction
                    source={other}
                    action={SessionCreationAction.Sidechat}
                  />
                </section>
              ) : null}
            </SessionForkProvider>
          </SessionTabsProvider>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const rendered = render(view());
  await waitFor(() => expect(client.isFetching()).toBe(0));
  return {
    sourceId,
    otherId: other.id,
    childId,
    fork,
    observe,
    read,
    client,
    navigate,
    navigateSession: () => {
      sessionNavigationGeneration.current += 1;
    },
    store: () => store,
    rendered,
    view,
    job,
    child,
    setRunnerHealthy: () => {
      runnerError = false;
    },
    setSessionReadFailing: (value: boolean) => {
      sessionReadFailing = value;
    },
    setResult: (value: typeof result) => {
      result = value;
    },
  };
}

it("only explicit flat menu activation creates one original request and focuses the editable draft", async () => {
  const f = await fixture();
  const trigger = screen.getByRole("button", { name: "Open tool" });
  fireEvent.click(trigger);
  const menu = screen.getByRole("menu");
  expect(
    [...menu.querySelectorAll('[role="menuitem"]')].map(
      (item) => item.textContent,
    ),
  ).toEqual([
    "Diff",
    "Files",
    "Terminals",
    "Browser",
    "Diagnostics",
    "Open Sidechat",
  ]);
  expect(f.fork).not.toHaveBeenCalled();
  fireEvent.keyDown(menu, { key: "Escape" });
  expect(document.activeElement).toBe(trigger);
  expect(f.fork).not.toHaveBeenCalled();
  fireEvent.click(trigger);
  fireEvent.click(menu.querySelector('[role="menuitem"]:last-child')!);
  const input = await screen.findByRole("textbox", { name: "Message" });
  expect(document.activeElement).toBe(input);
  expect(input).toHaveProperty("disabled", false);
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.change(input, { target: { value: "Retained question" } });
  await waitFor(() => expect(f.fork).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  await act(async () => {});
  expect(f.fork).toHaveBeenCalledTimes(1);
  expect(f.fork.mock.calls[0]![0]).toMatchObject({
    mutation: { id: f.sourceId, expectedRevision: 8n },
    expectedTurnId: "original-turn",
    purpose: ForkPurpose.SIDECHAT,
    name: "Original Sidechat",
  });
  expect(screen.getByRole("button", { name: "Queue message" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(
    f.read.mock.calls.every((call) =>
      [EntityKind.SESSION, EntityKind.MACHINE].includes(call[0].kind),
    ),
  ).toBe(true);
});

it.each(["selected", "background", "closed", "hidden"])(
  "publishes only the verified child in place, retaining draft and original caret without navigation: %s",
  async (mode) => {
    const f = await fixture();
    fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
    const input = (await screen.findByRole("textbox", {
      name: "Message",
    })) as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "My original question" } });
    input.setSelectionRange(3, 8);
    fireEvent.select(input);
    await waitFor(() => expect(f.observe).toHaveBeenCalled());
    const pending = f
      .store()
      .snapshot(f.sourceId)
      .tabs.find((tab) => tab.kind === SessionTabKind.PendingSidechat)!;
    if (mode === "background") {
      act(() => f.store().open(f.sourceId, { kind: SessionTabKind.Files }));
      screen.getByRole("tab", { name: "Files" }).focus();
    }
    if (mode === "closed")
      fireEvent.click(
        screen.getByRole("button", { name: "Close Original Sidechat tab" }),
      );
    if (mode === "hidden") f.rendered.rerender(f.view(false));
    const previousFocus = document.activeElement;
    f.setResult({ job: f.job("succeeded"), session: f.child() });
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await waitFor(() => expect(f.store().parent(f.childId)).toBe(f.sourceId));
    expect(f.navigate).not.toHaveBeenCalled();
    expect(f.fork).toHaveBeenCalledTimes(1);
    const snapshot = f.store().snapshot(f.sourceId);
    expect(
      snapshot.tabs.some((tab) => tab.kind === SessionTabKind.PendingSidechat),
    ).toBe(false);
    if (mode === "closed") {
      expect(f.store().childDraft(f.childId)).toMatchObject({
        text: "My original question",
        start: 3,
        end: 8,
        focus: false,
      });
      expect(
        snapshot.tabs.some((tab) => tab.kind === SessionTabKind.Sidechat),
      ).toBe(false);
      act(() =>
        f.store().open(f.sourceId, {
          kind: SessionTabKind.Sidechat,
          id: f.childId,
          name: "Verified Sidechat",
        }),
      );
    }
    const childInput = document.getElementById(
      `prompt-${f.childId}`,
    ) as HTMLTextAreaElement;
    expect(childInput.value).toBe("My original question");
    expect(f.store().childDraft(f.childId)).toBeUndefined();
    if (mode === "selected") {
      expect(document.activeElement).toBe(childInput);
      expect(childInput.selectionStart).toBe(3);
      expect(childInput.selectionEnd).toBe(8);
    }
    if (mode === "hidden" || mode === "closed")
      expect(document.activeElement).not.toBe(childInput);

    if (mode === "background") {
      expect(snapshot.selected).toBe(SessionTabKind.Files);
      expect(document.activeElement).toBe(previousFocus);
    }
    if (mode === "selected")
      expect(snapshot.tabs[1]).toMatchObject({
        kind: SessionTabKind.Sidechat,
        id: f.childId,
      });
  },
);

it("lost creation response retains draft and replays only the exact original request explicitly", async () => {
  const f = await fixture({ lost: true });
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  const input = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Keep this draft" } });
  const retry = await screen.findByRole("button", {
    name: "Retry original Sidechat request",
  });
  expect(f.fork).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Discard Sidechat draft" }),
  ).toBeNull();
  fireEvent.click(retry);
  await waitFor(() => expect(f.observe).toHaveBeenCalled());
  expect(f.fork.mock.calls[1]![0]).toEqual(f.fork.mock.calls[0]![0]);
  expect(input).toHaveProperty("value", "Keep this draft");
});

it.each(["uncertain", "foreign-child", "missing-overlay", "foreign-job"])(
  "unsettled publication permits original status reads without replay: %s",
  async (mode) => {
    const f = await fixture();
    fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
    await waitFor(() => expect(f.observe).toHaveBeenCalled());
    const input = screen.getByRole("textbox", { name: "Message" });
    fireEvent.change(input, { target: { value: "Still mine" } });
    f.setResult({
      job: f.job(
        mode === "uncertain" ? "uncertain" : "succeeded",
        mode === "foreign-job" ? newRequestId() : undefined,
      ),
      session:
        mode === "uncertain"
          ? undefined
          : f.child(
              mode === "foreign-child" ? newRequestId() : undefined,
              mode !== "missing-overlay",
            ),
    });
    await act(async () => {
      await f.client.invalidateQueries();
    });
    const retry = await screen.findByRole("button", {
      name: "Retry original status read",
    });
    fireEvent.click(retry);
    await waitFor(() =>
      expect(f.observe.mock.calls.length).toBeGreaterThanOrEqual(3),
    );
    expect(f.fork).toHaveBeenCalledTimes(1);
    expect(input).toHaveProperty("value", "Still mine");
    expect(f.store().parent(f.childId)).toBeUndefined();
    expect(
      f.observe.mock.calls.every(
        (call) => call[0].jobId === f.job("claimed").id,
      ),
    ).toBe(true);
  },
);

it("fresh source revision mismatch retains draft without admitting creation", async () => {
  const f = await fixture({ stale: true });
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  await screen.findByText(/The original turn changed/);
  expect(f.fork).not.toHaveBeenCalled();
  expect(screen.getByRole("textbox", { name: "Message" })).toHaveProperty(
    "disabled",
    false,
  );
});

it("Runner read retry only reinspects original metadata without creating Sidechat", async () => {
  const f = await fixture({ runnerError: true });
  expect(f.fork).not.toHaveBeenCalled();
  f.setRunnerHealthy();
  fireEvent.click(
    screen.getByRole("menuitem", { name: "Recheck original fork Runner" }),
  );
  await screen.findByRole("menuitem", { name: "Open Sidechat" });
  expect(f.fork).not.toHaveBeenCalled();
  expect(f.observe).not.toHaveBeenCalled();
  expect(screen.queryByRole("textbox", { name: "Message" })).toBeNull();
});

it("confirmed failure retains the editable draft until explicit guarded discard", async () => {
  const f = await fixture();
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  await waitFor(() => expect(f.observe).toHaveBeenCalled());
  const input = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Failed but retained" } });
  f.setResult({ job: f.job("failed") });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await screen.findByText(/Sidechat preparation did not complete/);
  expect(input).toHaveProperty("value", "Failed but retained");
  expect(input).toHaveProperty("disabled", false);
  fireEvent.click(
    screen.getByRole("button", { name: "Discard Sidechat draft" }),
  );
  expect(
    f
      .store()
      .snapshot(f.sourceId)
      .tabs.some((tab) => tab.kind === SessionTabKind.PendingSidechat),
  ).toBe(false);
  expect(f.fork).toHaveBeenCalledTimes(1);
});

it("definite creation rejection with a healthy parent read offers no read retry and keeps the draft", async () => {
  const f = await fixture({ rejected: true });
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  const input = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Denied but retained" } });
  await waitFor(() => expect(f.fork).toHaveBeenCalledTimes(1));
  await screen.findByRole("button", { name: "Discard Sidechat draft" });
  expect(
    screen.queryByRole("button", { name: "Retry current read" }),
  ).toBeNull();
  expect(input).toHaveProperty("value", "Denied but retained");
  expect(input).toHaveProperty("disabled", false);
  expect(f.fork).toHaveBeenCalledTimes(1);
});

it("failed parent read retries only the read and never resends rejected creation", async () => {
  const f = await fixture({ rejected: true });
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  const input = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Still here" } });
  await waitFor(() => expect(f.fork).toHaveBeenCalledTimes(1));
  expect(
    screen.queryByRole("button", { name: "Retry current read" }),
  ).toBeNull();
  f.setSessionReadFailing(true);
  await act(async () => {
    await f.client.invalidateQueries();
  });
  const retry = await screen.findByRole("button", {
    name: "Retry current read",
  });
  const reads = f.read.mock.calls.length;
  f.setSessionReadFailing(false);
  fireEvent.click(retry);
  await waitFor(() => expect(f.read.mock.calls.length).toBeGreaterThan(reads));
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry current read" }),
    ).toBeNull(),
  );
  expect(f.fork).toHaveBeenCalledTimes(1);
  expect(input).toHaveProperty("value", "Still here");
  expect(
    screen.getByRole("button", { name: "Discard Sidechat draft" }),
  ).toBeTruthy();
});

it("definite error on explicit replay keeps the original uncertainty and discard unavailable", async () => {
  const f = await fixture({ lost: true, replayRejected: true });
  fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
  const input = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(input, { target: { value: "Keep this draft" } });
  const retry = await screen.findByRole("button", {
    name: "Retry original Sidechat request",
  });
  expect(
    screen.queryByRole("button", { name: "Retry current read" }),
  ).toBeNull();
  fireEvent.click(retry);
  await waitFor(() => expect(f.fork).toHaveBeenCalledTimes(2));
  expect(f.fork.mock.calls[1]![0]).toEqual(f.fork.mock.calls[0]![0]);
  await act(async () => {});
  expect(
    screen.getByRole("button", { name: "Retry original Sidechat request" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Discard Sidechat draft" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Retry current read" }),
  ).toBeNull();
  expect(input).toHaveProperty("value", "Keep this draft");
});

it.each(["parent-id", "empty-id"])(
  "malformed succeeded readback never publishes, keeps pending state and reinspects only the original job: %s",
  async (mode) => {
    const f = await fixture();
    const info = vi.spyOn(console, "info");
    fireEvent.click(screen.getByRole("menuitem", { name: "Open Sidechat" }));
    await waitFor(() => expect(f.observe).toHaveBeenCalled());
    const input = screen.getByRole("textbox", {
      name: "Message",
    }) as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "My original question" } });
    input.setSelectionRange(3, 8);
    fireEvent.select(input);
    f.setResult({
      job: f.job("succeeded"),
      session: f.child(
        f.sourceId,
        true,
        mode === "parent-id" ? f.sourceId : "",
      ),
    });
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await screen.findByText(/Sidechat could not be verified/);
    const retry = screen.getByRole("button", {
      name: "Retry original status read",
    });
    expect(f.store().parent(f.sourceId)).toBeUndefined();
    expect(f.store().parent("")).toBeUndefined();
    expect(f.store().sidechats(f.sourceId)).toEqual([]);
    const snapshot = f.store().snapshot(f.sourceId);
    expect(
      snapshot.tabs.filter((tab) => tab.kind === SessionTabKind.Sidechat),
    ).toEqual([]);
    expect(
      snapshot.tabs.filter(
        (tab) => tab.kind === SessionTabKind.PendingSidechat,
      ),
    ).toHaveLength(1);
    expect(input).toHaveProperty("value", "My original question");
    expect(
      info.mock.calls.some(
        (call) =>
          call[0] === "delidev.sidechat.preparation" &&
          (call[1] as { phase?: string }).phase === "published",
      ),
    ).toBe(false);
    const reads = f.observe.mock.calls.length;
    fireEvent.click(retry);
    await waitFor(() =>
      expect(f.observe.mock.calls.length).toBeGreaterThan(reads),
    );
    expect(f.fork).toHaveBeenCalledTimes(1);
    expect(
      f.observe.mock.calls.every(
        (call) => call[0].jobId === f.job("claimed").id,
      ),
    ).toBe(true);
    // A later valid distinct child publishes exactly once with the draft.
    f.setResult({ job: f.job("succeeded"), session: f.child() });
    await act(async () => {
      await f.client.invalidateQueries();
    });
    await waitFor(() => expect(f.store().parent(f.childId)).toBe(f.sourceId));
    expect(f.store().sidechats(f.sourceId)).toHaveLength(1);
    expect(
      (document.getElementById(`prompt-${f.childId}`) as HTMLTextAreaElement)
        .value,
    ).toBe("My original question");
    expect(
      info.mock.calls.filter(
        (call) =>
          call[0] === "delidev.sidechat.preparation" &&
          (call[1] as { phase?: string }).phase === "published",
      ),
    ).toHaveLength(1);
    expect(f.fork).toHaveBeenCalledTimes(1);
    info.mockRestore();
  },
);

async function retainedInA() {
  const f = await fixture({ second: true });
  fireEvent.click(
    screen.getAllByRole("menuitem", { name: "Open Sidechat" })[0]!,
  );
  const input = (await screen.findByRole("textbox", {
    name: "Message",
  })) as HTMLTextAreaElement;
  fireEvent.change(input, { target: { value: "Retained in A" } });
  input.setSelectionRange(2, 6);
  fireEvent.select(input);
  await waitFor(() => expect(f.fork).toHaveBeenCalledTimes(1));
  const pending = f
    .store()
    .snapshot(f.sourceId)
    .tabs.find((tab) => tab.kind === SessionTabKind.PendingSidechat)!;
  const callbacks: FrameRequestCallback[] = [];
  vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation((callback) =>
    callbacks.push(callback),
  );
  const openFromB = () =>
    fireEvent.click(
      within(
        screen.getByRole("region", { name: "Session B actions" }),
      ).getByRole("menuitem", { name: "Open Sidechat" }),
    );
  const flush = () =>
    act(() => {
      for (const callback of callbacks.splice(0)) callback(0);
    });
  return { f, input, pending, callbacks, openFromB, flush };
}

it("opening Sidechat from another Session routes to the retained parent and focuses the original draft only after it is active", async () => {
  const { f, input, pending, callbacks, openFromB, flush } =
    await retainedInA();
  // Session B is current: A's workspace is hidden, so nothing may be focused yet.
  f.rendered.rerender(f.view(false));
  f.store().select(f.sourceId, SessionTabKind.Conversation);
  openFromB();
  expect(f.navigate).toHaveBeenCalledTimes(1);
  expect(f.navigate).toHaveBeenCalledWith(f.sourceId);
  expect(f.store().snapshot(f.sourceId).selected).toBe(sessionTabKey(pending));
  expect(callbacks).toHaveLength(1);
  // The route activates A before the deferred focus callback runs.
  f.rendered.rerender(f.view(true));
  flush();
  expect(document.activeElement).toBe(input);
  expect(input).toHaveProperty("value", "Retained in A");
  expect(input.selectionStart).toBe(2);
  expect(input.selectionEnd).toBe(6);
  expect(f.fork).toHaveBeenCalledTimes(1);
  expect(
    f
      .store()
      .snapshot(f.sourceId)
      .tabs.filter((tab) => tab.kind === SessionTabKind.PendingSidechat),
  ).toHaveLength(1);
  expect(f.store().snapshot(f.sourceId).tabs).toContainEqual(pending);
  // Repeating reuses the same tab and still sends no second request.
  openFromB();
  expect(f.navigate).toHaveBeenCalledTimes(2);
  expect(
    f
      .store()
      .snapshot(f.sourceId)
      .tabs.filter((tab) => tab.kind === SessionTabKind.PendingSidechat),
  ).toHaveLength(1);
  expect(f.fork).toHaveBeenCalledTimes(1);
});

it("a deferred reopen focus callback cannot steal focus after later navigation away", async () => {
  const { f, input, callbacks, openFromB, flush } = await retainedInA();
  openFromB();
  expect(callbacks).toHaveLength(1);
  const elsewhere = document.createElement("button");
  document.body.append(elsewhere);
  elsewhere.focus();
  // Navigating to Session B hides A before the callback fires.
  f.rendered.rerender(f.view(false));
  flush();
  expect(document.activeElement).toBe(elsewhere);
  expect(document.activeElement).not.toBe(input);
  elsewhere.remove();
  // A newer reopen gesture invalidates an older callback.
  f.rendered.rerender(f.view(true));
  openFromB();
  openFromB();
  expect(callbacks).toHaveLength(2);
  document.body.append(elsewhere);
  elsewhere.focus();
  const [stale, current] = callbacks.splice(0);
  act(() => stale!(0));
  expect(document.activeElement).toBe(elsewhere);
  act(() => current!(0));
  expect(document.activeElement).toBe(input);
  elsewhere.remove();
  expect(f.fork).toHaveBeenCalledTimes(1);
});

it("a deferred reopen focus callback cannot steal focus after leaving and returning to the same Session", async () => {
  const { f, input, pending, callbacks, openFromB, flush } =
    await retainedInA();
  openFromB();
  expect(callbacks).toHaveLength(1);
  f.navigateSession();
  f.rendered.rerender(f.view(false));
  f.navigateSession();
  f.rendered.rerender(f.view(true));
  expect(f.store().snapshot(f.sourceId).selected).toBe(sessionTabKey(pending));
  expect(input.closest("[hidden], [inert]")).toBeNull();
  const laterFocus = document.createElement("button");
  document.body.append(laterFocus);
  laterFocus.focus();
  flush();
  expect(document.activeElement).toBe(laterFocus);
  expect(input.value).toBe("Retained in A");
  expect(f.store().snapshot(f.sourceId).tabs).toContainEqual(pending);
  expect(f.fork).toHaveBeenCalledTimes(1);
  laterFocus.remove();
});

it("a deferred reopen focus callback cannot steal focus after leaving and returning to the same tab", async () => {
  const { f, input, pending, callbacks, openFromB, flush } =
    await retainedInA();
  openFromB();
  expect(callbacks).toHaveLength(1);
  act(() => {
    f.store().open(f.sourceId, { kind: SessionTabKind.Files });
    f.store().select(f.sourceId, sessionTabKey(pending));
  });
  expect(f.store().snapshot(f.sourceId).selected).toBe(sessionTabKey(pending));
  expect(input.closest("[hidden], [inert]")).toBeNull();
  const laterFocus = document.createElement("button");
  document.body.append(laterFocus);
  laterFocus.focus();
  flush();
  expect(document.activeElement).toBe(laterFocus);
  expect(input.value).toBe("Retained in A");
  expect(f.store().snapshot(f.sourceId).tabs).toContainEqual(pending);
  expect(f.fork).toHaveBeenCalledTimes(1);
  laterFocus.remove();
});

it("a deferred reopen focus callback ignores a changed tab", async () => {
  const { f, callbacks, openFromB, flush } = await retainedInA();
  openFromB();
  expect(callbacks).toHaveLength(1);
  act(() => f.store().open(f.sourceId, { kind: SessionTabKind.Files }));
  const laterFocus = document.createElement("button");
  document.body.append(laterFocus);
  laterFocus.focus();
  flush();
  expect(document.activeElement).toBe(laterFocus);
  expect(f.fork).toHaveBeenCalledTimes(1);
  laterFocus.remove();
});

it("a deferred reopen focus callback ignores provider disposal", async () => {
  const { f, callbacks, openFromB, flush } = await retainedInA();
  openFromB();
  expect(callbacks).toHaveLength(1);
  f.rendered.unmount();
  const laterFocus = document.createElement("button");
  document.body.append(laterFocus);
  laterFocus.focus();
  flush();
  expect(document.activeElement).toBe(laterFocus);
  expect(f.fork).toHaveBeenCalledTimes(1);
  laterFocus.remove();
});

it("automatic publication while backgrounded or closed never navigates or reopens presentation", async () => {
  const { f, openFromB, flush } = await retainedInA();
  openFromB();
  expect(f.navigate).toHaveBeenCalledTimes(1);
  f.navigate.mockClear();
  act(() => f.store().open(f.sourceId, { kind: SessionTabKind.Files }));
  f.setResult({ job: f.job("succeeded"), session: f.child() });
  await act(async () => {
    await f.client.invalidateQueries();
  });
  await waitFor(() => expect(f.store().parent(f.childId)).toBe(f.sourceId));
  flush();
  expect(f.navigate).not.toHaveBeenCalled();
  expect(f.store().snapshot(f.sourceId).selected).toBe(SessionTabKind.Files);
  expect(f.fork).toHaveBeenCalledTimes(1);
});
