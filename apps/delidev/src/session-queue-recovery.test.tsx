// SPDX-License-Identifier: Apache-2.0
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, EventAction, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SessionQuery, SystemService, WatchEventsResponseSchema, newRequestId, type RemoveQueuedInputRequest, type WatchEventsResponse } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

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
