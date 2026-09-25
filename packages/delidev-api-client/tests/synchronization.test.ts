import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient, createRouterTransport } from "@connectrpc/connect";
import { expect, it, vi } from "vitest";
import {
  EntityKind, ErrorDetailSchema, EventAction, ResourceSchema, ResourceService, WatchEventsResponseSchema,
  type Resource, type GetResourceRequest,
} from "../src/gen/delidev/v1/delidev_pb.js";
import { ConnectionState, SyncKind, synchronizeResources, type SyncUpdate } from "../src/synchronization.js";
import { newRequestId } from "../src/validation.js";

function resource(revision = 1n): Resource {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MESSAGE, revision, schemaVersion: 1, sessionId: newRequestId(), documentJson: new TextEncoder().encode('{"text":"retained"}') });
}
function event(r: Resource, revision = r.revision, action = EventAction.UPDATED, cursor = `cursor-${revision}`) {
  return create(WatchEventsResponseSchema, { id: newRequestId(), entityId: r.id, kind: r.kind, sessionId: r.sessionId, revision, action, cursor });
}
const expired = () => new ConnectError("Cursor expired.", Code.OutOfRange, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "cursor_expired" }) }]);

it.each([Code.Unavailable, Code.DeadlineExceeded, Code.Unknown])("applies indexed revisions once and resumes the committed cursor after %s", async (code) => {
  const first = resource();
  const update = event(first, 2n);
  const deletion = event(first, 3n, EventAction.DELETED);
  const controls = new AbortController();
  const cursors: string[] = [];
  const snapshots = vi.fn(() => ({ resources: [first], cursor: "snapshot" }));
  const reads = vi.fn((_request: GetResourceRequest) => ({ resource: { ...first, revision: 2n } }));
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: snapshots,
    getResource: reads,
    async *watchEvents(request) {
      cursors.push(request.cursor);
      if (cursors.length === 1) { yield update; throw new ConnectError("Disconnected.", code); }
      yield update;
      yield deletion;
    },
  })));
  const updates: SyncUpdate[] = [];
  for await (const value of synchronizeResources(client, { kind: first.kind, sessionId: first.sessionId }, { signal: controls.signal, initialRetryMs: 1 })) {
    updates.push(value);
    if (value.kind === SyncKind.Remove) controls.abort();
  }
  expect(snapshots).toHaveBeenCalledTimes(1);
  expect(reads).toHaveBeenCalledTimes(1);
  expect(reads.mock.calls[0][0]).toMatchObject({ kind: first.kind, id: first.id });
  expect(cursors).toEqual(["snapshot", update.cursor]);
  expect(updates.filter((u) => u.kind === SyncKind.Upsert)).toHaveLength(1);
  expect(updates.filter((u) => u.kind === SyncKind.Remove)).toHaveLength(1);
});

it("resnapshots on the typed gap error without replaying mutations", async () => {
  const first = resource();
  const newer = { ...first, revision: 9n };
  const controls = new AbortController();
  let count = 0;
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: () => ({ resources: [++count === 1 ? first : newer], cursor: `snapshot-${count}` }),
    async *watchEvents() { throw expired(); },
  })));
  const snapshots: Resource[][] = [];
  for await (const value of synchronizeResources(client, { kind: first.kind }, { signal: controls.signal, initialRetryMs: 1 })) {
    if (value.kind === SyncKind.Snapshot) {
      snapshots.push([...value.resources]);
      if (snapshots.length === 2) controls.abort();
    }
  }
  expect(snapshots.map((s) => s[0].revision)).toEqual([1n, 9n]);
});

it("does not checkpoint a failed indexed read", async () => {
  const first = resource();
  const update = event(first, 2n);
  let reads = 0;
  const cursors: string[] = [];
  const controls = new AbortController();
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: () => ({ resources: [first], cursor: "snapshot" }),
    getResource: () => {
      if (++reads === 1) throw new ConnectError("Disconnected.", Code.Unavailable);
      return { resource: { ...first, revision: 2n } };
    },
    async *watchEvents(request) { cursors.push(request.cursor); yield update; },
  })));
  for await (const value of synchronizeResources(client, { kind: first.kind }, { signal: controls.signal, initialRetryMs: 1 })) {
    if (value.kind === SyncKind.Upsert) controls.abort();
  }
  expect(cursors).toEqual(["snapshot", "snapshot"]);
  expect(reads).toBe(2);
});

