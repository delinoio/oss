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
  if (!error) return null;
  const problems: Record<string, string> = {
    busy: "Another connection operation is running. Wait, then retry the original operation.",
    "invalid-input": "The connection input is invalid. Use a nonempty name of at most 256 UTF-8 bytes and retain the original pairing or edit identity.",
    "invalid-evidence": "The original saved connection needs inspection. Preserve its pairing and credential files; they will not be replaced automatically.",
    "credential-unavailable": "This client credential is unavailable or revoked. Inspect the original server registration; no replacement was created.",
    "permission-denied": "This connection is not authorized. Check the exact server origin and private-directory permissions.",
    incompatible: "The selected server or bundled client is incompatible. Keep the original server and use the matching bundled desktop version.",
    "timed-out": "The operation has not been confirmed. Its original pairing may still be retained; retry that same operation.",
  };
  return <p role="alert">{typeof error === "string" && Object.hasOwn(problems, error) ? problems[error] : "The saved connection operation could not be confirmed. Inspect its original state and retry without changing its identity."}</p>;
}
export function SavedConnections({ visible, close, actions }: { visible: boolean; close: () => void; actions: SavedConnectionActions }) {
  const readGeneration = useRef(0);
  const [profiles, setProfiles] = useState<SavedConnection[]>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const [grant, setGrant] = useState("");
  const [attempt, setAttempt] = useState<{ id: string; name: string; grant: string }>();
  const [message, setMessage] = useState("");
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
      setMessage("The client pairing is saved. Open its server window to verify authorization and connect.");
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
        setMessage("The original client pairing is saved.");
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
      setMessage(`Name edit accepted. Current connection name: ${result.name}. Open windows and server sessions keep their original connection.`);
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
      setMessage("This local client connection was removed. Its server sessions, independent Worker and work files remain retained.");
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
  return <Modal title="Saved servers" visible={visible} close={() => { if (!busy) close(); }}>
    <p>Each server opens in its own window with separate client authorization, drafts and query cache. Server sessions continue when a window closes.</p>
    <SavedConnectionProblem error={error} />{message ? <p role="status">{message}</p> : null}
    <button disabled={busy} onClick={() => void refresh()}>Refresh saved servers</button>
    {profiles ? profiles.length ? <ul>{profiles.map((profile) => <li key={profile.id}>
      <h3>{profile.name}</h3><p>{profile.endpoint}</p><p>Server: {profile.server_id}</p>
      <p>{profile.state === SavedConnectionState.Paired ? "Pairing saved; current authorization is checked when connecting." : profile.state === SavedConnectionState.Removing ? "Removal accepted; original client credential cleanup is still pending." : "Original pairing pending; retry retains its request identity."}</p>
      {profile.state !== SavedConnectionState.Removing ? <>
        <button disabled={busy || Boolean(attempt || edit || removal)} onClick={() => void operate(profile)}>{profile.state === SavedConnectionState.Paired ? `Open ${profile.name}` : `Retry ${profile.name}`}</button>
        <button disabled={busy || Boolean(attempt || edit || removal)} onClick={() => { setEdit({ profile, name: profile.name }); setError(undefined); setMessage(""); }}>{`Rename ${profile.name}`}</button>
      </> : null}
      <button disabled={busy || Boolean(attempt || edit || removal)} onClick={() => beginRemoval(profile)}>{profile.state === SavedConnectionState.Removing ? `Retry removal of ${profile.name}` : `Remove ${profile.name}`}</button>
    </li>)}</ul> : <p>No saved servers.</p> : <p>Saved server inventory is unavailable.</p>}
    {edit ? <section><h3>Rename {edit.profile.name}</h3>
      <label>New connection name<input autoFocus value={edit.name} maxLength={256} disabled={busy || Boolean(edit.requestId)} onChange={(event) => setEdit({ ...edit, name: event.target.value })} /></label>
      {staleEdit ? <p role="alert">This connection changed while you were editing. The original draft is retained; start a new edit from the refreshed name after resolving any pending request.</p> : null}
      <button disabled={busy || (!edit.requestId && staleEdit) || !edit.name.trim() || new TextEncoder().encode(edit.name).byteLength > 256} onClick={() => void rename()}>{edit.requestId ? "Retry original name edit" : "Save connection name"}</button>
      <button disabled={busy} onClick={() => setEdit(undefined)}>Discard name edit</button>
      {edit.requestId ? <p>The original name edit may already be saved. Retrying checks the same request; discarding this draft does not undo an accepted change.</p> : null}
    </section> : null}
    {removal ? <section aria-label="Confirm connection removal"><h3>Remove {removal.profile.name}</h3>
      <p>This closes this server's window and discards its unsent drafts. It deletes this computer's saved client credential and pairing material. Server sessions, independent Workers and work files continue unchanged.</p>
      <p>This is local removal. Previously copied credentials and other running clients are not revoked. Use the server's Paired devices settings to revoke a device explicitly.</p>
      {staleRemoval ? <p role="alert">The connection changed. Keep it, refresh, and confirm the current name before removing it.</p> : null}
      <button disabled={busy || staleRemoval} onClick={() => void remove()}>{removal.requestId ? "Retry original connection removal" : "Confirm connection removal"}</button>
      <button disabled={busy} onClick={() => setRemoval(undefined)}>{removal.requestId ? "Inspect removal state later" : "Keep connection"}</button>
      {removal.requestId ? <p>The original removal may already be accepted. Closing this confirmation does not restore its credential; pending cleanup remains in the saved inventory.</p> : null}
    </section> : null}
    <section><h3>Add a server connection</h3><p>Obtain a short-lived client pairing document from the server owner. Check the displayed server before pairing. This does not start or update a remote server.</p>
      <label>Connection name<input value={name} maxLength={256} disabled={busy || Boolean(attempt)} onChange={(event) => setName(event.target.value)} /></label>
      <label>Private client pairing document<input type="password" autoComplete="off" spellCheck={false} value={grant} maxLength={32768} disabled={busy || Boolean(attempt)} onChange={(event) => setGrant(event.target.value)} /></label>
      {text(preview.endpoint) ? <p>Pairing endpoint: {text(preview.endpoint)}</p> : null}{text(preview.server_id) ? <p>Expected server: {text(preview.server_id)}</p> : null}
      <button disabled={busy || Boolean(edit || removal) || (!attempt && !valid)} onClick={() => void pair()}>{attempt ? "Retry original server pairing" : "Pair this server"}</button>
      {attempt ? <p>The original request is retained while this dialog is closed. Resolve it before submitting different pairing input.</p> : null}
    </section>
    <RemovedConnections visible={visible} actions={actions} />
  </Modal>;
}

