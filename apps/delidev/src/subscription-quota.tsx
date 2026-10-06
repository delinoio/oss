import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, SubscriptionObservationAction, SubscriptionQuery, SystemCapability, SystemQuery, EntityKind, isEntityId, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, text } from "./documents";
import { serviceAccount } from "./subscription-accounts";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

interface CreditConfirmation { account: Resource; creditId: string; next: boolean; inventoryId: string; connection: string; generation: string }
const outcomeLabels: Record<string, string> = { get reset() { return copy("subscription-quota.resetCreditConsumed_ac52a6"); }, get alreadyRedeemed() { return copy("subscription-quota.originalResetCreditWasAlreadyConsumed_3d5beb"); }, get nothingToReset() { return copy("subscription-quota.noCurrentQuotaWindowNeededA_972b84"); }, get noCredit() { return copy("subscription-quota.noResetCreditWasAvailable_648345"); } };

export function quotaObservationMachine(state: Record<string, unknown>, preferred = ""): string {
  const lease = object(state.lease);
  return Object.keys(lease).length ? text(lease.action) === "execute" ? text(lease.machine_id) : "" : preferred || text(state.owner_machine_id);
}

export function quotaAccountAvailable(data: Record<string, unknown>): boolean {
  const state = object(data.subscription), observation = object(state.observation);
  return data.health === "ready" && isEntityId(text(object(data.connection).id)) && isEntityId(text(state.generation)) && !data.removal && state.recovery_required !== true && !state.pending && !["queued", "sending", "uncertain"].includes(text(observation.phase));
}

