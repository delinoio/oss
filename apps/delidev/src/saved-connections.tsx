import {  ownedMessage, useProductMessage, LocalizedText, copy, useLocale   } from "./localization";
import { useEffect, useRef, useState } from "react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { object, text } from "./documents";
import { Modal } from "./ui";
import { LocalWorkerControls, type LocalWorkerAction, type LocalWorkerStatus } from "./local-worker-controls";

export enum SavedConnectionState { Pending = "pending", Paired = "paired", Removing = "removing", Removed = "removed" }
export interface SavedConnection { version: number; revision: number; id: string; name: string; endpoint: string; server_id: string; pairing_id: string; device_id: string; state: SavedConnectionState; created_at: string; removal?: { request_id: string; expected_revision: number } }
export interface SavedConnectionActions {
  list: () => Promise<SavedConnection[]>;
  removed: (after: string) => Promise<RemovedPage>;
  remove: (id: string, requestId: string, revision: number) => Promise<SavedConnection>;
  retainedWorker: (id: string, action: LocalWorkerAction, generation?: string) => Promise<LocalWorkerStatus>;
  pair: (id: string, name: string, grant: string) => Promise<SavedConnection>;
  retry: (id: string) => Promise<SavedConnection>;
  rename: (id: string, requestId: string, revision: number, name: string) => Promise<SavedConnection>;
  open: (id: string) => Promise<void>;
}
export interface RemovedPage { connections: SavedConnection[]; next_after?: string }
export function SavedConnectionProblem({ error }: { error?: unknown }) {
  useLocale();
  if (!error) return null;
  const problems: Record<string, string> = {
    get busy() { return copy("saved-connections.anotherConnectionOperationIsRunningWait_3f097a"); },
    get "invalid-input"() { return copy("saved-connections.theConnectionInputIsInvalidUse_0cf712"); },
    get "invalid-evidence"() { return copy("saved-connections.theOriginalSavedConnectionNeedsInspection_be2d5a"); },
    get "credential-unavailable"() { return copy("saved-connections.thisClientCredentialIsUnavailableOr_ae5e66"); },
    get "permission-denied"() { return copy("saved-connections.thisConnectionIsNotAuthorizedCheck_870a15"); },
    get incompatible() { return copy("saved-connections.theSelectedServerOrBundledClient_36422e"); },
    get "timed-out"() { return copy("saved-connections.theOperationHasNotBeenConfirmed_3f4db2"); },
  };
  return <p role="alert">{typeof error === "string" && Object.hasOwn(problems, error) ? problems[error] : copy("saved-connections.theSavedConnectionOperationCouldNot_abcd89")}</p>;
}
export function SavedConnections({ visible, close, actions }: { visible: boolean; close: () => void; actions: SavedConnectionActions }) {
  useLocale();
  const readGeneration = useRef(0);
  const [profiles, setProfiles] = useState<SavedConnection[]>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const [grant, setGrant] = useState("");
  const [attempt, setAttempt] = useState<{ id: string; name: string; grant: string }>();
  const [message, setMessage] = useProductMessage("");
  const [edit, setEdit] = useState<{ profile: SavedConnection; name: string; requestId?: string }>();
  const [removal, setRemoval] = useState<{ profile: SavedConnection; requestId?: string; revision: number }>();
  const refresh = async () => {
    const generation = ++readGeneration.current;
    setError(undefined);
    try { const values = await actions.list(); if (readGeneration.current === generation) setProfiles(values); }
    catch (error) { if (readGeneration.current === generation) setError(error); }
  };
  useEffect(() => {
    if (!visible) return;
    let canceled = false;
    const generation = ++readGeneration.current;
    void actions.list().then((value) => { if (!canceled && generation === readGeneration.current) { setProfiles(value); setError(undefined); } }, (error) => { if (!canceled && generation === readGeneration.current) setError(error); });
    return () => { canceled = true; };
  }, [visible, actions]);
  let preview: Record<string, unknown> = {};
  if (grant.length <= 32768) { try { preview = object(JSON.parse(grant)); } catch { /* A partial private grant is never displayed or submitted. */ } }
  const valid = name.trim().length > 0 && new TextEncoder().encode(name).byteLength <= 256 && new TextEncoder().encode(grant).byteLength <= 32768 && preview.version === 1 && Boolean(text(preview.endpoint) && text(preview.server_id) && text(preview.pairing_id) && text(preview.code));
  const pair = async () => {
    if (busy || (!attempt && !valid)) return;
    const original = attempt ?? { id: newRequestId(), name, grant };
    readGeneration.current++;
    setAttempt(original); setBusy(true); setError(undefined); setMessage("");
    try {
      const result = await actions.pair(original.id, original.name, original.grant);
      if (result.id !== original.id || result.state !== SavedConnectionState.Paired) throw "invalid-evidence";
      setAttempt(undefined); setGrant(""); setName("");
      setMessage(ownedMessage("saved-connections.extra.2c160a020769"));
      await refresh();
    } catch (error) { setError(error); if (error === "invalid-input") setAttempt(undefined); }
    finally { setBusy(false); }
  };
  const operate = async (profile: SavedConnection) => {
    if (busy) return;
    readGeneration.current++;
    setBusy(true); setError(undefined); setMessage("");
    try {
      if (profile.state === SavedConnectionState.Pending) {
        const value = await actions.retry(profile.id);
        if (value.id !== profile.id || value.state !== SavedConnectionState.Paired) throw "invalid-evidence";
        setMessage(ownedMessage("saved-connections.extra.6fd6eef963de"));
        await refresh();
      } else await actions.open(profile.id);
    } catch (error) { setError(error); }
    finally { setBusy(false); }
  };
  const rename = async () => {
    if (busy || !edit || !edit.name.trim() || new TextEncoder().encode(edit.name).byteLength > 256) return;
    const original = { ...edit, requestId: edit.requestId ?? newRequestId() };
    readGeneration.current++;
    setEdit(original); setBusy(true); setError(undefined); setMessage("");
    try {
      const result = await actions.rename(original.profile.id, original.requestId, original.profile.revision, original.name);
      if (result.id !== original.profile.id || result.server_id !== original.profile.server_id || result.endpoint !== original.profile.endpoint || result.pairing_id !== original.profile.pairing_id || result.revision <= original.profile.revision) throw "invalid-evidence";
      setEdit(undefined);
      setMessage(ownedMessage("saved-connections.sentence.99477aa7f910", { v0: result.name }));
      await refresh();
    } catch (error) { setError(error); if (error === "invalid-input") setEdit({ ...original, requestId: undefined }); }
    finally { setBusy(false); }
  };
  const remove = async () => {
    if (busy || !removal) return;
    const original = { ...removal, requestId: removal.requestId ?? newRequestId() };
    readGeneration.current++;
    setRemoval(original); setBusy(true); setError(undefined); setMessage("");
    try {
      const result = await actions.remove(original.profile.id, original.requestId, original.revision);
      if (result.id !== original.profile.id || result.state !== SavedConnectionState.Removed || result.removal?.request_id !== original.requestId || result.removal.expected_revision !== original.revision) throw "invalid-evidence";
      setRemoval(undefined);
      setMessage(ownedMessage("saved-connections.extra.880fe388c7ae"));
      await refresh();
    } catch (error) { setError(error); }
    finally { setBusy(false); }
  };
  const beginRemoval = (profile: SavedConnection) => {
    setRemoval({ profile, revision: profile.removal?.expected_revision ?? profile.revision, requestId: profile.removal?.request_id });
    setError(undefined); setMessage("");
  };
  const staleRemoval = Boolean(removal && !removal.requestId && profiles?.some((profile) => profile.id === removal.profile.id && profile.revision !== removal.revision));
  const staleEdit = Boolean(edit && profiles?.some((profile) => profile.id === edit.profile.id && profile.revision !== edit.profile.revision));
  return <Modal title={copy("saved-connections.savedServers_4bf084")} visible={visible} close={() => { if (!busy) close(); }}>
    <p>{copy("saved-connections.eachServerOpensInItsOwn_2934f3")}</p>
    <SavedConnectionProblem error={error} />{message ? <p role="status">{message}</p> : null}
    <button disabled={busy} onClick={() => void refresh()}>{copy("saved-connections.refreshSavedServers_91a8fa")}</button>
    {profiles ? profiles.length ? <ul>{profiles.map((profile) => <li key={profile.id}>
      <h3>{profile.name}</h3><p>{profile.endpoint}</p><p><LocalizedText id="saved-connections.server_90ff00" components={{ s0: <>{profile.server_id}</> }} /></p>
      <p>{profile.state === SavedConnectionState.Paired ? copy("saved-connections.pairingSavedCurrentAuthorizationIsChecked_c6a54e") : profile.state === SavedConnectionState.Removing ? copy("saved-connections.removalAcceptedOriginalClientCredentialCleanup_31d574") : copy("saved-connections.originalPairingPendingRetryRetainsIts_240c96")}</p>
      {profile.state !== SavedConnectionState.Removing ? <>
        <button disabled={busy || Boolean(attempt || edit || removal)} onClick={() => void operate(profile)}>{profile.state === SavedConnectionState.Paired ? copy("saved-connections.open_afaef5", { v0: profile.name }) : copy("saved-connections.retry_37e45a", { v0: profile.name })}</button>
        <button disabled={busy || Boolean(attempt || edit || removal)} onClick={() => { setEdit({ profile, name: profile.name }); setError(undefined); setMessage(""); }}>{copy("saved-connections.rename_089ce7", { v0: profile.name })}</button>
      </> : null}
      <button disabled={busy || Boolean(attempt || edit || removal)} onClick={() => beginRemoval(profile)}>{profile.state === SavedConnectionState.Removing ? copy("saved-connections.retryRemovalOf_def714", { v0: profile.name }) : copy("saved-connections.remove_86790c", { v0: profile.name })}</button>
    </li>)}</ul> : <p>{copy("saved-connections.noSavedServers_b0a3d9")}</p> : <p>{copy("saved-connections.savedServerInventoryIsUnavailable_4a30fe")}</p>}
    {edit ? <section><h3><LocalizedText id="saved-connections.rename_286b04" components={{ s0: <>{edit.profile.name}</> }} /></h3>
      <label>{copy("saved-connections.newConnectionName_7e3c81")}<input autoFocus value={edit.name} maxLength={256} disabled={busy || Boolean(edit.requestId)} onChange={(event) => setEdit({ ...edit, name: event.target.value })} /></label>
      {staleEdit ? <p role="alert">{copy("saved-connections.thisConnectionChangedWhileYouWere_67bff4")}</p> : null}
      <button disabled={busy || (!edit.requestId && staleEdit) || !edit.name.trim() || new TextEncoder().encode(edit.name).byteLength > 256} onClick={() => void rename()}>{edit.requestId ? copy("saved-connections.retryOriginalNameEdit_0efce4") : copy("saved-connections.saveConnectionName_63f298")}</button>
      <button disabled={busy} onClick={() => setEdit(undefined)}>{copy("saved-connections.discardNameEdit_ac773a")}</button>
      {edit.requestId ? <p>{copy("saved-connections.theOriginalNameEditMayAlready_1ba462")}</p> : null}
    </section> : null}
    {removal ? <section aria-label={copy("saved-connections.confirmConnectionRemoval_36e149")}><h3><LocalizedText id="saved-connections.remove_d25d10" components={{ s0: <>{removal.profile.name}</> }} /></h3>
      <p>{copy("saved-connections.thisClosesThisServerSWindow_340931")}</p>
      <p>{copy("saved-connections.thisIsLocalRemovalPreviouslyCopied_af18b5")}</p>
      {staleRemoval ? <p role="alert">{copy("saved-connections.theConnectionChangedKeepItRefresh_3297d6")}</p> : null}
      <button disabled={busy || staleRemoval} onClick={() => void remove()}>{removal.requestId ? copy("saved-connections.retryOriginalConnectionRemoval_34f433") : copy("saved-connections.confirmConnectionRemoval_36e149")}</button>
      <button disabled={busy} onClick={() => setRemoval(undefined)}>{removal.requestId ? copy("saved-connections.inspectRemovalStateLater_336970") : copy("saved-connections.keepConnection_264051")}</button>
      {removal.requestId ? <p>{copy("saved-connections.theOriginalRemovalMayAlreadyBe_a5878d")}</p> : null}
    </section> : null}
    <section><h3>{copy("saved-connections.addAServerConnection_191cce")}</h3><p>{copy("saved-connections.obtainAShortLivedClientPairing_e08e27")}</p>
      <label>{copy("saved-connections.connectionName_686d4d")}<input value={name} maxLength={256} disabled={busy || Boolean(attempt)} onChange={(event) => setName(event.target.value)} /></label>
      <label>{copy("saved-connections.privateClientPairingDocument_9a8297")}<input type="password" autoComplete="off" spellCheck={false} value={grant} maxLength={32768} disabled={busy || Boolean(attempt)} onChange={(event) => setGrant(event.target.value)} /></label>
      {text(preview.endpoint) ? <p><LocalizedText id="saved-connections.pairingEndpoint_5b2ef5" components={{ s0: <>{text(preview.endpoint)}</> }} /></p> : null}{text(preview.server_id) ? <p><LocalizedText id="saved-connections.expectedServer_4ed0bf" components={{ s0: <>{text(preview.server_id)}</> }} /></p> : null}
      <button disabled={busy || Boolean(edit || removal) || (!attempt && !valid)} onClick={() => void pair()}>{attempt ? copy("saved-connections.retryOriginalServerPairing_f5aad2") : copy("saved-connections.pairThisServer_bbbfaf")}</button>
      {attempt ? <p>{copy("saved-connections.theOriginalRequestIsRetainedWhile_5fac3d")}</p> : null}
    </section>
    <RemovedConnections visible={visible} actions={actions} />
  </Modal>;
}

