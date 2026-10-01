import { useRef, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { newRequestId } from "@delinoio/delidev-api-client";
import { Modal } from "./ui";

export interface NativeConnection { endpoint: string; token: string; server_id: string; device_id: string }
export enum RegistrationState { Authorized = "authorized", Revoked = "revoked", Recovering = "recovering" }
export interface DesktopRegistration { state: RegistrationState; server_id: string; device_id: string; revision: string; request_id?: string }
interface RecoveryRequest { serverId: string; deviceId: string; revision: string; requestId: string }
export const localPermissionProblem = "Access was denied. Check that the selected device is authorized and that DeliDev's private data directory and registration files are owned by your account and accessible only to you. On macOS/Linux, use 0700 for private directories and 0600 for private files, then retry. Preserve existing data.";
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
function registration(value: DesktopRegistration): DesktopRegistration {
  if (!value || !Object.values(RegistrationState).includes(value.state) || !id.test(value.server_id) || !id.test(value.device_id)
    || typeof value.revision !== "string" || !/^[1-9][0-9]{0,19}$/.test(value.revision) || BigInt(value.revision) > 18446744073709551615n
    || (value.state === RegistrationState.Recovering ? typeof value.request_id !== "string" || !id.test(value.request_id) : value.request_id !== undefined)) throw "invalid-evidence";
  return value;
}
function recoveryProblem(error: unknown) {
  if (error === "credential-unavailable") return "The original owner or replacement credential is unavailable. Restore the original owner access before retrying; no automatic registration will occur.";
  if (error === "permission-denied") return localPermissionProblem;
  if (error === "invalid-evidence") return "The original registration or recovery files changed or are incomplete. Preserve them for inspection; recovery cannot safely continue.";
  if (error === "busy") return "Another operation or a changed registration prevents this request. Check desktop registration and retain any original recovery request.";
  if (error === "incompatible") return "Use a desktop version compatible with the original local server.";
  return "The operation could not be confirmed. Check desktop registration or retry the same recovery; do not reset the server.";
}

export function LocalRegistrationRecovery({ busy, setBusy, recovered, active = true }: { active?: boolean; busy: boolean; setBusy: (value: boolean) => void; recovered: (connection: NativeConnection) => Promise<void> }) {
  const [status, setStatus] = useState<DesktopRegistration>();
  const [confirm, setConfirm] = useState(false);
  const [pending, setPending] = useState<RecoveryRequest>();
  const [error, setError] = useState<unknown>();
  const operating = useRef(false);
  const inspect = async () => {
    if (busy || operating.current) return;
    operating.current = true; setBusy(true); setError(undefined);
    try {
      const value = registration(await invoke<DesktopRegistration>("inspect_local_registration"));
      if (pending && pending.serverId !== value.server_id) throw "invalid-evidence";
      setStatus(value);
      if (value.state === RegistrationState.Recovering) {
        const original = { serverId: value.server_id, deviceId: value.device_id, revision: value.revision, requestId: value.request_id! };
        if (pending && (pending.deviceId !== original.deviceId || pending.revision !== original.revision || pending.requestId !== original.requestId)) throw "invalid-evidence";
        setPending(original);
      } else if (pending && (pending.deviceId !== value.device_id || pending.revision !== value.revision)) {
        // Go has no pending recovery and verified a different current registration.
        // Retire the completed request; any new recovery needs fresh confirmation.
        setPending(undefined); setConfirm(false);
      }
    } catch (reason) { setError(reason); setStatus(undefined); }
    finally { operating.current = false; setBusy(false); }
  };
  const recover = async () => {
    if (busy || operating.current || !status || (status.state === RegistrationState.Authorized && !pending)) return;
    const original = pending ?? { serverId: status.server_id, deviceId: status.device_id, revision: status.revision, requestId: newRequestId() };
    operating.current = true; setPending(original); setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("recover_local_registration", { deviceId: original.deviceId, revision: original.revision, requestId: original.requestId });
      if (!id.test(connection.device_id) || connection.device_id === original.deviceId || connection.server_id !== status.server_id || connection.endpoint !== "http://127.0.0.1:46310") throw "invalid-evidence";
      await recovered(connection);
      setPending(undefined); setConfirm(false); setStatus(undefined);
    } catch (reason) { setError(reason); }
    finally { operating.current = false; setBusy(false); }
  };
  return <section aria-label="Desktop registration">
    <button disabled={busy} onClick={() => void inspect()}>Check desktop registration</button>
    {status ? <p role="status">{status.state === RegistrationState.Authorized ? "This desktop registration is authorized." : status.state === RegistrationState.Revoked ? "This desktop registration was revoked. You can explicitly register this desktop again using this computer's server owner access." : "A desktop registration recovery is pending. Continue its original request."}</p> : null}
    {status && (status.state !== RegistrationState.Authorized || pending) ? <button disabled={busy} onClick={() => setConfirm(true)}>{pending ? "Continue desktop recovery" : "Re-register this desktop"}</button> : null}
    {error && !confirm ? <p role="alert">{recoveryProblem(error)}</p> : null}
    <Modal title="Re-register this desktop" visible={confirm && active} close={() => setConfirm(false)}>
      <p>This creates a new desktop registration. The original registration stays revoked. Your server, saved sessions, account settings and Workers are preserved.</p>
      <p>Switching to the new registration clears this window's unsent drafts and pending app actions. Sessions are not automatically resumed.</p>
      {pending ? <p>The original recovery request is retained. Closing this dialog does not cancel an accepted registration.</p> : null}
      {error ? <p role="alert">{recoveryProblem(error)}</p> : null}
      <button disabled={busy || !status} onClick={() => void recover()}>{busy ? "Recovering…" : pending ? "Retry original desktop recovery" : "Confirm desktop re-registration"}</button>
      <button onClick={() => setConfirm(false)}>Close</button>
    </Modal>
  </section>;
}
