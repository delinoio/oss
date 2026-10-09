// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, EventAction, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService, WatchEventsResponseSchema, newRequestId, type EnqueueInputRequest, type Resource, type WatchEventsResponse } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import { sessionInputReceipt } from "./test-session-input";

function fixture() {
  const make = (name: string) => {
    const id = newRequestId();
    return create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 1n, schemaVersion: 1, documentJson: encode({ name, workspace: "general-chat", archive: "active", outcome: "stopped", dispatch: "blocked", recovery: "required" }) });
  };
  const original = make("Immediate original"), other = make("Immediate other");
  const resources = new Map<string, Resource>([[original.id, original], [other.id, other]]);
  const channels = new Set<{ sessionId: string; events: WatchEventsResponse[]; notify: () => void }>();
  const receipt = (request: EnqueueInputRequest) => {
    const result = sessionInputReceipt(original, request); resources.set(result.change.input.id, result.change.input); return result;
  };
  const enqueue = vi.fn(async (request: EnqueueInputRequest) => receipt(request));
  const control = vi.fn(() => ({}));
  const list = vi.fn((_request: { filter?: { kind?: EntityKind; pageToken?: string } }) => ({ resources: [] as Resource[] }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, { listSessions: () => ({ sessions: [original, other] }), listQueue: () => ({ inputs: [] }), enqueueInput: enqueue, controlSession: control });
    router.service(ResourceService, {
      getResource: request => ({ resource: resources.get(request.id) }), getSnapshot: request => ({ resources: [resources.get(request.filter!.sessionId)!], cursor: "original-snapshot" }), listResources: list,
      async *watchEvents(request, context) {
        const channel = { sessionId: request.sessionId, events: [] as WatchEventsResponse[], notify: () => {} };
        channels.add(channel);
        const aborted = () => channel.notify(); context.signal.addEventListener("abort", aborted);
        try {
          while (!context.signal.aborted) {
            if (!channel.events.length) await new Promise<void>(resolve => { channel.notify = resolve; if (context.signal.aborted) resolve(); });
            while (channel.events.length && !context.signal.aborted) yield channel.events.shift()!;
          }
        } finally { channels.delete(channel); context.signal.removeEventListener("abort", aborted); }
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n, interactions: true }) }) });
  });
  const publish = (resource: Resource) => {
    resources.set(resource.id, resource);
    for (const channel of channels) if (channel.sessionId === resource.sessionId) {
      channel.events.push(create(WatchEventsResponseSchema, { id: newRequestId(), cursor: newRequestId(), entityId: resource.id, kind: resource.kind, sessionId: resource.sessionId, revision: resource.revision, action: EventAction.UPDATED })); channel.notify();
    }
  };
  const enter = async () => { fireEvent.click(await screen.findByRole("button", { name: /General Chat Immediate original/ })); return screen.findByRole("textbox", { name: "Message" }); };
  const send = async () => { const input = await enter(); fireEvent.change(input, { target: { value: "웹 검색 할 줄 알아?" } }); fireEvent.click(screen.getByRole("button", { name: "Queue message" })); return input; };
  return { original, other, transport, enqueue, control, list, publish, enter, send, receipt, resources };
}

it("shows Sending before enqueue settles, then advances through queue and one native message without Resume", async () => {
  const f = fixture(); let finish!: () => void;
  f.enqueue.mockImplementationOnce(async request => { await new Promise<void>(resolve => { finish = resolve; }); return f.receipt(request); });
  render(<App transport={f.transport} />); const input = await f.send();
  const projected = screen.getByRole("article", { name: "Submitted user message" });
  expect(within(projected).getByText("웹 검색 할 줄 알아?")).toBeDefined(); expect(within(projected).getByRole("status").textContent).toBe("Sending");
  await waitFor(() => expect(f.enqueue).toHaveBeenCalledOnce()); expect(input).toHaveProperty("disabled", true);
  await act(async () => finish()); await within(projected).findByText("Queued"); expect(input).toHaveProperty("value", "");
  const queue = (await f.enqueue.mock.results[0].value).change.input;
  for (const [delivery, label, revision] of [["claimed", "Delivery in progress", 2n], ["accepted", "Accepted by runner", 3n]] as const) {
    await act(async () => f.publish(create(ResourceSchema, { ...queue, revision, documentJson: encode({ prompt: "웹 검색 할 줄 알아?", mode: "execute", delivery }) })));
    await within(projected).findByText(label);
  }
  await act(async () => f.publish(create(ResourceSchema, { id: newRequestId(), sessionId: f.original.id, kind: EntityKind.MESSAGE, schemaVersion: 1, revision: 1n, documentJson: encode({ role: "user", state: "complete", input_id: queue.id, text: "웹 검색 할 줄 알아?" }) })));
  await waitFor(() => expect(screen.queryByRole("article", { name: "Submitted user message" })).toBeNull());
  expect(within(screen.getByLabelText("Conversation", { selector: ".transcript" })).getAllByText("웹 검색 할 줄 알아?")).toHaveLength(1); expect(f.control).not.toHaveBeenCalled();
});

