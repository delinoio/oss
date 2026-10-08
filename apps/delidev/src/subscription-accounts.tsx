import { LocalConnectionHelp } from "./local-connection-presentation";
import { SettingsTaskDismissButton } from "./settings-task";
import { LocalizedText, copy, useLocale } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { SettingsEmpty } from "./settings-presentation";
import { SettingsTaskDialog, SettingsDialogSize, SettingsDialogFocus, SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  AccountTypeFilter, EntityKind, FailureCode, ResourceQuery,
  SubscriptionAction, SubscriptionObservationAction, SubscriptionQuery, SubscriptionServiceId, SystemCapability, SystemQuery,
  clientFailure, isEntityId, newRequestId, subscriptionService, subscriptionServiceNames, type Resource,
} from "@delinoio/delidev-api-client";
import { useFailedSubscriptionCleanup } from "./subscription-cleanup";
import { SubscriptionQuotaControls, quotaAccountAvailable, quotaObservationMachine, serverQuotaAvailable } from "./subscription-quota";
import { useSubscriptionLogin } from "./subscription-login";
import { serviceAccount } from "./subscription-resource";
import { document, items, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Failure, InlineRemediation, Problem } from "./ui";
import { SubscriptionBrand } from "./subscription-catalog";
import { QuotaObservationState, SubscriptionOperationState, SubscriptionConnectionState, SubscriptionReadState, SubscriptionSettingsView, SubscriptionRow, type SubscriptionAccountRow, type SubscriptionAccountDetails } from "./subscription-settings";

export { serviceAccount } from "./subscription-resource";

