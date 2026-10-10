// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { CodexAppsQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation, useRetainedMutationIntents } from "./mutation";
import { useSessionActive, useSessionQuery as useQuery } from "./session-activity";
import { retainedSessionHarness } from "./session-harness";
import { timestampInstant } from "./timestamp-format";
import { Timestamp } from "./timestamp-display";
import { Modal, Problem } from "./ui";

interface Configuration { version: 1; session_id: string; account_id: string; generation: string; app_ids: string[] }
interface App { id: string; name: string; discovered: boolean; accessible: boolean; installed: boolean; enabled: boolean; callable: boolean; selected: boolean }
interface Inventory { operation_id: string; claim_id: string; native_catalog_refresh_verified: true; version: 1; session_id: string; account_id: string; configuration_generation: string; execution_id: string; execution_job_id: string; machine_id: string; instance_id: string; native_thread_id: string; observed_at: string; apps: App[] }
interface Operation { claim_id?: string; version: 1; id: string; revision: string; request_id: string; actor_id: string; action: "inspect" | "revoke"; state: "queued" | "claimed" | "succeeded" | "failed" | "uncertain"; original: Configuration; next?: Configuration; execution_id: string; execution_job_id: string; machine_id: string; instance_id: string; native_thread_id: string; inventory?: Inventory; problem?: Record<string, unknown> }
const id = (v: unknown): v is string => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const bounded = (v: unknown, max: number, required = true): v is string => typeof v === "string" && (!required || v.trim().length > 0) && new TextEncoder().encode(v).byteLength <= max;
const closed = (v: Record<string, unknown>, fields: readonly string[]) => Object.keys(v).every(k => fields.includes(k));
function appIDs(v: unknown): v is string[] { return Array.isArray(v) && v.length <= 100 && v.every(x => bounded(x, 1024)) && new Set(v).size === v.length; }
function configuration(v: unknown): Configuration | undefined {
  const c = object(v);
  return c.version === 1 && closed(c, ["version", "session_id", "account_id", "generation", "app_ids"]) && id(c.session_id) && id(c.account_id) && id(c.generation) && appIDs(c.app_ids) ? c as unknown as Configuration : undefined;
}
function inventory(v: unknown): Inventory | undefined {
  const i = object(v), fields = ["operation_id", "claim_id", "native_catalog_refresh_verified", "version", "session_id", "account_id", "configuration_generation", "execution_id", "execution_job_id", "machine_id", "instance_id", "native_thread_id", "observed_at", "apps"];
  if (i.version !== 1 || i.native_catalog_refresh_verified !== true || !id(i.operation_id) || !id(i.claim_id) || !closed(i, fields) || ![i.session_id, i.account_id, i.configuration_generation, i.execution_id, i.execution_job_id, i.machine_id, i.instance_id, i.native_thread_id].every(id) || typeof i.observed_at !== "string" || timestampInstant(i.observed_at) === undefined || !Array.isArray(i.apps) || i.apps.length > 1000) return;
  const seen = new Set<string>();
  for (const value of i.apps) {
    const a = object(value);
    if (!closed(a, ["id", "name", "discovered", "accessible", "installed", "enabled", "callable", "selected"]) || !bounded(a.id, 1024) || !bounded(a.name, 4096, false) || seen.has(a.id) || ![a.discovered, a.accessible, a.installed, a.enabled, a.callable, a.selected].every(x => typeof x === "boolean") || a.callable && (!a.installed || !a.enabled)) return;
    seen.add(a.id);
  }
  return i as unknown as Inventory;
}
function operation(v: unknown): Operation | undefined {
  const o = object(v), original = configuration(o.original), next = o.next === undefined ? undefined : configuration(o.next), snapshot = o.inventory === undefined ? undefined : inventory(o.inventory);
  const states = ["queued", "claimed", "succeeded", "failed", "uncertain"];
  if (o.version !== 1 || !closed(o, ["claim_id", "version", "id", "revision", "request_id", "actor_id", "action", "state", "original", "next", "execution_id", "execution_job_id", "machine_id", "instance_id", "native_thread_id", "inventory", "problem"]) || ![o.id, o.request_id, o.actor_id, o.execution_id, o.execution_job_id, o.machine_id, o.instance_id, o.native_thread_id].every(id) || typeof o.revision !== "string" || !/^[1-9][0-9]{0,19}$/.test(o.revision) || BigInt(o.revision) > 18446744073709551615n || !states.includes(o.state as string) || !original || o.action !== "inspect" && o.action !== "revoke" || o.next !== undefined && !next || o.inventory !== undefined && !snapshot) return;
  if (o.state === "queued" ? o.claim_id !== undefined : !id(o.claim_id)) return;
  if (o.action === "inspect" && next || o.action === "revoke" && (!next || next.session_id !== original.session_id || next.account_id !== original.account_id || next.generation === original.generation || next.app_ids.some(value => !original.app_ids.includes(value)))) return;
  const expected = o.state === "succeeded" && next ? next : original;
  if (snapshot && (snapshot.operation_id !== o.id || snapshot.claim_id !== o.claim_id || snapshot.session_id !== expected.session_id || snapshot.account_id !== expected.account_id || snapshot.configuration_generation !== expected.generation || snapshot.execution_id !== o.execution_id || snapshot.execution_job_id !== o.execution_job_id || snapshot.machine_id !== o.machine_id || snapshot.instance_id !== o.instance_id || snapshot.native_thread_id !== o.native_thread_id)) return;
  if (o.state === "succeeded" && (!snapshot || o.problem !== undefined) || ["failed", "uncertain"].includes(o.state as string) && (!o.problem || typeof object(o.problem).code !== "string") || ["queued", "claimed"].includes(o.state as string) && (snapshot || o.problem !== undefined)) return;
  return o as unknown as Operation;
}
// Dedicated metadata envelopes are not generic Session/Job resources and grant
// no EntityKind authority. Decode only their closed original-scope documents.
function payload(resource?: Resource): unknown {
  if (!resource || !id(resource.id) || resource.revision <= 0n || resource.schemaVersion !== 1 || resource.documentJson.byteLength > 1 << 20) return;
  try { return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson)); } catch { return; }
}
export function codexAppsView(reply: { configuration?: Resource; inventory?: Resource; operation?: Resource } | undefined, sessionId: string, accountId: string) {
  const config = reply?.configuration ? configuration(payload(reply.configuration)) : undefined;
  const observed = reply?.inventory ? inventory(payload(reply.inventory)) : undefined;
  const action = reply?.operation ? operation(payload(reply.operation)) : undefined;
  const invalid = Boolean(reply?.configuration && (!config || reply.configuration.id !== config.generation || reply.configuration.sessionId !== sessionId) || reply?.inventory && (!observed || reply.inventory.sessionId !== sessionId) || reply?.operation && (!action || reply.operation.id !== action.id || reply.operation.sessionId !== sessionId) || config && (config.session_id !== sessionId || config.account_id !== accountId) || observed && (observed.session_id !== sessionId || observed.account_id !== accountId) || action && (action.original.session_id !== sessionId || action.original.account_id !== accountId));
  return { config: invalid ? undefined : config, observed: invalid ? undefined : observed, action: invalid ? undefined : action, invalid };
}
export function codexAppsIdle(session: Resource): boolean {
  const d = document(session), prep = object(d.preparation);
  return d.archive === "active" && d.recovery === "none" && d.pending_inputs === 0 && !d.active_execution_id && !d.pending_steer_id && !d.start_preparation && !d.revert_job_id && !d.compaction_job_id && (!d.preparation || prep.state === "ready");
}
const sameIDs = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((value, n) => value === b[n]);

