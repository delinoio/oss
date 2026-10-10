// SPDX-License-Identifier: Apache-2.0
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, EventAction, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SessionQuery, SystemService, WatchEventsResponseSchema, newRequestId, type RemoveQueuedInputRequest, type WatchEventsResponse } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import { i18n } from "./localization";

it("retains the original removal in SessionView after streamed tombstone and resnapshot", async () => {
  const makeSession = (name: string) => { const id = newRequestId(); return create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name, workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "clear" }) }); };
  const original = makeSession("Queue recovery original"), other = makeSession("Queue recovery other");
  const input = create(ResourceSchema, { id: newRequestId(), sessionId: original.id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ prompt: "Original removable input", sequence: 1, delivery: "queued", mode: "plan" }) });
  const tombstone = create(ResourceSchema, { ...input, revision: 2n, documentJson: encode({ delivery: "removed", sequence: 1, mode: "plan" }) });
  const resources = new Map([[original.id, original], [other.id, other], [input.id, input]]);
  let removed = false, accounting = 0;
  const receipts = new Map<string, { change: { requestId: string; input: typeof input; session: typeof original } }>();
  const remove = vi.fn(async (request: RemoveQueuedInputRequest) => {
    const id = request.mutation!.requestId;
    if (!receipts.has(id)) { receipts.set(id, { change: { requestId: id, input: tombstone, session: original } }); removed = true; accounting++; resources.set(input.id, tombstone); throw new ConnectError("Lost original acknowledgment", Code.Unavailable); }
    return receipts.get(id)!;
  });
  const channels = new Set<{ sessionId: string; events: WatchEventsResponse[]; notify: () => void }>();
  const snapshot = vi.fn((request: { filter?: { sessionId: string } }) => ({ resources: [resources.get(request.filter!.sessionId)!], cursor: newRequestId() }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [original, other] }), listQueue: request => ({ inputs: !removed && request.sessionId === original.id ? [input] : [] }), removeQueuedInput: remove });
    router.service(ResourceService, { getResource: request => ({ resource: resources.get(request.id) }), getSnapshot: snapshot, listResources: () => ({ resources: [] }),
      async *watchEvents(request, context) {
        const channel = { sessionId: request.sessionId, events: [] as WatchEventsResponse[], notify: () => {} }; channels.add(channel);
        const aborted = () => channel.notify(); context.signal.addEventListener("abort", aborted);
        try { while (!context.signal.aborted) { if (!channel.events.length) await new Promise<void>(resolve => { channel.notify = resolve; if (context.signal.aborted) resolve(); }); while (channel.events.length && !context.signal.aborted) yield channel.events.shift()!; } }
        finally { channels.delete(channel); context.signal.removeEventListener("abort", aborted); }
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
  });
  render(<App transport={transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Queue recovery original/ }));
  await screen.findByRole("textbox", { name: "Message" });
  fireEvent.click(await screen.findByRole("button", { name: "Remove input" }));
  await screen.findByRole("button", { name: "Retry the same removal" });
  await waitFor(() => expect([...channels].some(channel => channel.sessionId === original.id)).toBe(true));
  await act(async () => { for (const channel of channels) if (channel.sessionId === original.id) { channel.events.push(create(WatchEventsResponseSchema, { id: newRequestId(), cursor: newRequestId(), entityId: input.id, kind: EntityKind.QUEUE, sessionId: original.id, revision: 2n, action: EventAction.UPDATED })); channel.notify(); } });
  await waitFor(() => expect(screen.queryByText("Original removable input")).toBeNull());
  expect(screen.getByRole("region", { name: "Pending input actions" })).toBeDefined();
  expect(screen.getAllByRole("button", { name: "Retry the same removal" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: /General Chat Queue recovery other/ }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry the same removal" })).toBeNull());
  const before = snapshot.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: /General Chat Queue recovery original/ }));
  await waitFor(() => expect(snapshot.mock.calls.length).toBeGreaterThan(before));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same removal" }));
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(2));
  expect(toBinary(SessionQuery.removeQueuedInput.input, remove.mock.calls[1]![0])).toEqual(toBinary(SessionQuery.removeQueuedInput.input, remove.mock.calls[0]![0]));
  expect(accounting).toBe(1);
  await waitFor(() => expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull());
  expect(screen.queryByText("Original removable input")).toBeNull();
}, 15000);