function RemovedConnections({ visible, actions }: { visible: boolean; actions: SavedConnectionActions }) {
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
  return <section><h3>Removed connections</h3><p>Removal history keeps independent Worker controls available. It cannot restore the deleted client credential.</p>
    <button disabled={busy} onClick={() => { setShown(true); void refresh(""); }}>Show removed connections</button>
    <div hidden={!shown}><SavedConnectionProblem error={error} />
      {page ? <><ul>{page.connections.map((profile) => <li key={profile.id}><h4>{profile.name}</h4><p>{profile.endpoint}</p><button disabled={workerPending} onClick={() => setSelected(profile)}>Inspect retained Worker for {profile.name}</button></li>)}</ul>{!page.connections.length ? <p>No removed connections.</p> : null}
        {after ? <button disabled={busy} onClick={() => void refresh("")}>First removed connections</button> : null}
        {page.next_after ? <button disabled={busy} onClick={() => void refresh(page.next_after!)}>More removed connections</button> : null}
      </> : null}
      {selected ? <section aria-label={`Retained Worker for ${selected.name}`}><h4>Retained Worker for {selected.name}</h4>
        <LocalWorkerControls key={selected.id} allowRegistration={false} pendingChanged={setWorkerPending} active={visible && shown} changed={() => {}} control={(action, generation) => actions.retainedWorker(selected.id, action, generation)} />
        <button disabled={workerPending} onClick={() => setSelected(undefined)}>Close retained Worker controls</button>
      </section> : null}
    </div>
  </section>;
}