interface SubscriptionManagementSelection { id: string; revision: bigint; service: SubscriptionServiceId; opener?: HTMLElement | null }
export function ManagedSubscriptionAccount({ initial, active, close }: { initial: Resource | SubscriptionManagementSelection; active: boolean; close: () => void }) {
  useLocale();
  const closeTask = useCloseSettingsTask(close);
  const initialResource = "kind" in initial ? initial : undefined;
  const service = initialResource ? subscriptionService(document(initialResource).subscription_service)! : (initial as SubscriptionManagementSelection).service;
  const [quotaBusy, setQuotaBusy] = useState(false);
  const [logoutConfirmation, setLogoutConfirmation] = useState<Resource>();
  const [accepted, setAccepted] = useState<Resource>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const read = useQuery(ResourceQuery.getResource, { kind: EntityKind.ACCOUNT, id: initial.id }, { enabled: active, refetchInterval: active ? 2000 : false });
  const flow = useSubscriptionLogin(active, () => { void read.refetch(); });
  const observed = read.data?.resource;
  const minimumRevision = accepted && accepted.revision > initial.revision ? accepted.revision : initial.revision;
  const verifiedObservation = serviceAccount(observed, initial.id, service, minimumRevision);
  const current = verifiedObservation ? observed : accepted && accepted.revision >= initial.revision ? accepted : initialResource;
  const data = document(current), state = object(data.subscription), pending = object(state.pending);
  const validRead = !read.error && (!read.isSuccess || verifiedObservation);
  const operation = useRetainedMutation("subscription:lifecycle:" + initial.id, SubscriptionQuery.requestSubscription, (result) => { setAccepted(result.account); void read.refetch(); },
    (result, request) => result.operationId === request.mutation?.requestId && serviceAccount(result.account, initial.id, service, request.mutation?.expectedRevision ?? 1n));
  const blocked = quotaBusy || operation.busy || operation.uncertain;
  const connected = Boolean(text(object(data.connection).id)), pendingID = text(pending.id);
  const capable = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1) === true;
  const serviceSupported = service === SubscriptionServiceId.ChatGPT || service === SubscriptionServiceId.Claude;
  const supported = serviceSupported && (service === SubscriptionServiceId.Claude ? status.data?.capabilities.includes(SystemCapability.CLAUDE_SUBSCRIPTIONS_V1) === true : capable);
  const ownerMachine = text(state.owner_machine_id);
  const owner = useQuery(ResourceQuery.getResource,{kind:EntityKind.MACHINE,id:ownerMachine},{enabled:active && service === SubscriptionServiceId.Claude && isEntityId(ownerMachine)});
  const logoutReady = active && supported && !status.error && validRead && !blocked && !pendingID && state.recovery_required !== true && !data.removal;
  const ready = logoutReady && !["queued", "sending", "uncertain"].includes(text(object(state.observation).phase));
  const request = (action: SubscriptionAction) => {
    if (!current || !(action === SubscriptionAction.LOGOUT ? logoutReady : ready)) return;
    void operation.send({ mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() }, action, ...(service === SubscriptionServiceId.Claude ? {machineId:text(state.owner_machine_id)} : {}) });
  };
  if (!current) return <><p role="status">{copy("subscription-settings.loadingSubscriptions_d98d84")}</p><Problem error={read.error} summary={<p>{copy("subscription-accounts.theCurrentAccountCouldNotBe_9c686a")}</p>} />{read.isSuccess && !verifiedObservation ? <p role="alert">{copy("subscription-accounts.theCurrentAccountCouldNotBe_9c686a")}</p> : null}</>;
  if (flow.body) return service === SubscriptionServiceId.Claude ? <SettingsTaskDialog title={copy("claude-subscription.title")} size={SettingsDialogSize.Wide} retained={flow.retained} close={flow.hide}>{flow.body}</SettingsTaskDialog> : flow.body;
  return <section className="subscription-account-create" aria-label={copy("subscription-accounts.manageSubscription_5ac1d0", { v0: resourceName(current) })}>
    <h2>{resourceName(current)}</h2><p>{subscriptionServiceNames[service]} · {connected ? copy("subscription-accounts.connected_229655") : copy("subscription-accounts.disconnected_04dfac")}</p>
    {service === SubscriptionServiceId.Claude && ownerMachine ? <p>{copy("claude-subscription.ownerRunner",{runner:owner.data?.resource?.id === ownerMachine ? resourceName(owner.data.resource) : copy("claude-subscription.originalRunner")})}</p> : null}
    {status.isFetching ? <p role="status">{copy("account-connection.inline.supportLoading")}</p> : null}
    {!supported && (!serviceSupported || status.isSuccess && !status.error && !status.isFetching) ? <InlineRemediation summary={<p>{copy("account-connection.inline.subscriptionUnsupported")}</p>} actions={<button type="button" disabled={!active || status.isFetching} onClick={() => void status.refetch()}>{copy("account-connection.inline.supportRecheck")}</button>} /> : null}
    {state.recovery_required === true ? <InlineRemediation summary={<p>{copy("account-connection.inline.subscriptionRecovery")}</p>} /> : null}
    <SettingsTaskActions className="">{!connected ? <button type="button" disabled={!ready || !flow.available} onClick={() => flow.begin(service, current)}><LocalizedText id="subscription-accounts.signInTo_fa4edf" components={{ s0: <>{subscriptionServiceNames[service]}</> }} /></button> : <><button type="button" disabled={!ready} onClick={() => service === SubscriptionServiceId.Claude ? flow.begin(service,current,true) : request(SubscriptionAction.REFRESH)}>{copy(service === SubscriptionServiceId.Claude ? "claude-subscription.reauth" : "subscription-accounts.refreshLogin_85188a")}</button><button type="button" disabled={!logoutReady} onClick={() => setLogoutConfirmation(current)}>{copy("subscription-accounts.logOut_496161")}</button></>}</SettingsTaskActions>
    {flow.hidden ? <button type="button" onClick={flow.show}>{copy("claude-subscription.viewOriginalOperation")}</button> : null}
    {logoutConfirmation ? <SettingsTaskDialog title={copy("subscription-accounts.logOut_496161")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setLogoutConfirmation(undefined)}><section aria-label={copy("subscription-accounts.confirmSubscriptionLogout_1d058a")}><p><LocalizedText id="subscription-accounts.logOutActiveExecutionsAreCanceled_dc5bf0" components={{ s0: <>{resourceName(logoutConfirmation)}</> }} /></p>{current.revision !== logoutConfirmation.revision ? <p role="alert">{copy("subscription-accounts.theAccountChangedInspectItAnd_316118")}</p> : null}<SettingsTaskActions className=""><button type="button" disabled={!logoutReady || current.revision !== logoutConfirmation.revision} onClick={() => { request(SubscriptionAction.LOGOUT); setLogoutConfirmation(undefined); }}>{copy("subscription-accounts.confirmLogout_2e9d08")}</button><SettingsTaskDismissButton data-settings-task-cancel type="button" disabled={blocked} onClick={() => setLogoutConfirmation(undefined)}>{copy("subscription-accounts.keepAccountConnected_00ae06")}</SettingsTaskDismissButton></SettingsTaskActions></section></SettingsTaskDialog> : null}
    {pendingID ? <><p>{copy("account-connection.inline.subscriptionPending")}</p><p role="status">{text(pending.action)} · {pending.canceled === true ? copy("subscription-accounts.cancellationRequestedWaitingForOriginalCleanup_4f54b9") : text(pending.phase)}</p></> : null}
    {!validRead ? <p role="alert">{copy("subscription-accounts.theCurrentAccountCouldNotBe_9c686a")}</p> : null}
    <Problem error={read.error || operation.error || status.error} summary={<p>{copy(read.error || status.error ? "account-connection.inline.read" : "account-connection.inline.operation")}</p>} actions={read.error || status.error ? <button type="button" disabled={!active || read.isFetching || status.isFetching} onClick={() => { void read.refetch(); void status.refetch(); }}>{copy("subscription-accounts.refreshAccountStatus_fa2870")}</button> : undefined} />
    {read.error || status.error ? <LocalConnectionHelp active={active} /> : null}
    {operation.uncertain ? <button type="button" disabled={operation.busy} onClick={operation.retry}>{copy("subscription-accounts.retryOriginalSubscriptionOperation_691fa2")}</button> : null}
    {service === SubscriptionServiceId.ChatGPT && connected ? <SubscriptionQuotaControls current={current} machine={quotaObservationMachine(state)} active={active && validRead} accepted={setAccepted} busyChanged={setQuotaBusy} /> : null}
    <SettingsTaskActions className=""><button type="button" disabled={blocked} onClick={() => void read.refetch()}>{copy("subscription-accounts.refreshAccountStatus_fa2870")}</button><SettingsTaskDismissButton data-settings-task-cancel type="button" disabled={blocked} onClick={closeTask}>{copy("subscription-accounts.backToSubscriptions_257d53")}</SettingsTaskDismissButton></SettingsTaskActions>
  </section>;
}

