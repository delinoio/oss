// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import {
  AccountService, ConfigurationService, DeleteConfigurationRequestSchema, DisconnectAccountRequestSchema,
  EntityKind, FailureCode, ResourceSchema, clientFailure, isEntityId, newRequestId,
  type ClientFailure, type DeleteConfigurationRequest, type DisconnectAccountRequest, type Resource,
} from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { document, resourceName } from "./documents";
import { accountPreferencesDocument } from "./account-preferences";
import { accountRemovalMutation } from "./account-removal";
import { useAccountDeletionCompletion } from "./account-deletion";
import { useSettingsOpening } from "./settings-lifetime";
import { SettingsTaskActions } from "./settings-task";
import { SettingsTaskStatus, useRetainSettingsTask } from "./settings-task-context";
import "./account-deletion.css";

enum Stage { Confirmation, Checking, Disconnecting, Deleting, Paused, Deleted }
enum Retry { Disconnect, Status, Delete }
interface Attempt {
  confirmed: Resource;
  minimumRevision: bigint;
  disconnect?: DisconnectAccountRequest;
  deletion?: DeleteConfigurationRequest;
  retry?: Retry;
  disconnectUncertain?: boolean;
  deleteUncertain?: boolean;
  busy: boolean;
  disposed: boolean;
}
interface View { stage: Stage; failure?: ClientFailure }
const preferences = ["alias", "provider_id", "type", "enabled", "exclude_automatic", "recovery_notifications"] as const;
const uncertain = (error: unknown) => [FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(clientFailure(error).code);

function apiEntry(resource?: Resource): resource is Resource {
  if (!resource || resource.kind !== EntityKind.ACCOUNT || resource.schemaVersion !== 1 || !isEntityId(resource.id) || resource.revision === 0n) return false;
  try {
    // Reuse the bounded scanner to reject duplicate members without rounding
    // the original protected numeric tokens or rewriting the document.
    accountPreferencesDocument(resource, {});
    const data = document(resource);
    return data.type === "api" && typeof data.provider_id === "string" && isEntityId(data.provider_id) && !Object.hasOwn(data, "subscription") && !Object.hasOwn(data, "subscription_service");
  } catch { return false; }
}
function unchanged(resource: Resource | undefined, attempt: Attempt): resource is Resource {
  if (!apiEntry(resource) || resource.id !== attempt.confirmed.id || resource.revision < attempt.minimumRevision) return false;
  const current = document(resource), confirmed = document(attempt.confirmed);
  return preferences.every(key => current[key] === confirmed[key]);
}
function cleared(resource: Resource) {
  const data = document(resource);
  return data.health === "disconnected" && !Object.hasOwn(data, "connection") && !Object.hasOwn(data, "removal");
}
function cleanupFailure(bytes: Uint8Array): ClientFailure | undefined {
  if (!bytes.byteLength) return;
  try {
    if (bytes.byteLength > 16 << 10) throw new Error("Invalid cleanup problem");
    const value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
    if (!value || !Object.values(FailureCode).includes(value.code) || typeof value.message !== "string" || !value.message || value.guidance !== undefined && typeof value.guidance !== "string") throw new Error("Invalid cleanup problem");
    return { code: value.code, message: value.message, guidance: value.guidance ?? "", correlationId: typeof value.correlation_id === "string" && isEntityId(value.correlation_id) ? value.correlation_id : undefined };
  } catch {
    return { code: FailureCode.Internal, message: copy("account-deletion.api.cleanupUnconfirmed"), guidance: copy("account-deletion.api.retryCleanupGuidance") };
  }
}

// Go owns credential cleanup and reference checks. This controller composes
// only explicitly confirmed, original requests inside the Settings lifetime.
export function ApiAccountDeletion({ initial, active, deleted, close }: { initial: Resource; active: boolean; deleted: () => void; close: () => void }) {
  useLocale();
  const transport = useTransport(), opening = useSettingsOpening();
  const clients = useMemo(() => ({ account: createClient(AccountService, transport), configuration: createClient(ConfigurationService, transport) }), [transport]);
  const [confirmed, setConfirmed] = useState(initial);
  const [view, setView] = useState<View>({ stage: Stage.Confirmation });
  const pending = useRef<Attempt | undefined>(undefined);
  const mounted = useRef(false), activeRef = useRef(active);
  activeRef.current = active;
  const live = (p: Attempt) => mounted.current && activeRef.current && !opening?.disposed && !p.disposed && pending.current === p;
  const waiting = [Stage.Checking, Stage.Disconnecting, Stage.Deleting].includes(view.stage);
  const retained = view.stage !== Stage.Deleted && (waiting || Boolean(!pending.current?.disposed && (pending.current?.disconnect || pending.current?.deletion)));
  useRetainSettingsTask(retained, waiting ? SettingsTaskStatus.Pending : pending.current?.retry === Retry.Disconnect || pending.current?.retry === Retry.Delete ? SettingsTaskStatus.Uncertain : SettingsTaskStatus.AwaitingConfirmation);
  useAccountDeletionCompletion(view.stage === Stage.Deleted, active, deleted);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; if (pending.current) pending.current.disposed = true; };
  }, [opening]);
  useEffect(() => {
    if (!active && pending.current) pending.current.disposed = true;
  }, [active]);

  const pause = (p: Attempt, failure: ClientFailure, retry?: Retry) => {
    if (!live(p)) return;
    p.retry = retry;
    setView({ stage: Stage.Paused, failure });
  };
  const changed = (p: Attempt) => pause(p, { code: FailureCode.ConfirmationRequired, message: copy("account-deletion.api.changed"), guidance: copy("account-deletion.api.refreshGuidance") });
  const remove = async (p: Attempt, resource?: Resource) => {
    if (!live(p) || p.busy) return;
    if (!p.deletion) {
      if (!unchanged(resource, p) || !cleared(resource)) { changed(p); return; }
      p.deletion = create(DeleteConfigurationRequestSchema, { kind: EntityKind.ACCOUNT, mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() } });
    }
    p.busy = true; p.retry = undefined; setView({ stage: Stage.Deleting });
    try {
      const result = await clients.configuration.deleteConfiguration(p.deletion);
      if (!live(p)) return;
      if (result.id !== p.confirmed.id || result.requestId !== p.deletion.mutation!.requestId) {
        p.deleteUncertain = true;
        pause(p, { code: FailureCode.Internal, message: copy("account-deletion.extra.011cbdc78098"), guidance: copy("account-deletion.extra.7e70cd1f1e1b") }, Retry.Delete); return;
      }
      p.disposed = true; setView({ stage: Stage.Deleted });
    } catch (error) {
      p.deleteUncertain ||= uncertain(error);
      pause(p, clientFailure(error), p.deleteUncertain ? Retry.Delete : undefined);
    } finally { p.busy = false; }
  };
  const inspect = async (p: Attempt) => {
    if (!live(p) || p.busy) return;
    p.busy = true; p.retry = undefined; setView({ stage: Stage.Checking });
    let ready: Resource | undefined;
    try {
      const result = await clients.account.getAccountStatus({ id: p.confirmed.id });
      if (!live(p)) return;
      if (!unchanged(result.account, p) || !cleared(result.account)) { changed(p); return; }
      ready = result.account;
    } catch (error) { pause(p, clientFailure(error), Retry.Status); }
    finally { p.busy = false; }
    if (ready && live(p)) await remove(p, ready);
  };
  const disconnect = async (p: Attempt) => {
    if (!live(p) || p.busy || !p.disconnect) return;
    p.busy = true; p.retry = undefined; setView({ stage: Stage.Disconnecting });
    let cleaned = false;
    try {
      const result = await clients.account.disconnectAccount(p.disconnect);
      if (!live(p)) return;
      if (result.requestId !== p.disconnect.mutation!.requestId || !apiEntry(result.account) || result.account.id !== p.confirmed.id || document(result.account).provider_id !== document(p.confirmed).provider_id || result.account.revision <= p.disconnect.mutation!.expectedRevision) {
        p.disconnectUncertain = true;
        pause(p, { code: FailureCode.Internal, message: copy("account-deletion.api.cleanupUnconfirmed"), guidance: copy("account-deletion.api.retryCleanupGuidance") }, Retry.Disconnect); return;
      }
      if (!unchanged(result.account, p)) { changed(p); return; }
      p.minimumRevision = result.account.revision;
      const problem = cleanupFailure(result.cleanupProblemJson);
      if (problem) { p.disconnectUncertain = true; pause(p, problem, Retry.Disconnect); return; }
      if (!cleared(result.account)) {
        const original = accountRemovalMutation(result.account);
        if (original?.requestId === p.disconnect.mutation!.requestId && original.expectedRevision === p.disconnect.mutation!.expectedRevision) {
          pause(p, { code: FailureCode.RecoveryRequired, message: copy("account-deletion.api.cleanupUnconfirmed"), guidance: copy("account-deletion.api.retryCleanupGuidance") }, Retry.Disconnect);
        } else changed(p);
        return;
      }
      cleaned = true;
    } catch (error) {
      p.disconnectUncertain ||= uncertain(error);
      pause(p, clientFailure(error), p.disconnectUncertain ? Retry.Disconnect : undefined);
    } finally { p.busy = false; }
    if (cleaned && live(p)) await inspect(p);
  };
  const confirm = async () => {
    if (!active || !mounted.current || opening?.disposed || !apiEntry(confirmed) || pending.current && !pending.current.disposed) return;
    const p: Attempt = { confirmed: create(ResourceSchema, { ...confirmed, documentJson: confirmed.documentJson.slice() }), minimumRevision: confirmed.revision, busy: true, disposed: false };
    pending.current = p; setView({ stage: Stage.Checking });
    try {
      const result = await clients.account.getAccountStatus({ id: confirmed.id });
      if (!live(p)) return;
      if (!unchanged(result.account, p) || result.account.revision !== confirmed.revision) { changed(p); return; }
      const mutation = Object.hasOwn(document(result.account), "removal") ? accountRemovalMutation(result.account) : { id: confirmed.id, expectedRevision: confirmed.revision, requestId: newRequestId() };
      if (!mutation) { changed(p); return; }
      // Disconnected credential-bearing accounts may still own staged intents.
      // Only the server's disconnect result can establish their cleanup.
      p.disconnect = create(DisconnectAccountRequestSchema, { mutation });
    } catch (error) { pause(p, clientFailure(error)); }
    finally { p.busy = false; }
    if (p.disconnect && live(p)) await disconnect(p);
  };
  const refresh = async () => {
    const previous = pending.current;
    if (!active || previous?.busy || previous?.retry !== undefined) return;
    if (previous) previous.disposed = true;
    const p: Attempt = { confirmed, minimumRevision: 1n, busy: true, disposed: false };
    pending.current = p; setView({ stage: Stage.Checking });
    try {
      const result = await clients.account.getAccountStatus({ id: initial.id });
      if (!live(p)) return;
      if (!apiEntry(result.account) || result.account.id !== initial.id || document(result.account).provider_id !== document(initial).provider_id) { changed(p); return; }
      setConfirmed(result.account); p.disposed = true; setView({ stage: Stage.Confirmation });
    } catch (error) { pause(p, clientFailure(error)); }
    finally { p.busy = false; }
  };
  const retry = () => {
    const p = pending.current;
    if (!p || !live(p) || p.busy) return;
    if (p.retry === Retry.Disconnect) void disconnect(p);
    else if (p.retry === Retry.Delete) void remove(p);
    else if (p.retry === Retry.Status) void inspect(p);
  };
  if (view.stage === Stage.Deleted) return null;
  return <section className="api-entry-workflow account-deletion">
    <h3>{copy("account-deletion.delete_a19801", { v0: resourceName(confirmed) })}</h3>
    {view.stage === Stage.Confirmation ? <>
      <p>{copy("account-deletion.api.description")}</p>
      <p>{copy("account-deletion.api.consequences")}</p>
      <p className="settings-scope">{copy("account-deletion.api.browserCleanup")}</p>
      <SettingsTaskActions><button data-settings-task-cancel disabled={!active} onClick={close}>{copy("account-deletion.api.keepEntry")}</button><button className="api-account-deletion-confirm" disabled={!active || !apiEntry(confirmed)} onClick={() => void confirm()}>{copy("account-deletion.api.confirm")}</button></SettingsTaskActions>
    </> : <>
      <p role="status">{copy(view.stage === Stage.Checking ? "account-deletion.api.checking" : view.stage === Stage.Disconnecting ? "account-deletion.api.disconnecting" : view.stage === Stage.Deleting ? "account-deletion.api.deleting" : "account-deletion.api.paused")}</p>
      {view.failure ? <div className="problem" role="alert"><strong>{copy("ui.requestFailed")}</strong><p>{view.failure.message}</p><p>{view.failure.guidance}</p><details><summary>{copy("ui.technicalDetails")}</summary><code>{view.failure.code}</code></details>{view.failure.correlationId ? <small>{copy("ui.reference_0eac07", { v0: view.failure.correlationId })}</small> : null}</div> : null}
      <p className="settings-scope">{copy("account-deletion.api.departure")}</p>
      <SettingsTaskActions><button data-settings-task-cancel disabled={!active} onClick={close}>{copy("account-connection.backToAiApiKeys_2d6214")}</button>{view.stage === Stage.Paused ? <button disabled={!active || pending.current?.busy} onClick={pending.current?.retry !== undefined ? retry : () => void refresh()}>{copy(pending.current?.retry === Retry.Disconnect ? "account-connection.retryOriginalCredentialCleanup_bb5e5d" : pending.current?.retry === Retry.Delete ? "account-deletion.retryTheSameDeletion_b32bf6" : pending.current?.retry === Retry.Status ? "account-deletion.retryOriginalStatusCheck_88bd61" : "account-deletion.api.refresh")}</button> : null}</SettingsTaskActions>
    </>}
  </section>;
}
