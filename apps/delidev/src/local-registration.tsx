import { validateDesktopRuntime } from "./desktop-runtime";
import { copy, useLocale } from "./localization";
import { useEffect, useRef, useState } from "react";
import { PersistentConnectionView } from "./connections-page";
import { invoke } from "@tauri-apps/api/core";
import { newRequestId } from "@delinoio/delidev-api-client";
import { Modal } from "./ui";

export interface NativeConnection { endpoint: string; token: string; server_id: string; device_id: string; runtime_generation?: string; runtime_key?: string; keychain_access_required?: boolean; keychain_access_skipped?: boolean }
export enum RegistrationState { Authorized = "authorized", Revoked = "revoked", Recovering = "recovering" }
export interface DesktopRegistration { state: RegistrationState; server_id: string; device_id: string; revision: string; request_id?: string }
interface RecoveryRequest { serverId: string; deviceId: string; revision: string; requestId: string }
export const localPermissionProblem = () => copy("local-registration.extra.26ef939a1688");
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
function registration(value: DesktopRegistration): DesktopRegistration {
  if (!value || !Object.values(RegistrationState).includes(value.state) || !id.test(value.server_id) || !id.test(value.device_id)
    || typeof value.revision !== "string" || !/^[1-9][0-9]{0,19}$/.test(value.revision) || BigInt(value.revision) > 18446744073709551615n
    || (value.state === RegistrationState.Recovering ? typeof value.request_id !== "string" || !id.test(value.request_id) : value.request_id !== undefined)) throw "invalid-evidence";
  return value;
}
function recoveryProblem(error: unknown) {
  if (error === "credential-unavailable") return copy("local-registration.extra.4dd6b9aec612");
  if (error === "permission-denied") return localPermissionProblem();
  if (error === "invalid-evidence") return copy("local-registration.extra.9de930e2084f");
  if (error === "busy") return copy("local-registration.extra.0052bdc7399b");
  if (error === "incompatible") return copy("local-registration.extra.c2c03db23ff0");
  return copy("local-registration.extra.881bd1533eb2");
}

export function LocalRegistrationRecovery({ busy, setBusy, recovered, active = true, target, inline = true, readGeneration }: { readGeneration?: () => number; target?: HTMLElement; inline?: boolean; active?: boolean; busy: boolean; setBusy: (value: boolean) => void; recovered: (connection: NativeConnection, admissionGeneration?: number) => Promise<void | boolean> }) {
  useLocale();
  const [status, setStatus] = useState<DesktopRegistration>();
  const [confirm, setConfirm] = useState(false);
  const [pending, setPending] = useState<RecoveryRequest>();
  const [error, setError] = useState<unknown>();
  const operating = useRef(false), alive = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const inspect = async () => {
    if (busy || operating.current) return;
    const admissionGeneration = readGeneration?.();
    const current = () => alive.current && (admissionGeneration === undefined || admissionGeneration === readGeneration?.());
    operating.current = true; setBusy(true); setError(undefined);
    try {
      const value = registration(await invoke<DesktopRegistration>("inspect_local_registration"));
      if (!current()) return;
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
    } catch (reason) { if (current()) { setError(reason); setStatus(undefined); } }
    finally { operating.current = false; if (current()) setBusy(false); }
  };
  const recover = async () => {
    if (busy || operating.current || !status || (status.state === RegistrationState.Authorized && !pending)) return;
    const original = pending ?? { serverId: status.server_id, deviceId: status.device_id, revision: status.revision, requestId: newRequestId() };
    const admissionGeneration = readGeneration?.();
    const current = () => alive.current && (admissionGeneration === undefined || admissionGeneration === readGeneration?.());
    operating.current = true; setPending(original); setBusy(true); setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("recover_local_registration", { deviceId: original.deviceId, revision: original.revision, requestId: original.requestId });
      if (!alive.current) return;
      validateDesktopRuntime(connection);
      if (!id.test(connection.device_id) || connection.device_id === original.deviceId || connection.server_id !== status.server_id) throw "invalid-evidence";
      const accepted = await (admissionGeneration === undefined ? recovered(connection) : recovered(connection, admissionGeneration));
      if (!alive.current) return;
      if (accepted === false) throw "invalid-evidence";
      setPending(undefined); setConfirm(false); setStatus(undefined);
    } catch (reason) { if (alive.current) setError(reason); }
    finally { operating.current = false; if (current()) setBusy(false); }
  };
  const view = <section data-connection-attention={Boolean(pending || error || busy)} aria-label={copy("local-registration.desktopRegistration_65a097")}>
    <button disabled={busy} onClick={() => void inspect()}>{copy("local-registration.checkDesktopRegistration_a540c5")}</button>
    {status ? <p role="status">{status.state === RegistrationState.Authorized ? copy("local-registration.thisDesktopRegistrationIsAuthorized_f356ec") : status.state === RegistrationState.Revoked ? copy("local-registration.thisDesktopRegistrationWasRevokedYou_1f8ea7") : copy("local-registration.aDesktopRegistrationRecoveryIsPending_de3baf")}</p> : null}
    {status && (status.state !== RegistrationState.Authorized || pending) ? <button disabled={busy} onClick={() => setConfirm(true)}>{pending ? copy("local-registration.continueDesktopRecovery_250cf5") : copy("local-registration.reRegisterThisDesktop_405621")}</button> : null}
    {error && !confirm ? <p role="alert">{recoveryProblem(error)}</p> : null}
    <Modal focusClose trapFocus title={copy("local-registration.reRegisterThisDesktop_405621")} visible={confirm && active} close={() => setConfirm(false)}>
      <p>{copy("local-registration.thisCreatesANewDesktopRegistration_0adc71")}</p>
      <p>{copy("local-registration.switchingToTheNewRegistrationClears_d3ec15")}</p>
      {pending ? <p>{copy("local-registration.theOriginalRecoveryRequestIsRetained_a082ac")}</p> : null}
      {error ? <p role="alert">{recoveryProblem(error)}</p> : null}
      <button disabled={busy || !status} onClick={() => void recover()}>{busy ? copy("local-registration.recovering_959bdc") : pending ? copy("local-registration.retryOriginalDesktopRecovery_3b14fa") : copy("local-registration.confirmDesktopReRegistration_d9ee3f")}</button>
    </Modal>
  </section>;
  return <PersistentConnectionView target={target} hidden={!inline}>{view}</PersistentConnectionView>;
}