it("keeps the immediate projection beside retained historical messages without input identities", async () => {
  const f = fixture();
  const historical = [
    { role: "user", state: "complete", text: "웹 검색 할 줄 알아?" },
    { role: "assistant", state: "complete", text: "Historical assistant without input identity" },
  ].map(document => create(ResourceSchema, { id: newRequestId(), sessionId: f.original.id, kind: EntityKind.MESSAGE, schemaVersion: 1, revision: 1n, documentJson: encode(document) }));
  f.list.mockImplementation(request => ({ resources: request.filter?.kind === EntityKind.MESSAGE ? historical : [] }));
  let finish!: () => void;
  f.enqueue.mockImplementationOnce(async request => { await new Promise<void>(resolve => { finish = resolve; }); return f.receipt(request); });
  render(<App transport={f.transport} />);
  const input = await f.enter();
  const transcript = screen.getByLabelText("Conversation", { selector: ".transcript" });
  await within(transcript).findByText("Historical assistant without input identity");
  fireEvent.change(input, { target: { value: "웹 검색 할 줄 알아?" } });
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  const projected = within(transcript).getByRole("article", { name: "Submitted user message" });
  expect(within(projected).getByRole("status").textContent).toBe("Sending");
  expect(within(transcript).getAllByText("웹 검색 할 줄 알아?")).toHaveLength(2);
  await waitFor(() => expect(f.enqueue).toHaveBeenCalledOnce());
  await act(async () => finish());
  await within(projected).findByText("Queued");
  const queue = (await f.enqueue.mock.results[0].value).change.input;
  await act(async () => f.publish(create(ResourceSchema, { id: newRequestId(), sessionId: f.original.id, kind: EntityKind.MESSAGE, schemaVersion: 1, revision: 1n, documentJson: encode({ role: "user", state: "complete", input_id: queue.id, text: "웹 검색 할 줄 알아?" }) })));
  await waitFor(() => expect(within(transcript).queryByRole("article", { name: "Submitted user message" })).toBeNull());
  // The historical identical prompt remains separate from the exact new input.
  expect(within(transcript).getAllByText("웹 검색 할 줄 알아?")).toHaveLength(2);
  expect(within(transcript).getByText("Historical assistant without input identity")).toBeDefined();
  expect(f.control).not.toHaveBeenCalled();
});

it.each(["uncertain", "malformed", "rejected"])("keeps truthful %s status and original retry/draft ownership", async scenario => {
  const f = fixture();
  if (scenario === "malformed") f.enqueue.mockImplementationOnce(async () => ({ change: { session: f.original } }) as never);
  else f.enqueue.mockRejectedValueOnce(new ConnectError("Synthetic failure", scenario === "uncertain" ? Code.Unavailable : Code.PermissionDenied));
  render(<App transport={f.transport} />); const input = await f.send();
  await screen.findByText(scenario === "rejected" ? "Rejected" : "Confirmation unavailable");
  expect(input).toHaveProperty("value", "웹 검색 할 줄 알아?"); expect(f.control).not.toHaveBeenCalled();
  if (scenario === "rejected") { expect(input).toHaveProperty("disabled", false); expect(screen.queryByRole("button", { name: "Retry the same message" })).toBeNull(); return; }
  expect(input).toHaveProperty("disabled", true);
  const original = f.enqueue.mock.calls[0][0]; fireEvent.click(screen.getByRole("button", { name: "Retry the same message" }));
  await screen.findByText("Queued"); expect(f.enqueue.mock.calls[1][0]).toEqual(original);
});

it("settles the original queue mapping while unmounted through same-identity reconnect and fences identity replacement", async () => {
  const f = fixture(); let finish!: () => void;
  f.enqueue.mockImplementationOnce(async request => { await new Promise<void>(resolve => { finish = resolve; }); return f.receipt(request); });
  const identity = { pairingAuthority: { endpoint: "http://127.0.0.1:46399", serverId: newRequestId() }, currentDeviceId: newRequestId() };
  const mounted = render(<App transport={f.transport} {...identity} />); await f.send(); await waitFor(() => expect(f.enqueue).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: /General Chat Immediate other/ }));
  const other = await screen.findByRole("textbox", { name: "Message" }); fireEvent.change(other, { target: { value: "Keep independent draft" } });
  mounted.rerender(<App transport={{ ...f.transport }} {...identity} connectionEpoch={1} />);
  await act(async () => finish()); expect(other).toHaveProperty("value", "Keep independent draft");
  await f.enter(); await screen.findByText("Queued"); expect(screen.getByRole("textbox", { name: "Message" })).toHaveProperty("value", "");
  mounted.rerender(<App transport={f.transport} {...identity} currentDeviceId={newRequestId()} />);
  await f.enter(); expect(screen.queryByRole("article", { name: "Submitted user message" })).toBeNull();
});

