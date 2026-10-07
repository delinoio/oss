// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useCallback, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, SessionAction, SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { canRetryExecutionStartup } from "./execution-startup";
import { useRetainedMutation } from "./mutation";

const Context = createContext<{ resources: ReadonlyMap<string, Resource>; accept: (resource: Resource) => void } | undefined>(undefined);
// Acknowledgments belong to the connection, including responses received after
// an action menu or detail view closes. They never authorize automatic replay.
export function SessionControlProvider({ children }: { children: ReactNode }) {
  const [resources, setResources] = useState<ReadonlyMap<string, Resource>>(() => new Map());
  const accept = useCallback((resource: Resource) => setResources(previous => {
    if ((previous.get(resource.id)?.revision ?? 0n) >= resource.revision) return previous;
    const next = new Map(previous); next.set(resource.id, resource); return next;
  }), []);
  return <Context.Provider value={{ resources, accept }}>{children}</Context.Provider>;
}
export function sessionControlEligibility(resource: Resource | undefined, budgetBlocked: boolean) {
  const data = document(resource);
  const startupRetry = canRetryExecutionStartup(data);
  return { startupRetry, resume: Boolean(resource) && !budgetBlocked && !Object.hasOwn(data, "startup_rejection") && !(Boolean(object(data.startup).failure) && !startupRetry) && text(data.archive) === "active", archiveAction: text(data.archive) === "archived" ? SessionAction.RESTORE : SessionAction.ARCHIVE };
}
export function useSessionControl(id: string, observed?: Resource) {
  const shared = useContext(Context);
  const [local, setLocal] = useState<Resource>();
  const acknowledged = shared?.resources.get(id) ?? local;
  const resource = acknowledged && (!observed || acknowledged.revision > observed.revision) ? acknowledged : observed;
  const client = useQueryClient();
  const accept = shared?.accept;
  const control = useRetainedMutation(`control:${id}`, SessionQuery.controlSession, result => {
    if (result.change?.session) setLocal(result.change.session);
  });
  const action = (value: SessionAction) => {
    if (!resource || control.busy || control.uncertain) return;
    void control.send({ mutation: { id, expectedRevision: resource.revision, requestId: newRequestId() }, action: value }, (result, request) => {
      const accepted = result.change?.session;
      if (!accepted || accepted.kind !== EntityKind.SESSION || accepted.schemaVersion !== 1 || accepted.id !== request.mutation?.id || accepted.revision < request.mutation.expectedRevision) return false;
      accept?.(accepted);
      void client.invalidateQueries({ refetchType: "active" });
      return true;
    });
  };
  return { resource, control, action };
}
