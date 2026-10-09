// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { DisclosureButton, DisclosureContent, DisclosureDensity } from "./disclosure";
import { Timestamp } from "./timestamp-display";
import { timestampInstant } from "./timestamp-format";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import "./subscription-quota.css";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, SubscriptionObservationAction, SubscriptionQuery, SystemCapability, SystemQuery, EntityKind, isEntityId, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { accountPreferencesDocument } from "./account-preferences";
import { serviceAccount } from "./subscription-accounts";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

interface CreditConfirmation { machine: string; account: Resource; creditId: string; ordinal?: number; next: boolean; inventoryId: string; connection: string; generation: string }
const outcomeLabels: Record<string, string> = { get reset() { return copy("subscription-quota.resetCreditConsumed_ac52a6"); }, get alreadyRedeemed() { return copy("subscription-quota.originalResetCreditWasAlreadyConsumed_3d5beb"); }, get nothingToReset() { return copy("subscription-quota.noCurrentQuotaWindowNeededA_972b84"); }, get noCredit() { return copy("subscription-quota.noResetCreditWasAvailable_648345"); } };

export function quotaObservationMachine(state: Record<string, unknown>, preferred = ""): string {
  const lease = object(state.lease);
  return Object.keys(lease).length ? text(lease.action) === "execute" ? text(lease.machine_id) : "" : preferred || text(state.owner_machine_id);
}

export function serverCreditAvailable(state: Record<string, unknown>, supported: boolean): boolean {
  return supported && !text(state.owner_machine_id) && !Object.keys(object(state.lease)).length && text(state.server_quota_generation) === text(state.generation) && isEntityId(text(state.generation));
}

export function serverQuotaAvailable(state: Record<string, unknown>, supported: boolean): boolean {
 const lease=object(state.lease);
 return supported && isEntityId(text(state.generation)) && (!Object.keys(lease).length || text(lease.action)==="execute");
}

export function quotaAccountAvailable(data: Record<string, unknown>): boolean {
  const state = object(data.subscription), observation = object(state.observation), serverQuota = object(state.server_quota), serverCredit = object(state.server_credit);
  return data.health === "ready" && isEntityId(text(object(data.connection).id)) && isEntityId(text(state.generation)) && !data.removal && state.recovery_required !== true && !state.pending && !["queued", "sending", "uncertain"].includes(text(observation.phase)) && !["queued", "sending", "uncertain"].includes(text(serverQuota.phase)) && !["queued", "sending", "uncertain"].includes(text(serverCredit.phase));
}

