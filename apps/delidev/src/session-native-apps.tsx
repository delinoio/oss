// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { useMutation } from "@connectrpc/connect-query";
import { useEffect, useRef, useState } from "react";
import { EntityKind, ResourceQuery, SessionNativeAppsQuery, SessionNativeAppScopeSchema, SystemCapability, SystemQuery, newRequestId, sameNativeAppScope, sessionNativeAppViews, validNativeAppScope, validNativeAppSelection, type ReadSessionAppsResponse, type Resource, type SessionNativeAppScope, type UpdateSessionAppsRequest, type UpdateSessionAppsResponse } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useSessionQuery } from "./session-activity";
import { useRetainedMutation, useRetainedMutationIntents } from "./mutation";
import { copy, useLocale } from "./localization";
import { Modal, Problem } from "./ui";

export function sessionAppsScope(session: Resource): SessionNativeAppScope | undefined {
  const d = document(session), initial = object(d.initial_execution), current = object(d.current_execution);
  if (session.kind !== EntityKind.SESSION || session.schemaVersion !== 1 || session.revision <= 0n || d.fork != null || d.archive !== "active" || d.recovery !== "none") return;
  const scope = create(SessionNativeAppScopeSchema, { sessionId: session.id, machineId: text(d.machine_id), originalAccountId: d.current_execution != null ? text(current.account_id) : text(initial.initial_account_id), originalConnectionId: d.current_execution != null ? text(current.connection_id) : text(initial.connection_id), configurationDigest: text(initial.configuration_digest) });
  return validNativeAppScope(scope) ? scope : undefined;
}
export function validSessionAppsReceipt(response: UpdateSessionAppsResponse, request: UpdateSessionAppsRequest, scope: SessionNativeAppScope): boolean {
  const r = response.receipt, selection = response.selection;
  return !!r && !!selection && !!request.mutation && r.id === request.sessionId && r.kind === EntityKind.SESSION && r.schemaVersion === 1 && r.revision > request.mutation.expectedRevision && validNativeAppSelection(selection, scope) && selection.inventoryId === request.inventoryId && JSON.stringify(selection.appIds) === JSON.stringify(request.selectedAppIds) && !!sessionAppsScope(r) && sameNativeAppScope(sessionAppsScope(r)!, scope);
}
export function SessionNativeApps({ session, changed, active }: { session: Resource; changed: (resource: Resource) => void; active: boolean }) {
  useLocale();
  const [open, setOpen] = useState(false), scope = sessionAppsScope(session);
  return <><button type="button" disabled={!active || !scope} onClick={() => setOpen(true)}>{copy("native-apps.title")}</button><AppsController key={JSON.stringify(scope ?? null)} session={session} scope={scope} changed={changed} visible={open && active} close={() => setOpen(false)} /></>;
}
function AppsController({ session, scope, changed, visible, close }: { session: Resource; scope?: SessionNativeAppScope; changed: (resource: Resource) => void; visible: boolean; close: () => void }) {
  const [inventory, setInventory] = useState<ReadSessionAppsResponse>(), [selected, setSelected] = useState<string[]>([]), [revision, setRevision] = useState<bigint>(), [observing, setObserving] = useState(false), [failure, setFailure] = useState(false), [saved, setSaved] = useState(false);
  const alive = useRef(true); useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const current = useRef(session); current.current = session;
  const capabilities = useSessionQuery(SystemQuery.getStatus, {}, { enabled: visible, retry: false });
  const machine = useSessionQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: scope?.machineId ?? "" }, { enabled: visible && !!scope, retry: false });
  const getSession = useMutation(ResourceQuery.getResource, { retry: false });
  const machineRow = machine.data?.resource;
  const supported = !!scope && !capabilities.error && capabilities.data?.capabilities.includes(SystemCapability.SESSION_NATIVE_APPS_V1) === true && !machine.error && machineRow?.id === scope.machineId && machineRow.kind === EntityKind.MACHINE && machineRow.schemaVersion === 1 && machineRow.revision > 0n && Array.isArray(document(machineRow).worker_capabilities) && (document(machineRow).worker_capabilities as unknown[]).includes("session-native-apps-v1");
  const prefix = `session-apps:${session.id}:`, key = `${prefix}${JSON.stringify(scope ?? null)}`;
  const pending = useRetainedMutationIntents(prefix);
  const read = useRetainedMutation(`${key}:read`, SessionNativeAppsQuery.readSessionApps, async (value, request) => {
    if (!scope || !alive.current || !sessionAppsScope(current.current) || !sameNativeAppScope(sessionAppsScope(current.current)!, scope)) return;
    const rows = sessionNativeAppViews(value, scope, request.requestId);
    if (!rows) { setFailure(true); return; }
    setObserving(true);
    try {
      const result = await getSession.mutateAsync({ kind: EntityKind.SESSION, id: scope.sessionId });
      const fresh = result.resource, freshScope = fresh && sessionAppsScope(fresh);
      if (!alive.current) return;
      if (!fresh || !freshScope || !sameNativeAppScope(freshScope, scope) || fresh.revision < current.current.revision) { setFailure(true); return; }
      changed(fresh); setRevision(fresh.revision); setInventory(value); setSelected(value.selection?.appIds ?? []); setFailure(false); setSaved(false);
    } catch { if (alive.current) setFailure(true); }
    finally { if (alive.current) setObserving(false); }
  });
  const update = useRetainedMutation(`${key}:update`, SessionNativeAppsQuery.updateSessionApps, (value) => {
    if (!scope || !alive.current || !sessionAppsScope(current.current) || !sameNativeAppScope(sessionAppsScope(current.current)!, scope)) return;
    if (value.receipt && value.receipt.revision >= current.current.revision) changed(value.receipt);
    setInventory(undefined); setRevision(undefined); setSaved(true);
  });
  const busy = observing || pending.some(v => v.busy || v.uncertain), rows = inventory && scope ? sessionNativeAppViews(inventory, scope, inventory.requestId) : undefined;
  const fresh = !!scope && !!inventory && revision === session.revision;
  function inspect(forceRefresh: boolean) {
    if (!supported || busy || !scope) return;
    const original = scope;
    void read.send({ requestId: newRequestId(), sessionId: session.id, forceRefresh }, (value, request) => !!sessionNativeAppViews(value, original, request.requestId));
  }
  function save() {
    if (!scope || !supported || busy || !fresh || !inventory || !rows || selected.some(id => !rows.some(row => row.id === id && row.installed && row.enabled && row.accessible))) return;
    const original = scope;
    void update.send({ mutation: { id: session.id, requestId: newRequestId(), expectedRevision: session.revision }, sessionId: session.id, originalAccountId: scope.originalAccountId, originalConnectionId: scope.originalConnectionId, configurationDigest: scope.configurationDigest, inventoryId: inventory.inventoryId, selectedAppIds: selected }, (value, request) => validSessionAppsReceipt(value, request, original));
  }
  return visible ? <Modal title={copy("native-apps.title")} close={close} focusClose>
    <p>{copy("native-apps.sessionOnly")}</p>
    {!supported ? <p>{copy(scope && (capabilities.isPending || machine.isPending) ? "native-apps.checking" : "native-apps.unsupported")}</p> : <div className="actions"><button type="button" disabled={busy} onClick={() => inspect(false)}>{copy("native-apps.read")}</button><button type="button" disabled={busy} onClick={() => inspect(true)}>{copy("native-apps.refresh")}</button></div>}
    <Problem error={capabilities.error ?? machine.error ?? read.error ?? update.error} />
    {failure ? <p role="alert">{copy("native-apps.invalid")}</p> : null}
    {pending.some(v => v.uncertain) ? <p role="alert">{copy("native-apps.uncertain")}</p> : null}
    {read.uncertain && supported ? <button type="button" disabled={read.busy} onClick={read.retry}>{copy("native-apps.retryRead")}</button> : null}
    {update.uncertain && supported ? <button type="button" disabled={update.busy} onClick={update.retry}>{copy("native-apps.retryUpdate")}</button> : null}
    {rows ? <fieldset disabled={busy || !supported || !fresh}><legend>{copy("native-apps.selection")}</legend>{rows.length === 0 ? <p>{copy("native-apps.empty")}</p> : rows.map(row => <div key={row.id}><label><input type="checkbox" checked={selected.includes(row.id)} disabled={busy || !supported || !fresh || !row.installed || !row.enabled || !row.accessible} onChange={event => setSelected(ids => event.target.checked ? [...ids, row.id] : ids.filter(id => id !== row.id))} />{row.name}</label><dl>{([["discovered",row.discovered],["installed",row.installed],["enabled",row.enabled],["callable",row.callable]] as const).map(([name,value]) => <div key={name}><dt>{copy(`native-apps.${name}`)}</dt><dd>{copy(value ? "native-apps.yes" : "native-apps.no")}</dd></div>)}</dl></div>)}<button type="button" disabled={busy || !supported || !fresh} onClick={() => setSelected([])}>{copy("native-apps.revoke")}</button><button type="button" disabled={busy || !supported || !fresh} onClick={save}>{copy("native-apps.save")}</button></fieldset> : null}
    {rows && !fresh ? <p role="alert">{copy("native-apps.stale")}</p> : null}{saved ? <p role="status">{copy("native-apps.saved")}</p> : null}
  </Modal> : null;
}
