// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  AccountTypeFilter, ConfigurationQuery, EntityKind, FailureCode, ResourceQuery,
  SubscriptionAction, SubscriptionObservationAction, SubscriptionQuery, SubscriptionServiceId, SystemCapability, SystemQuery,
  clientFailure, isEntityId, newRequestId, subscriptionService, subscriptionServiceNames, supportsResourceSchema, type Resource,
} from "@delinoio/delidev-api-client";
import { SubscriptionQuotaControls, quotaObservationMachine } from "./subscription-quota";
 import { ResourceChoice } from "./configuration-fields";
import { document, encode, items, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { SubscriptionBrand } from "./subscription-catalog";
import { QuotaObservationState, SubscriptionOperationState, SubscriptionConnectionState, SubscriptionReadState, SubscriptionSettingsView, type SubscriptionAccountRow } from "./subscription-settings";

export function serviceAccount(resource: Resource | undefined, id?: string, service?: SubscriptionServiceId, minimumRevision = 1n): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || resource.schemaVersion !== 2 || !supportsResourceSchema(resource) ||
      !isEntityId(resource.id) || resource.revision < minimumRevision || id && resource.id !== id) return false;
  const data = document(resource);
  return data.retired !== true && data.type === "subscription" && Boolean(subscriptionService(data.subscription_service)) &&
    (!service || data.subscription_service === service);
}

function loginPresentation(url: string, userCode: string) {
  if (!url || url.length > 8192 || userCode && !/^[A-Z0-9-]{4,32}$/.test(userCode)) return false;
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" && ["auth.openai.com", "chatgpt.com"].includes(parsed.host) && !parsed.username && !parsed.password && !parsed.hash;
  } catch { return false; }
}