export function SubscriptionQuotaControls({ current, machine, active, accepted, busyChanged }: { current: Resource; machine: string; active: boolean; accepted: (resource: Resource) => void; busyChanged: (busy: boolean) => void }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const workerQuotaSupported = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_QUOTA_V1) === true;
  const serverSupported = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2) === true;
  const quotaSupported = workerQuotaSupported || serverSupported;
  const workerCreditsSupported = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1) === true;
  const serverCreditsSupported = status.data?.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V1) === true;
  const creditsSupported = workerCreditsSupported || serverCreditsSupported;
  const data = document(current), state = object(data.subscription), connection = text(object(data.connection).id), generation = text(state.generation), observation = object(state.observation), inventory = object(state.reset_credits);
  const ownerMachine = quotaObservationMachine(state, machine);
  const serverAvailable = serverQuotaAvailable(state, serverSupported);
  const serverObservation = object(state.server_quota), serverCredit = object(state.server_credit);
  const creditObservation = observation.action === "reset-credit" && observation.phase === "uncertain" ? observation : text(serverCredit.id) ? serverCredit : observation;
  const reconcilingServer = creditObservation === serverCredit;
  const serverCreditReady = serverCreditAvailable(state, serverCreditsSupported);
  const creditLaneAvailable = isEntityId(ownerMachine) ? workerCreditsSupported : serverCreditReady;
  const [confirmation, setConfirmation] = useState<CreditConfirmation>();
  const [expanded, setExpanded] = useState(false);
  const sectionHeading = useRef<HTMLHeadingElement>(null), detailsHeading = useRef<HTMLHeadingElement>(null), confirmationHeading = useRef<HTMLHeadingElement>(null);
  const selectionOpener = useRef<HTMLElement | null>(null), focusDetails = useRef(false), focusConfirmation = useRef(false);
  const detailsId = useId(), detailsHeadingId = useId();
  useEffect(() => {
    if (!expanded || !focusDetails.current) return;
    focusDetails.current = false;
    const heading = detailsHeading.current, originalFocus = globalThis.document.activeElement;
    heading?.focus();
    // DisclosureContent removes its staged hidden state in a layout update.
    // Retry only this explicit Use intent after visibility settles, without
    // taking focus back from an intervening keyboard or pointer action.
    if (heading && globalThis.document.activeElement !== heading) {
      const frame = requestAnimationFrame(() => { if (globalThis.document.activeElement === originalFocus) heading.focus(); });
      return () => cancelAnimationFrame(frame);
    }
  }, [expanded]);
  useEffect(() => { if (confirmation && focusConfirmation.current) { focusConfirmation.current = false; confirmationHeading.current?.focus(); } }, [confirmation]);
  const [preferenceProblem, setPreferenceProblem] = useState("");
  const observe = useRetainedMutation("subscription:observe:" + current.id, SubscriptionQuery.requestSubscriptionObservation, (result) => { if (result.account) accepted(result.account); setConfirmation(undefined); }, (result, request) => result.operationId === request.mutation?.requestId && serviceAccount(result.account, current.id, undefined, request.mutation?.expectedRevision ?? 1n));
  const reconcile = useRetainedMutation("subscription:credit:reconcile:" + current.id, SubscriptionQuery.reconcileSubscriptionCredit, (result) => { if (result.account) accepted(result.account); }, (result, request) => serviceAccount(result.account, current.id, undefined, request.mutation?.expectedRevision ?? 1n));
  const preferences = useRetainedMutation("subscription:quota:preferences:" + current.id, ConfigurationQuery.saveConfiguration, (result) => { if (result.resource) accepted(result.resource); }, (result, request) => serviceAccount(result.resource, current.id, undefined, request.mutation?.expectedRevision ?? 1n));
  const busy = observe.busy || observe.uncertain || reconcile.busy || reconcile.uncertain || preferences.busy || preferences.uncertain;
  useEffect(() => { busyChanged(busy); return () => busyChanged(false); }, [busy, busyChanged]);
  const originalActive = ["queued", "sending", "uncertain"].includes(text(observation.phase)) || ["queued", "sending", "uncertain"].includes(text(serverObservation.phase)) || ["queued", "sending", "uncertain"].includes(text(serverCredit.phase));
  const ready = active && quotaSupported && serviceAccount(current) && data.subscription_service === "chatgpt" && data.health === "ready" && isEntityId(connection) && isEntityId(generation) && serverAvailable && state.recovery_required !== true && !text(object(state.pending).id) && !data.removal && !busy;
  const creditReady = active && creditLaneAvailable && serviceAccount(current) && data.subscription_service === "chatgpt" && data.health === "ready" && isEntityId(connection) && isEntityId(generation) && state.recovery_required !== true && !state.pending && !data.removal && !busy;
  const count = text(inventory.available_count);
  const countValid = /^(?:0|[1-9][0-9]{0,18})$/.test(count) && BigInt(count) <= 9223372036854775807n;
  const details = inventory.credits;
  const credits = Array.isArray(details) && details.length <= 100 ? details.map(object) : undefined;
  const inventoryFresh = isEntityId(text(inventory.observation_id)) && Number.isFinite(Date.parse(text(inventory.observed_at))) && Date.now() - Date.parse(text(inventory.observed_at)) >= 0 && Date.now() - Date.parse(text(inventory.observed_at)) <= 300000;
  const inventoryAvailable = countValid && BigInt(count) > 0n && inventoryFresh;
  const selectable = (credits ?? []).filter((credit) => /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(text(credit.id)) && credit.reset_type === "codexRateLimits" && credit.status === "available" && (credit.expires_at == null || (timestampInstant(text(credit.expires_at)) ?? -Infinity) > Date.now()));
  const confirmCredit = (creditId: string, next: boolean, opener: HTMLElement, ordinal?: number) => {
    if (!creditReady || originalActive || !creditsSupported || !countValid || BigInt(count) <= 0n || !inventoryFresh) return;
    selectionOpener.current = opener; focusConfirmation.current = true;
    setConfirmation({ machine: serverCreditReady ? "" : ownerMachine, account: current, creditId, ordinal, next, inventoryId: text(inventory.observation_id), connection, generation });
  };
  const useAvailable = creditReady && !originalActive && inventoryAvailable && (details === null || selectable.length > 0);
  const creditReason = status.isPending ? copy("subscription-quota.loadingCredits") : status.error ? copy("subscription-quota.creditReadFailed") : !creditsSupported ? copy("subscription-quota.resetCreditConsumptionIsUnavailableOn_9c9ea3") : originalActive || busy ? copy("subscription-quota.creditPending") : !creditLaneAvailable || !creditReady ? copy("subscription-quota.creditOwnerUnavailable") : !countValid ? copy("subscription-quota.availableCreditCountUnknown_4e840a") : count === "0" ? copy("subscription-quota.noCredits") : !inventoryFresh ? copy("subscription-quota.creditInventoryStale") : details !== null && !credits ? copy("subscription-quota.creditDetailsMalformed") : details !== null && selectable.length === 0 ? copy("subscription-quota.noEligibleCredits") : "";
  const useCredit = (opener: HTMLElement) => {
    if (!useAvailable) return;
    if (details === null) confirmCredit("", true, opener);
    else { focusDetails.current = true; if (expanded) { focusDetails.current = false; detailsHeading.current?.focus(); } else setExpanded(true); }
  };
  const keepCredit = () => {
    setConfirmation(undefined);
    const opener = selectionOpener.current;
    if (opener?.isConnected && !opener.matches(":disabled") && !opener.closest("[hidden], [inert]")) opener.focus(); else sectionHeading.current?.focus();
  };
  const exactConfirmation = confirmation && confirmation.account.id === current.id && confirmation.account.revision === current.revision && confirmation.connection === connection && confirmation.generation === generation && confirmation.inventoryId === inventory.observation_id && confirmation.machine === (serverCreditReady ? "" : ownerMachine);
  const saveRecoveryNotifications = (enabled: boolean) => {
    let documentJson: Uint8Array;
    try { documentJson = accountPreferencesDocument(current, { recovery_notifications: enabled }); }
    catch { setPreferenceProblem(copy("subscription-quota.accountPreferencesCouldNotBeSavedReload_0d91a4")); return; }
    setPreferenceProblem("");
    void preferences.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson });
  };
  return <section aria-label={copy("subscription-quota.nativeQuotaAndResetCredits_8a07a3")}>
    <h3>{copy("subscription-quota.quota_6c105c")}</h3>
    {!quotaSupported ? <p role="status">{copy("subscription-quota.updateTheServerAndRunnerDevice_57363b")}</p> : <><p><LocalizedText id="subscription-quota.lastSuccessfulObservation_836238" components={{ s0: <><Timestamp value={text(state.quota_observed_at)} fallback={copy("subscription-quota.extra.ca1844969742")} /></>, s1: <>{state.quota_state === "failed" ? copy("subscription-quota.theLatestRefreshFailedTheLast_78f81e") : copy("subscription-quota.quotaIsObservedByTheOriginal_16d486")}</> }} /></p><SettingsActionButton icon={SettingsActionIcon.Refresh} type="button" disabled={!ready || originalActive} onClick={() => void observe.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, machineId: "", action: SubscriptionObservationAction.QUOTA, connectionId: connection, generationId: generation })}>{copy("subscription-quota.refreshQuota_3e8708")}</SettingsActionButton></>}
    {quotaSupported && !serverSupported ? <p role="status">{copy("subscription-quota.serverQuotaUnsupported")}</p> : null}
    <label className="checkbox"><input type="checkbox" checked={data.recovery_notifications === true} disabled={!active || !quotaSupported || busy} onChange={(event) => saveRecoveryNotifications(event.target.checked)} />{copy("subscription-quota.notifyMeOfObservedQuotaRecovery_b8f166")}</label>
    {preferenceProblem ? <p role="alert">{preferenceProblem}</p> : null}
    <section className="reset-credit-section" aria-labelledby={detailsHeadingId + "-section"}>
      <div className="reset-credit-summary">
        <h3 ref={sectionHeading} id={detailsHeadingId + "-section"} tabIndex={-1}>{copy("subscription-quota.resetCredits_321f63")}</h3>
        <span>{countValid ? copy("subscription-quota.availableResetCredits_d85d49", { v0: count }) : copy("subscription-quota.availableCreditCountUnknown_4e840a")}</span>
        <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" className="primary" disabled={!useAvailable} onClick={event => useCredit(event.currentTarget)}>{copy("subscription-quota.useCredit")}</SettingsActionButton>
      </div>
      {creditReason ? <p role="status" className="reset-credit-secondary">{creditReason}</p> : null}
      {creditsSupported && !isEntityId(ownerMachine) && !serverCreditsSupported ? <p role="status" className="reset-credit-secondary">{copy("subscription-quota.serverCreditsUnavailable")}</p> : null}
      <DisclosureButton density={DisclosureDensity.Settings} type="button" className="reset-credit-disclosure" aria-expanded={expanded} aria-controls={detailsId} onClick={() => setExpanded(value => !value)}>{copy(expanded ? "subscription-quota.hideDetails" : "subscription-quota.viewDetails")}</DisclosureButton>
      <DisclosureContent id={detailsId} hidden={!expanded}>
        <h4 ref={detailsHeading} id={detailsHeadingId} tabIndex={-1}>{copy("subscription-quota.creditDetails")}</h4>
        <p className="reset-credit-secondary">{details === null ? copy("subscription-quota.detailsUnavailable") : credits ? copy("subscription-quota.returnedDetailCount", { v0: credits.length }) : copy("subscription-quota.creditDetailsMalformed")}</p>
        <div className="reset-credit-rows">
          {credits?.map((credit, index) => <div className="reset-credit-row" key={text(credit.id) + ":" + index}>
            <div><strong>{copy("subscription-quota.creditNumber", { v0: index + 1 })}</strong><span className="reset-credit-secondary">{copy("subscription-quota.creditId", { v0: text(credit.id) || copy("subscription-quota.extra.ca1844969742") })}</span><span className="reset-credit-secondary">{copy(credit.status === "available" ? "subscription-quota.creditAvailable" : "subscription-quota.creditUnavailable")}</span>{timestampInstant(text(credit.expires_at)) !== undefined ? <span className="reset-credit-secondary">{copy("subscription-quota.creditExpiry")} <Timestamp value={text(credit.expires_at)} /></span> : null}</div>
            {selectable.includes(credit) ? <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={!creditReady || originalActive || !inventoryAvailable} onClick={event => confirmCredit(text(credit.id), false, event.currentTarget, index + 1)} aria-label={copy("subscription-quota.selectCreditName", { v0: index + 1 })}>{copy("subscription-quota.selectCredit")}</SettingsActionButton> : null}
          </div>)}
        </div>
      </DisclosureContent>
      {confirmation ? <section className="reset-credit-confirmation" aria-label={copy("subscription-quota.confirmResetCreditConsumption_1ce436")}>
        <h4 ref={confirmationHeading} tabIndex={-1}>{copy("subscription-quota.confirmResetCreditConsumption_1ce436")}</h4>
        <p>{copy("subscription-quota.confirmAccount", { v0: text(document(confirmation.account).alias) || copy("subscription-quota.extra.ca1844969742") })}</p>
        <p>{confirmation.next ? copy("subscription-quota.detailsUnavailableNext") : copy("subscription-quota.consumeResetCredit_7eb25b", { v0: confirmation.ordinal })}</p>
        {!confirmation.next ? <p className="reset-credit-secondary">{copy("subscription-quota.creditId", { v0: confirmation.creditId })}</p> : null}
        <p className="reset-credit-secondary">{copy("subscription-quota.consumptionWarning")}</p>
        {!exactConfirmation ? <p role="alert">{copy("subscription-quota.theAccountOrInventoryChangedRefresh_e2f6b9")}</p> : null}
        <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Confirm} type="button" className="primary" disabled={!creditReady || originalActive || !exactConfirmation || !inventoryAvailable} onClick={() => void observe.send({ mutation: { requestId: newRequestId(), id: confirmation.account.id, expectedRevision: confirmation.account.revision }, machineId: confirmation.machine, action: SubscriptionObservationAction.RESET_CREDIT, connectionId: confirmation.connection, generationId: confirmation.generation, creditsObservationId: confirmation.inventoryId, creditId: confirmation.creditId, nextCredit: confirmation.next, confirmed: true })}>{copy("subscription-quota.confirmCreditConsumption_251822")}</SettingsActionButton><SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" disabled={busy} onClick={keepCredit}>{copy("subscription-quota.keepCredit_53e67f")}</SettingsActionButton></div>
      </section> : null}
    </section>
    {serverSupported && text(serverObservation.error_code) === "unsupported" ? <p role="status">{copy("subscription-quota.serverNativeQuotaUnavailable")}</p> : null}
    {serverSupported && text(serverObservation.error_code) === "unavailable" ? <p role="status">{copy("subscription-quota.serverQuotaAuthenticationUnavailable")}</p> : null}
    {text(serverObservation.id) ? <p role="status">{statusLabel(text(serverObservation.phase))}</p> : null}
    {text(serverCredit.id) ? <p role="status">{statusLabel(text(serverCredit.phase))}{outcomeLabels[text(serverCredit.outcome)] ? copy("subscription-quota.message_2fa20b", { v0: outcomeLabels[text(serverCredit.outcome)] }) : ""}</p> : null}
    {text(observation.id) ? <p role="status">{text(observation.action)} · {statusLabel(text(observation.phase))}{outcomeLabels[text(observation.outcome)] ? copy("subscription-quota.message_2fa20b", { v0: outcomeLabels[text(observation.outcome)] }) : ""}</p> : null}
    {(observation.action === "reset-credit" || text(serverCredit.id)) && creditObservation.phase === "uncertain" ? <><p>{copy("subscription-quota.theOriginalConsumptionResultIsUncertain_11cf11")}</p><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!creditReady || !creditsSupported || reconcilingServer && serverCredit.cleanup_confirmed !== true} onClick={() => void reconcile.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, operationId: text(creditObservation.id), connectionId: connection, generationId: generation })}>{copy("subscription-quota.reconcileOriginalCreditOperation_2ca418")}</SettingsActionButton></> : null}
    <Problem error={status.error || observe.error || reconcile.error || preferences.error} />
    {observe.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={observe.busy} onClick={observe.retry}>{copy("subscription-quota.retryOriginalQuotaOrCreditRequest_071535")}</SettingsActionButton> : null}
    {reconcile.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={reconcile.busy} onClick={reconcile.retry}>{copy("subscription-quota.retryOriginalCreditReconciliationRequest_1dd094")}</SettingsActionButton> : null}
    {preferences.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={preferences.busy} onClick={preferences.retry}>{copy("subscription-quota.retryOriginalRecoveryPreference_fa3cb3")}</SettingsActionButton> : null}
  </section>;
}
