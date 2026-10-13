// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import {
  DisclosureButton,
  DisclosureContent,
  DisclosureDensity,
} from "./disclosure";
import { Timestamp } from "./timestamp-display";
import { timestampInstant } from "./timestamp-format";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import "./subscription-quota.css";
import { useQuery } from "@connectrpc/connect-query";
import {
  ConfigurationQuery,
  SubscriptionObservationAction,
  SubscriptionQuery,
  SystemCapability,
  SystemQuery,
  EntityKind,
  isEntityId,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { accountPreferencesDocument } from "./account-preferences";
import { serviceAccount } from "./subscription-accounts";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import {
  SettingsTaskDialog,
  SettingsTaskActions,
  SettingsDialogFocus,
  SettingsDialogSize,
} from "./settings-task";

interface CreditConfirmation {
  machine: string;
  account: Resource;
  creditId: string;
  ordinal?: number;
  next: boolean;
  inventoryId: string;
  connection: string;
  generation: string;
}
const outcomeLabels: Record<string, string> = {
  get reset() {
    return copy("subscription-quota.resetCreditConsumed_ac52a6");
  },
  get alreadyRedeemed() {
    return copy(
      "subscription-quota.originalResetCreditWasAlreadyConsumed_3d5beb",
    );
  },
  get nothingToReset() {
    return copy("subscription-quota.noCurrentQuotaWindowNeededA_972b84");
  },
  get noCredit() {
    return copy("subscription-quota.noResetCreditWasAvailable_648345");
  },
};

// The server admits quota and credit requests only when the account has no
// native lease or an Execute lease; login, refresh and logout leases deny them.
function serverLeaseAllows(state: Record<string, unknown>): boolean {
  const lease = object(state.lease);
  return !Object.keys(lease).length || text(lease.action) === "execute";
}

// System 101 consumes with the sealed generation's access token on the server.
// It needs no Runner and may coexist with an active Execute, so only the
// negotiated capability, a settled generation and a permitted lease gate it.
export function serverCreditAvailable(
  state: Record<string, unknown>,
  supported: boolean,
): boolean {
  return (
    supported && isEntityId(text(state.generation)) && serverLeaseAllows(state)
  );
}

export function serverQuotaAvailable(
  state: Record<string, unknown>,
  supported: boolean,
): boolean {
  return (
    supported && isEntityId(text(state.generation)) && serverLeaseAllows(state)
  );
}

export function quotaAccountAvailable(data: Record<string, unknown>): boolean {
  const state = object(data.subscription),
    observation = object(state.observation),
    serverQuota = object(state.server_quota),
    serverCredit = object(state.server_credit);
  return (
    data.health === "ready" &&
    isEntityId(text(object(data.connection).id)) &&
    isEntityId(text(state.generation)) &&
    !data.removal &&
    state.recovery_required !== true &&
    !state.pending &&
    !["queued", "sending", "uncertain"].includes(text(observation.phase)) &&
    !["queued", "sending", "uncertain"].includes(text(serverQuota.phase)) &&
    !["queued", "sending", "uncertain"].includes(text(serverCredit.phase))
  );
}

export function SubscriptionQuotaControls({
  current,
  active,
  accepted,
  busyChanged,
  footer,
  visible = true,
}: {
  current: Resource;
  active: boolean;
  accepted: (resource: Resource) => void;
  busyChanged: (busy: boolean) => void;
  footer?: (refreshQuota?: () => Promise<void>) => ReactNode;
  visible?: boolean;
}) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const workerQuotaSupported =
    status.data?.capabilities.includes(
      SystemCapability.SUBSCRIPTION_QUOTA_V1,
    ) === true;
  const serverSupported =
    status.data?.capabilities.includes(
      SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2,
    ) === true;
  const quotaSupported = workerQuotaSupported || serverSupported;
  const workerCreditsSupported =
    status.data?.capabilities.includes(
      SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1,
    ) === true;
  // New requests need System 101 and never select a Runner. Historical
  // capabilities only keep reconciling an already accepted operation.
  const serverCreditsSupported =
    status.data?.capabilities.includes(
      SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V2,
    ) === true;
  const historicalServerCreditsSupported =
    serverCreditsSupported ||
    status.data?.capabilities.includes(
      SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V1,
    ) === true;
  const creditsSupported = serverCreditsSupported;
  const data = document(current),
    state = object(data.subscription),
    connection = text(object(data.connection).id),
    generation = text(state.generation),
    observation = object(state.observation),
    inventory = object(state.reset_credits);
  const serverAvailable = serverQuotaAvailable(state, serverSupported);
  const serverObservation = object(state.server_quota),
    serverCredit = object(state.server_credit);
  const creditObservation =
    observation.action === "reset-credit" && observation.phase === "uncertain"
      ? observation
      : text(serverCredit.id)
        ? serverCredit
        : observation;
  const reconcilingServer = creditObservation === serverCredit;
  const serverCreditReady = serverCreditAvailable(
    state,
    serverCreditsSupported,
  );
  const creditLaneAvailable = serverCreditReady;
  // The original operation keeps its lane: a server credit reconciles with the
  // server capabilities, a Worker observation with the Worker capability.
  const reconcileLaneSupported = reconcilingServer
    ? historicalServerCreditsSupported
    : workerCreditsSupported;
  const [confirmation, setConfirmation] = useState<CreditConfirmation>();
  const [expanded, setExpanded] = useState(false);
  const [consentConfirmation, setConsentConfirmation] = useState<Resource>();
  const consentOpener = useRef<HTMLInputElement>(null);
  const sectionHeading = useRef<HTMLHeadingElement>(null),
    detailsHeading = useRef<HTMLHeadingElement>(null),
    confirmationHeading = useRef<HTMLHeadingElement>(null);
  const selectionOpener = useRef<HTMLElement | null>(null),
    focusDetails = useRef(false),
    focusConfirmation = useRef(false);
  const detailsId = useId(),
    detailsHeadingId = useId();
  useEffect(() => {
    if (!expanded || !focusDetails.current) return;
    focusDetails.current = false;
    const heading = detailsHeading.current,
      originalFocus = globalThis.document.activeElement;
    heading?.focus();
    // DisclosureContent removes its staged hidden state in a layout update.
    // Retry only this explicit Use intent after visibility settles, without
    // taking focus back from an intervening keyboard or pointer action.
    if (heading && globalThis.document.activeElement !== heading) {
      const frame = requestAnimationFrame(() => {
        if (globalThis.document.activeElement === originalFocus)
          heading.focus();
      });
      return () => cancelAnimationFrame(frame);
    }
  }, [expanded]);
  useEffect(() => {
    if (confirmation && focusConfirmation.current) {
      focusConfirmation.current = false;
      confirmationHeading.current?.focus();
    }
  }, [confirmation]);
  const [preferenceProblem, setPreferenceProblem] = useState("");
  const observe = useRetainedMutation(
    "subscription:observe:" + current.id,
    SubscriptionQuery.requestSubscriptionObservation,
    (result) => {
      if (result.account) accepted(result.account);
      setConfirmation(undefined);
    },
    (result, request) =>
      result.operationId === request.mutation?.requestId &&
      serviceAccount(
        result.account,
        current.id,
        undefined,
        request.mutation?.expectedRevision ?? 1n,
      ),
  );
  const reconcile = useRetainedMutation(
    "subscription:credit:reconcile:" + current.id,
    SubscriptionQuery.reconcileSubscriptionCredit,
    (result) => {
      if (result.account) accepted(result.account);
    },
    (result, request) =>
      serviceAccount(
        result.account,
        current.id,
        undefined,
        request.mutation?.expectedRevision ?? 1n,
      ),
  );
  const preferences = useRetainedMutation(
    "subscription:quota:preferences:" + current.id,
    ConfigurationQuery.saveConfiguration,
    (result) => {
      if (result.resource) accepted(result.resource);
    },
    (result, request) =>
      serviceAccount(
        result.resource,
        current.id,
        undefined,
        request.mutation?.expectedRevision ?? 1n,
      ),
  );
  const consent = useRetainedMutation(
    "subscription:automatic-credit:consent:" + current.id,
    SubscriptionQuery.setAutomaticResetCreditConsent,
    (result) => {
      if (result.account) accepted(result.account);
      setConsentConfirmation(undefined);
    },
    (result, request) =>
      serviceAccount(
        result.account,
        current.id,
        undefined,
        request.mutation?.expectedRevision ?? 1n,
      ),
  );
  const busy =
    consent.busy ||
    consent.uncertain ||
    observe.busy ||
    observe.uncertain ||
    reconcile.busy ||
    reconcile.uncertain ||
    preferences.busy ||
    preferences.uncertain;
  useEffect(() => {
    busyChanged(busy);
    return () => busyChanged(false);
  }, [busy, busyChanged]);
  const originalActive =
    ["queued", "sending", "uncertain"].includes(text(observation.phase)) ||
    ["queued", "sending", "uncertain"].includes(
      text(serverObservation.phase),
    ) ||
    ["queued", "sending", "uncertain"].includes(text(serverCredit.phase));
  const ready =
    active &&
    !status.error &&
    !status.isFetching &&
    quotaSupported &&
    serviceAccount(current) &&
    data.subscription_service === "chatgpt" &&
    quotaAccountAvailable(data) &&
    serverAvailable &&
    !busy &&
    !originalActive;
  // The enclosing Manage footer uses this original retained owner. Capture the
  // verified resource bindings before a concurrent status read changes the view.
  const refreshQuota = ready
    ? () =>
        observe.send({
          mutation: {
            requestId: newRequestId(),
            id: current.id,
            expectedRevision: current.revision,
          },
          machineId: "",
          action: SubscriptionObservationAction.QUOTA,
          connectionId: connection,
          generationId: generation,
        })
    : undefined;
  const creditAccountReady =
    active &&
    serviceAccount(current) &&
    data.subscription_service === "chatgpt" &&
    data.health === "ready" &&
    isEntityId(connection) &&
    isEntityId(generation) &&
    state.recovery_required !== true &&
    !state.pending &&
    !data.removal &&
    !busy;
  const creditReady = creditAccountReady && creditLaneAvailable;
  const count = text(inventory.available_count);
  const countValid =
    /^(?:0|[1-9][0-9]{0,18})$/.test(count) &&
    BigInt(count) <= 9223372036854775807n;
  const details = inventory.credits;
  const credits =
    Array.isArray(details) && details.length <= 100
      ? details.map(object)
      : undefined;
  const inventoryFresh =
    isEntityId(text(inventory.observation_id)) &&
    Number.isFinite(Date.parse(text(inventory.observed_at))) &&
    Date.now() - Date.parse(text(inventory.observed_at)) >= 0 &&
    Date.now() - Date.parse(text(inventory.observed_at)) <= 300000;
  const inventoryAvailable = countValid && BigInt(count) > 0n && inventoryFresh;
  const selectable = (credits ?? []).filter(
    (credit) =>
      /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(text(credit.id)) &&
      credit.reset_type === "codexRateLimits" &&
      credit.status === "available" &&
      (credit.expires_at == null ||
        (timestampInstant(text(credit.expires_at)) ?? -Infinity) > Date.now()),
  );
  const confirmCredit = (
    creditId: string,
    next: boolean,
    opener: HTMLElement,
    ordinal?: number,
  ) => {
    if (
      !creditReady ||
      originalActive ||
      !creditsSupported ||
      !countValid ||
      BigInt(count) <= 0n ||
      !inventoryFresh
    )
      return;
    selectionOpener.current = opener;
    focusConfirmation.current = true;
    setConfirmation({
      machine: "",
      account: current,
      creditId,
      ordinal,
      next,
      inventoryId: text(inventory.observation_id),
      connection,
      generation,
    });
  };
  const useAvailable =
    creditReady &&
    !originalActive &&
    inventoryAvailable &&
    (details === null || selectable.length > 0);
  const creditReason = status.isPending
    ? copy("subscription-quota.loadingCredits")
    : status.error
      ? copy("subscription-quota.creditReadFailed")
      : !creditsSupported
        ? workerCreditsSupported || historicalServerCreditsSupported
          ? copy("subscription-quota.serverCreditsUnavailable")
          : copy(
              "subscription-quota.resetCreditConsumptionIsUnavailableOn_9c9ea3",
            )
        : originalActive || busy
          ? copy("subscription-quota.creditPending")
          : !creditLaneAvailable || !creditReady
            ? copy("subscription-quota.creditOwnerUnavailable")
            : !countValid
              ? copy("subscription-quota.availableCreditCountUnknown_4e840a")
              : count === "0"
                ? copy("subscription-quota.noCredits")
                : !inventoryFresh
                  ? copy("subscription-quota.creditInventoryStale")
                  : details !== null && !credits
                    ? copy("subscription-quota.creditDetailsMalformed")
                    : details !== null && selectable.length === 0
                      ? copy("subscription-quota.noEligibleCredits")
                      : "";
  const useCredit = (opener: HTMLElement) => {
    if (!useAvailable) return;
    if (details === null) confirmCredit("", true, opener);
    else {
      focusDetails.current = true;
      if (expanded) {
        focusDetails.current = false;
        detailsHeading.current?.focus();
      } else setExpanded(true);
    }
  };
  const keepCredit = () => {
    setConfirmation(undefined);
    const opener = selectionOpener.current;
    if (
      opener?.isConnected &&
      !opener.matches(":disabled") &&
      !opener.closest("[hidden], [inert]")
    )
      opener.focus();
    else sectionHeading.current?.focus();
  };
  const exactConfirmation =
    confirmation &&
    confirmation.account.id === current.id &&
    confirmation.account.revision === current.revision &&
    confirmation.connection === connection &&
    confirmation.generation === generation &&
    confirmation.inventoryId === inventory.observation_id &&
    confirmation.machine === "";
  const saveRecoveryNotifications = (enabled: boolean) => {
    let documentJson: Uint8Array;
    try {
      documentJson = accountPreferencesDocument(current, {
        recovery_notifications: enabled,
      });
    } catch {
      setPreferenceProblem(
        copy(
          "subscription-quota.accountPreferencesCouldNotBeSavedReload_0d91a4",
        ),
      );
      return;
    }
    setPreferenceProblem("");
    void preferences.send({
      mutation: {
        requestId: newRequestId(),
        id: current.id,
        expectedRevision: current.revision,
      },
      kind: EntityKind.ACCOUNT,
      schemaVersion: 2,
      documentJson,
    });
  };
  const automaticSupported =
    status.data?.capabilities.includes(
      SystemCapability.AUTOMATIC_RESET_CREDIT_CONSENT_V1,
    ) === true;
  const automaticConsent = object(state.automatic_credit_consent);
  const automaticEnabled =
    automaticConsent.connection_id === connection &&
    automaticConsent.generation === generation;
  // Revocation fences future admissions while the original operation settles.
  // Its enabled/sending/uncertain state cannot trap standing spending consent.
  const automaticDisableReady =
    active &&
    automaticSupported &&
    serviceAccount(current) &&
    data.subscription_service === "chatgpt" &&
    data.enabled !== false &&
    data.health === "ready" &&
    isEntityId(connection) &&
    isEntityId(generation) &&
    state.recovery_required !== true &&
    !state.pending &&
    !data.removal &&
    !consent.busy &&
    !consent.uncertain;
  const automaticReady =
    active &&
    automaticSupported &&
    serviceAccount(current) &&
    data.subscription_service === "chatgpt" &&
    quotaAccountAvailable(data) &&
    !busy;
  const sendConsent = (enabled: boolean, original = current) => {
    const saved = document(original),
      savedState = object(saved.subscription);
    void consent.send({
      mutation: {
        requestId: newRequestId(),
        id: original.id,
        expectedRevision: original.revision,
      },
      connectionId: text(object(saved.connection).id),
      generationId: text(savedState.generation),
      enabled,
      confirmed: enabled,
    });
  };
  const closeConsent = () => {
    setConsentConfirmation(undefined);
    queueMicrotask(() => consentOpener.current?.focus());
  };
  if (consentConfirmation)
    return (
      <SettingsTaskDialog
        title={copy("subscription-quota.automaticConfirmTitle")}
        size={SettingsDialogSize.Form}
        focus={SettingsDialogFocus.Heading}
        close={closeConsent}
        onDismiss={() => setConsentConfirmation(undefined)}
      >
        <div className="reset-credit-consent">
          <dl className="reset-credit-account">
            <dt>{copy("subscription-quota.accountLabel")}</dt>
            <dd>
              {text(document(consentConfirmation).alias) ||
                copy("subscription-quota.extra.ca1844969742")}
            </dd>
          </dl>
          <p>{copy("subscription-quota.automaticConfirmBody")}</p>
          <ul className="reset-credit-rules">
            <li>{copy("subscription-quota.automaticEarliestExpiry")}</li>
            <li>{copy("subscription-quota.automaticNativeNext")}</li>
            <li>{copy("subscription-quota.automaticAfterClose")}</li>
          </ul>
          <div className="reset-credit-note reset-credit-secondary">
            <p>{copy("subscription-quota.automaticConsentLifetime")}</p>
            <p>{copy("subscription-quota.automaticSession")}</p>
          </div>
          <p className="reset-credit-warning">
            {copy("subscription-quota.automaticRecoveryWarning")}
          </p>
        </div>
        {consentConfirmation.revision !== current.revision ||
        consentConfirmation.id !== current.id ? (
          <p role="alert">{copy("subscription-quota.automaticChanged")}</p>
        ) : null}
        <Problem error={consent.error} />
        <SettingsTaskActions>
          <SettingsActionButton
            icon={SettingsActionIcon.Cancel}
            type="button"
            disabled={consent.busy}
            onClick={closeConsent}
          >
            {copy("subscription-quota.automaticCancel")}
          </SettingsActionButton>
          <SettingsActionButton
            icon={SettingsActionIcon.Confirm}
            type="button"
            className="primary"
            disabled={
              !automaticReady ||
              consentConfirmation.revision !== current.revision ||
              consentConfirmation.id !== current.id
            }
            onClick={() => sendConsent(true, consentConfirmation)}
          >
            {copy("subscription-quota.automaticEnable")}
          </SettingsActionButton>
        </SettingsTaskActions>
        {consent.uncertain ? (
          <SettingsActionButton
            icon={SettingsActionIcon.Retry}
            type="button"
            disabled={consent.busy}
            onClick={consent.retry}
          >
            {copy("subscription-quota.automaticRetry")}
          </SettingsActionButton>
        ) : null}
      </SettingsTaskDialog>
    );
  if (!visible) return <>{footer?.()}</>;
  return (
    <>
      <section
        aria-label={copy(
          "subscription-quota.nativeQuotaAndResetCredits_8a07a3",
        )}
      >
        <h3>{copy("subscription-quota.quota_6c105c")}</h3>
        {!quotaSupported ? (
          <p role="status">
            {copy("subscription-quota.updateTheServerAndRunnerDevice_57363b")}
          </p>
        ) : (
          <>
            <p>
              <LocalizedText
                id="subscription-quota.lastSuccessfulObservation_836238"
                components={{
                  s0: (
                    <>
                      <Timestamp
                        value={text(state.quota_observed_at)}
                        fallback={copy("subscription-quota.extra.ca1844969742")}
                      />
                    </>
                  ),
                  s1: (
                    <>
                      {state.quota_state === "failed"
                        ? copy(
                            "subscription-quota.theLatestRefreshFailedTheLast_78f81e",
                          )
                        : copy(
                            "subscription-quota.quotaIsObservedByTheOriginal_16d486",
                          )}
                    </>
                  ),
                }}
              />
            </p>
          </>
        )}
        {quotaSupported && !serverSupported ? (
          <p role="status">
            {copy("subscription-quota.serverQuotaUnsupported")}
          </p>
        ) : null}
        <label className="checkbox">
          <input
            type="checkbox"
            checked={data.recovery_notifications === true}
            disabled={!active || !quotaSupported || busy}
            onChange={(event) =>
              saveRecoveryNotifications(event.target.checked)
            }
          />
          {copy("subscription-quota.notifyMeOfObservedQuotaRecovery_b8f166")}
        </label>
        {preferenceProblem ? <p role="alert">{preferenceProblem}</p> : null}
        <section
          className="reset-credit-section"
          aria-labelledby={detailsHeadingId + "-section"}
        >
          <h3
            ref={sectionHeading}
            id={detailsHeadingId + "-section"}
            tabIndex={-1}
          >
            {copy("subscription-quota.resetCredits_321f63")}
          </h3>
          <div className="reset-credit-summary">
            <strong>
              {countValid
                ? copy("subscription-quota.availableResetCredits_d85d49", {
                    v0: count,
                  })
                : copy("subscription-quota.availableCreditCountUnknown_4e840a")}
            </strong>
            <SettingsActionButton
              icon={SettingsActionIcon.Inspect}
              type="button"
              className="primary"
              disabled={!useAvailable}
              onClick={(event) => useCredit(event.currentTarget)}
            >
              {copy("subscription-quota.useCredit")}
            </SettingsActionButton>
          </div>
          <p className="reset-credit-secondary">
            {copy("subscription-quota.summaryRecoveryWarning")}
          </p>
          {creditReason ? (
            <p role="status" className="reset-credit-secondary">
              {creditReason}
            </p>
          ) : null}
          <div className="reset-credit-setting">
            <label className="checkbox reset-credit-automatic">
              <span>{copy("subscription-quota.automaticLabel")}</span>
              <input
                ref={consentOpener}
                type="checkbox"
                checked={automaticEnabled}
                disabled={
                  automaticEnabled ? !automaticDisableReady : !automaticReady
                }
                onChange={(event) =>
                  event.target.checked
                    ? setConsentConfirmation(current)
                    : sendConsent(false)
                }
              />
            </label>
            <p>{copy("subscription-quota.automaticDescription")}</p>
            <p className="reset-credit-secondary">
              {copy("subscription-quota.automaticLifetime")}
            </p>
            {!automaticSupported ? (
              <p role="status" className="reset-credit-secondary">
                {copy("subscription-quota.automaticUnsupported")}
              </p>
            ) : null}
            {text(object(state.automatic_credit_episode).id) ? (
              <p role="status" className="reset-credit-secondary">
                {copy("subscription-quota.automaticEpisode")}
              </p>
            ) : null}
          </div>
          <DisclosureButton
            density={DisclosureDensity.Settings}
            type="button"
            className="reset-credit-disclosure"
            aria-expanded={expanded}
            aria-controls={detailsId}
            onClick={() => setExpanded((value) => !value)}
          >
            {copy(
              expanded
                ? "subscription-quota.hideDetails"
                : "subscription-quota.viewDetails",
            )}
          </DisclosureButton>
          <DisclosureContent id={detailsId} hidden={!expanded}>
            <h4 ref={detailsHeading} id={detailsHeadingId} tabIndex={-1}>
              {copy("subscription-quota.creditDetails")}
            </h4>
            <p className="reset-credit-secondary">
              {details === null
                ? copy("subscription-quota.detailsUnavailable")
                : credits
                  ? copy("subscription-quota.returnedDetailCount", {
                      v0: credits.length,
                    })
                  : copy("subscription-quota.creditDetailsMalformed")}
            </p>
            <div className="reset-credit-rows">
              {credits?.map((credit, index) => (
                <div
                  className="reset-credit-row"
                  key={text(credit.id) + ":" + index}
                >
                  <div>
                    <strong>
                      {copy("subscription-quota.creditNumber", {
                        v0: index + 1,
                      })}
                    </strong>
                    <span className="reset-credit-availability">
                      {copy(
                        credit.status === "available"
                          ? "subscription-quota.creditAvailable"
                          : "subscription-quota.creditUnavailable",
                      )}
                    </span>
                    {timestampInstant(text(credit.expires_at)) !== undefined ? (
                      <span className="reset-credit-secondary">
                        {copy("subscription-quota.creditExpiry")}{" "}
                        <Timestamp value={text(credit.expires_at)} />
                      </span>
                    ) : null}
                    <span className="reset-credit-secondary">
                      {copy("subscription-quota.creditId", {
                        v0:
                          text(credit.id) ||
                          copy("subscription-quota.extra.ca1844969742"),
                      })}
                    </span>
                  </div>
                  {selectable.includes(credit) ? (
                    <SettingsActionButton
                      icon={SettingsActionIcon.Inspect}
                      type="button"
                      disabled={
                        !creditReady || originalActive || !inventoryAvailable
                      }
                      onClick={(event) =>
                        confirmCredit(
                          text(credit.id),
                          false,
                          event.currentTarget,
                          index + 1,
                        )
                      }
                      aria-label={copy("subscription-quota.selectCreditName", {
                        v0: index + 1,
                      })}
                    >
                      {copy("subscription-quota.selectCredit")}
                    </SettingsActionButton>
                  ) : null}
                </div>
              ))}
            </div>
          </DisclosureContent>
          {confirmation ? (
            <section
              className="reset-credit-confirmation"
              aria-label={copy(
                "subscription-quota.confirmResetCreditConsumption_1ce436",
              )}
            >
              <h4 ref={confirmationHeading} tabIndex={-1}>
                {copy(
                  "subscription-quota.confirmResetCreditConsumption_1ce436",
                )}
              </h4>
              <dl className="reset-credit-account">
                <dt>{copy("subscription-quota.accountLabel")}</dt>
                <dd>
                  {text(document(confirmation.account).alias) ||
                    copy("subscription-quota.extra.ca1844969742")}
                </dd>
              </dl>
              <p className="reset-credit-secondary">
                {copy("subscription-quota.selectedCreditLabel")}
              </p>
              <p>
                <strong>
                  {confirmation.next
                    ? copy("subscription-quota.detailsUnavailableNext")
                    : copy("subscription-quota.creditNumber", {
                        v0: confirmation.ordinal,
                      })}
                </strong>
              </p>
              {!confirmation.next ? (
                <p className="reset-credit-secondary">
                  {copy("subscription-quota.creditId", {
                    v0: confirmation.creditId,
                  })}
                </p>
              ) : null}
              <div className="reset-credit-warning">
                <p>{copy("subscription-quota.consumptionWarning")}</p>
                <p>{copy("subscription-quota.quotaRecoveryWarning")}</p>
              </div>
              {!exactConfirmation ? (
                <p role="alert">
                  {copy(
                    "subscription-quota.theAccountOrInventoryChangedRefresh_e2f6b9",
                  )}
                </p>
              ) : null}
              <div className="actions">
                <SettingsActionButton
                  icon={SettingsActionIcon.Cancel}
                  type="button"
                  disabled={busy}
                  onClick={keepCredit}
                >
                  {copy("subscription-quota.keepCredit_53e67f")}
                </SettingsActionButton>
                <SettingsActionButton
                  icon={SettingsActionIcon.Confirm}
                  type="button"
                  className="primary"
                  disabled={
                    !creditReady ||
                    originalActive ||
                    !exactConfirmation ||
                    !inventoryAvailable
                  }
                  onClick={() =>
                    void observe.send({
                      mutation: {
                        requestId: newRequestId(),
                        id: confirmation.account.id,
                        expectedRevision: confirmation.account.revision,
                      },
                      machineId: confirmation.machine,
                      action: SubscriptionObservationAction.RESET_CREDIT,
                      connectionId: confirmation.connection,
                      generationId: confirmation.generation,
                      creditsObservationId: confirmation.inventoryId,
                      creditId: confirmation.creditId,
                      nextCredit: confirmation.next,
                      confirmed: true,
                    })
                  }
                >
                  {copy("subscription-quota.confirmCreditConsumption_251822")}
                </SettingsActionButton>
              </div>
            </section>
          ) : null}
        </section>
        {text(serverObservation.id) ||
        (serverSupported &&
          ["unsupported", "unavailable"].includes(
            text(serverObservation.error_code),
          )) ||
        text(serverCredit.id) ||
        text(observation.id) ||
        status.error ||
        consent.error ||
        observe.error ||
        reconcile.error ||
        preferences.error ||
        observe.uncertain ||
        reconcile.uncertain ||
        consent.uncertain ||
        preferences.uncertain ? (
          <section
            className="reset-credit-results"
            aria-label={copy("subscription-quota.resultsTitle")}
          >
            <h4>{copy("subscription-quota.resultsTitle")}</h4>
            {serverSupported &&
            text(serverObservation.error_code) === "unsupported" ? (
              <p role="status">
                {copy("subscription-quota.serverNativeQuotaUnavailable")}
              </p>
            ) : null}
            {serverSupported &&
            text(serverObservation.error_code) === "unavailable" ? (
              <p role="status">
                {copy(
                  "subscription-quota.serverQuotaAuthenticationUnavailable",
                )}
              </p>
            ) : null}
            {text(serverObservation.id) ? (
              <p role="status">{statusLabel(text(serverObservation.phase))}</p>
            ) : null}
            {text(serverCredit.id) ? (
              <p role="status">
                {statusLabel(text(serverCredit.phase))}
                {outcomeLabels[text(serverCredit.outcome)]
                  ? copy("subscription-quota.message_2fa20b", {
                      v0: outcomeLabels[text(serverCredit.outcome)],
                    })
                  : ""}
              </p>
            ) : null}
            {text(observation.id) ? (
              <p role="status">
                {text(observation.action)} ·{" "}
                {statusLabel(text(observation.phase))}
                {outcomeLabels[text(observation.outcome)]
                  ? copy("subscription-quota.message_2fa20b", {
                      v0: outcomeLabels[text(observation.outcome)],
                    })
                  : ""}
              </p>
            ) : null}
            {(observation.action === "reset-credit" || text(serverCredit.id)) &&
            creditObservation.phase === "uncertain" ? (
              <>
                <div className="reset-credit-warning" role="status">
                  <h4>{copy("subscription-quota.uncertainResultTitle")}</h4>
                  <p>
                    {copy(
                      "subscription-quota.theOriginalConsumptionResultIsUncertain_11cf11",
                    )}
                  </p>
                </div>
                {reconcilingServer &&
                serverCredit.cleanup_confirmed !== true ? (
                  <p className="reset-credit-secondary">
                    {copy("subscription-quota.cleanupBeforeReconciliation")}
                  </p>
                ) : null}
                <SettingsActionButton
                  icon={SettingsActionIcon.Retry}
                  type="button"
                  disabled={
                    !creditAccountReady ||
                    !reconcileLaneSupported ||
                    (reconcilingServer &&
                      serverCredit.cleanup_confirmed !== true)
                  }
                  onClick={() =>
                    void reconcile.send({
                      mutation: {
                        requestId: newRequestId(),
                        id: current.id,
                        expectedRevision: current.revision,
                      },
                      operationId: text(creditObservation.id),
                      connectionId: connection,
                      // A server credit follows its retained captured generation
                      // across Execute rotations; it is never the live one.
                      generationId: reconcilingServer
                        ? text(serverCredit.generation) || generation
                        : generation,
                    })
                  }
                >
                  {copy(
                    "subscription-quota.reconcileOriginalCreditOperation_2ca418",
                  )}
                </SettingsActionButton>
              </>
            ) : null}
            <Problem
              error={
                status.error ||
                consent.error ||
                observe.error ||
                reconcile.error ||
                preferences.error
              }
            />
            {observe.uncertain ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Retry}
                type="button"
                disabled={observe.busy}
                onClick={observe.retry}
              >
                {copy(
                  "subscription-quota.retryOriginalQuotaOrCreditRequest_071535",
                )}
              </SettingsActionButton>
            ) : null}
            {reconcile.uncertain ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Retry}
                type="button"
                disabled={reconcile.busy}
                onClick={reconcile.retry}
              >
                {copy(
                  "subscription-quota.retryOriginalCreditReconciliationRequest_1dd094",
                )}
              </SettingsActionButton>
            ) : null}
            {consent.uncertain ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Retry}
                type="button"
                disabled={consent.busy}
                onClick={consent.retry}
              >
                {copy("subscription-quota.automaticRetry")}
              </SettingsActionButton>
            ) : null}
            {preferences.uncertain ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Retry}
                type="button"
                disabled={preferences.busy}
                onClick={preferences.retry}
              >
                {copy(
                  "subscription-quota.retryOriginalRecoveryPreference_fa3cb3",
                )}
              </SettingsActionButton>
            ) : null}
            {observation.action === "reset-credit" || text(serverCredit.id) ? (
              <p className="reset-credit-secondary">
                {copy("subscription-quota.resultRecoveryWarning")}
              </p>
            ) : null}
          </section>
        ) : null}
      </section>
      {footer?.(refreshQuota)}
    </>
  );
}