it("shows one retained retry for an image-rejected live arrival outside queue pages", async () => {
  const makeSession = (name: string) => { const id = newRequestId(); return create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name, workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "clear" }) }); };
  const original = makeSession("Queue recovery original"), other = makeSession("Queue recovery other");
  const input = create(ResourceSchema, { id: newRequestId(), sessionId: original.id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ prompt: "Original removable input", sequence: 1, delivery: "queued", mode: "plan" }) });
  const execution = newRequestId(), job = newRequestId();
  original.documentJson = encode({ name: "Queue recovery original", workspace: "general-chat", outcome: "not-started", archive: "active", dispatch: "paused", recovery: "none", initial_execution: { id: execution, input_id: input.id }, startup: { job_id: job, execution_id: execution, failure: { state: 2, phase: 5, harness: "codex", problem_code: "unsupported", correlation_id: job, input_delivery: 1, cleanup: 1, failure_kind: 1 } } });
  const tombstone = create(ResourceSchema, { ...input, revision: 2n, documentJson: encode({ prompt: "Original removable input", delivery: "rejected-before-start", execution_id: execution, sequence: 1, mode: "plan" }) });
  const resources = new Map([[original.id, original], [other.id, other], [input.id, input]]);
  let removed = false, accounting = 0;
  const receipts = new Map<string, { change: { requestId: string; input: typeof input; session: typeof original } }>();
  const remove = vi.fn(async (request: RemoveQueuedInputRequest) => {
    const id = request.mutation!.requestId;
    if (!receipts.has(id)) { receipts.set(id, { change: { requestId: id, input: tombstone, session: original } }); removed = true; accounting++; resources.set(input.id, tombstone); throw new ConnectError("Lost original acknowledgment", Code.Unavailable); }
    return receipts.get(id)!;
  });
  const channels = new Set<{ sessionId: string; events: WatchEventsResponse[]; notify: () => void }>();
  const snapshot = vi.fn((request: { filter?: { sessionId: string } }) => ({ resources: [resources.get(request.filter!.sessionId)!], cursor: newRequestId() }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [original, other] }), listQueue: request => ({ inputs: [] }), removeQueuedInput: remove });
    router.service(ResourceService, { getResource: request => ({ resource: resources.get(request.id) }), getSnapshot: snapshot, listResources: () => ({ resources: [] }),
      async *watchEvents(request, context) {
        const channel = { sessionId: request.sessionId, events: [] as WatchEventsResponse[], notify: () => {} }; channels.add(channel);
        const aborted = () => channel.notify(); context.signal.addEventListener("abort", aborted);
        try { while (!context.signal.aborted) { if (!channel.events.length) await new Promise<void>(resolve => { channel.notify = resolve; if (context.signal.aborted) resolve(); }); while (channel.events.length && !context.signal.aborted) yield channel.events.shift()!; } }
        finally { channels.delete(channel); context.signal.removeEventListener("abort", aborted); }
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
  });
  render(<App transport={transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Queue recovery original/ }));
  await screen.findByRole("textbox", { name: "Message" });
  await waitFor(() => expect([...channels].some(channel => channel.sessionId === original.id)).toBe(true));
  await act(async () => { for (const channel of channels) if (channel.sessionId === original.id) { channel.events.push(create(WatchEventsResponseSchema, { id: newRequestId(), cursor: newRequestId(), entityId: input.id, kind: EntityKind.QUEUE, sessionId: original.id, revision: 1n, action: EventAction.CREATED })); channel.notify(); } });
  fireEvent.click(await screen.findByRole("button", { name: "Remove input" }));
  await screen.findByRole("button", { name: "Retry the same removal" });
  await waitFor(() => expect([...channels].some(channel => channel.sessionId === original.id)).toBe(true));
  await act(async () => { for (const channel of channels) if (channel.sessionId === original.id) { channel.events.push(create(WatchEventsResponseSchema, { id: newRequestId(), cursor: newRequestId(), entityId: input.id, kind: EntityKind.QUEUE, sessionId: original.id, revision: 2n, action: EventAction.UPDATED })); channel.notify(); } });
  await waitFor(() => expect(screen.queryByRole("button", { name: "Remove input" })).toBeNull());
  expect(screen.getAllByText("Original removable input").length).toBeGreaterThan(0);
  expect(screen.getByText("1 input rejected because images are unsupported")).toBeDefined();
  expect(screen.queryByRole("region", { name: "Pending input actions" })).toBeNull();
  expect(screen.getAllByRole("button", { name: "Retry the same removal" })).toHaveLength(1);
}, 15000);


