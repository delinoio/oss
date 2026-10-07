import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDialog, SettingsTaskScope, SettingsDialogSize, SettingsTaskActions } from "./settings-task";
import { useEffect, useRef, useState, useId } from "react";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { InstallationQuery, InstallationService, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { useSettingsOpening } from "./settings-lifetime";
import { Problem } from "./ui";

enum Authentication { Password = "password", PrivateKey = "private-key" }
export function SSHSetup({ active }: { active: boolean }) {
  useLocale();
  const [expanded, setExpanded] = useState(false);
  return <section><button type="button" onClick={() => setExpanded(true)}>{copy("ssh-setup.setUpAWorkerOverSsh_9a1626")}</button>
    {expanded ? <SettingsTaskScope><SSHSetupTask active={active} close={() => setExpanded(false)} /></SettingsTaskScope> : null}
  </section>;
}

function SSHSetupTask({ active, close }: { active: boolean; close: () => void }) {
  useLocale();
  const formId = useId();
  const [host, setHost] = useState(""), [port, setPort] = useState("22"), [user, setUser] = useState(""), [name, setName] = useState(""), [id, setId] = useState("");
  const [submittedStartId, setSubmittedStartId] = useState("");
  const [accepted, setAccepted] = useState<Resource>(), [confirmed, setConfirmed] = useState(false), [method, setMethod] = useState(Authentication.PrivateKey);
  const [busy, setBusy] = useState(false), [uncertain, setUncertain] = useState(false), [error, setError] = useState<unknown>();
  const secret = useRef<HTMLTextAreaElement>(null), passphrase = useRef<HTMLInputElement>(null);
  const alive = useRef(true), controller = useRef<AbortController>(undefined);
  const transport = useTransport(), opening = useSettingsOpening();
  useEffect(() => { alive.current = true; return () => { alive.current = false; controller.current?.abort(); if (secret.current) secret.current.value = ""; if (passphrase.current) passphrase.current.value = ""; }; }, []);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.SSH_WORKER_SETUP_V1) === true;
  const currentRead = useQuery(InstallationQuery.getSSHSetup, { id }, { enabled: active && supported && Boolean(id), refetchInterval: active && id ? 2000 : false });
  const current = [accepted, currentRead.data?.setup].filter((r): r is Resource => Boolean(r) && r!.id === id).reduce<Resource | undefined>((a, b) => !a || b.revision >= a.revision ? b : a, undefined);
  const data = current ? document(current) : {}, target = object(data.target), identity = object(data.identity), result = object(data.result);
  const inspection = useRetainedMutation("ssh:inspect", InstallationQuery.inspectSSHHost, response => { if (response.setup) { setAccepted(response.setup); setId(response.setup.id); setConfirmed(false); setUncertain(false); } });
  const cancel = useRetainedMutation(`ssh:cancel:${id}`, InstallationQuery.cancelSSHSetup, response => { if (response.setup) setAccepted(response.setup); });
  const reconcile = useRetainedMutation(`ssh:reconcile:${id}`, InstallationQuery.reconcileSSHSetup, response => { if (response.setup) setAccepted(response.setup); });
  const mutation = () => ({ id, expectedRevision: current!.revision, requestId: newRequestId() });
  const start = async () => {
    if (!active || !supported || !current || submittedStartId || text(data.state) !== "OBSERVED" || !confirmed || busy || uncertain || opening?.disposed) return;
    // Credentials never enter React Query keys, mutation variables or receipts.
    // An ambiguous response is inspected by the original ID, never retransmitted.
    const bytes = encode({ method, secret: btoa(String.fromCharCode(...new TextEncoder().encode(secret.current?.value ?? ""))), ...(passphrase.current?.value ? { passphrase: btoa(String.fromCharCode(...new TextEncoder().encode(passphrase.current.value))) } : {}) });
    if (secret.current) secret.current.value = "";
    if (passphrase.current) passphrase.current.value = "";
    setSubmittedStartId(id); setBusy(true); setError(undefined);
    const abort = new AbortController(); controller.current = abort;
    try {
      const response = await createClient(InstallationService, transport).startSSHSetup({ mutation: mutation(), name, confirmedFingerprint: text(identity.fingerprint), credential: bytes }, { signal: abort.signal, timeoutMs: 35000 });
      if (alive.current && !opening?.disposed && response.setup) setAccepted(response.setup);
    } catch (problem) { if (alive.current && !opening?.disposed) { setError(problem); setUncertain(true); void currentRead.refetch(); } }
    finally { bytes.fill(0); if (alive.current && !opening?.disposed) setBusy(false); }
  };
  const blocked = !active || busy || inspection.busy || inspection.uncertain || cancel.busy || reconcile.busy;
  return <SettingsTaskDialog title={copy("ssh-setup.setUpAWorkerOverSsh_9a1626")} size={SettingsDialogSize.Wide} onDismiss={() => { if (secret.current) secret.current.value = ""; if (passphrase.current) passphrase.current.value = ""; }} close={close}>
    <p>{copy("ssh-setup.firstInspectTheHostIdentityAnd_2194ce")}</p>
    {!supported ? <p>{status.isPending ? copy("ssh-setup.checkingServerSupport_8d95fa") : copy("ssh-setup.thisServerRequiresAnUpdateTo_df31b8")}</p> : <>
      <form id={`${formId}-inspect`} onSubmit={event => { event.preventDefault(); if (!blocked && !submittedStartId) void inspection.send({ requestId: newRequestId(), host, port: Number(port), user }); }}><fieldset disabled={blocked || Boolean(submittedStartId)}>
        <label>{copy("ssh-setup.sshHost_7e873f")}<input value={host} maxLength={253} required onChange={event => setHost(event.target.value)} /></label><label>{copy("ssh-setup.sshPort_9ffa73")}<input type="number" min={1} max={65535} value={port} required onChange={event => setPort(event.target.value)} /></label><label>{copy("ssh-setup.sshUser_cc0d7b")}<input value={user} maxLength={64} required onChange={event => setUser(event.target.value)} /></label>
      </fieldset>{!current ? <SettingsTaskActions form={`${formId}-inspect`}><button className="primary" disabled={blocked || Boolean(submittedStartId)}>{copy("ssh-setup.inspectHostIdentity_552d32")}</button></SettingsTaskActions> : <button disabled={blocked || Boolean(submittedStartId)}>{copy("ssh-setup.inspectHostIdentity_552d32")}</button>}</form>
      <form onSubmit={event => { event.preventDefault(); setAccepted(undefined); setConfirmed(false); setUncertain(false); void currentRead.refetch(); }}><label>{copy("ssh-setup.originalSetupId_888879")}<input disabled={Boolean(submittedStartId)} value={id} maxLength={36} onChange={event => { setId(event.target.value); setConfirmed(false); setAccepted(undefined); }} /></label><button disabled={blocked || !id}>{copy("ssh-setup.inspectOriginalSetup_544c70")}</button></form>
      {current ? <section aria-label={copy("ssh-setup.sshSetupProgress_04e250")}><p>{text(target.user)}@{text(target.host)}:{String(target.port)} · {statusLabel(text(data.state))}</p><p><LocalizedText id="ssh-setup.hostKey_6cf677" components={{ s0: <>{text(identity.algorithm)}</>, s1: <code>{text(identity.fingerprint)}</code> }} /></p>
        {text(data.state) === "OBSERVED" ? <form id={`${formId}-start`} onSubmit={event => { event.preventDefault(); void start(); }}><fieldset disabled={blocked || uncertain || Boolean(submittedStartId)}>
          <label className="checkbox"><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />{copy("ssh-setup.iVerifiedThisExactHostFingerprint_1b1591")}</label>
          <label>{copy("ssh-setup.runnerName_47c285")}<input value={name} maxLength={256} required onChange={event => setName(event.target.value)} /></label><label>{copy("ssh-setup.sshAuthentication_13f950")}<select value={method} onChange={event => setMethod(event.target.value as Authentication)}><option value={Authentication.PrivateKey}>{copy("ssh-setup.privateKey_477bf9")}</option><option value={Authentication.Password}>{copy("ssh-setup.password_e7cf3e")}</option></select></label>
          <label>{method === Authentication.PrivateKey ? copy("ssh-setup.sshPrivateKey_0e92a6") : copy("ssh-setup.sshPassword_1b0299")}<textarea ref={secret} maxLength={48000} autoComplete="off" required /></label>{method === Authentication.PrivateKey ? <label>{copy("ssh-setup.keyPassphrase_251003")}<input ref={passphrase} type="password" autoComplete="off" maxLength={8000} /></label> : null}
        </fieldset><SettingsTaskActions form={`${formId}-start`}><button className="primary" disabled={blocked || uncertain || Boolean(submittedStartId) || !confirmed}>{copy("ssh-setup.installAndStartWorker_a7dc9a")}</button></SettingsTaskActions></form> : null}
        {text(data.state) === "UNCERTAIN" && data.credential_removed !== true ? <button disabled={blocked} onClick={() => void reconcile.send({ mutation: mutation() })}>{copy("ssh-setup.reconcileOriginalSetup_611eeb")}</button> : null}
        {data.credential_removed !== true ? <button disabled={blocked} onClick={() => void cancel.send({ mutation: mutation() })}>{copy("ssh-setup.cancelSetupAndRemoveSettledSsh_cc4a9c")}</button> : null}
        {result.running === true ? <p><LocalizedText id="ssh-setup.workerReadinessConfirmedMachine_699397" components={{ s0: <>{text(result.worker_version)}</>, s1: <>{text(result.machine_id)}</> }} /></p> : null}
        {text(data.problem_code) ? <p role="alert"><LocalizedText id="ssh-setup.setupIsUnconfirmedReconcileTheOriginal_11b460" components={{ s0: <>{text(data.problem_code)}</> }} /></p> : null}
        {data.cancellation_requested === true ? <p>{copy("ssh-setup.cancellationRequestedADetachedWorkerKeeps_063704")}</p> : null}
      </section> : null}
      {uncertain ? <p role="alert">{copy("ssh-setup.theOriginalStartResponseIsUnconfirmed_a4e7bf")}</p> : null}
      {inspection.uncertain ? <button disabled={inspection.busy} onClick={inspection.retry}>{copy("ssh-setup.retryTheSameHostInspection_7b2096")}</button> : null}
    </>}
    <Problem error={status.error || currentRead.error || inspection.error || cancel.error || reconcile.error || error} />
  </SettingsTaskDialog>;
}
