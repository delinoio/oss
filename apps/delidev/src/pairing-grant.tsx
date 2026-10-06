import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
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
  useLocale();
  const [editing, setEditing] = useState(false), [name, setName] = useState("");
  const [kind, setKind] = useState(PairingKind.Client);
  const [attempt, setAttempt] = useState<Attempt>();
  const original = useRef<Attempt>(undefined);
  const [accepted, setAccepted] = useState<Resource | "unknown">();
  const [revealed, setRevealed] = useState(false), [preparing, setPreparing] = useState(false);
  const [problem, setProblem] = useProductMessage("");
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
    if (!name || new TextEncoder().encode(name).byteLength > 256 || name.includes("\0")) { setProblem(ownedMessage("pairing-grant.extra.f8ed54ba4886")); return; }
    if (!canonicalId.test(authority.serverId)) { setProblem(ownedMessage("pairing-grant.extra.13e686c6b6c8")); return; }
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
    } catch { if (alive.current) setProblem(ownedMessage("pairing-grant.extra.25c7a1082af4")); }
    finally { gate.current = false; if (alive.current) setPreparing(false); }
  };
  const discard = () => { original.current = undefined; setAttempt(undefined); setAccepted(undefined); setRevealed(false); setName(""); setProblem(""); focusTrigger.current = true; setEditing(false); };
  const json = revealable && attempt && initial ? JSON.stringify({ version: 1, pairing_id: initial.id, server_id: attempt.authority.serverId, endpoint: attempt.authority.endpoint, code: attempt.code }, null, 2) : "";
  const createAction = <button type="button" ref={trigger} className="primary" onClick={() => { focusName.current = true; setEditing(true); }}><LocalizedText id="pairing-grant.createPairingDocument_888619" components={{ s0: <span aria-hidden="true">+ </span> }} /></button>;
  return <section aria-label={copy("pairing-grant.devicePairing_8e0d01")} className={editing ? "paired-device-form" : undefined}>
    {!editing ? triggerContainer === undefined ? createAction : triggerContainer ? createPortal(createAction, triggerContainer) : null : <>
      <h3>{copy("pairing-grant.pairAnotherDevice_7ceef1")}</h3><p>{copy("pairing-grant.thisSingleUseDocumentAuthorizesOne_61614e")}</p><p><LocalizedText id="pairing-grant.server_90ff00" components={{ s0: <>{authority.endpoint}</> }} /></p>
      {!accepted ? <form onSubmit={(event) => { event.preventDefault(); void submit(); }}><fieldset disabled={blocked || Boolean(attempt)}><label>{copy("pairing-grant.deviceName_155106")}<input ref={nameInput} value={name} onChange={(event) => setName(event.target.value)} autoComplete="off" /></label><label>{copy("pairing-grant.deviceType_8562a3")}<select value={kind} onChange={(event) => setKind(event.target.value as PairingKind)}><option value={PairingKind.Client}>{copy("pairing-grant.desktopClient_cfe9ad")}</option><option value={PairingKind.Worker}>{copy("pairing-grant.worker_a67b04")}</option></select></label></fieldset><button disabled={blocked || Boolean(attempt) || changed}>{copy("pairing-grant.issueSingleUseDocument_376ab7")}</button></form> : null}
      {accepted === "unknown" ? <p role="alert">{copy("pairing-grant.issuanceWasAcknowledgedWithoutAMatching_647b8d")}</p> : initial ? <><p><LocalizedText id="pairing-grant.expires_7c578e" components={{ s0: <>{initial.expiresAt}</> }} /></p><p role="status">{used ? copy("pairing-grant.pairingDocumentWasUsedItsPrivate_ff1ab5") : expired ? copy("pairing-grant.pairingDocumentExpiredItsPrivateCode_63e0c9") : latest && !current.error && !inconsistent ? copy("pairing-grant.singleUseGrantIssuedTheLatest_d0a05f") : copy("pairing-grant.grantIssuedCurrentUseStatusIs_9d25c2")}</p><Problem error={current.error} />{inconsistent ? <p role="alert">{copy("pairing-grant.theGrantObservationNoLongerMatches_e3cf5d")}</p> : null}<button disabled={current.isFetching} onClick={() => void current.refetch()}>{copy("pairing-grant.refreshPairingStatus_08e298")}</button>{revealable ? <button onClick={() => setRevealed((value) => !value)}>{revealed ? copy("pairing-grant.hidePrivateDocument_5c1efa") : copy("pairing-grant.revealPrivateDocument_9353b2")}</button> : null}{revealed && revealable ? <><label>{copy("pairing-grant.privatePairingDocument_55ef2e")}<textarea ref={output} readOnly value={json} rows={8} spellCheck={false} /></label><button onClick={() => { output.current?.focus(); output.current?.select(); }}>{copy("pairing-grant.selectPrivateDocument_93a9e3")}</button><p>{copy("pairing-grant.copyTheSelectedDocumentToThe_0ea9b2")}</p></> : null}</> : null}
      {changed ? <p role="alert">{copy("pairing-grant.theSelectedServerChangedThisOriginal_fe768e")}</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
      {mutation.uncertain ? <button disabled={mutation.busy || changed} onClick={mutation.retry}>{copy("pairing-grant.retryOriginalPairingIssuance_519736")}</button> : null}<button disabled={blocked} onClick={discard}>{attempt ? copy("pairing-grant.discardPrivatePairingDocument_00f569") : copy("pairing-grant.cancelPairing_b0fcb1")}</button>{attempt ? <p>{copy("pairing-grant.discardingClearsThisWindowSPrivate_9c7ba6")}</p> : null}
    </>}
  </section>;
}