export function ManagedSubscriptionAccount({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
  const service = subscriptionService(document(initial).subscription_service)!;
  const [machine, setMachine] = useState("");
 const [quotaBusy,setQuotaBusy]=useState(false);
  const [deviceCode, setDeviceCode] = useState(true);
  const [logoutConfirmation, setLogoutConfirmation] = useState<Resource>();
  const [accepted, setAccepted] = useState<Resource>();
  const [initiatedOperation, setInitiatedOperation] = useState("");
  const read = useQuery(ResourceQuery.getResource, { kind: EntityKind.ACCOUNT, id: initial.id }, { enabled: active, refetchInterval: active ? 2000 : false });
  const observed = read.data?.resource;
  const current = serviceAccount(observed, initial.id, service) && observed.revision >= (accepted?.revision ?? initial.revision) ? observed : accepted ?? initial;
  const data = document(current), state = object(data.subscription), pending = object(state.pending);
  const validRead = !read.error && (!observed || serviceAccount(observed, initial.id, service));
  const operation = useRetainedMutation("subscription:lifecycle:" + initial.id, SubscriptionQuery.requestSubscription, (result) => {
    setAccepted(result.account); setInitiatedOperation(result.operationId); void read.refetch();
  }, (result, request) => result.operationId === request.mutation?.requestId && serviceAccount(result.account, initial.id, service, request.mutation?.expectedRevision ?? 1n));
  const cancel = useRetainedMutation("subscription:cancel:" + initial.id, SubscriptionQuery.cancelSubscription, (result) => {
    setAccepted(result.account); setInitiatedOperation(""); void read.refetch();
  }, (result, request) => serviceAccount(result.account, request.mutation?.id, service, request.mutation?.expectedRevision ?? 1n));
  const ownLogin = active && validRead && Boolean(initiatedOperation) && pending.id === initiatedOperation && pending.action === "login" && pending.canceled !== true;
  const progress = useQuery(SubscriptionQuery.getSubscriptionProgress, { accountId: initial.id, operationId: initiatedOperation }, { enabled: ownLogin, refetchInterval: ownLogin ? 1500 : false, retry: false });
  const blocked = quotaBusy || operation.busy || operation.uncertain || cancel.busy || cancel.uncertain;
  const connected = Boolean(text(object(data.connection).id));
  const pendingID = text(pending.id);
  const supported = service === SubscriptionServiceId.ChatGPT;
  const ready = active && supported && validRead && !blocked && !pendingID && !["queued","sending","uncertain"].includes(text(object(state.observation).phase)) && state.recovery_required !== true && !data.removal;
  const logoutReady=active && supported && validRead && !blocked && !pendingID && state.recovery_required!==true && !data.removal;
 const request = (action: SubscriptionAction) => {
    if (!(action===SubscriptionAction.LOGOUT ? logoutReady : ready) || !machine) return;
    void operation.send({ mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() }, machineId: machine, action, deviceCode: action === SubscriptionAction.LOGIN && deviceCode });
  };
  // Login URLs remain transient presentation. Disposing Settings drops the
  // scoped query and cannot cancel, repeat or inherit a native login operation.
  const presentation = ownLogin && !progress.error && progress.data && !progress.data.canceled && loginPresentation(progress.data.url, progress.data.userCode) ? progress.data : undefined;
  return <section className="subscription-account-create" aria-label={`Manage subscription ${resourceName(current)}`}>
    <h2>{resourceName(current)}</h2><p>{subscriptionServiceNames[service]} · {connected ? "Connected" : "Disconnected"}</p>
    {!supported ? <p role="status">Native login for this service is unavailable in this server profile.</p> : null}
    {state.recovery_required === true ? <p role="alert">The original credential owner requires recovery. Inspect Connection & diagnostics before starting another operation.</p> : null}
    <fieldset disabled={!logoutReady}>
      <ResourceChoice label="Runner Device" kind={EntityKind.MACHINE} value={machine} change={setMachine} active={active && supported} required />
      {!connected ? <label className="checkbox"><input type="checkbox" checked={deviceCode} onChange={(event) => setDeviceCode(event.target.checked)} />Use a device code</label> : null}
      <div className="actions">{!connected ? <button type="button" disabled={!ready || !machine} onClick={() => request(SubscriptionAction.LOGIN)}>Log in with Codex</button> : <><button type="button" disabled={!ready || !machine} onClick={() => request(SubscriptionAction.REFRESH)}>Refresh login</button><button type="button" disabled={!machine} onClick={() => setLogoutConfirmation(current)}>Log out</button></>}</div>
    </fieldset>
    {logoutConfirmation ? <section aria-label="Confirm subscription logout"><p>Log out {resourceName(logoutConfirmation)}? Active executions are canceled and credentials are removed after original native cleanup. Preferences and history remain.</p>{current.revision !== logoutConfirmation.revision ? <p role="alert">The account changed. Inspect it and confirm the current account again.</p> : null}<div className="actions"><button type="button" disabled={!logoutReady || !machine || current.revision !== logoutConfirmation.revision} onClick={() => { request(SubscriptionAction.LOGOUT); setLogoutConfirmation(undefined); }}>Confirm logout</button><button type="button" disabled={blocked} onClick={() => setLogoutConfirmation(undefined)}>Keep account connected</button></div></section> : null}
    {pendingID ? <p role="status">{text(pending.action)} · {pending.canceled === true ? "Cancellation requested; waiting for original cleanup" : text(pending.phase)}</p> : null}
    {presentation ? <section aria-label="Subscription login instructions"><p>Open this address in your browser to finish the original login.</p><label>Login address<input readOnly value={presentation.url} /></label>{presentation.userCode ? <p>Device code: <code>{presentation.userCode}</code></p> : null}</section> : null}
    {ownLogin ? <button type="button" disabled={blocked} onClick={() => void cancel.send({ mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() } })}>Cancel login</button> : null}
    {!validRead ? <p role="alert">The current account could not be verified. Refresh it before another operation.</p> : null}
    <Problem error={read.error || operation.error || cancel.error || (ownLogin ? progress.error : undefined)} />
    {operation.uncertain ? <button type="button" disabled={operation.busy || cancel.busy} onClick={operation.retry}>Retry original subscription operation</button> : null}
    {cancel.uncertain ? <button type="button" disabled={operation.busy || cancel.busy} onClick={cancel.retry}>Retry original login cancellation</button> : null}
    {supported && connected ? <SubscriptionQuotaControls current={current} machine={machine} active={active && validRead} accepted={setAccepted} busyChanged={setQuotaBusy} /> : null}
 <div className="actions"><button type="button" disabled={blocked} onClick={() => void read.refetch()}>Refresh account status</button><button type="button" disabled={blocked} onClick={close}>Back to subscriptions</button></div>
  </section>;
}

