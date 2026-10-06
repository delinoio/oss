// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDialog, SettingsDialogSize, SettingsTaskActions } from "./settings-task";
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
  const formId = useId();
  const [expanded, setExpanded] = useState(false), [host, setHost] = useState(""), [port, setPort] = useState("22"), [user, setUser] = useState(""), [name, setName] = useState(""), [id, setId] = useState("");
  const [submittedStartId, setSubmittedStartId] = useState("");
  const [accepted, setAccepted] = useState<Resource>(), [confirmed, setConfirmed] = useState(false), [method, setMethod] = useState(Authentication.PrivateKey);
  const [busy, setBusy] = useState(false), [uncertain, setUncertain] = useState(false), [error, setError] = useState<unknown>();
  const secret = useRef<HTMLTextAreaElement>(null), passphrase = useRef<HTMLInputElement>(null);
  const alive = useRef(true), controller = useRef<AbortController>(undefined);
  const transport = useTransport(), opening = useSettingsOpening();
  useEffect(() => { alive.current = true; return () => { alive.current = false; controller.current?.abort(); if (secret.current) secret.current.value = ""; if (passphrase.current) passphrase.current.value = ""; }; }, []);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && expanded });
  const supported = status.data?.capabilities.includes(SystemCapability.SSH_WORKER_SETUP_V1) === true;
  const currentRead = useQuery(InstallationQuery.getSSHSetup, { id }, { enabled: active && expanded && supported && Boolean(id), refetchInterval: active && expanded && id ? 2000 : false });
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
  return <section><button type="button" onClick={() => setExpanded(true)}>Set up a Worker over SSH</button>{expanded ? <SettingsTaskDialog title="Set up a Worker over SSH" size={SettingsDialogSize.Wide} retained={busy || uncertain || inspection.busy || inspection.uncertain || cancel.busy || cancel.uncertain || reconcile.busy || reconcile.uncertain || Boolean(submittedStartId && (!current || !["SUCCEEDED", "CANCELED", "FAILED"].includes(text(data.state)))) || Boolean(current && !["OBSERVED", "SUCCEEDED", "CANCELED", "FAILED"].includes(text(data.state)))} onDismiss={() => { if (secret.current) secret.current.value = ""; if (passphrase.current) passphrase.current.value = ""; }} close={() => { setExpanded(false); setHost(""); setPort("22"); setUser(""); setName(""); setId(""); setSubmittedStartId(""); setAccepted(undefined); setConfirmed(false); }}>
    <p>First inspect the host identity and confirm its fingerprint through a trusted channel. Installation preserves existing registration and workspaces.</p>
    {!supported ? <p>{status.isPending ? "Checking server support…" : "This server requires an update to support SSH Worker setup."}</p> : <>
      <form id={`${formId}-inspect`} onSubmit={event => { event.preventDefault(); if (!blocked) void inspection.send({ requestId: newRequestId(), host, port: Number(port), user }); }}><fieldset disabled={blocked}>
        <label>SSH host<input value={host} maxLength={253} required onChange={event => setHost(event.target.value)} /></label><label>SSH port<input type="number" min={1} max={65535} value={port} required onChange={event => setPort(event.target.value)} /></label><label>SSH user<input value={user} maxLength={64} required onChange={event => setUser(event.target.value)} /></label>
      </fieldset>{!current ? <SettingsTaskActions form={`${formId}-inspect`}><button className="primary" disabled={blocked}>Inspect host identity</button></SettingsTaskActions> : <button disabled={blocked}>Inspect host identity</button>}</form>
      <form onSubmit={event => { event.preventDefault(); setAccepted(undefined); setConfirmed(false); setUncertain(false); void currentRead.refetch(); }}><label>Original setup ID<input disabled={Boolean(submittedStartId)} value={id} maxLength={36} onChange={event => { setId(event.target.value); setConfirmed(false); setAccepted(undefined); }} /></label><button disabled={blocked || !id}>Inspect original setup</button></form>
      {current ? <section aria-label="SSH setup progress"><p>{text(target.user)}@{text(target.host)}:{String(target.port)} · {text(data.state)}</p><p>Host key: {text(identity.algorithm)} <code>{text(identity.fingerprint)}</code></p>
        {text(data.state) === "OBSERVED" ? <form id={`${formId}-start`} onSubmit={event => { event.preventDefault(); void start(); }}><fieldset disabled={blocked || uncertain || Boolean(submittedStartId)}>
          <label className="checkbox"><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />I verified this exact host fingerprint through a trusted channel</label>
          <label>Runner name<input value={name} maxLength={256} required onChange={event => setName(event.target.value)} /></label><label>SSH authentication<select value={method} onChange={event => setMethod(event.target.value as Authentication)}><option value={Authentication.PrivateKey}>Private key</option><option value={Authentication.Password}>Password</option></select></label>
          <label>{method === Authentication.PrivateKey ? "SSH private key" : "SSH password"}<textarea ref={secret} maxLength={48000} autoComplete="off" required /></label>{method === Authentication.PrivateKey ? <label>Key passphrase<input ref={passphrase} type="password" autoComplete="off" maxLength={8000} /></label> : null}
        </fieldset><SettingsTaskActions form={`${formId}-start`}><button className="primary" disabled={blocked || uncertain || Boolean(submittedStartId) || !confirmed}>Install and start Worker</button></SettingsTaskActions></form> : null}
        {text(data.state) === "UNCERTAIN" && data.credential_removed !== true ? <button disabled={blocked} onClick={() => void reconcile.send({ mutation: mutation() })}>Reconcile original setup</button> : null}
        {data.credential_removed !== true ? <button disabled={blocked} onClick={() => void cancel.send({ mutation: mutation() })}>Cancel setup and remove settled SSH credentials</button> : null}
        {result.running === true ? <p>Worker readiness confirmed: {text(result.worker_version)} · Machine {text(result.machine_id)}</p> : null}
        {text(data.problem_code) ? <p role="alert">Setup is unconfirmed ({text(data.problem_code)}). Reconcile the original operation before starting another installation.</p> : null}
        {data.cancellation_requested === true ? <p>Cancellation requested. A detached Worker keeps its independent lifetime; uncertain remote effects retain their original credentials until reconciled.</p> : null}
      </section> : null}
      {uncertain ? <p role="alert">The original start response is unconfirmed. Inspect the original setup; credentials are cleared and installation will not be sent again.</p> : null}
      {inspection.uncertain ? <button disabled={inspection.busy} onClick={inspection.retry}>Retry the same host inspection</button> : null}
    </>}
    <Problem error={status.error || currentRead.error || inspection.error || cancel.error || reconcile.error || error} />
  </SettingsTaskDialog> : null}</section>;
}
