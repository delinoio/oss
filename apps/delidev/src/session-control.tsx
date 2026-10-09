// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { fromBinary, toBinary } from "@bufbuild/protobuf";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, SessionAction, SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { canRetryExecutionStartup } from "./execution-startup";
import { useRetainedMutation } from "./mutation";

export function validSessionActionResource(resource: Resource | undefined, id: string): resource is Resource {
  if (!resource || resource.kind !== EntityKind.SESSION || resource.id !== id || resource.schemaVersion !== 1 || typeof resource.revision !== "bigint" || resource.revision <= 0n || resource.revision > 0xffffffffffffffffn || resource.documentJson.byteLength > 1 << 20) return false;
  try {
    const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson));
    return value !== null && typeof value === "object" && !Array.isArray(value) && typeof (value as Record<string, unknown>).archive === "string" && typeof (value as Record<string, unknown>).outcome === "string";
  } catch { return false; }
}
// This cache contains accepted presentation snapshots only. Original pending
// and uncertain request bytes remain in the independent mutation registry.
export class SessionControlAcknowledgments {
  private resources: ReadonlyMap<string, Resource> = new Map();
  private sizes = new Map<string, number>();
  private pins = new Map<string, number>();
  private listeners = new Set<() => void>();
  constructor(private maximumCount = 1000, private maximumBytes = 8 << 20) {}
  snapshot = () => this.resources;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  pin = (id: string) => {
    this.pins.set(id, (this.pins.get(id) ?? 0) + 1);
    return () => { const remaining = (this.pins.get(id) ?? 1) - 1; if (remaining) this.pins.set(id, remaining); else this.pins.delete(id); };
  };
  accept = (resource: Resource) => {
    if ((this.resources.get(resource.id)?.revision ?? 0n) >= resource.revision) return true;
    const wire = toBinary(ResourceSchema, resource);
    const size = wire.byteLength;
    if (size > this.maximumBytes) return false;
    const next = new Map(this.resources), sizes = new Map(this.sizes);
    next.delete(resource.id); sizes.delete(resource.id);
    let bytes = [...sizes.values()].reduce((sum, value) => sum + value, 0);
    for (const id of next.keys()) {
      if (next.size < this.maximumCount && bytes + size <= this.maximumBytes) break;
      if (this.pins.has(id)) continue;
      bytes -= sizes.get(id) ?? 0; sizes.delete(id); next.delete(id);
    }
    if (next.size >= this.maximumCount || bytes + size > this.maximumBytes) return false;
    next.set(resource.id, fromBinary(ResourceSchema, wire)); sizes.set(resource.id, size);
    this.resources = next; this.sizes = sizes;
    for (const listener of this.listeners) listener();
    return true;
  };
}
const Context = createContext<SessionControlAcknowledgments | undefined>(undefined);
// The connection owns late acknowledgments; navigation never replays controls.
export function SessionControlProvider({ children }: { children: ReactNode }) {
  const [acknowledgments] = useState(() => new SessionControlAcknowledgments());
  return <Context.Provider value={acknowledgments}>{children}</Context.Provider>;
}
const emptyResources: ReadonlyMap<string, Resource> = new Map();
const emptySnapshot = () => emptyResources;
const noSubscription = () => () => {};
export function sessionControlEligibility(resource: Resource | undefined, budgetBlocked: boolean) {
  const data = document(resource);
  const startupRetry = canRetryExecutionStartup(data);
  return { startupRetry, resume: Boolean(resource) && !budgetBlocked && !Object.hasOwn(data, "startup_rejection") && !(Boolean(object(data.startup).failure) && !startupRetry) && text(data.archive) === "active", archiveAction: text(data.archive) === "archived" ? SessionAction.RESTORE : SessionAction.ARCHIVE };
}
export function useSessionControl(id: string, observed?: Resource, active = true) {
  const shared = useContext(Context);
  const [local, setLocal] = useState<Resource>();
  const resources = useSyncExternalStore(shared?.subscribe ?? noSubscription, shared?.snapshot ?? emptySnapshot);
  useEffect(() => active ? shared?.pin(id) : undefined, [active, id, shared]);
  const acknowledged = resources.get(id) ?? (local?.id === id ? local : undefined);
  const resource = acknowledged && (!observed || acknowledged.revision > observed.revision) ? acknowledged : observed;
  const client = useQueryClient();
  const accept = shared?.accept;
  const control = useRetainedMutation(`control:${id}`, SessionQuery.controlSession, result => {
    if (!accept && result.change?.session) setLocal(result.change.session);
  });
  const action = (value: SessionAction) => {
    if (!resource || control.busy || control.uncertain) return;
    void control.send({ mutation: { id, expectedRevision: resource.revision, requestId: newRequestId() }, action: value }, (result, request) => {
      const accepted = result.change?.session;
      if (!request.mutation || !validSessionActionResource(accepted, request.mutation.id) || accepted.revision < request.mutation.expectedRevision) return false;
      accept?.(accepted);
      void client.invalidateQueries({ refetchType: "active" });
      return true;
    });
  };
  return { resource, control, action };
}