it.each([
  { locale: "en", valid: true, waiting: false, label: "1 input rejected because images are unsupported" },
  { locale: "en", valid: true, waiting: true, label: "1 input rejected because images are unsupported" },
  { locale: "ko", valid: true, waiting: true, label: "이미지 지원 문제로 거부된 입력 1개" },
  { locale: "en", valid: false, waiting: false, label: "1 input rejected because images are unsupported" },
])("counts only validated retained image rejection ($locale, proof=$valid, waiting=$waiting)", async ({ locale, valid, waiting, label }) => {
  await i18n.changeLanguage(locale);
  try {
    const id = newRequestId(), inputId = newRequestId(), execution = newRequestId(), job = newRequestId();
    const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Image rejection count", workspace: "general-chat", outcome: "not-started", archive: "active", dispatch: "paused", recovery: "none", initial_execution: { id: execution, input_id: inputId }, startup: { job_id: job, execution_id: execution, failure: { state: 2, phase: 5, harness: "codex", problem_code: "unsupported", correlation_id: job, input_delivery: 1, cleanup: valid ? 1 : 0, failure_kind: 1 } } }) });
    const rejected = create(ResourceSchema, { id: inputId, sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ prompt: "Original rejected prompt", delivery: "rejected-before-start", execution_id: execution, sequence: 1, mode: "plan" }) });
    const queued = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ prompt: "Independent waiting prompt", delivery: "queued", sequence: 2, mode: "plan" }) });
    const rows = waiting ? [rejected, queued] : [rejected];
    const transport = createRouterTransport(router => {
      router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
      router.service(SessionService, { listSessions: () => ({ sessions: [session] }), listQueue: () => ({ inputs: rows }) });
      router.service(ResourceService, { getResource: request => ({ resource: [session, ...rows].find(row => row.id === request.id) }), getSnapshot: () => ({ resources: [session], cursor: newRequestId() }), listResources: () => ({ resources: [] }), async *watchEvents(_request, context) { await new Promise<void>(resolve => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); }); } });
      router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
    });
    const view = render(<App transport={transport} />);
    fireEvent.click(await screen.findByRole("button", { name: /Image rejection count/ }));
    if (valid) {
      expect(await screen.findByText(label)).toBeDefined();
      expect(screen.getByText("Original rejected prompt")).toBeDefined();
      expect(screen.getByText("Original rejected prompt").closest("details")).toBeNull();
    } else {
      await screen.findByRole("textbox", { name: "Message" });
      expect(screen.queryByText(label)).toBeNull();
      expect(screen.queryByText("Original rejected prompt")).toBeNull();
    }
    if (waiting) expect(await screen.findByText("Independent waiting prompt")).toBeDefined();
    view.unmount();
  } finally { await i18n.changeLanguage("en"); }
});

it("keeps the legacy empty queue hidden through three automatic same-connection rereads and preserves the composer", async () => {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Stable empty queue", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "clear" }) });
  const read = vi.fn(async () => ({ inputs: [] }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [session] }), listQueue: read });
    router.service(ResourceService, { getResource: () => ({ resource: session }), getSnapshot: () => ({ resources: [session], cursor: newRequestId() }), listResources: () => ({ resources: [] }), async *watchEvents(_request, context) { await new Promise<void>(resolve => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); }); } });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
  });
  const view = render(<App transport={transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /Stable empty queue/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Retained multiline\ndraft" } });
  composer.focus();
  const surface = view.container.querySelector<HTMLDivElement>(".queue-read-state")!;
  await waitFor(() => expect(surface.hidden).toBe(true));
  for (let cycle = 0; cycle < 3; cycle++) {
    let release: () => void = () => {};
    const before = read.mock.calls.length;
    read.mockImplementationOnce(async () => { await new Promise<void>(resolve => { release = resolve; }); return { inputs: [] }; });
    view.rerender(<App transport={transport} connectionEpoch={cycle + 1} />);
    await waitFor(() => expect(read.mock.calls.length).toBeGreaterThan(before));
    expect(surface.hidden).toBe(true);
    const status = screen.getByText("Loading input queue…");
    expect(status.classList.contains("sidebar-sr-only")).toBe(true);
    expect(surface.contains(status)).toBe(false);
    expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
    expect((composer as HTMLTextAreaElement).value).toBe("Retained multiline\ndraft");
    expect(document.activeElement).toBe(composer);
    await act(async () => release());
    await waitFor(() => expect(surface.classList.contains("queue-background-read")).toBe(false));
    expect(surface.hidden).toBe(true);
  }
});