function RemovedConnections({ visible, actions }: { visible: boolean; actions: SavedConnectionActions }) {
  useLocale();
  const [shown, setShown] = useState(false), [busy, setBusy] = useState(false);
  const [page, setPage] = useState<RemovedPage>(), [after, setAfter] = useState("");
  const [error, setError] = useState<unknown>();
  const [selected, setSelected] = useState<SavedConnection>(), [workerPending, setWorkerPending] = useState(false);
  const refresh = async (cursor: string) => {
    if (busy) return;
    setBusy(true); setError(undefined);
    try { const value = await actions.removed(cursor); setPage(value); setAfter(cursor); }
    catch (error) { setError(error); }
    finally { setBusy(false); }
  };
  return <section><h3>{copy("saved-connections.removedConnections_2e4440")}</h3><p>{copy("saved-connections.removalHistoryKeepsIndependentWorkerControls_c576dd")}</p>
    <button disabled={busy} onClick={() => { setShown(true); void refresh(""); }}>{copy("saved-connections.showRemovedConnections_08135e")}</button>
    <div hidden={!shown}><SavedConnectionProblem error={error} />
      {page ? <><ul>{page.connections.map((profile) => <li key={profile.id}><h4>{profile.name}</h4><p>{profile.endpoint}</p><button disabled={workerPending} onClick={() => setSelected(profile)}><LocalizedText id="saved-connections.inspectRetainedWorkerFor_c42af0" components={{ s0: <>{profile.name}</> }} /></button></li>)}</ul>{!page.connections.length ? <p>{copy("saved-connections.noRemovedConnections_53a79f")}</p> : null}
        {after ? <button disabled={busy} onClick={() => void refresh("")}>{copy("saved-connections.firstRemovedConnections_a591e6")}</button> : null}
        {page.next_after ? <button disabled={busy} onClick={() => void refresh(page.next_after!)}>{copy("saved-connections.moreRemovedConnections_5f8390")}</button> : null}
      </> : null}
      {selected ? <section aria-label={copy("saved-connections.retainedWorkerFor_8f7a9b", { v0: selected.name })}><h4><LocalizedText id="saved-connections.retainedWorkerFor_0cc5fa" components={{ s0: <>{selected.name}</> }} /></h4>
        <LocalWorkerControls key={selected.id} allowRegistration={false} pendingChanged={setWorkerPending} active={visible && shown} changed={() => {}} control={(action, generation) => actions.retainedWorker(selected.id, action, generation)} />
        <button disabled={workerPending} onClick={() => setSelected(undefined)}>{copy("saved-connections.closeRetainedWorkerControls_52e73a")}</button>
      </section> : null}
    </div>
  </section>;
}
