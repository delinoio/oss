import { Code, ConnectError, type Client } from "@connectrpc/connect";
import {
  EntityKind, EventAction, ErrorDetailSchema, ResourceService,
  type Resource, type WatchEventsResponse,
} from "./gen/delidev/v1/delidev_pb.js";
import { clientFailure, type ClientFailure } from "./errors.js";
import { supportsResourceSchema } from "./configuration-identity.js";
import { requireEntityId } from "./validation.js";

export enum SyncKind {
  Snapshot = "snapshot",
  Upsert = "upsert",
  Remove = "remove",
  Connection = "connection",
}
export enum ConnectionState {
  Connecting = "connecting",
  Live = "live",
  Reconnecting = "reconnecting",
  Failed = "failed",
}
export type SyncUpdate =
  | { kind: SyncKind.Snapshot; resources: readonly Resource[] }
  | { kind: SyncKind.Upsert; resource: Resource; eventId: string }
  | { kind: SyncKind.Remove; id: string; eventId: string }
  | { kind: SyncKind.Connection; state: ConnectionState; failure?: ClientFailure };

export interface ResourceScope {
  kind: EntityKind;
  sessionId?: string;
  projectId?: string;
}
export interface SynchronizationOptions {
  signal: AbortSignal;
  // Additional indexed kinds share the primary snapshot's event cursor. Their
  // initial lists remain explicit paginated reads, never snapshot contents.
  watchKinds?: readonly EntityKind[];
  // This is a bounded live scope, not an unbounded transcript archive. Larger
  // histories use explicit paginated reads and indexed per-message events.
  maxResources?: number;
  maxDocumentBytes?: number;
  initialRetryMs?: number;
  maxRetryMs?: number;
}

const knownKinds = new Set<number>(Object.values(EntityKind).filter((v) => typeof v === "number" && v !== 0));
function invalid(): never {
  throw new ConnectError("The server returned inconsistent synchronization evidence.", Code.FailedPrecondition);
}
function positiveBound(value: number, max: number): number {
  if (!Number.isInteger(value) || value < 1 || value > max) throw new ConnectError("Invalid synchronization bound.", Code.InvalidArgument);
  return value;
}
function cursor(value: string): void {
  if (value.length === 0 || value.length > 2048) invalid();
}
function validateResource(value: Resource, kind: EntityKind): void {
  requireEntityId(value.id);
  if (value.kind !== kind || value.revision <= 0n || !supportsResourceSchema(value) || value.documentJson.byteLength > 1 << 20) invalid();
  if (value.sessionId) requireEntityId(value.sessionId);
  if (value.projectId) requireEntityId(value.projectId);
}
function validateEvent(value: WatchEventsResponse, sessionId: string): void {
  cursor(value.cursor);
  requireEntityId(value.id);
  requireEntityId(value.entityId);
  if (!knownKinds.has(value.kind) || value.revision <= 0n ||
      ![EventAction.CREATED, EventAction.UPDATED, EventAction.DELETED].includes(value.action) ||
      (sessionId && value.sessionId !== sessionId)) invalid();
}
function matches(value: Resource, scope: ResourceScope): boolean {
  return (!scope.sessionId || value.sessionId === scope.sessionId) && (!scope.projectId || value.projectId === scope.projectId);
}
function wait(milliseconds: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener("abort", finish); resolve(); };
    const timer = setTimeout(finish, milliseconds);
    signal.addEventListener("abort", finish, { once: true });
  });
}

/**
 * Read-only snapshot/replay. Each yielded update must be applied before the
 * consumer requests another: cursors advance afterward. No mutation, native
 * notification, polling of complete history, or browser persistence occurs.
 * Dispose the caller's AbortController when switching identity or server.
 */
