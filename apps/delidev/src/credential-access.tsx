// SPDX-License-Identifier: Apache-2.0
import { invoke } from "@tauri-apps/api/core";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { copy, useLocale, type MessageKey } from "./localization";
import type { NativeConnection } from "./local-registration";
import "./credential-access.css";

export enum CredentialAccessState { Checking = "checking", Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum CredentialAccessAction { Observe = "observe", Retry = "retry", Skip = "skip" }
export enum CredentialAccessIssue { ConfirmationRequired = "confirmation-required", Unavailable = "unavailable", RecoveryRequired = "recovery-required", ExecutableChanged = "executable-changed", ExecutableInvalid = "executable-invalid", PermissionDenied = "permission-denied" }
export interface CredentialAccessResult { attempt_id: string; state: CredentialAccessState; issue?: CredentialAccessIssue }
const issueMessages: Record<CredentialAccessIssue, MessageKey> = {
  [CredentialAccessIssue.ConfirmationRequired]: "desktop.keychainConfirmation",
  [CredentialAccessIssue.Unavailable]: "desktop.keychainUnavailable",
  [CredentialAccessIssue.RecoveryRequired]: "desktop.keychainRecovery",
  [CredentialAccessIssue.ExecutableChanged]: "desktop.keychainExecutableChanged",
  [CredentialAccessIssue.ExecutableInvalid]: "desktop.keychainExecutableChanged",
  [CredentialAccessIssue.PermissionDenied]: "desktop.keychainDenied",
};

// Polling observes the native process-owned attempt. Strict Mode replay and
// renderer replacement must never own another Begin or an automatic retry.
export function CredentialAccessGate({ connection, diagnostics, children }: {
  connection: NativeConnection; diagnostics: () => void; children: ReactNode;
}) {
  useLocale();
  const required = connection.keychain_access_required === true;
  const server = connection.server_id, generation = connection.runtime_generation;
  const scope = `${server}:${generation}`;
  const [observation, setObservation] = useState<{ scope: string; value: CredentialAccessResult }>();
  const result = observation?.scope === scope ? observation.value : undefined;
  const [error, setError] = useState(false);
  const [acting, setActing] = useState(false);
  const [dismissedNotice, setDismissedNotice] = useState(false);
  const controller = useRef<(action: CredentialAccessAction) => Promise<void>>(undefined);
  const content = useRef<HTMLDivElement>(null);
  const movedFocus = useRef(false);
  useEffect(() => {
    if (!required) return;
    let disposed = false, sequence = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const setResult = (value?: CredentialAccessResult) => setObservation(value ? { scope, value } : undefined);
    setResult(undefined); setError(false); setActing(false); setDismissedNotice(false); movedFocus.current = false;
    const read = async (action: CredentialAccessAction) => {
      const request = ++sequence;
      clearTimeout(timer);
      if (action !== CredentialAccessAction.Observe) setActing(true);
      setError(false);
      try {
        if (!generation) throw new Error("Missing original desktop generation");
        const value = await invoke<CredentialAccessResult>("desktop_credential_access", { action, server, generation });
        if (disposed || request !== sequence) return;
        if (!Object.values(CredentialAccessState).includes(value.state) || !value.attempt_id
          || (value.state === CredentialAccessState.Failed) !== Boolean(value.issue)
          || (value.issue && !Object.values(CredentialAccessIssue).includes(value.issue))) throw new Error("Invalid credential access observation");
        setResult(value);
        if (value.state === CredentialAccessState.Checking) timer = setTimeout(() => void read(CredentialAccessAction.Observe), 500);
      } catch {
        if (!disposed && request === sequence) setError(true);
      } finally {
        if (!disposed && request === sequence) setActing(false);
      }
    };
    controller.current = read;
    void read(CredentialAccessAction.Observe);
    return () => { disposed = true; ++sequence; clearTimeout(timer); controller.current = undefined; };
  }, [required, server, generation, scope]);
  const finished = !required || result?.state === CredentialAccessState.Succeeded || result?.state === CredentialAccessState.Skipped;
  useEffect(() => {
    if (!required || !finished || movedFocus.current) return;
    movedFocus.current = true;
    const heading = content.current?.querySelector<HTMLElement>("#main h1, #main h2") ?? content.current?.querySelector<HTMLElement>("#main");
    if (heading) { heading.setAttribute("tabindex", "-1"); heading.focus(); }
  }, [required, finished]);
  if (finished) return <div ref={content}>{(connection.keychain_access_skipped || result?.state === CredentialAccessState.Skipped) && !dismissedNotice ? <div className="credential-access-notice" role="status"><span>{copy("desktop.keychainSkipped")}</span><button onClick={() => setDismissedNotice(true)}>{copy("desktop.keychainDismiss")}</button></div> : null}{children}</div>;
  const failed = error || result?.state === CredentialAccessState.Failed;
  return <main className="credential-access-page"><section className="credential-access-card" aria-labelledby="credential-access-title">
    <p className="credential-access-brand">DeliDev</p>
    <h1 id="credential-access-title">{copy(failed ? "desktop.keychainFailed" : "desktop.keychainChecking")}</h1>
    <p>{copy("desktop.keychainDescription")}<br />{copy("desktop.keychainPrompt")}</p>
    {failed ? <p role="alert">{copy(error ? "desktop.keychainObservationFailed" : issueMessages[result!.issue!])}</p> : <p className="credential-access-progress" role="status"><span className="credential-access-spinner" aria-hidden="true" />{copy("desktop.keychainProgress")}</p>}
    <div className="credential-access-actions">{failed ? <button className="primary" disabled={acting} onClick={() => void controller.current?.(error ? CredentialAccessAction.Observe : CredentialAccessAction.Retry)}>{copy("desktop.keychainRetry")}</button> : null}<button disabled={acting} onClick={() => void controller.current?.(CredentialAccessAction.Skip)}>{copy("desktop.keychainContinue")}</button></div>
  </section><button className="credential-access-diagnostics" onClick={diagnostics}>{copy("desktop.keychainDiagnostics")}</button></main>;
}