it("adopts a stream-before-receipt edit and replaces a stream-before-receipt native message", async () => {
  const f = fixture(); let finish!: () => void;
  let receipt!: ReturnType<typeof sessionInputReceipt>;
  f.enqueue.mockImplementationOnce(async request => { receipt = f.receipt(request); await new Promise<void>(resolve => { finish = resolve; }); return receipt; });
  render(<App transport={f.transport} />); await f.send(); await waitFor(() => expect(f.enqueue).toHaveBeenCalledOnce());
  await act(async () => f.publish(create(ResourceSchema, { ...receipt.change.input, revision: 5n, documentJson: encode({ prompt: "Authoritative edited input", mode: "execute", delivery: "queued" }) })));
  await act(async () => finish()); await within(screen.getByRole("article", { name: "Submitted user message" })).findByText("Authoritative edited input");
  expect(screen.queryByText("웹 검색 할 줄 알아?", { selector: "article[data-submission] pre" })).toBeNull();
  // A distinct identical prompt stays projected until its own input identity arrives.
  let secondFinish!: () => void; let secondReceipt!: ReturnType<typeof sessionInputReceipt>;
  f.enqueue.mockImplementationOnce(async request => { secondReceipt = f.receipt(request); await new Promise<void>(resolve => { secondFinish = resolve; }); return secondReceipt; });
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), { target: { value: "Authoritative edited input" } }); fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await waitFor(() => expect(f.enqueue).toHaveBeenCalledTimes(2));
  await act(async () => f.publish(create(ResourceSchema, { id: newRequestId(), sessionId: f.original.id, kind: EntityKind.MESSAGE, schemaVersion: 1, revision: 1n, documentJson: encode({ role: "user", state: "complete", input_id: secondReceipt.change.input.id, text: "Authoritative edited input" }) })));
  await act(async () => secondFinish()); await waitFor(() => expect(screen.getAllByRole("article", { name: "Submitted user message" })).toHaveLength(1));
});

it("reinspects a removed original queue identity after navigation without loading all history", async () => {
  const f = fixture(); render(<App transport={f.transport} />); await f.send(); await screen.findByText("Queued");
  const queue = (await f.enqueue.mock.results[0].value).change.input;
  fireEvent.click(screen.getByRole("button", { name: /General Chat Immediate other/ })); await screen.findByRole("heading", { name: "Immediate other" });
  f.resources.set(queue.id, create(ResourceSchema, { ...queue, revision: 4n, documentJson: encode({ prompt: "", delivery: "removed", mode: "execute" }) }));
  await f.enter(); await within(screen.getByRole("article", { name: "Submitted user message" })).findByText("Removed");
  expect(within(screen.getByRole("article", { name: "Submitted user message" })).queryByText("웹 검색 할 줄 알아?")).toBeNull();
  expect(f.enqueue).toHaveBeenCalledOnce(); expect(f.control).not.toHaveBeenCalled();
  expect(f.list.mock.calls.every(call => !call.length || !((call[0] as { filter?: { kind?: EntityKind; pageToken?: string } }).filter?.pageToken))).toBe(true);
});
it("ignores a late accepted response after connection identity replacement", async () => {
  const f = fixture(); let finish!: () => void;
  f.enqueue.mockImplementationOnce(async request => { await new Promise<void>(resolve => { finish = resolve; }); return f.receipt(request); });
  const identity = { pairingAuthority: { endpoint: "http://127.0.0.1:46399", serverId: newRequestId() }, currentDeviceId: newRequestId() };
  const mounted = render(<App transport={f.transport} {...identity} />); await f.send(); await waitFor(() => expect(f.enqueue).toHaveBeenCalledOnce());
  mounted.rerender(<App transport={f.transport} {...identity} currentDeviceId={newRequestId()} />);
  const input = await f.enter(); fireEvent.change(input, { target: { value: "Replacement connection draft" } });
  await act(async () => finish()); expect(input).toHaveProperty("value", "Replacement connection draft"); expect(screen.queryByRole("article", { name: "Submitted user message" })).toBeNull();
});
