// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDialog, SettingsDialogSize, SettingsDialogFocus, SettingsTaskActions } from "./settings-task";
import { useRetainSettingsTask } from "./settings-task-context";
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  AccountTypeFilter, EntityKind, FailureCode, ResourceQuery,
  SubscriptionAction, SubscriptionObservationAction, SubscriptionQuery, SubscriptionServiceId, SystemCapability, SystemQuery,
  clientFailure, isEntityId, newRequestId, subscriptionService, subscriptionServiceNames, type Resource,
} from "@delinoio/delidev-api-client";
import { SubscriptionQuotaControls, quotaAccountAvailable, quotaObservationMachine } from "./subscription-quota";
import { useSubscriptionLogin } from "./subscription-login";
import { serviceAccount } from "./subscription-resource";
import { document, items, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { SubscriptionBrand } from "./subscription-catalog";
import { QuotaObservationState, SubscriptionOperationState, SubscriptionConnectionState, SubscriptionReadState, SubscriptionSettingsView, type SubscriptionAccountRow } from "./subscription-settings";

export { serviceAccount } from "./subscription-resource";

export function ManagedSubscriptionAccount({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
  const service = subscriptionService(document(initial).subscription_service)!;
  const [quotaBusy, setQuotaBusy] = useState(false);
  const [logoutConfirmation, setLogoutConfirmation] = useState<Resource>();
  const [accepted, setAccepted] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const read = useQuery(ResourceQuery.getResource, { kind: EntityKind.ACCOUNT, id: initial.id }, { enabled: active, refetchInterval: active ? 2000 : false });
  const flow = useSubscriptionLogin(active, () => { void read.refetch(); });
  const observed = read.data?.resource;
  const current = serviceAccount(observed, initial.id, service) && observed.revision >= (accepted?.revision ?? initial.revision) ? observed : accepted ?? initial;
  const data = document(current), state = object(data.subscription), pending = object(state.pending);
  const validRead = !read.error && (!observed || serviceAccount(observed, initial.id, service));
  const operation = useRetainedMutation("subscription:lifecycle:" + initial.id, SubscriptionQuery.requestSubscription, (result) => { setAccepted(result.account); void read.refetch(); },
    (result, request) => result.operationId === request.mutation?.requestId && serviceAccount(result.account, initial.id, service, request.mutation?.expectedRevision ?? 1n));
  const blocked = quotaBusy || operation.busy || operation.uncertain;
  const connected = Boolean(text(object(data.connection).id)), pendingID = text(pending.id);
  useRetainSettingsTask(Boolean(flow.workflow || pendingID));
  const capable = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1) === true;
  const serviceSupported = service === SubscriptionServiceId.ChatGPT;
  const supported = serviceSupported && capable;
  const logoutReady = active && supported && validRead && !blocked && !pendingID && state.recovery_required !== true && !data.removal;
  const ready = logoutReady && !["queued", "sending", "uncertain"].includes(text(object(state.observation).phase));
  const request = (action: SubscriptionAction) => {
    if (!(action === SubscriptionAction.LOGOUT ? logoutReady : ready)) return;
    void operation.send({ mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() }, action });
  };
  if (flow.body) return flow.body;
  return <section className="subscription-account-create" aria-label={`Manage subscription ${resourceName(current)}`}>
    <h2>{resourceName(current)}</h2><p>{subscriptionServiceNames[service]} · {connected ? "Connected" : "Disconnected"}</p>
    {!supported ? <p role="status">Native login for this service is unavailable in this server profile.</p> : null}
    {state.recovery_required === true ? <p role="alert">The original credential owner requires recovery. Inspect Connection & diagnostics before starting another operation.</p> : null}
    <SettingsTaskActions className="">{!connected ? <button type="button" disabled={!ready || !flow.available} onClick={() => flow.begin(service, current)}>Sign in to {subscriptionServiceNames[service]}</button> : <><button type="button" disabled={!ready} onClick={() => request(SubscriptionAction.REFRESH)}>Refresh login</button><button type="button" disabled={!logoutReady} onClick={() => setLogoutConfirmation(current)}>Log out</button></>}</SettingsTaskActions>
    {logoutConfirmation ? <SettingsTaskDialog title="Log out subscription" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setLogoutConfirmation(undefined)}><section aria-label="Confirm subscription logout"><p>Log out {resourceName(logoutConfirmation)}? Active executions are canceled and credentials are removed after original native cleanup. Preferences and history remain.</p>{current.revision !== logoutConfirmation.revision ? <p role="alert">The account changed. Inspect it and confirm the current account again.</p> : null}<SettingsTaskActions className=""><button type="button" disabled={!logoutReady || current.revision !== logoutConfirmation.revision} onClick={() => { request(SubscriptionAction.LOGOUT); setLogoutConfirmation(undefined); }}>Confirm logout</button><button type="button" disabled={blocked} onClick={() => setLogoutConfirmation(undefined)}>Keep account connected</button></SettingsTaskActions></section></SettingsTaskDialog> : null}
    {pendingID ? <p role="status">{text(pending.action)} · {pending.canceled === true ? "Cancellation requested; waiting for original cleanup" : text(pending.phase)}</p> : null}
    {!validRead ? <p role="alert">The current account could not be verified. Refresh it before another operation.</p> : null}
    <Problem error={read.error || operation.error || status.error} />
    {operation.uncertain ? <button type="button" disabled={operation.busy} onClick={operation.retry}>Retry original subscription operation</button> : null}
    {serviceSupported && connected ? <SubscriptionQuotaControls current={current} machine={quotaObservationMachine(state)} active={active && validRead} accepted={setAccepted} busyChanged={setQuotaBusy} /> : null}
    <SettingsTaskActions className=""><button type="button" disabled={blocked} onClick={() => void read.refetch()}>Refresh account status</button><button type="button" disabled={blocked} onClick={close}>Back to subscriptions</button></SettingsTaskActions>
  </section>;
}