export function CodexAppsPanel({ session, account, onClose }: { session: Resource; account: Resource; onClose: () => void }) {
  useLocale();
  const active = useSessionActive();
  const data = document(session), currentAccount = text(object(data.current_execution).account_id) || text(object(data.initial_execution).initial_account_id);
  const scope = currentAccount === account.id && retainedSessionHarness(session) === "codex" && !object(data.fork).sidechat_parent_snapshot && session.revision > 0n && account.revision > 0n;
  const query = useQuery(CodexAppsQuery.getCodexApps, { sessionId: session.id }, { enabled: scope, retry: false, refetchInterval: 5000 });
  const view = codexAppsView(query.data, session.id, account.id);
  const generation = view.config?.generation ?? "";
  const fresh = active && scope && Boolean(query.data) && !view.invalid && !query.error && !query.isFetching;
  const pending = view.action && ["queued", "claimed", "uncertain"].includes(view.action.state);
  const prefix = `codex-apps:${session.id}:`;
  const retained = useRetainedMutationIntents(prefix);
  const [draft, setDraft] = useState<{ sessionId: string; accountId: string; sessionRevision: bigint; accountRevision: bigint; generation: string; ids: string[] }>();
  const [removal, setRemoval] = useState<{ sessionId: string; accountId: string; revision: bigint; generation: string; ids: string[] }>();
  const accepted = () => { void query.refetch(); };
  const inspect = useRetainedMutation(prefix + "inspect", CodexAppsQuery.inspectCodexApps, accepted, (reply, request) => {
    const v = codexAppsView(reply, request.mutation!.id, request.accountId);
    return !v.invalid && v.action?.action === "inspect" && v.action.request_id === request.mutation?.requestId && v.action.original.generation === request.configurationGeneration;
  });
  const select = useRetainedMutation(prefix + "select", CodexAppsQuery.selectCodexApps, () => { setDraft(undefined); accepted(); }, (reply, request) => {
    const v = codexAppsView(reply, request.mutation!.id, request.accountId);
    return !v.invalid && Boolean(v.config && v.config.generation === request.mutation?.requestId && sameIDs(v.config.app_ids, request.appIds));
  });
  const revoke = useRetainedMutation(prefix + "revoke", CodexAppsQuery.revokeCodexApps, () => { setRemoval(undefined); accepted(); }, (reply, request) => {
    const v = codexAppsView(reply, request.mutation!.id, request.accountId);
    return !v.invalid && v.action?.action === "revoke" && v.action.request_id === request.mutation?.requestId && v.action.original.generation === request.configurationGeneration && Boolean(v.action.next && v.action.next.generation === request.mutation?.requestId && sameIDs(v.action.next.app_ids, v.action.original.app_ids.filter(value => !request.appIds.includes(value))));
  });
  const locked = Boolean(pending || retained.length);
  const stale = Boolean(draft && (draft.sessionId !== session.id || draft.accountId !== account.id || draft.sessionRevision !== session.revision || draft.accountRevision !== account.revision || draft.generation !== generation));
  const removalStale = Boolean(removal && (removal.sessionId !== session.id || removal.accountId !== account.id || removal.revision !== session.revision || removal.generation !== generation));
  const live = fresh && view.config && data.recovery === "none" && text(data.active_execution_id) && text(object(data.execution).execution_id) === text(data.active_execution_id) && !object(data.execution).cleanup_verified;
  const inventoryCurrent = Boolean(view.observed && view.config && view.observed.configuration_generation === generation && view.observed.execution_id === text(object(data.execution).execution_id) && view.observed.native_thread_id === text(object(data.execution).native_thread_id));
  const listed = inventoryCurrent ? view.observed!.apps : [];
  const selectedIDs = view.config?.app_ids ?? [];
  const choices = [...listed.map(app => app.id), ...selectedIDs.filter(value => !listed.some(app => app.id === value))];
  const name = (appId: string, n: number) => listed.find(app => app.id === appId)?.name || copy("codex-apps.appOrdinal", { number: n + 1 });
  return <Modal title={copy("codex-apps.title")} close={onClose}>
    <p>{copy("codex-apps.scope")}</p><p>{copy("codex-apps.authority")}</p>
    <Problem error={query.error || inspect.error || select.error || revoke.error} />
    {!scope || view.invalid ? <p role="alert">{copy("codex-apps.invalid")}</p> : null}
    {query.isLoading ? <p role="status">{copy("codex-apps.loading")}</p> : null}
    {!view.config && query.data && !view.invalid ? <p>{copy("codex-apps.unconfigured")}</p> : null}
    {view.action ? <p role="status">{copy(`codex-apps.state.${view.action.state}`)}</p> : null}
    {view.action?.state === "uncertain" ? <p role="alert">{copy("codex-apps.nativeUncertain")}</p> : null}
    {view.observed ? <p>{copy(inventoryCurrent ? "codex-apps.observed" : "codex-apps.historical")} <Timestamp value={view.observed.observed_at} /></p> : null}
    {query.error ? <p>{copy("codex-apps.stale")}</p> : null}
    <div className="actions"><button type="button" disabled={!scope || query.isFetching} onClick={() => void query.refetch()}>{copy("codex-apps.reload")}</button><button type="button" disabled={!live || locked} onClick={() => { if (view.config && live && !locked) void inspect.send({ mutation: { id: session.id, expectedRevision: session.revision, requestId: newRequestId() }, accountId: account.id, accountRevision: account.revision, configurationGeneration: generation }); }}>{copy("codex-apps.inspect")}</button><button type="button" disabled={!fresh || locked || !codexAppsIdle(session) || Boolean(draft)} onClick={() => setDraft({ sessionId: session.id, accountId: account.id, sessionRevision: session.revision, accountRevision: account.revision, generation, ids: [...selectedIDs] })}>{copy(view.config ? "codex-apps.edit" : "codex-apps.initialize")}</button></div>
    {inventoryCurrent && listed.length === 0 ? <p>{copy("codex-apps.empty")}</p> : !inventoryCurrent ? <p>{copy("codex-apps.unknown")}</p> : null}
    {listed.map((app, n) => <section key={app.id} aria-label={name(app.id, n)}><h3>{name(app.id, n)}</h3><dl>{(["discovered", "accessible", "installed", "enabled", "callable", "selected"] as const).map(state => <div key={state}><dt>{copy(`codex-apps.flag.${state}`)}</dt><dd>{copy(app[state] ? "codex-apps.yes" : "codex-apps.no")}</dd></div>)}</dl></section>)}
    {draft ? <form onSubmit={event => { event.preventDefault(); if (fresh && !locked && !stale && codexAppsIdle(session)) void select.send({ mutation: { id: draft.sessionId, expectedRevision: draft.sessionRevision, requestId: newRequestId() }, accountId: draft.accountId, accountRevision: draft.accountRevision, configurationGeneration: draft.generation, appIds: [...draft.ids] }); }}>
      <h3>{copy("codex-apps.nextSelection")}</h3><p>{copy("codex-apps.nextOnly")}</p><fieldset disabled={locked}>{choices.map((appId, n) => <label className="checkbox" key={appId}><input type="checkbox" checked={draft.ids.includes(appId)} disabled={!draft.ids.includes(appId) && draft.ids.length >= 100} onChange={event => setDraft({ ...draft, ids: event.target.checked ? [...draft.ids, appId] : draft.ids.filter(value => value !== appId) })} />{name(appId, n)}</label>)}</fieldset>
      {stale ? <p role="alert">{copy("codex-apps.changed")}</p> : null}<div className="actions"><button className="primary" disabled={!fresh || locked || stale || !codexAppsIdle(session)}>{copy("codex-apps.save")}</button><button type="button" disabled={select.busy || select.uncertain} onClick={() => setDraft(undefined)}>{copy("codex-apps.cancel")}</button></div>
    </form> : null}
    {selectedIDs.length ? <section><h3>{copy("codex-apps.revoke")}</h3><p>{copy("codex-apps.removeOnly")}</p><fieldset disabled={!live || locked || removalStale}>{selectedIDs.map((appId, n) => <label className="checkbox" key={appId}><input type="checkbox" checked={Boolean(removal?.ids.includes(appId))} onChange={event => { const original = removal ?? { sessionId: session.id, accountId: account.id, revision: session.revision, generation, ids: [] }; setRemoval({ ...original, ids: event.target.checked ? [...original.ids, appId] : original.ids.filter(value => value !== appId) }); }} />{name(appId, n)}</label>)}</fieldset><button type="button" disabled={!live || locked || removalStale || !removal?.ids.length || removal.ids.some(value => !selectedIDs.includes(value))} onClick={() => { if (live && !locked && !removalStale && removal?.ids.length && removal.ids.every(value => selectedIDs.includes(value))) void revoke.send({ mutation: { id: removal.sessionId, expectedRevision: removal.revision, requestId: newRequestId() }, accountId: removal.accountId, configurationGeneration: removal.generation, appIds: [...removal.ids] }); }}>{copy("codex-apps.remove")}</button>{removalStale ? <p role="alert">{copy("codex-apps.changed")}</p> : null}{removal ? <button type="button" disabled={revoke.busy || revoke.uncertain} onClick={() => setRemoval(undefined)}>{copy("codex-apps.cancelRemoval")}</button> : null}</section> : null}
    {([inspect, select, revoke] as const).map((mutation, n) => mutation.uncertain ? <div key={n}><p role="alert">{copy("codex-apps.receiptUncertain")}</p><button type="button" disabled={mutation.busy || !active} onClick={mutation.retry}>{copy("codex-apps.retry")}</button></div> : null)}
  </Modal>;
}