export function SubscriptionQuotaControls({ current, machine, active, accepted, busyChanged }: { current: Resource; machine: string; active: boolean; accepted: (resource: Resource) => void; busyChanged: (busy: boolean) => void }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const quotaSupported = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_QUOTA_V1) === true;
  const creditsSupported = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1) === true;
  const data = document(current), state = object(data.subscription), connection = text(object(data.connection).id), generation = text(state.generation), observation = object(state.observation), inventory = object(state.reset_credits);
  const ownerMachine = quotaObservationMachine(state, machine);
  const [confirmation, setConfirmation] = useState<CreditConfirmation>();
  const observe = useRetainedMutation("subscription:observe:" + current.id, SubscriptionQuery.requestSubscriptionObservation, (result) => { if (result.account) accepted(result.account); setConfirmation(undefined); }, (result, request) => result.operationId === request.mutation?.requestId && serviceAccount(result.account, current.id, undefined, request.mutation?.expectedRevision ?? 1n));
  const reconcile = useRetainedMutation("subscription:credit:reconcile:" + current.id, SubscriptionQuery.reconcileSubscriptionCredit, (result) => { if (result.account) accepted(result.account); }, (result, request) => serviceAccount(result.account, current.id, undefined, request.mutation?.expectedRevision ?? 1n));
  const preferences = useRetainedMutation("subscription:quota:preferences:" + current.id, ConfigurationQuery.saveConfiguration, (result) => { if (result.resource) accepted(result.resource); }, (result, request) => serviceAccount(result.resource, current.id, undefined, request.mutation?.expectedRevision ?? 1n));
  const busy = observe.busy || observe.uncertain || reconcile.busy || reconcile.uncertain || preferences.busy || preferences.uncertain;
  useEffect(() => { busyChanged(busy); return () => busyChanged(false); }, [busy, busyChanged]);
  const originalActive = ["queued", "sending", "uncertain"].includes(text(observation.phase));
  const ready = active && quotaSupported && serviceAccount(current) && data.subscription_service === "chatgpt" && data.health === "ready" && isEntityId(connection) && isEntityId(generation) && isEntityId(ownerMachine) && state.recovery_required !== true && !text(object(state.pending).id) && !data.removal && !busy;
  const count = text(inventory.available_count);
  const countValid = /^(?:0|[1-9][0-9]{0,18})$/.test(count) && BigInt(count) <= 9223372036854775807n;
  const details = inventory.credits;
  const credits = Array.isArray(details) && details.length <= 100 ? details.map(object) : undefined;
  const inventoryFresh = isEntityId(text(inventory.observation_id)) && Number.isFinite(Date.parse(text(inventory.observed_at))) && Date.now() - Date.parse(text(inventory.observed_at)) >= 0 && Date.now() - Date.parse(text(inventory.observed_at)) <= 300000;
  const selectable = (credits ?? []).filter((credit) => /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(text(credit.id)) && credit.reset_type === "codexRateLimits" && credit.status === "available" && (!credit.expires_at || Date.parse(text(credit.expires_at)) > Date.now()));
  const confirmCredit = (creditId: string, next: boolean) => {
    if (!ready || originalActive || !creditsSupported || !countValid || BigInt(count) <= 0n || !inventoryFresh) return;
    setConfirmation({ account: current, creditId, next, inventoryId: text(inventory.observation_id), connection, generation });
  };
  const exactConfirmation = confirmation && confirmation.account.revision === current.revision && confirmation.connection === connection && confirmation.generation === generation && confirmation.inventoryId === inventory.observation_id;
  return <section aria-label={copy("subscription-quota.nativeQuotaAndResetCredits_8a07a3")}>
    <h3>{copy("subscription-quota.quota_6c105c")}</h3>
    {!quotaSupported ? <p role="status">{copy("subscription-quota.updateTheServerAndRunnerDevice_57363b")}</p> : <><p><LocalizedText id="subscription-quota.lastSuccessfulObservation_836238" components={{ s0: <>{text(state.quota_observed_at) || "Unavailable"}</>, s1: <>{state.quota_state === "failed" ? copy("subscription-quota.theLatestRefreshFailedTheLast_78f81e") : copy("subscription-quota.quotaIsObservedByTheOriginal_16d486")}</> }} /></p><button type="button" disabled={!ready || originalActive} onClick={() => void observe.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, machineId: ownerMachine, action: SubscriptionObservationAction.QUOTA, connectionId: connection, generationId: generation })}>{copy("subscription-quota.refreshQuota_3e8708")}</button></>}
    <label className="checkbox"><input type="checkbox" checked={data.recovery_notifications === true} disabled={!active || !quotaSupported || busy} onChange={(event) => void preferences.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ ...data, recovery_notifications: event.target.checked }) })} />{copy("subscription-quota.notifyMeOfObservedQuotaRecovery_b8f166")}</label>
    <h3>{copy("subscription-quota.resetCredits_321f63")}</h3>
    {!creditsSupported ? <p role="status">{copy("subscription-quota.resetCreditConsumptionIsUnavailableOn_9c9ea3")}</p> : <><p>{countValid ? copy("subscription-quota.availableResetCredits_d85d49", { v0: count }) : copy("subscription-quota.availableCreditCountUnknown_4e840a")}{countValid && details === null ? copy("subscription-quota.individualCreditDetailsUnavailable_50e212") : credits ? copy("subscription-quota.returnedDetails_0ebcde", { v0: credits.length }) : ""}.</p>{selectable.map((credit) => <button key={text(credit.id)} type="button" disabled={!ready || originalActive || !inventoryFresh} onClick={() => confirmCredit(text(credit.id), false)}><LocalizedText id="subscription-quota.reviewResetCredit_f3c5d5" components={{ s0: <>{text(credit.id)}</> }} /></button>)}{countValid && details === null && BigInt(count) > 0n ? <button type="button" disabled={!ready || originalActive || !inventoryFresh} onClick={() => confirmCredit("", true)}>{copy("subscription-quota.reviewNativeNextCreditSelection_778df5")}</button> : null}</>}
    {confirmation ? <section aria-label={copy("subscription-quota.confirmResetCreditConsumption_1ce436")}><p><LocalizedText id="subscription-quota.forThisAccountThisConsumesA_488945" components={{ s0: <>{confirmation.next ? copy("subscription-quota.letCodexSelectItsNextAvailable_be8c4e") : copy("subscription-quota.consumeResetCredit_7eb25b", { v0: confirmation.creditId })}</> }} /></p>{!exactConfirmation ? <p role="alert">{copy("subscription-quota.theAccountOrInventoryChangedRefresh_e2f6b9")}</p> : null}<div className="actions"><button type="button" disabled={!ready || originalActive || !exactConfirmation || !inventoryFresh} onClick={() => void observe.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, machineId: ownerMachine, action: SubscriptionObservationAction.RESET_CREDIT, connectionId: confirmation.connection, generationId: confirmation.generation, creditsObservationId: confirmation.inventoryId, creditId: confirmation.creditId, nextCredit: confirmation.next, confirmed: true })}>{copy("subscription-quota.confirmCreditConsumption_251822")}</button><button type="button" disabled={busy} onClick={() => setConfirmation(undefined)}>{copy("subscription-quota.keepCredit_53e67f")}</button></div></section> : null}
    {text(observation.id) ? <p role="status">{text(observation.action)} · {text(observation.phase)}{outcomeLabels[text(observation.outcome)] ? copy("subscription-quota.message_2fa20b", { v0: outcomeLabels[text(observation.outcome)] }) : ""}</p> : null}
    {observation.action === "reset-credit" && observation.phase === "uncertain" ? <><p>{copy("subscription-quota.theOriginalConsumptionResultIsUncertain_11cf11")}</p><button type="button" disabled={!ready || !creditsSupported} onClick={() => void reconcile.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, operationId: text(observation.id), connectionId: connection, generationId: generation })}>{copy("subscription-quota.reconcileOriginalCreditOperation_2ca418")}</button></> : null}
    <Problem error={status.error || observe.error || reconcile.error || preferences.error} />
    {observe.uncertain ? <button type="button" disabled={observe.busy} onClick={observe.retry}>{copy("subscription-quota.retryOriginalQuotaOrCreditRequest_071535")}</button> : null}
    {reconcile.uncertain ? <button type="button" disabled={reconcile.busy} onClick={reconcile.retry}>{copy("subscription-quota.retryOriginalCreditReconciliationRequest_1dd094")}</button> : null}
    {preferences.uncertain ? <button type="button" disabled={preferences.busy} onClick={preferences.retry}>{copy("subscription-quota.retryOriginalRecoveryPreference_fa3cb3")}</button> : null}
  </section>;
}
