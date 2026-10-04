// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, SubscriptionObservationAction, SubscriptionQuery, SystemCapability, SystemQuery, EntityKind, isEntityId, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, text } from "./documents";
import { serviceAccount } from "./subscription-accounts";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

interface CreditConfirmation { account: Resource; creditId: string; next: boolean; inventoryId: string; connection: string; generation: string }
const outcomeLabels: Record<string, string> = { reset: "Reset credit consumed", alreadyRedeemed: "Original reset credit was already consumed", nothingToReset: "No current quota window needed a reset", noCredit: "No reset credit was available" };

export function quotaObservationMachine(state: Record<string, unknown>, preferred = ""): string {
  const lease = object(state.lease);
  return Object.keys(lease).length ? text(lease.action) === "execute" ? text(lease.machine_id) : "" : preferred || text(state.owner_machine_id);
}

export function quotaAccountAvailable(data: Record<string, unknown>): boolean {
  const state = object(data.subscription), observation = object(state.observation);
  return data.health === "ready" && isEntityId(text(object(data.connection).id)) && isEntityId(text(state.generation)) && !data.removal && state.recovery_required !== true && !state.pending && !["queued", "sending", "uncertain"].includes(text(observation.phase));
}

export function SubscriptionQuotaControls({ current, machine, active, accepted, busyChanged }: { current: Resource; machine: string; active: boolean; accepted: (resource: Resource) => void; busyChanged: (busy: boolean) => void }) {
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
  return <section aria-label="Native quota and reset credits">
    <h3>Quota</h3>
    {!quotaSupported ? <p role="status">Update the server and Runner Device to use native quota observations.</p> : <><p>Last successful observation: {text(state.quota_observed_at) || "Unavailable"}. {state.quota_state === "failed" ? "The latest refresh failed; the last successful values remain." : "Quota is observed by the original Codex owner every five minutes while connected."}</p><button type="button" disabled={!ready || originalActive} onClick={() => void observe.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, machineId: ownerMachine, action: SubscriptionObservationAction.QUOTA, connectionId: connection, generationId: generation })}>Refresh quota</button></>}
    <label className="checkbox"><input type="checkbox" checked={data.recovery_notifications === true} disabled={!active || !quotaSupported || busy} onChange={(event) => void preferences.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ ...data, recovery_notifications: event.target.checked }) })} />Notify me of observed quota recovery</label>
    <h3>Reset credits</h3>
    {!creditsSupported ? <p role="status">Reset credit consumption is unavailable on this server.</p> : <><p>{countValid ? `${count} available reset credits` : "Available credit count unknown"}{countValid && details === null ? "; individual credit details unavailable" : credits ? `; ${credits.length} returned details` : ""}.</p>{selectable.map((credit) => <button key={text(credit.id)} type="button" disabled={!ready || originalActive || !inventoryFresh} onClick={() => confirmCredit(text(credit.id), false)}>Review reset credit {text(credit.id)}</button>)}{countValid && details === null && BigInt(count) > 0n ? <button type="button" disabled={!ready || originalActive || !inventoryFresh} onClick={() => confirmCredit("", true)}>Review native next-credit selection</button> : null}</>}
    {confirmation ? <section aria-label="Confirm reset credit consumption"><p>{confirmation.next ? "Let Codex select its next available reset credit" : `Consume reset credit ${confirmation.creditId}`} for this account? This consumes a credit. Quota recovery is verified separately afterward.</p>{!exactConfirmation ? <p role="alert">The account or inventory changed. Refresh and confirm the current selection again.</p> : null}<div className="actions"><button type="button" disabled={!ready || originalActive || !exactConfirmation || !inventoryFresh} onClick={() => void observe.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, machineId: ownerMachine, action: SubscriptionObservationAction.RESET_CREDIT, connectionId: confirmation.connection, generationId: confirmation.generation, creditsObservationId: confirmation.inventoryId, creditId: confirmation.creditId, nextCredit: confirmation.next, confirmed: true })}>Confirm credit consumption</button><button type="button" disabled={busy} onClick={() => setConfirmation(undefined)}>Keep credit</button></div></section> : null}
    {text(observation.id) ? <p role="status">{text(observation.action)} · {text(observation.phase)}{outcomeLabels[text(observation.outcome)] ? ` · ${outcomeLabels[text(observation.outcome)]}` : ""}</p> : null}
    {observation.action === "reset-credit" && observation.phase === "uncertain" ? <><p>The original consumption result is uncertain. Reconciliation uses the same official operation key.</p><button type="button" disabled={!ready || !creditsSupported} onClick={() => void reconcile.send({ mutation: { requestId: newRequestId(), id: current.id, expectedRevision: current.revision }, operationId: text(observation.id), connectionId: connection, generationId: generation })}>Reconcile original credit operation</button></> : null}
    <Problem error={status.error || observe.error || reconcile.error || preferences.error} />
    {observe.uncertain ? <button type="button" disabled={observe.busy} onClick={observe.retry}>Retry original quota or credit request</button> : null}
    {reconcile.uncertain ? <button type="button" disabled={reconcile.busy} onClick={reconcile.retry}>Retry original credit reconciliation request</button> : null}
    {preferences.uncertain ? <button type="button" disabled={preferences.busy} onClick={preferences.retry}>Retry original recovery preference</button> : null}
  </section>;
}