export async function* synchronizeResources(
  client: Pick<Client<typeof ResourceService>, "getSnapshot" | "getResource" | "watchEvents">,
  requestedScope: ResourceScope,
  options: SynchronizationOptions,
): AsyncGenerator<SyncUpdate> {
  const scope = { ...requestedScope };
  const watchKinds = new Set([scope.kind, ...(options.watchKinds ?? [])]);
  if ([...watchKinds].some((kind) => !knownKinds.has(kind))) throw new ConnectError("Select supported indexed resource kinds.", Code.InvalidArgument);
  if (!knownKinds.has(scope.kind)) throw new ConnectError("Select a resource kind.", Code.InvalidArgument);
  if (scope.sessionId) requireEntityId(scope.sessionId);
  if (scope.projectId) requireEntityId(scope.projectId);
  const maxResources = positiveBound(options.maxResources ?? 1000, 10000);
  const maxBytes = positiveBound(options.maxDocumentBytes ?? 4 << 20, 16 << 20);
  const initialRetry = positiveBound(options.initialRetryMs ?? 250, 30000);
  const maxRetry = positiveBound(options.maxRetryMs ?? 30000, 30000);
  if (initialRetry > maxRetry) throw new ConnectError("Invalid retry interval.", Code.InvalidArgument);
  const signal = options.signal;
  const requestOptions = { signal, timeoutMs: 15000 };
  const revisions = new Map<string, bigint>();
  const sizes = new Map<string, number>();
  const seen = new Set<string>();
  let bytes = 0;
  let after = "";
  let attempt = 0;
  let verified = false;
  const remember = (event: WatchEventsResponse) => {
    after = event.cursor;
    seen.add(event.id);
    if (seen.size > 512) seen.delete(seen.values().next().value!);
  };
  const account = (resource: Resource) => {
    const next = bytes - (sizes.get(resource.id) ?? 0) + resource.documentJson.byteLength;
    if (next > maxBytes || (!sizes.has(resource.id) && sizes.size >= maxResources)) {
      throw new ConnectError("The live scope exceeds its memory bound; select a narrower scope.", Code.ResourceExhausted);
    }
    bytes = next;
    sizes.set(resource.id, resource.documentJson.byteLength);
    revisions.set(resource.id, resource.revision);
  };
  const remove = (id: string) => {
    bytes -= sizes.get(id) ?? 0;
    sizes.delete(id);
    revisions.delete(id);
  };
  try {
    while (!signal.aborted) {
      try {
        if (!after) {
          yield { kind: SyncKind.Connection, state: ConnectionState.Connecting };
          if (signal.aborted) return;
          const snapshot = await client.getSnapshot({ filter: { ...scope, pageSize: 200 } }, requestOptions);
          if (signal.aborted) return;
          cursor(snapshot.cursor);
          // Verify the entire snapshot before emitting any replacement state.
          revisions.clear(); sizes.clear(); seen.clear(); bytes = 0;
          for (const resource of snapshot.resources) {
            validateResource(resource, scope.kind);
            if (!matches(resource, scope) || revisions.has(resource.id)) invalid();
            account(resource);
          }
          yield { kind: SyncKind.Snapshot, resources: snapshot.resources };
          if (signal.aborted) return;
          after = snapshot.cursor;
          verified = true;
        }
        yield { kind: SyncKind.Connection, state: verified ? ConnectionState.Live : ConnectionState.Connecting };
        if (signal.aborted) return;
        for await (const event of client.watchEvents({ cursor: after, sessionId: scope.sessionId ?? "" }, { signal })) {
          if (signal.aborted) return;
          validateEvent(event, scope.sessionId ?? "");
          if (!verified) {
            yield { kind: SyncKind.Connection, state: ConnectionState.Live };
            if (signal.aborted) return;
            verified = true;
          }
          // An old duplicate must not rewind the opaque committed cursor.
          if (seen.has(event.id)) continue;
          if (!watchKinds.has(event.kind)) { remember(event); continue; }
          if ((revisions.get(event.entityId) ?? 0n) >= event.revision && event.action !== EventAction.DELETED) {
            remember(event); continue;
          }
          if (event.action === EventAction.DELETED) {
            if ((revisions.get(event.entityId) ?? 0n) > event.revision) invalid();
            remove(event.entityId);
            yield { kind: SyncKind.Remove, id: event.entityId, eventId: event.id };
          } else {
            let resource: Resource | undefined;
            try {
              const response = await client.getResource({ kind: event.kind, id: event.entityId }, requestOptions);
              resource = response.resource;
              if (!resource) invalid();
            } catch (reason) {
              if (ConnectError.from(reason).code !== Code.NotFound) throw reason;
              // The resource may have been deleted after the retained event.
              // Never reconstruct its old document from events or local input.
            }
            if (signal.aborted) return;
            if (resource) {
              validateResource(resource, event.kind);
              if (resource.id !== event.entityId || resource.revision < event.revision) invalid();
            }
            if (resource && matches(resource, scope)) {
              account(resource);
              yield { kind: SyncKind.Upsert, resource, eventId: event.id };
            } else {
              remove(event.entityId);
              yield { kind: SyncKind.Remove, id: event.entityId, eventId: event.id };
            }
          }
          if (signal.aborted) return;
          remember(event);
          attempt = 0;
        }
        if (signal.aborted) return;
        throw new ConnectError("The event connection ended.", Code.Unavailable);
      } catch (reason) {
        if (signal.aborted) return;
        const error = ConnectError.from(reason);
        verified = false;
        const expired = error.code === Code.OutOfRange && error.findDetails(ErrorDetailSchema).some((d) => d.code === "cursor_expired");
        if (expired) after = "";
        // A fetch body interruption can surface as Unknown after response
        // headers. Reconnecting this read-only stream never retries a mutation.
        if (!expired && ![Code.Unavailable, Code.DeadlineExceeded, Code.Unknown].includes(error.code)) {
          yield { kind: SyncKind.Connection, state: ConnectionState.Failed, failure: clientFailure(error) };
          return;
        }
        yield { kind: SyncKind.Connection, state: ConnectionState.Reconnecting, failure: clientFailure(error) };
        const delay = Math.min(maxRetry, initialRetry * 2 ** Math.min(attempt++, 16));
        await wait(Math.max(1, Math.floor(delay * (0.5 + Math.random() * 0.5))), signal);
      }
    }
  } finally {
    revisions.clear(); sizes.clear(); seen.clear();
  }
}
