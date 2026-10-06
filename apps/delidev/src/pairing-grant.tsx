import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize } from "./settings-task";
import { useEffect, useLayoutEffect, useRef, useState, useId } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@connectrpc/connect-query";
import { DeviceQuery, DeviceType, EntityKind, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export interface PairingAuthority { endpoint: string; serverId: string }
interface Attempt { requestId: string; name: string; type: DeviceType; code: string; authority: PairingAuthority }
enum PairingKind { Client = "client", Worker = "worker" }
const canonicalId = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
function grantDetails(resource: Resource | undefined, attempt: Attempt) {
  const value = document(resource);
  if (!resource || resource.kind !== EntityKind.PAIRING || !canonicalId.test(resource.id) || resource.schemaVersion !== 1 || value.name !== attempt.name || value.type !== (attempt.type === DeviceType.CLIENT ? PairingKind.Client : PairingKind.Worker) || !Number.isFinite(Date.parse(text(value.expires_at))) || (value.used_by !== undefined && !canonicalId.test(text(value.used_by)))) return;
  return { id: resource.id, expiresAt: text(value.expires_at), usedBy: text(value.used_by) };
}

// Raw grant material stays in one opening-owned component, never mutation
// variables, query keys, Web Storage or logs. Only the digest crosses CreatePairing.
export function PairingGrant({ authority, active, triggerContainer }: { authority: PairingAuthority; active: boolean; triggerContainer?: HTMLElement | null }) {
  const [editing, setEditing] = useState(false), [name, setName] = useState("");
  const [kind, setKind] = useState(PairingKind.Client);
  const [attempt, setAttempt] = useState<Attempt>();
  const formId = useId();
  const original = useRef<Attempt>(undefined);
  const [accepted, setAccepted] = useState<Resource | "unknown">();
  const [revealed, setRevealed] = useState(false), [preparing, setPreparing] = useState(false);
  const [problem, setProblem] = useState("");
  const [now, setNow] = useState(Date.now);
  const alive = useRef(false), gate = useRef(false);
  const output = useRef<HTMLTextAreaElement>(null);
  const nameInput = useRef<HTMLInputElement>(null), trigger = useRef<HTMLButtonElement>(null);
  const focusName = useRef(false), focusTrigger = useRef(false);
  useLayoutEffect(() => {
    if (!active) return;
    if (editing && focusName.current && nameInput.current) { focusName.current = false; nameInput.current.focus(); }
    if (!editing && focusTrigger.current && trigger.current) { focusTrigger.current = false; trigger.current.focus(); }
  }, [active, editing, triggerContainer]);
  useEffect(() => { alive.current = true; return () => { alive.current = false; original.current = undefined; }; }, []);
  useEffect(() => { if (!active) setRevealed(false); }, [active]);
  const mutation = useRetainedMutation("create-device-pairing", DeviceQuery.createPairing, (result) => {
    const current = original.current;
    setAccepted(current && result.requestId === current.requestId && grantDetails(result.pairing, current) ? result.pairing : "unknown");
  });
  const initial = attempt && accepted && accepted !== "unknown" ? grantDetails(accepted, attempt) : undefined;
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.PAIRING, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active && initial ? 5000 : false });
  const latest = attempt && current.data?.resource ? grantDetails(current.data.resource, attempt) : undefined;
  const changed = Boolean(attempt && (attempt.authority.endpoint !== authority.endpoint || attempt.authority.serverId !== authority.serverId));
  const inconsistent = Boolean(initial && current.data && (!latest || latest.id !== initial.id || latest.expiresAt !== initial.expiresAt));
  const expired = Boolean(initial && Date.parse(initial.expiresAt) <= now);
  const used = Boolean(latest?.usedBy);
  useEffect(() => {
    if (!initial) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [initial?.id]);
  useEffect(() => {
    if (!expired && !used) return;
    setRevealed(false);
    if (original.current) original.current = { ...original.current, code: "" };
    setAttempt((value) => value ? { ...value, code: "" } : value);
  }, [expired, used]);
  const blocked = preparing || mutation.busy || mutation.uncertain;
  const revealable = active && initial && latest && !changed && !inconsistent && !current.error && !expired && !used && Boolean(attempt?.code);
  const submit = async () => {
    if (gate.current || attempt || !active || !alive.current) return;
    if (!name || new TextEncoder().encode(name).byteLength > 256 || name.includes("\0")) { setProblem("Enter a device name of at most 256 UTF-8 bytes without NUL characters."); return; }
    if (!canonicalId.test(authority.serverId)) { setProblem("Verify the selected server before creating a pairing document."); return; }
    gate.current = true; setPreparing(true); setProblem("");
    try {
      const random = crypto.getRandomValues(new Uint8Array(32));
      const code = btoa(String.fromCharCode(...random)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
      random.fill(0);
      const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(code)));
      if (!alive.current) return;
      const value: Attempt = { requestId: newRequestId(), name, type: kind === PairingKind.Client ? DeviceType.CLIENT : DeviceType.WORKER, code, authority: { ...authority } };
      original.current = value; setAttempt(value);
      await mutation.send({ requestId: value.requestId, name: value.name, type: value.type, codeDigest: digest });
    } catch { if (alive.current) setProblem("Secure pairing material could not be prepared. Use the trusted desktop app and try again."); }
    finally { gate.current = false; if (alive.current) setPreparing(false); }
  };
  const discard = () => { original.current = undefined; setAttempt(undefined); setAccepted(undefined); setRevealed(false); setName(""); setProblem(""); focusTrigger.current = true; setEditing(false); };
  const json = revealable && attempt && initial ? JSON.stringify({ version: 1, pairing_id: initial.id, server_id: attempt.authority.serverId, endpoint: attempt.authority.endpoint, code: attempt.code }, null, 2) : "";
  const createAction = <button type="button" ref={trigger} className="primary" onClick={() => { focusName.current = true; setEditing(true); }}><span aria-hidden="true">+ </span>Create pairing document</button>;
  return <section aria-label="Device pairing" className={editing ? "paired-device-form" : undefined}>
    {triggerContainer === undefined ? createAction : triggerContainer ? createPortal(createAction, triggerContainer) : null}
    {editing ? <SettingsTaskDialog title="Pair another device" size={SettingsDialogSize.Wide} retained={preparing || mutation.busy || mutation.uncertain || Boolean(attempt || accepted)} onDismiss={() => setRevealed(false)} close={discard}>
      <h3>Pair another device</h3><p>This single-use document authorizes one desktop client or manually installed Worker. Share it only with your intended device; it expires five minutes after server issuance.</p><p>Server: {authority.endpoint}</p>
      {!accepted ? <form id={formId} onSubmit={(event) => { event.preventDefault(); void submit(); }}><fieldset disabled={blocked || Boolean(attempt)}><label>Device name<input ref={nameInput} value={name} onChange={(event) => setName(event.target.value)} autoComplete="off" /></label><label>Device type<select value={kind} onChange={(event) => setKind(event.target.value as PairingKind)}><option value={PairingKind.Client}>Desktop client</option><option value={PairingKind.Worker}>Worker</option></select></label></fieldset><SettingsTaskActions form={formId}><button className="primary" disabled={blocked || Boolean(attempt) || changed}>Issue single-use document</button></SettingsTaskActions></form> : null}
      {accepted === "unknown" ? <p role="alert">Issuance was acknowledged without a matching grant. Inspect the original pairing request; no replacement was issued.</p> : initial ? <><p>Expires: {initial.expiresAt}</p><p role="status">{used ? "Pairing document was used. Its private code has been cleared." : expired ? "Pairing document expired. Its private code has been cleared." : latest && !current.error && !inconsistent ? "Single-use grant issued; the latest read has no paired device." : "Grant issued; current use status is unavailable."}</p><Problem error={current.error} />{inconsistent ? <p role="alert">The grant observation no longer matches the original issuance.</p> : null}<button disabled={current.isFetching} onClick={() => void current.refetch()}>Refresh pairing status</button>{revealable ? <button onClick={() => setRevealed((value) => !value)}>{revealed ? "Hide private document" : "Reveal private document"}</button> : null}{revealed && revealable ? <><label>Private pairing document<textarea ref={output} readOnly value={json} rows={8} spellCheck={false} /></label><button onClick={() => { output.current?.focus(); output.current?.select(); }}>Select private document</button><p>Copy the selected document to the intended device. It remains private when this settings area closes.</p></> : null}</> : null}
      {changed ? <p role="alert">The selected server changed. This original grant cannot be shared or retried through another connection.</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
      <SettingsTaskActions>{mutation.uncertain ? <button disabled={mutation.busy || changed} onClick={mutation.retry}>Retry original pairing issuance</button> : null}<button type="button" data-settings-task-cancel={attempt ? undefined : true} disabled={blocked} onClick={discard}>{attempt ? "Discard private pairing document" : "Cancel pairing"}</button></SettingsTaskActions>{attempt ? <p>Discarding clears this window's private copy; an already-issued grant remains valid until used or expired. Closing this server window also discards the copy.</p> : null}
    </SettingsTaskDialog> : null}
  </section>;
}