import { useResourceScrollQuery } from "./resource-scroll-query";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
export function SubscriptionAccounts({ active, editAccount, deleteAccount, onWorkflowReadyChange }: { active: boolean; editAccount: (resource: Resource) => void; deleteAccount: (resource: Resource) => void; onWorkflowReadyChange?: (active: boolean) => void }) {
  useLocale();
  const content = useRef<HTMLDivElement>(null), root = useScrollRoot(content);
  const [selected, setSelected] = useState<Resource | SubscriptionManagementSelection>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const capable = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const refreshInventory = useRef<() => void>(() => {});
  // Login/status authority stays active while its own presentation pauses only
  // the background inventory reader. The callback uses the current reader.
  const flow = useSubscriptionLogin(active, () => refreshInventory.current());
  const inventory = useResourceScrollQuery(EntityKind.ACCOUNT, active && capable && !selected && !flow.workflow, "subscriptions", false, AccountTypeFilter.SUBSCRIPTION, "", serviceAccount, true);
  // An inert parent beneath its original edit/delete dialog retains its three
  // resident pages and mounted disclosure owners. Reads remain suspended; the
  // Settings visit/category owns disposal when this component unmounts.
  const resident = inventory.payloadPages.flatMap(page => page.payload);
  const rows = { data: inventory.loaded ? { resources: resident, nextPageToken: inventory.nextPageToken } : undefined,
    error: inventory.error?.failure, isFetching: Boolean(inventory.loading), refetch: () => inventory.refreshExplicit() };
  refreshInventory.current = rows.refetch;
  const quota = useRetainedMutation("subscription:quota:row",SubscriptionQuery.requestSubscriptionObservation,()=>{void rows.refetch();},(result,request)=>result.operationId===request.mutation?.requestId && serviceAccount(result.account,request.mutation?.id,undefined,request.mutation?.expectedRevision ?? 1n));
 const refreshAll = useRetainedMutation("subscription:quota:all",SubscriptionQuery.refreshAllSubscriptionQuotas,()=>{void rows.refetch();},(result,request)=>result.requestId===request.requestId && result.accounts.length<=10000 && new Set(result.accounts).size===result.accounts.length && result.accounts.every(isEntityId));
 const workerQuotaSupported=status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_QUOTA_V1)===true;
 const serverQuotaSupported=status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V1)===true;
 const quotaSupported=workerQuotaSupported || serverQuotaSupported;
 const loginCapable = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1) === true;
 const accountOperationsBlocked = Boolean(selected || flow.workflow || quota.busy || quota.uncertain || refreshAll.busy || refreshAll.uncertain);
 const cleanupCapable = status.data?.capabilities.includes(SystemCapability.FAILED_SUBSCRIPTION_CLEANUP_V1) === true;
 const cleanup = useFailedSubscriptionCleanup(active, cleanupCapable && !status.error && !rows.error && Boolean(rows.data) && !accountOperationsBlocked, () => { void rows.refetch(); });
 const workflow = Boolean(cleanup.blocked || accountOperationsBlocked);
  useEffect(() => { onWorkflowReadyChange?.(workflow); return () => onWorkflowReadyChange?.(false); }, [onWorkflowReadyChange, workflow]);
  const validPage = !rows.data || rows.data.resources.every((row) => serviceAccount(row)) && new Set(rows.data.resources.map((row) => row.id)).size === rows.data.resources.length;
  const problem = status.error;
  const anyProblem = Boolean(problem || rows.error);
  const failure = rows.error?.code ?? (problem ? clientFailure(problem).code : undefined);
  const readState = failure === FailureCode.PermissionDenied ? SubscriptionReadState.PermissionDenied : failure === FailureCode.Unauthenticated ? SubscriptionReadState.AuthenticationExpired : anyProblem || !validPage ? SubscriptionReadState.Failed : !status.data || capable && !rows.data ? SubscriptionReadState.Loading : !capable ? SubscriptionReadState.Unsupported : SubscriptionReadState.Ready;
  const accounts: SubscriptionAccountRow[] = validPage ? (rows.data?.resources ?? []).map((row) => {
    const data = document(row), service = subscriptionService(data.subscription_service)!;
    const quotaMachine = quotaObservationMachine(object(data.subscription));
    return { id: row.id, revision: row.revision, alias: resourceName(row), providerName: subscriptionServiceNames[service], brand: service as unknown as SubscriptionBrand,
      connection: data.removal ? SubscriptionConnectionState.CleanupPending : object(data.subscription).recovery_required === true ? SubscriptionConnectionState.CleanupPending : text(object(data.connection).id) ? SubscriptionConnectionState.Connected : SubscriptionConnectionState.Disconnected,
      refresh: quotaSupported && service===SubscriptionServiceId.ChatGPT && quotaAccountAvailable(data) && (isEntityId(quotaMachine) && workerQuotaSupported || serverQuotaAvailable(object(data.subscription), serverQuotaSupported)) && !quota.busy && !quota.uncertain ? ()=>{ if (cleanup.canMutate()) void quota.send({mutation:{requestId:newRequestId(),id:row.id,expectedRevision:row.revision},machineId:quotaMachine,action:SubscriptionObservationAction.QUOTA,connectionId:text(object(data.connection).id),generationId:text(object(data.subscription).generation)}); } : undefined,
 health: text(data.health), enabled: data.enabled === true, providerState: copy("subscription-accounts.extra.22fc4e1096f2"), confirmedExhausted: data.confirmed_exhausted === true,
      windows: items(data.quota).map((entry) => { const window = object(entry); return { id: text(window.id), state: Object.values(QuotaObservationState).find((state) => state === window.state) ?? QuotaObservationState.Unknown, remaining: typeof window.remaining === "number" ? window.remaining : undefined, observedAt: text(window.observed_at), resetAt: text(window.reset_at) }; }),
      metadataAvailable: true, connect: service === SubscriptionServiceId.ChatGPT || service === SubscriptionServiceId.Claude ? () => { if (cleanup.canMutate()) setSelected(row); } : undefined,
      details: () => { if (cleanup.canMutate()) setSelected(row); }, edit: () => { if (cleanup.canMutate()) editAccount(row); }, delete: () => { if (cleanup.canMutate()) deleteAccount(row); },
    };
  }) : [];
  const blocked = cleanup.blocked || accountOperationsBlocked;
  const manageDetails = (account: SubscriptionAccountDetails, opener: HTMLElement | null) => {
    const identity = inventory.rows.find(row => row.id === account.id);
    const service = subscriptionService(account.brand);
    if (!active || blocked || !identity || !service || !cleanup.canMutate()) return;
    // The retained read-only projection grants no lifecycle authority. The
    // existing management reader must verify this exact ID/service/revision.
    setSelected({ id: identity.id, revision: identity.revision, service, opener });
  };
  return <div ref={content}>
    <SubscriptionSettingsView manageDetails={manageDetails} accountIds={inventory.loaded && !inventory.loading && !inventory.error ? inventory.rows.map(row => row.id) : undefined} actionsBlocked={blocked} cleanup={active && readState === SubscriptionReadState.Ready && cleanupCapable && !status.data?.stopping && !accountOperationsBlocked ? cleanup.begin : undefined} cleanupBusy={cleanup.busy} cleanupBlocked={cleanup.blocked} cleanupStatus={cleanup.body} cleanupUnavailable={readState !== SubscriptionReadState.Ready || !active || status.data?.stopping ? copy("subscription-settings.cleanupUnavailable") : !cleanupCapable ? copy("subscription-settings.cleanupUnsupported") : undefined} refreshAll={quotaSupported && !blocked ? ()=>{ if (cleanup.canMutate()) void refreshAll.send({requestId:newRequestId()}); } : undefined} refreshAllOperation={refreshAll.uncertain ? {state:SubscriptionOperationState.Uncertain,retry:refreshAll.retry} : refreshAll.busy ? {state:SubscriptionOperationState.Busy} : undefined} accountList={(now, openDetails) => <><ScrollPayloadWindow query={inventory} root={root} active={active && !workflow} identity={paginationIdentity} revision={paginationRevision}>{resources => resources.map(row => { const account = accounts.find(value => value.id === row.id); return account ? <SubscriptionRow key={row.id} account={account} now={now} unavailable={copy("claude-subscription.availability")} actionsBlocked={blocked} openDetails={openDetails} /> : null; })}</ScrollPayloadWindow>{inventory.loaded && !inventory.rows.length && !inventory.nextPageToken && !inventory.error ? <SettingsEmpty title={copy("subscription-settings.noSubscriptionsYet_9c2ace")}><p>{copy("subscription-settings.savedSubscriptionsWillAppearHereIncluding_383bff")}</p></SettingsEmpty> : null}</>} completeEmpty={!inventory.nextPageToken} accounts={accounts} state={readState} active={active} clearFilter={() => {}} problem={<><Problem error={problem} /><Failure failure={rows.error} />{!validPage ? <p role="alert">{copy("subscription-accounts.theServerReturnedAnUnsupportedSubscription_ac7e8b")}</p> : null}</>} retryRead={() => { void status.refetch(); if (capable) void rows.refetch(); }}
      lifecycleUnavailable={copy("claude-subscription.availability")}
      selectService={capable && (loginCapable || status.data?.capabilities.includes(SystemCapability.CLAUDE_SUBSCRIPTIONS_V1)) && flow.available && !blocked ? (brand) => { const service = subscriptionService(brand); if (service && cleanup.canMutate()) flow.begin(service); } : undefined}
      serviceLoginAvailable={(brand) => brand === SubscriptionBrand.ChatGPT ? loginCapable : brand === SubscriptionBrand.Claude && status.data?.capabilities.includes(SystemCapability.CLAUDE_SUBSCRIPTIONS_V1) === true}
      pagination={<ScrollContinuation showInitial={false} showErrors={false} query={inventory} root={root} active={active && !workflow} label={copy("subscription-accounts.subscriptionAccountPages_6c8f61")} />}
      advanced={<p>{copy("subscription-accounts.serviceIdentityIsIndependentOfApi_fe87a6")}</p>} />
    {flow.hidden ? <button type="button" onClick={flow.show}>{copy("claude-subscription.viewOriginalOperation")}</button> : null}
    {flow.body ? <SettingsTaskDialog title={copy(flow.service === SubscriptionServiceId.Claude ? "claude-subscription.title" : "subscription-accounts.connectSubscription")} size={SettingsDialogSize.Wide} retained={flow.retained} close={flow.hide}>{flow.body}</SettingsTaskDialog> : null}
    {selected ? <SettingsTaskDialog key={selected.id} fallbackFocus={"kind" in selected ? undefined : () => selected.opener?.isConnected && !selected.opener.closest("[hidden]") && !selected.opener.hasAttribute("disabled") ? selected.opener : content.current?.querySelector<HTMLElement>("h1, [data-subscription-details-fallback]") ?? null} title={copy("subscription-accounts.manageSubscriptionTitle")} size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={() => { setSelected(undefined); void rows.refetch(); }}><ManagedSubscriptionAccount initial={selected} active={active} close={() => { setSelected(undefined); void rows.refetch(); }} /></SettingsTaskDialog> : null}
    <Problem error={quota.error || refreshAll.error} summary={<p>{copy("account-connection.inline.quota")}</p>} />{quota.uncertain ? <button type="button" disabled={quota.busy} onClick={quota.retry}>{copy("subscription-accounts.retryOriginalQuotaRefresh_8a9eca")}</button> : null}
  </div>;
}