export function SubscriptionAccounts({ active, editAccount, deleteAccount, onWorkflowReadyChange }: { active: boolean; editAccount: (resource: Resource) => void; deleteAccount: (resource: Resource) => void; onWorkflowReadyChange?: (active: boolean) => void }) {
  const [page, setPage] = useState("");
  const [service, setService] = useState<SubscriptionServiceId>();
  const [alias, setAlias] = useState("");
  const [selected, setSelected] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const capable = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const rows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: page }, accountType: AccountTypeFilter.SUBSCRIPTION }, { enabled: active && capable && !selected });
  const quota = useRetainedMutation("subscription:quota:row",SubscriptionQuery.requestSubscriptionObservation,()=>{void rows.refetch();},(result,request)=>result.operationId===request.mutation?.requestId && serviceAccount(result.account,request.mutation?.id,undefined,request.mutation?.expectedRevision ?? 1n));
 const refreshAll = useRetainedMutation("subscription:quota:all",SubscriptionQuery.refreshAllSubscriptionQuotas,()=>{void rows.refetch();},(result,request)=>result.requestId===request.requestId && result.accounts.length<=10000 && new Set(result.accounts).size===result.accounts.length && result.accounts.every(isEntityId));
 const quotaSupported=status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_QUOTA_V1)===true;
 const create = useRetainedMutation("subscription:configuration:create", ConfigurationQuery.saveConfiguration, (result) => {
    setService(undefined); setAlias(""); setSelected(result.resource); void rows.refetch();
  }, (result, request) => {
    let body: Record<string, unknown>;
    try { body = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(request.documentJson)); } catch { return false; }
    return result.requestId === request.mutation?.requestId && serviceAccount(result.resource, undefined, subscriptionService(body.subscription_service)) && document(result.resource).alias === body.alias;
  });
  const workflow = Boolean(selected || service || alias || create.busy || create.uncertain || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain);
  useEffect(() => { onWorkflowReadyChange?.(workflow); return () => onWorkflowReadyChange?.(false); }, [onWorkflowReadyChange, workflow]);
  const validPage = !rows.data || rows.data.resources.length <= 50 && rows.data.resources.every((row) => serviceAccount(row)) && new Set(rows.data.resources.map((row) => row.id)).size === rows.data.resources.length;
  const problem = status.error || rows.error;
  const failure = problem ? clientFailure(problem).code : undefined;
  const readState = failure === FailureCode.PermissionDenied ? SubscriptionReadState.PermissionDenied : failure === FailureCode.Unauthenticated ? SubscriptionReadState.AuthenticationExpired : problem || !validPage ? SubscriptionReadState.Failed : !status.data || capable && !rows.data ? SubscriptionReadState.Loading : !capable ? SubscriptionReadState.Unsupported : SubscriptionReadState.Ready;
  const accounts: SubscriptionAccountRow[] = validPage ? (rows.data?.resources ?? []).map((row) => {
    const data = document(row), service = subscriptionService(data.subscription_service)!;
    const quotaMachine = quotaObservationMachine(object(data.subscription));
    return { id: row.id, alias: resourceName(row), providerName: subscriptionServiceNames[service], brand: service as unknown as SubscriptionBrand,
      connection: data.removal ? SubscriptionConnectionState.CleanupPending : object(data.subscription).recovery_required === true ? SubscriptionConnectionState.CleanupPending : text(object(data.connection).id) ? SubscriptionConnectionState.Connected : SubscriptionConnectionState.Disconnected,
      refresh: quotaSupported && service===SubscriptionServiceId.ChatGPT && data.health==="ready" && isEntityId(quotaMachine) && !quota.busy && !quota.uncertain ? ()=>void quota.send({mutation:{requestId:newRequestId(),id:row.id,expectedRevision:row.revision},machineId:quotaMachine,action:SubscriptionObservationAction.QUOTA,connectionId:text(object(data.connection).id),generationId:text(object(data.subscription).generation)}) : undefined,
 health: text(data.health), enabled: data.enabled === true, providerState: "Service-native account", confirmedExhausted: data.confirmed_exhausted === true,
      windows: items(data.quota).map((entry) => { const window = object(entry); return { id: text(window.id), state: Object.values(QuotaObservationState).find((state) => state === window.state) ?? QuotaObservationState.Unknown, remaining: typeof window.remaining === "number" ? window.remaining : undefined, observedAt: text(window.observed_at), resetAt: text(window.reset_at) }; }),
      metadataAvailable: true, connect: service === SubscriptionServiceId.ChatGPT ? () => setSelected(row) : undefined,
      details: () => setSelected(row), edit: () => editAccount(row), delete: () => deleteAccount(row),
    };
  }) : [];
  const blocked = create.busy || create.uncertain || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain;
  const aliasValid = alias.trim().length > 0 && !alias.includes("\0") && new TextEncoder().encode(alias).byteLength <= 256;
  if (selected) return <ManagedSubscriptionAccount key={selected.id} initial={selected} active={active} close={() => { setSelected(undefined); void rows.refetch(); }} />;
  return <>
    <SubscriptionSettingsView refreshAll={quotaSupported && !blocked ? ()=>void refreshAll.send({requestId:newRequestId()}) : undefined} refreshAllOperation={refreshAll.uncertain ? {state:SubscriptionOperationState.Uncertain,retry:refreshAll.retry} : refreshAll.busy ? {state:SubscriptionOperationState.Busy} : undefined} accounts={accounts} state={readState} active={active} clearFilter={() => {}} problem={<><Problem error={problem} />{!validPage ? <p role="alert">The server returned an unsupported subscription account page.</p> : null}</>} retryRead={() => { void status.refetch(); if (capable) void rows.refetch(); }}
      lifecycleUnavailable="ChatGPT login is managed by Codex on your selected Runner Device. Quota observations require native support; refresh login does not refresh quota. Claude and Grok login are unavailable in this profile."
      selectService={capable && !blocked ? (brand) => { setService(subscriptionService(brand)); setAlias(""); } : undefined}
      pagination={page || rows.data?.nextPageToken ? <nav className="settings-pages" aria-label="Subscription account pages"><button type="button" disabled={!page || rows.isFetching || workflow} onClick={() => setPage("")}>First page</button><button type="button" disabled={!rows.data?.nextPageToken || rows.isFetching || workflow} onClick={() => setPage(rows.data!.nextPageToken)}>Next page</button></nav> : null}
      advanced={<p>Service identity is independent of API providers. Edit preferences from the account menu. Recovery notifications start disabled. Retired legacy configurations retain historical attribution and require explicit Agent Worker and schedule reconfiguration.</p>} />
    {service ? <form className="subscription-account-create" aria-label="Add subscription account" onSubmit={(event) => { event.preventDefault(); if (!capable || !aliasValid || blocked) return; void create.send({ mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ alias, subscription_service: service, type: "subscription", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [], confirmed_exhausted: false }) }); }}>
      <h2>Add {subscriptionServiceNames[service]} account</h2><fieldset disabled={blocked}><label>Account name<input autoFocus required maxLength={256} autoComplete="off" value={alias} onChange={(event) => setAlias(event.target.value)} /></label><p>Saving creates disconnected account preferences. Login is a separate explicit operation.</p><div className="actions"><button disabled={!capable || !aliasValid}>Save subscription account</button><button type="button" onClick={() => { setService(undefined); setAlias(""); }}>Cancel account creation</button></div></fieldset>
    </form> : null}
    <Problem error={create.error || quota.error || refreshAll.error} />{quota.uncertain ? <button type="button" disabled={quota.busy} onClick={quota.retry}>Retry original quota refresh</button> : null}{create.uncertain ? <button type="button" disabled={create.busy} onClick={create.retry}>Retry original subscription account creation</button> : null}
  </>;
}