export function SubscriptionAccounts({ active, editAccount, deleteAccount, onWorkflowReadyChange }: { active: boolean; editAccount: (resource: Resource) => void; deleteAccount: (resource: Resource) => void; onWorkflowReadyChange?: (active: boolean) => void }) {
  const [page, setPage] = useState("");
  const [selected, setSelected] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const capable = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const rows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: page }, accountType: AccountTypeFilter.SUBSCRIPTION }, { enabled: active && capable });
  const quota = useRetainedMutation("subscription:quota:row",SubscriptionQuery.requestSubscriptionObservation,()=>{void rows.refetch();},(result,request)=>result.operationId===request.mutation?.requestId && serviceAccount(result.account,request.mutation?.id,undefined,request.mutation?.expectedRevision ?? 1n));
 const refreshAll = useRetainedMutation("subscription:quota:all",SubscriptionQuery.refreshAllSubscriptionQuotas,()=>{void rows.refetch();},(result,request)=>result.requestId===request.requestId && result.accounts.length<=10000 && new Set(result.accounts).size===result.accounts.length && result.accounts.every(isEntityId));
 const quotaSupported=status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_QUOTA_V1)===true;
 const flow = useSubscriptionLogin(active, () => { void rows.refetch(); });
 const loginCapable = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1) === true;
 const workflow = Boolean(selected || flow.workflow || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain);
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
      refresh: quotaSupported && service===SubscriptionServiceId.ChatGPT && quotaAccountAvailable(data) && isEntityId(quotaMachine) && !quota.busy && !quota.uncertain ? ()=>void quota.send({mutation:{requestId:newRequestId(),id:row.id,expectedRevision:row.revision},machineId:quotaMachine,action:SubscriptionObservationAction.QUOTA,connectionId:text(object(data.connection).id),generationId:text(object(data.subscription).generation)}) : undefined,
 health: text(data.health), enabled: data.enabled === true, providerState: "Service-native account", confirmedExhausted: data.confirmed_exhausted === true,
      windows: items(data.quota).map((entry) => { const window = object(entry); return { id: text(window.id), state: Object.values(QuotaObservationState).find((state) => state === window.state) ?? QuotaObservationState.Unknown, remaining: typeof window.remaining === "number" ? window.remaining : undefined, observedAt: text(window.observed_at), resetAt: text(window.reset_at) }; }),
      metadataAvailable: true, connect: service === SubscriptionServiceId.ChatGPT ? () => setSelected(row) : undefined,
      details: () => setSelected(row), edit: () => editAccount(row), delete: () => deleteAccount(row),
    };
  }) : [];
  const blocked = flow.workflow || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain;
  return <>
    <SubscriptionSettingsView refreshAll={quotaSupported && !blocked ? ()=>void refreshAll.send({requestId:newRequestId()}) : undefined} refreshAllOperation={refreshAll.uncertain ? {state:SubscriptionOperationState.Uncertain,retry:refreshAll.retry} : refreshAll.busy ? {state:SubscriptionOperationState.Busy} : undefined} accounts={accounts} state={readState} active={active} clearFilter={() => {}} problem={<><Problem error={problem} />{!validPage ? <p role="alert">The server returned an unsupported subscription account page.</p> : null}</>} retryRead={() => { void status.refetch(); if (capable) void rows.refetch(); }}
      lifecycleUnavailable="ChatGPT sign-in opens your browser and is independent of Runner Devices. Claude Code and Grok sign-in are not supported yet. Quota refresh requires an existing native observation owner."
      selectService={capable && loginCapable && flow.available && !blocked ? (brand) => { const service = subscriptionService(brand); if (service) flow.begin(service); } : undefined}
      serviceLoginAvailable={(brand) => brand === SubscriptionBrand.ChatGPT}
      pagination={page || rows.data?.nextPageToken ? <nav className="settings-pages" aria-label="Subscription account pages"><button type="button" disabled={!page || rows.isFetching || workflow} onClick={() => setPage("")}>First page</button><button type="button" disabled={!rows.data?.nextPageToken || rows.isFetching || workflow} onClick={() => setPage(rows.data!.nextPageToken)}>Next page</button></nav> : null}
      advanced={<p>Service identity is independent of API providers. Edit preferences from the account menu. Recovery notifications start disabled. Retired legacy configurations retain historical attribution and require explicit Agent Worker and schedule reconfiguration.</p>} />
    {flow.body ? <SettingsTaskDialog title="Connect a subscription" size={SettingsDialogSize.Wide} retained close={flow.leave}>{flow.body}</SettingsTaskDialog> : null}
    {selected ? <SettingsTaskDialog key={selected.id} title="Manage subscription" size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={() => { setSelected(undefined); void rows.refetch(); }}><ManagedSubscriptionAccount initial={selected} active={active} close={() => { setSelected(undefined); void rows.refetch(); }} /></SettingsTaskDialog> : null}
    <Problem error={quota.error || refreshAll.error} />{quota.uncertain ? <button type="button" disabled={quota.busy} onClick={quota.retry}>Retry original quota refresh</button> : null}
  </>;
}
