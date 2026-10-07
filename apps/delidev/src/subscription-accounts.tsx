import { LocalizedText, copy, useLocale } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDialog, SettingsTaskScope, SettingsDialogSize, SettingsDialogFocus, SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  AccountTypeFilter, EntityKind, FailureCode, ResourceQuery,
  SubscriptionAction, SubscriptionObservationAction, SubscriptionQuery, SubscriptionServiceId, SystemCapability, SystemQuery,
  clientFailure, isEntityId, newRequestId, subscriptionService, subscriptionServiceNames, type Resource,
} from "@delinoio/delidev-api-client";
import { useFailedSubscriptionCleanup } from "./subscription-cleanup";
import { SubscriptionQuotaControls, quotaAccountAvailable, quotaObservationMachine } from "./subscription-quota";
import { useSubscriptionLogin } from "./subscription-login";
import { useOAuthNativeControl } from "./account-oauth";
import { serviceAccount } from "./subscription-resource";
import { document, items, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { SubscriptionBrand } from "./subscription-catalog";
import { QuotaObservationState, SubscriptionOperationState, SubscriptionConnectionState, SubscriptionReadState, SubscriptionSettingsView, type SubscriptionAccountRow } from "./subscription-settings";

export { serviceAccount } from "./subscription-resource";

export function ManagedSubscriptionAccount({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
  useLocale();
  const closeTask = useCloseSettingsTask(close);
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
  return <section className="subscription-account-create" aria-label={copy("subscription-accounts.manageSubscription_5ac1d0", { v0: resourceName(current) })}>
    <h2>{resourceName(current)}</h2><p>{subscriptionServiceNames[service]} · {connected ? copy("subscription-accounts.connected_229655") : copy("subscription-accounts.disconnected_04dfac")}</p>
    {!supported ? <p role="status">{copy("subscription-accounts.nativeLoginForThisServiceIs_09b215")}</p> : null}
    {state.recovery_required === true ? <p role="alert">{copy("subscription-accounts.theOriginalCredentialOwnerRequiresRecovery_4da135")}</p> : null}
    <SettingsTaskActions className="">{!connected ? <button type="button" disabled={!ready || !flow.available} onClick={() => flow.begin(service, current)}><LocalizedText id="subscription-accounts.signInTo_fa4edf" components={{ s0: <>{subscriptionServiceNames[service]}</> }} /></button> : <><button type="button" disabled={!ready} onClick={() => request(SubscriptionAction.REFRESH)}>{copy("subscription-accounts.refreshLogin_85188a")}</button><button type="button" disabled={!logoutReady} onClick={() => setLogoutConfirmation(current)}>{copy("subscription-accounts.logOut_496161")}</button></>}</SettingsTaskActions>
    {logoutConfirmation ? <SettingsTaskDialog title={copy("subscription-accounts.logOut_496161")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setLogoutConfirmation(undefined)}><section aria-label={copy("subscription-accounts.confirmSubscriptionLogout_1d058a")}><p><LocalizedText id="subscription-accounts.logOutActiveExecutionsAreCanceled_dc5bf0" components={{ s0: <>{resourceName(logoutConfirmation)}</> }} /></p>{current.revision !== logoutConfirmation.revision ? <p role="alert">{copy("subscription-accounts.theAccountChangedInspectItAnd_316118")}</p> : null}<SettingsTaskActions className=""><button type="button" disabled={!logoutReady || current.revision !== logoutConfirmation.revision} onClick={() => { request(SubscriptionAction.LOGOUT); setLogoutConfirmation(undefined); }}>{copy("subscription-accounts.confirmLogout_2e9d08")}</button><button data-settings-task-cancel type="button" disabled={blocked} onClick={() => setLogoutConfirmation(undefined)}>{copy("subscription-accounts.keepAccountConnected_00ae06")}</button></SettingsTaskActions></section></SettingsTaskDialog> : null}
    {pendingID ? <p role="status">{text(pending.action)} · {pending.canceled === true ? copy("subscription-accounts.cancellationRequestedWaitingForOriginalCleanup_4f54b9") : text(pending.phase)}</p> : null}
    {!validRead ? <p role="alert">{copy("subscription-accounts.theCurrentAccountCouldNotBe_9c686a")}</p> : null}
    <Problem error={read.error || operation.error || status.error} />
    {operation.uncertain ? <button type="button" disabled={operation.busy} onClick={operation.retry}>{copy("subscription-accounts.retryOriginalSubscriptionOperation_691fa2")}</button> : null}
    {serviceSupported && connected ? <SubscriptionQuotaControls current={current} machine={quotaObservationMachine(state)} active={active && validRead} accepted={setAccepted} busyChanged={setQuotaBusy} /> : null}
    <SettingsTaskActions className=""><button type="button" disabled={blocked} onClick={() => void read.refetch()}>{copy("subscription-accounts.refreshAccountStatus_fa2870")}</button><button data-settings-task-cancel type="button" disabled={blocked} onClick={closeTask}>{copy("subscription-accounts.backToSubscriptions_257d53")}</button></SettingsTaskActions>
  </section>;
}

export function SubscriptionAccounts({ active, editAccount, deleteAccount, onWorkflowReadyChange }: { active: boolean; editAccount: (resource: Resource) => void; deleteAccount: (resource: Resource) => void; onWorkflowReadyChange?: (active: boolean) => void }) {
  useLocale();
  const [page, setPage] = useState("");
  const [selected, setSelected] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const capable = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const rows = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: page }, accountType: AccountTypeFilter.SUBSCRIPTION }, { enabled: active && capable });
  const quota = useRetainedMutation("subscription:quota:row",SubscriptionQuery.requestSubscriptionObservation,()=>{void rows.refetch();},(result,request)=>result.operationId===request.mutation?.requestId && serviceAccount(result.account,request.mutation?.id,undefined,request.mutation?.expectedRevision ?? 1n));
 const refreshAll = useRetainedMutation("subscription:quota:all",SubscriptionQuery.refreshAllSubscriptionQuotas,()=>{void rows.refetch();},(result,request)=>result.requestId===request.requestId && result.accounts.length<=10000 && new Set(result.accounts).size===result.accounts.length && result.accounts.every(isEntityId));
 const quotaSupported=status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_QUOTA_V1)===true;
 const native = useOAuthNativeControl();
 const [loginService, setLoginService] = useState<SubscriptionServiceId>();
 const loginCapable = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1) === true;
 const accountOperationsBlocked = Boolean(selected || loginService || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain);
 const cleanupCapable = status.data?.capabilities.includes(SystemCapability.FAILED_SUBSCRIPTION_CLEANUP_V1) === true;
 const cleanup = useFailedSubscriptionCleanup(active, cleanupCapable && !status.error && !rows.error && Boolean(rows.data) && !accountOperationsBlocked, () => { void rows.refetch(); });
 const workflow = Boolean(cleanup.blocked || accountOperationsBlocked);
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
      refresh: quotaSupported && service===SubscriptionServiceId.ChatGPT && quotaAccountAvailable(data) && isEntityId(quotaMachine) && !quota.busy && !quota.uncertain ? ()=>{ if (cleanup.canMutate()) void quota.send({mutation:{requestId:newRequestId(),id:row.id,expectedRevision:row.revision},machineId:quotaMachine,action:SubscriptionObservationAction.QUOTA,connectionId:text(object(data.connection).id),generationId:text(object(data.subscription).generation)}); } : undefined,
 health: text(data.health), enabled: data.enabled === true, providerState: copy("subscription-accounts.extra.22fc4e1096f2"), confirmedExhausted: data.confirmed_exhausted === true,
      windows: items(data.quota).map((entry) => { const window = object(entry); return { id: text(window.id), state: Object.values(QuotaObservationState).find((state) => state === window.state) ?? QuotaObservationState.Unknown, remaining: typeof window.remaining === "number" ? window.remaining : undefined, observedAt: text(window.observed_at), resetAt: text(window.reset_at) }; }),
      metadataAvailable: true, connect: service === SubscriptionServiceId.ChatGPT ? () => { if (cleanup.canMutate()) setSelected(row); } : undefined,
      details: () => { if (cleanup.canMutate()) setSelected(row); }, edit: () => { if (cleanup.canMutate()) editAccount(row); }, delete: () => { if (cleanup.canMutate()) deleteAccount(row); },
    };
  }) : [];
  const blocked = cleanup.blocked || Boolean(loginService) || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain;
  return <>
    <SubscriptionSettingsView actionsBlocked={cleanup.blocked} cleanup={active && readState === SubscriptionReadState.Ready && cleanupCapable && !status.data?.stopping && !accountOperationsBlocked ? cleanup.begin : undefined} cleanupBusy={cleanup.busy} cleanupBlocked={cleanup.blocked} cleanupStatus={cleanup.body} cleanupUnavailable={readState !== SubscriptionReadState.Ready || !active || status.data?.stopping ? copy("subscription-settings.cleanupUnavailable") : !cleanupCapable ? copy("subscription-settings.cleanupUnsupported") : undefined} refreshAll={quotaSupported && !blocked ? ()=>{ if (cleanup.canMutate()) void refreshAll.send({requestId:newRequestId()}); } : undefined} refreshAllOperation={refreshAll.uncertain ? {state:SubscriptionOperationState.Uncertain,retry:refreshAll.retry} : refreshAll.busy ? {state:SubscriptionOperationState.Busy} : undefined} accounts={accounts} state={readState} active={active} clearFilter={() => {}} problem={<><Problem error={problem} />{!validPage ? <p role="alert">{copy("subscription-accounts.theServerReturnedAnUnsupportedSubscription_ac7e8b")}</p> : null}</>} retryRead={() => { void status.refetch(); if (capable) void rows.refetch(); }}
      lifecycleUnavailable="ChatGPT sign-in opens your browser and is independent of Runner Devices. Claude Code and Grok sign-in are not supported yet. Quota refresh requires an existing native observation owner."
      selectService={capable && loginCapable && native && !blocked ? (brand) => { const service = subscriptionService(brand); if (service && cleanup.canMutate()) setLoginService(service); } : undefined}
      serviceLoginAvailable={(brand) => brand === SubscriptionBrand.ChatGPT}
      pagination={page || rows.data?.nextPageToken ? <nav className="settings-pages" aria-label={copy("subscription-accounts.subscriptionAccountPages_6c8f61")}><button type="button" disabled={!page || rows.isFetching || workflow} onClick={() => setPage("")}>{copy("subscription-accounts.firstPage_0bdbb7")}</button><button type="button" disabled={!rows.data?.nextPageToken || rows.isFetching || workflow} onClick={() => setPage(rows.data!.nextPageToken)}>{copy("subscription-accounts.nextPage_c08ac7")}</button></nav> : null}
      advanced={<p>{copy("subscription-accounts.serviceIdentityIsIndependentOfApi_fe87a6")}</p>} />
    {loginService ? <SettingsTaskScope><SubscriptionLoginTask service={loginService} active={active} changed={() => { void rows.refetch(); }} close={() => setLoginService(undefined)} /></SettingsTaskScope> : null}
    {selected ? <SettingsTaskDialog key={selected.id} title={copy("subscription-accounts.manageSubscriptionTitle")} size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={() => { setSelected(undefined); void rows.refetch(); }}><ManagedSubscriptionAccount initial={selected} active={active} close={() => { setSelected(undefined); void rows.refetch(); }} /></SettingsTaskDialog> : null}
    <Problem error={quota.error || refreshAll.error} />{quota.uncertain ? <button type="button" disabled={quota.busy} onClick={quota.retry}>{copy("subscription-accounts.retryOriginalQuotaRefresh_8a9eca")}</button> : null}
  </>;
}

function SubscriptionLoginTask({ service, active, changed, close }: { service: SubscriptionServiceId; active: boolean; changed: () => void; close: () => void }) {
  const flow = useSubscriptionLogin(active, changed, close);
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    let live = true;
    queueMicrotask(() => { if (live && !started.current) { started.current = true; flow.begin(service); } });
    return () => { live = false; };
  }, [flow, service]);
  return <SettingsTaskDialog title={copy("subscription-accounts.connectSubscription")} size={SettingsDialogSize.Wide} close={flow.leave}>{flow.body}</SettingsTaskDialog>;
}
