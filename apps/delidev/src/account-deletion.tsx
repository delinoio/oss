// SPDX-License-Identifier: Apache-2.0
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  BrowserQuery, ConfigurationService, DeleteConfigurationRequestSchema, EntityKind, FailureCode,
  RequestSubscriptionRequestSchema, ResourceSchema, ResourceService, SubscriptionAction, SubscriptionLoginState,
  SubscriptionService, SubscriptionServiceId, SystemCapability, SystemService, clientFailure, isEntityId, newRequestId,
  type ClientFailure, type DeleteConfigurationRequest, type RequestSubscriptionRequest, type Resource,
} from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { useSettingsOpening } from "./settings-lifetime";
import { serviceAccount } from "./subscription-resource";
import { safeDiagnostic } from "./subscription-onboarding";
import { Failure, Problem } from "./ui";
import "./account-deletion.css";

enum Stage { Confirmation, Checking, Logout, Deleting, Paused, Deleted }
enum Retry { Logout, Delete, Progress }
interface Attempt {
  confirmed: Resource;
  minimumRevision: bigint;
  connection: string;
  operation: string;
  logout?: RequestSubscriptionRequest;
  deletion?: DeleteConfigurationRequest;
  retry?: Retry;
  observing: boolean;
  busy: boolean;
  disposed: boolean;
}
interface View { stage: Stage; failure?: ClientFailure }
const preferences = ["alias", "type", "subscription_service", "enabled", "exclude_automatic", "recovery_notifications"] as const;
const uncertain = (error: unknown) => [FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(clientFailure(error).code);
function unchanged(resource: Resource | undefined, attempt: Attempt): resource is Resource {
  if (!serviceAccount(resource, attempt.confirmed.id, SubscriptionServiceId.ChatGPT, attempt.minimumRevision)) return false;
  const current = document(resource), confirmed = document(attempt.confirmed);
  return preferences.every((key) => current[key] === confirmed[key]);
}
function cleared(resource: Resource) {
  const data = document(resource), state = object(data.subscription);
  return data.health === "disconnected" && !data.connection && !data.removal && !state.pending && !state.lease &&
    !state.recovery_required && !state.generation && !object(state.server_operation).native_started;
}

export function AccountDeletionResult({ initial, active, deleted }: { initial: Resource; active: boolean; deleted: () => void }) {
  useLocale();
  const isApi = document(initial).type === "api";
  const isChatGPT = serviceAccount(initial, undefined, SubscriptionServiceId.ChatGPT);
  const cleanup = useQuery(BrowserQuery.getAccountBrowserCleanup, { accountId: initial.id }, { enabled: active, retry: false });
  const actions = <><button disabled={!active || cleanup.isFetching} onClick={() => void cleanup.refetch()}>{copy("account-deletion.refreshCleanupStatus_fb7773")}</button><button disabled={!active} onClick={deleted}>{isApi ? copy("account-deletion.returnToAiApiKeys_4b92a5") : copy("account-deletion.returnToAccounts_4b7a6d")}</button></>;
  return <section className={isApi ? "api-entry-workflow" : isChatGPT ? "subscription-account-create account-deletion" : undefined}>
    {isApi ? <header className="api-entry-heading"><h2>{copy("account-deletion.apiKeyEntryDeleted_3200d9")}</h2><p className="api-entry-scope">{copy("account-deletion.savedOnTheSelectedServer_93dbee")}</p></header> : <h3>{copy("account-deletion.accountConfigurationDeleted_2a5579")}</h3>}
    <p>{copy("account-deletion.browserCleanupIsTrackedSeparatelyOffline_5550d1")}</p>
    <Problem error={cleanup.error} />
    {cleanup.data ? <p><LocalizedText id="account-deletion.profileCleanupObligationsPendingConfirmedRemoved_063eba" components={{ s0: <>{cleanup.data.pending}</>, s1: <>{cleanup.data.removed}</> }} /></p> : <p>{copy("account-deletion.cleanupStatusIsUnavailableUntilThe_de4aa8")}</p>}
    {isChatGPT ? <div className="actions">{actions}</div> : actions}
  </section>;
}

// This category owns only the confirmed client sequence. Go retains every
// credential, execution, revision, reference and native-cleanup authority.
export function ChatGPTAccountDeletion({ initial, active, deleted, close }: { initial: Resource; active: boolean; deleted: () => void; close: () => void }) {
  useLocale();
  const transport = useTransport(), opening = useSettingsOpening();
  const clients = useMemo(() => ({
    resource: createClient(ResourceService, transport), system: createClient(SystemService, transport),
    subscription: createClient(SubscriptionService, transport), configuration: createClient(ConfigurationService, transport),
  }), [transport]);
  const [confirmed, setConfirmed] = useState(initial);
  const [view, setView] = useState<View>({ stage: Stage.Confirmation });
  const pending = useRef<Attempt | undefined>(undefined);
  const mounted = useRef(false), activeRef = useRef(active);
  activeRef.current = active;
  const live = (p: Attempt) => mounted.current && activeRef.current && !opening?.disposed && !p.disposed && pending.current === p;
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; if (pending.current) pending.current.disposed = true; };
  }, [opening]);
  useEffect(() => {
    if (!active && pending.current) {
      pending.current.disposed = true;
      setView({ stage: Stage.Paused, failure: { code: FailureCode.ConfirmationRequired, message: "Account deletion paused.", guidance: "Refresh the account and confirm deletion again." } });
    }
  }, [active]);

  const pause = (p: Attempt, message: string, guidance: string, retry?: Retry, error?: unknown) => {
    if (!live(p)) return;
    p.observing = false;
    p.retry = retry;
    setView({ stage: Stage.Paused, failure: error ? clientFailure(error) : { code: FailureCode.ConfirmationRequired, message, guidance } });
  };
  const changed = (p: Attempt) => pause(p, copy("account-deletion.extra.36a29426269b"), copy("account-deletion.extra.3811b20f1087"));
  const remove = async (p: Attempt, resource?: Resource) => {
    if (!live(p) || p.busy) return;
    if (!p.deletion) {
      if (!unchanged(resource, p) || !cleared(resource)) { changed(p); return; }
      p.deletion = create(DeleteConfigurationRequestSchema, { kind: EntityKind.ACCOUNT, mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() } });
    }
    p.observing = false; p.busy = true; p.retry = undefined; setView({ stage: Stage.Deleting });
    try {
      const result = await clients.configuration.deleteConfiguration(p.deletion);
      if (!live(p)) return;
      if (result.id !== p.confirmed.id || result.requestId !== p.deletion.mutation!.requestId) {
        pause(p, copy("account-deletion.extra.011cbdc78098"), copy("account-deletion.extra.7e70cd1f1e1b"), Retry.Delete); return;
      }
      p.disposed = true;
      setView({ stage: Stage.Deleted });
    } catch (error) { pause(p, "", "", uncertain(error) ? Retry.Delete : undefined, error); }
    finally { p.busy = false; }
  };
  const logout = async (p: Attempt) => {
    if (!live(p) || p.busy || !p.logout) return;
    p.busy = true; p.retry = undefined; setView({ stage: Stage.Logout });
    try {
      const result = await clients.subscription.requestSubscription(p.logout);
      if (!live(p)) return;
      const state = object(document(result.account).subscription), operation = object(state.server_operation);
      if (result.operationId !== p.logout.mutation!.requestId || !unchanged(result.account, p) || operation.id !== result.operationId || operation.action !== "logout") {
        pause(p, copy("account-deletion.extra.9cbf6853e01d"), copy("account-deletion.extra.a59a4ec6ba3c"), Retry.Logout); return;
      }
      const connection = text(object(document(result.account).connection).id);
      if (connection && connection !== p.connection) { changed(p); return; }
      p.minimumRevision = result.account.revision; p.operation = result.operationId;
      p.observing = true;
      // Force a new observation effect even if logout completed synchronously.
      setView({ stage: Stage.Logout });
    } catch (error) { pause(p, "", "", uncertain(error) ? Retry.Logout : undefined, error); }
    finally { p.busy = false; }
  };
  const inspect = async (p: Attempt) => {
    if (!live(p) || p.busy || !p.operation || !p.observing) return;
    p.busy = true; p.retry = undefined;
    let ready: Resource | undefined;
    try {
      // Progress stays outside query caches: another pending login may contain
      // sensitive browser data. Only this original logout can permit deletion.
      const progress = await clients.subscription.getSubscriptionProgress({ accountId: p.confirmed.id, operationId: p.operation });
      if (!live(p)) return;
      if (progress.state === SubscriptionLoginState.PREPARING || progress.state === SubscriptionLoginState.WAITING) {
        setView({ stage: Stage.Logout }); return;
      }
      if (progress.state !== SubscriptionLoginState.SUCCEEDED || progress.canceled) {
        const diagnostic = safeDiagnostic(progress.diagnostic);
        const reasons: Partial<Record<SubscriptionLoginState, string>> = {
          [SubscriptionLoginState.FAILED]: copy("account-deletion.extra.76d4a13459c6"),
          [SubscriptionLoginState.CANCELED]: copy("account-deletion.extra.491112623c4d"),
          [SubscriptionLoginState.EXPIRED]: copy("account-deletion.extra.af1abd00d38a"),
          [SubscriptionLoginState.UNSUPPORTED]: copy("account-deletion.extra.28727cc37ddb"),
          [SubscriptionLoginState.RECOVERY_REQUIRED]: copy("account-deletion.extra.8c8625da5c7c"),
        };
        const reason = reasons[progress.state] ?? copy("account-deletion.extra.8d3007d5bd3d");
        pause(p, diagnostic?.message ?? reason, copy("account-deletion.sentence.1ab129957de8", { v0: diagnostic?.correlation ? ` Reference: ${diagnostic.correlation}` : "" })); return;
      }
      const result = await clients.resource.getResource({ kind: EntityKind.ACCOUNT, id: p.confirmed.id });
      if (!live(p)) return;
      const operation = object(object(document(result.resource).subscription).server_operation);
      if (!unchanged(result.resource, p) || !cleared(result.resource) || operation.id !== p.operation || operation.action !== "logout" || operation.state !== "succeeded") {
        changed(p); return;
      }
      ready = result.resource;
    } catch (error) { pause(p, "", "", uncertain(error) ? Retry.Progress : undefined, error); }
    finally { p.busy = false; }
    if (ready && live(p)) await remove(p, ready);
  };
  useEffect(() => {
    const p = pending.current;
    if (!active || view.stage !== Stage.Logout || !p?.operation) return;
    // Nonoverlapping reads only. Timers and late continuations cannot survive
    // a category departure or authorize another native lifecycle request.
    void inspect(p);
    const timer = setInterval(() => void inspect(p), 2000);
    return () => clearInterval(timer);
  }, [active, view.stage, pending.current?.operation, clients]);

  const confirm = async () => {
    if (!active || !mounted.current || opening?.disposed || pending.current && !pending.current.disposed) return;
    const p: Attempt = { confirmed: create(ResourceSchema, { ...confirmed, documentJson: confirmed.documentJson.slice() }), minimumRevision: confirmed.revision, connection: text(object(document(confirmed).connection).id), operation: "", observing: false, busy: true, disposed: false };
    pending.current = p; setView({ stage: Stage.Checking });
    let current: Resource | undefined;
    try {
      const result = await clients.resource.getResource({ kind: EntityKind.ACCOUNT, id: confirmed.id });
      if (!live(p)) return;
      current = result.resource;
      if (!unchanged(current, p) || current.revision !== confirmed.revision) { changed(p); return; }
      if (cleared(current)) { p.busy = false; await remove(p, current); return; }
      const data = document(current), state = object(data.subscription), operation = object(state.server_operation), pendingOperation = object(state.pending);
      if (state.recovery_required || data.removal) {
        pause(p, copy("account-deletion.extra.775c54df4fb5"), copy("account-deletion.extra.a395ee1c2744")); return;
      }
      const status = await clients.system.getStatus({});
      if (!live(p)) return;
      if (!status.capabilities.includes(SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1)) {
        pause(p, copy("account-deletion.extra.28727cc37ddb"), copy("account-deletion.extra.432a2d9f1fe9")); return;
      }
      if (state.pending) {
        if (pendingOperation.action !== "logout" || pendingOperation.machine_id || !isEntityId(text(pendingOperation.id)) || operation.id !== pendingOperation.id || operation.action !== "logout" || pendingOperation.canceled) {
          pause(p, copy("account-deletion.extra.2ce284085b03"), copy("account-deletion.extra.37108ca03e31")); return;
        }
        p.operation = text(pendingOperation.id); p.minimumRevision = current.revision; p.observing = true;
        setView({ stage: Stage.Logout }); return;
      }
      if (!isEntityId(p.connection) || !isEntityId(text(state.generation)) || object(state.lease).action && object(state.lease).action !== "execute" || operation.native_started) {
        changed(p); return;
      }
      p.logout = create(RequestSubscriptionRequestSchema, { mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() }, action: SubscriptionAction.LOGOUT });
    } catch (error) { pause(p, "", "", undefined, error); }
    finally { p.busy = false; }
    if (p.logout && live(p)) await logout(p);
  };
  const refresh = async () => {
    const previous = pending.current;
    if (!active || previous?.busy) return;
    if (previous) previous.disposed = true;
    const p: Attempt = { confirmed, minimumRevision: 1n, connection: "", operation: "", observing: false, busy: true, disposed: false };
    pending.current = p; setView({ stage: Stage.Checking });
    try {
      const result = await clients.resource.getResource({ kind: EntityKind.ACCOUNT, id: initial.id });
      if (!live(p)) return;
      if (!serviceAccount(result.resource, initial.id, SubscriptionServiceId.ChatGPT)) { changed(p); return; }
      setConfirmed(result.resource); p.disposed = true;
      setView({ stage: Stage.Confirmation });
    } catch (error) { pause(p, "", "", undefined, error); }
    finally { p.busy = false; }
  };
  const retry = () => {
    const p = pending.current;
    if (!p || !live(p) || p.busy) return;
    if (p.retry === Retry.Logout) void logout(p);
    else if (p.retry === Retry.Delete) void remove(p);
    else if (p.retry === Retry.Progress) { p.observing = true; setView({ stage: Stage.Logout }); void inspect(p); }
  };
  const leave = () => { if (pending.current) pending.current.disposed = true; close(); };
  if (view.stage === Stage.Deleted) return <AccountDeletionResult initial={confirmed} active={active} deleted={deleted} />;
  const waiting = [Stage.Checking, Stage.Logout, Stage.Deleting].includes(view.stage);
  return <section className="subscription-account-create account-deletion">
    <h3>{waiting ? copy("account-deletion.deleting_983c74", { v0: resourceName(confirmed) }) : copy("account-deletion.delete_a19801", { v0: resourceName(confirmed) })}</h3>
    {view.stage === Stage.Confirmation ? <>
      <p>{copy("account-deletion.thisLogsOutTheAccountAnd_bc0446")}</p>
      <p>{copy("account-deletion.activeExecutionsWillBeCanceledRetained_066584")}</p>
      <div className="actions"><button className="account-deletion-confirm" disabled={!active} onClick={() => void confirm()}>{copy("account-deletion.disconnectAndDeleteAccount_fdb5f5")}</button><button disabled={!active} onClick={leave}>{copy("account-deletion.keepAccount_9be7d9")}</button></div>
    </> : <>
      <p role="status">{view.stage === Stage.Checking ? copy("account-deletion.checkingTheCurrentAccount_9c2ed5") : view.stage === Stage.Logout ? copy("account-deletion.loggingOutAndCleaningUpCredentials_6b3730") : view.stage === Stage.Deleting ? copy("account-deletion.deletingTheAccountConfiguration_9b97e4") : copy("account-deletion.accountDeletionPaused_df3fd2")}</p>
      {view.stage === Stage.Logout ? <p>{copy("account-deletion.theAccountWillBeDeletedAfter_6b177a")}</p> : null}
      <Failure failure={view.failure} />
      <div className="actions">{view.stage === Stage.Paused ? pending.current?.retry !== undefined ? <button disabled={!active || pending.current.busy} onClick={retry}>{pending.current.retry === Retry.Logout ? copy("account-deletion.retryOriginalLogoutRequest_ce84e9") : pending.current.retry === Retry.Delete ? copy("account-deletion.retryTheSameDeletion_b32bf6") : copy("account-deletion.retryOriginalStatusCheck_88bd61")}</button> : <button disabled={!active || pending.current?.busy} onClick={() => void refresh()}>{copy("account-deletion.refreshAccountForConfirmation_deba05")}</button> : null}<button disabled={!active} onClick={leave}>{copy("account-deletion.backToSubscriptions_257d53")}</button></div>
      <p className="settings-scope">{copy("account-deletion.leavingThisScreenStopsAutomaticDeletion_7a5a3c")}</p>
    </>}
  </section>;
}