it.each([Code.Unauthenticated, Code.PermissionDenied, Code.ResourceExhausted, Code.Unimplemented, Code.DataLoss])("stops on %s without losing retained state or looping", async (code) => {
  const first = resource();
  const watches = vi.fn(async function* () { throw new ConnectError("private diagnostic", code); });
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: () => ({ resources: [first], cursor: "snapshot" }), watchEvents: watches,
  })));
  const updates: SyncUpdate[] = [];
  for await (const value of synchronizeResources(client, { kind: first.kind }, { signal: new AbortController().signal })) updates.push(value);
  expect(watches).toHaveBeenCalledTimes(1);
  expect(updates.at(-1)).toMatchObject({ kind: SyncKind.Connection, state: ConnectionState.Failed });
  expect(updates.filter((v) => v.kind === SyncKind.Snapshot)).toHaveLength(1);
  expect(JSON.stringify(updates.filter((v) => v.kind === SyncKind.Connection))).not.toContain("private diagnostic");
});

it("cancels an indexed read and never publishes a late resource", async () => {
  const first = resource();
  const controls = new AbortController();
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: () => ({ resources: [first], cursor: "snapshot" }),
    getResource: () => { controls.abort(); return { resource: { ...first, revision: 2n } }; },
    async *watchEvents() { yield event(first, 2n); },
  })));
  const updates: SyncUpdate[] = [];
  for await (const value of synchronizeResources(client, { kind: first.kind }, { signal: controls.signal })) updates.push(value);
  expect(updates.some((v) => v.kind === SyncKind.Upsert || v.kind === SyncKind.Remove)).toBe(false);
});

it("removes a resource already deleted after its update and excludes a moved project", async () => {
  const first = { ...resource(), projectId: newRequestId() };
  const other = { ...resource(), projectId: first.projectId };
  const controls = new AbortController();
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: () => ({ resources: [first, other], cursor: "snapshot" }),
    getResource: (request) => {
      if (request.id === first.id) throw new ConnectError("Deleted.", Code.NotFound);
      return { resource: { ...other, revision: 2n, projectId: newRequestId() } };
    },
    async *watchEvents() { yield event(first, 2n); yield event(other, 2n, EventAction.UPDATED, "second"); },
  })));
  const removed: string[] = [];
  for await (const value of synchronizeResources(client, { kind: first.kind, projectId: first.projectId }, { signal: controls.signal })) {
    if (value.kind === SyncKind.Remove) { removed.push(value.id); if (removed.length === 2) controls.abort(); }
  }
  expect(removed).toEqual([first.id, other.id]);
});

it.each(["foreign", "old", "oversized"])("refuses %s indexed content before publishing it", async (scenario) => {
  const first = resource();
  const next = { ...first, revision: scenario === "old" ? 1n : 2n,
    id: scenario === "foreign" ? newRequestId() : first.id,
    documentJson: scenario === "oversized" ? new Uint8Array(1024) : first.documentJson };
  const client = createClient(ResourceService, createRouterTransport((router) => router.service(ResourceService, {
    getSnapshot: () => ({ resources: [first], cursor: "snapshot" }),
    getResource: () => ({ resource: next }),
    async *watchEvents() { yield event(first, 2n); },
  })));
  const updates: SyncUpdate[] = [];
  for await (const value of synchronizeResources(client, { kind: first.kind }, { signal: new AbortController().signal, maxDocumentBytes: 100 })) updates.push(value);
  expect(updates.some((v) => v.kind === SyncKind.Upsert)).toBe(false);
  expect(updates.at(-1)).toMatchObject({ kind: SyncKind.Connection, state: ConnectionState.Failed });
});
