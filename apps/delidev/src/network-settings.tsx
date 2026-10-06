// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useSettingsTaskDismiss } from "./settings-task-context";
import { useRef, useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, NetworkQuery, ResourceQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import type { PairingAuthority } from "./pairing-grant";
import { useSettingsOpening } from "./settings-lifetime";
import { Problem } from "./ui";
import { WorkerNetworkNative } from "./worker-network-native";
import { NativeRouteState, ProxyMode, ciphertextBase64, workerRecipient, workerRouteStatus, type WorkerRecipient } from "./worker-network";

// Reads and write-only drafts live only in this Settings visit. Closing the
// presentation never cancels an accepted server/native operation or retries it.
export function NetworkSettings({ active, machine = "", authority }: { active: boolean; machine?: string; authority?: PairingAuthority }) {
  const [open, setOpen] = useState(false);
  return <section aria-label={machine ? "Runner Device network" : "Server network"}><button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? "Hide network settings" : "Network settings"}</button>{open ? <SettingsTaskDialog title={machine ? "Runner Device network" : "Server network"} size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={() => setOpen(false)}><NetworkWorkspace active={active} machine={machine} authority={authority} /></SettingsTaskDialog> : null}</section>;
}
function NetworkWorkspace({ active, machine, authority }: { active: boolean; machine: string; authority?: PairingAuthority }) {
  const [page, setPage] = useState(""), [draft, setDraft] = useState<Resource | "new">();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const ready = status.data?.capabilities.includes(SystemCapability.SERVER_OUTBOUND_PROXY_V1) === true;
  const bootstrap = status.data?.capabilities.includes(SystemCapability.WORKER_NETWORK_BOOTSTRAP_V1) === true;
  const profiles = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.NETWORK_PROFILE, pageSize: 50, pageToken: page } }, { enabled: active && ready });
  const route = useQuery(NetworkQuery.getNetworkRoute, { machineId: machine }, { enabled: active && ready });
  const observation = useQuery(NetworkQuery.getWorkerNetworkStatus, { machineId: machine }, { enabled: active && bootstrap && Boolean(machine), refetchInterval: active && bootstrap && machine ? 5000 : false });
  const [selection, setSelection] = useState<Resource>();
  const [deleting, setDeleting] = useState<Resource>();
  const [selected, setSelected] = useState<string>("");
  const current = selection && (!route.data?.route || selection.revision >= route.data.route.revision) ? selection : route.data?.route;
  const currentData = document(current), currentProfile = object(currentData.profile);
  const changed = () => { void profiles.refetch(); void route.refetch(); if (machine) void observation.refetch(); };
  const select = useRetainedMutation(`network-select:${machine || "server"}`, NetworkQuery.selectNetworkProfile, result => { if (result.resource) setSelection(result.resource); changed(); });
  const remove = useRetainedMutation(`network-delete:${machine || "server"}`, NetworkQuery.deleteNetworkProfile, () => { setDeleting(undefined); changed(); });
  const pending = select.busy || select.uncertain || remove.busy || remove.uncertain;
  const observed = observation.data ? workerRouteStatus(observation.data.statusJson, machine) : undefined;
  const rows = profiles.data?.resources ?? [];
  const selectedRow = rows.find(row => row.id === selected);
  return <>
    <h3>{machine ? "Runner Device routing" : "Server outbound routing"}</h3>
    <p>Each selection freezes a profile revision. Editing a profile affects routing only after another explicit selection. Connection failures never fall back to Direct.</p>
    {status.data && !ready ? <p role="status">Update the selected server to use explicit network profiles.</p> : null}
    <Problem error={status.error || profiles.error || route.error || observation.error || select.error || remove.error} />
    {ready ? <>
      <p>Selected route: {text(currentProfile.name) || "Direct"} · Generation {current?.revision.toString() ?? "0"}{current && route.error ? " · Last successful read" : ""}</p>
      {machine && bootstrap ? observed ? <dl><dt>Desired generation</dt><dd>{observed.desired_generation}</dd><dt>Effective control generation</dt><dd>{observed.effective_generation} · {observed.control_state}</dd><dt>Native API generation</dt><dd>{observed.native_generation} · {observed.native_state}</dd></dl> : observation.data ? <p role="alert">Worker routing status is unreadable. Update the client/server and inspect the original route.</p> : <p role="status">Reading Worker route status…</p> : null}
      {observed?.control_state === NativeRouteState.Stale ? <p role="status">This Worker must reconcile its encrypted configuration before starting new work. Already running work keeps its original generation.</p> : null}
      {machine ? <p>Native status concerns supported Codex API execution and title traffic. Control readiness does not prove native routing or provider success.</p> : null}
      <form onSubmit={event => { event.preventDefault(); if (pending || route.error || !route.data || selected && !selectedRow) return; void select.send({ mutation: { requestId: newRequestId(), id: current?.id ?? "", expectedRevision: current?.revision ?? 0n }, machineId: machine, profileId: selected, profileRevision: selectedRow?.revision ?? 0n }); }}>
        <fieldset disabled={pending || profiles.isFetching || route.isFetching || Boolean(profiles.error || route.error)}><label>Profile to select<select value={selected} onChange={event => setSelected(event.target.value)}><option value="">Direct</option>{rows.map(row => <option key={row.id} value={row.id}>{resourceName(row)} · Revision {row.revision.toString()}</option>)}</select></label><button type="submit">Select this revision</button></fieldset>
      </form>
      <button type="button" disabled={pending} onClick={() => setDraft("new")}>New network profile</button>
      {rows.map(row => <article className="result" key={row.id}><h4>{resourceName(row)}</h4><p>{text(document(row).mode)} · Revision {row.revision.toString()}{text(document(row).credential_generation) ? " · Protected credential configured" : ""}</p><button disabled={pending} onClick={() => setDraft(row)}>Edit profile</button><button disabled={pending || text(currentData.profile_id) === row.id} onClick={() => setDeleting(row)}>Delete profile</button></article>)}
      {deleting ? <SettingsTaskDialog title="Delete network profile" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setDeleting(undefined)}><section aria-label="Confirm network profile deletion"><p>Delete {resourceName(deleting)} at revision {deleting.revision.toString()}? Profiles selected by any server or Runner Device cannot be deleted.</p><SettingsTaskActions><button disabled={pending} onClick={() => void remove.send({ mutation: { requestId: newRequestId(), id: deleting.id, expectedRevision: deleting.revision } })}>Confirm profile deletion</button><button data-settings-task-cancel disabled={pending} onClick={() => setDeleting(undefined)}>Keep profile</button></SettingsTaskActions></section></SettingsTaskDialog> : null}
      <nav aria-label="Network profile pages"><button disabled={!page || profiles.isFetching} onClick={() => { setPage(""); setSelected(""); }}>First page</button><button disabled={!profiles.data?.nextPageToken || profiles.isFetching} onClick={() => { setPage(profiles.data!.nextPageToken); setSelected(""); }}>Next page</button></nav>
      {draft ? <SettingsTaskDialog key={draft === "new" ? "new" : draft.id} title={draft === "new" ? "New network profile" : "Edit network profile"} size={SettingsDialogSize.Form} close={() => setDraft(undefined)}><ProfileEditor initial={draft === "new" ? undefined : draft} saved={() => { setDraft(undefined); changed(); }} close={() => setDraft(undefined)} /></SettingsTaskDialog> : null}
      <button disabled={profiles.isFetching || route.isFetching || observation.isFetching} onClick={changed}>Refresh routing</button>
      {select.uncertain ? <button disabled={select.busy} onClick={select.retry}>Retry original route selection</button> : null}{remove.uncertain ? <button disabled={remove.busy} onClick={remove.retry}>Retry original profile deletion</button> : null}
      {authority && bootstrap ? <EncryptedWorkerExport active={active} authority={authority} machine={machine} profiles={rows} /> : null}
      {authority && !bootstrap ? <p>Update the selected server to export recipient-encrypted Worker configurations.</p> : null}
    </> : null}
  </>;
}
function ProfileEditor({ initial, saved, close }: { initial?: Resource; saved: () => void; close: () => void }) {
  const formId = useId();
  const data = document(initial);
  const [name, setName] = useState(text(data.name)), [mode, setMode] = useState((data.mode ?? ProxyMode.Direct) as ProxyMode);
  const [host, setHost] = useState(text(data.host)), [port, setPort] = useState(String(data.port ?? ""));
  const [bypasses, setBypasses] = useState(items(data.bypass).map(object).map(row => ({ host: text(row.host), port: String(row.port ?? "") })));
  const [username, setUsername] = useState(""), [password, setPassword] = useState(""), [clear, setClear] = useState(false);
  const save = useRetainedMutation(`network-save:${initial?.id ?? "new"}`, NetworkQuery.saveNetworkProfile, () => { setUsername(""); setPassword(""); saved(); });
  const pending = save.busy || save.uncertain;
  useSettingsTaskDismiss(() => { setUsername(""); setPassword(""); });
  return <form id={formId} aria-label="Network profile editor" onSubmit={event => { event.preventDefault(); if (pending) return; const definition = { name, mode, ...(mode === ProxyMode.Direct ? {} : { host, port: Number(port), bypass: bypasses.map(row => ({ host: row.host, ...(row.port ? { port: Number(row.port) } : {}) })) }) }; void save.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(definition), ...(mode !== ProxyMode.Direct && (username || password) ? { credentialJson: encode({ username, password }) } : {}), clearCredential: mode !== ProxyMode.Direct && clear }); }}>
    <h4>{initial ? "Edit network profile" : "New network profile"}</h4><fieldset disabled={pending}>
      <label>Profile name<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} /></label>
      <label>Connection mode<select value={mode} onChange={event => setMode(event.target.value as ProxyMode)}>{Object.values(ProxyMode).map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      {mode !== ProxyMode.Direct ? <><label>Proxy host<input required maxLength={253} value={host} onChange={event => setHost(event.target.value)} /></label><label>Proxy port<input required type="number" min={1} max={65535} value={port} onChange={event => setPort(event.target.value)} /></label><p>Use a canonical host or IP. Bypasses match exact hosts, literal IPs or canonical CIDRs; optional ports narrow the match.</p>
        {bypasses.map((row, index) => <div key={index}><label>Bypass host or CIDR<input required maxLength={253} value={row.host} onChange={event => setBypasses(values => values.map((value, position) => position === index ? { ...value, host: event.target.value } : value))} /></label><label>Bypass port (optional)<input type="number" min={1} max={65535} value={row.port} onChange={event => setBypasses(values => values.map((value, position) => position === index ? { ...value, port: event.target.value } : value))} /></label><button type="button" onClick={() => setBypasses(values => values.filter((_, position) => position !== index))}>Remove bypass</button></div>)}
        <button type="button" disabled={bypasses.length >= 128} onClick={() => setBypasses(values => [...values, { host: "", port: "" }])}>Add exact bypass</button>
        <p>Blank credentials preserve an existing credential only if mode, host and port stay the same. Changing that destination clears its association unless replacement credentials are supplied.</p>
        <label>Proxy username<input autoComplete="off" maxLength={255} value={username} disabled={clear} onChange={event => setUsername(event.target.value)} /></label><label>Proxy password<input type="password" autoComplete="new-password" maxLength={255} value={password} disabled={clear} onChange={event => setPassword(event.target.value)} /></label><label><input type="checkbox" checked={clear} onChange={event => { setClear(event.target.checked); if (event.target.checked) { setUsername(""); setPassword(""); } }} />Clear protected credential association</label>
      </> : null}
    </fieldset><SettingsTaskActions form={formId}><button type="button" data-settings-task-cancel disabled={pending} onClick={close}>Cancel profile edit</button><button type="submit" className="primary" disabled={pending}>Save profile</button></SettingsTaskActions><Problem error={save.error} />{save.uncertain ? <><p role="status">The save outcome is unknown. Keep the original request or inspect the latest profile.</p><button disabled={save.busy} onClick={save.retry}>Retry original profile save</button></> : null}
  </form>;
}
function EncryptedWorkerExport({ active, authority, machine, profiles }: { active: boolean; authority: PairingAuthority; machine: string; profiles: Resource[] }) {
  const opening = useSettingsOpening();
  const [recipient, setRecipient] = useState<WorkerRecipient>(), [problem, setProblem] = useState("");
  const [profile, setProfile] = useState(""), [output, setOutput] = useState<{ ciphertext: string; digest: string; generation: string }>();
  const readGeneration = useRef(0);
  const selectedMachine = recipient?.authority.machine_id ?? "";
  const route = useQuery(NetworkQuery.getNetworkRoute, { machineId: selectedMachine }, { enabled: active && Boolean(recipient) });
  const selectedProfile = profiles.find(row => row.id === profile);
  const exporting = useRetainedMutation(`network-export:${machine || "pending"}`, NetworkQuery.exportWorkerNetworkBundle, response => { if (!response.route || !response.ciphertext.length || response.ciphertext.length > 96 << 10 || !/^[0-9a-f]{64}$/.test(response.ciphertextDigest)) { setProblem("Export was acknowledged without a valid encrypted result. Inspect the original request; no replacement was created."); return; } setOutput({ ciphertext: ciphertextBase64(response.ciphertext), digest: response.ciphertextDigest, generation: response.route.revision.toString() }); void route.refetch(); });
  const pending = exporting.busy || exporting.uncertain;
  return <section aria-label="Encrypted Worker configuration"><h4>Recipient-encrypted Worker configuration</h4><p>Prepare the public recipient on the intended Worker, then load that document here. Transfer the ciphertext and obtain the displayed digest separately from this authenticated connection. Import requires that exact digest and original protected key.</p>
    {machine ? <WorkerNetworkNative machine={machine} authority={authority} prepared={value => { ++readGeneration.current; setRecipient(value); setOutput(undefined); setProblem(""); }} /> : null}
    {recipient ? <label>Original public recipient JSON<textarea readOnly rows={4} value={JSON.stringify(recipient, null, 2)} onFocus={event => event.target.select()} /></label> : null}
    <label>Worker public recipient document<input type="file" accept="application/json,.json" disabled={pending} onChange={event => { const file = event.target.files?.[0]; const generation = ++readGeneration.current; setRecipient(undefined); setOutput(undefined); setProblem(""); if (!file) return; if (file.size > 16384) { setProblem("The public recipient document exceeds its limit."); return; } const read = async () => { try { const raw = opening ? await opening.native(() => file.text()) : await file.text(); if (opening?.disposed || generation !== readGeneration.current) return; const result = workerRecipient(JSON.parse(raw), authority, machine || undefined); if (!result) throw new Error("scope"); setRecipient(result); } catch { if (!opening?.disposed && generation === readGeneration.current) setProblem("This public recipient does not match the selected server and Runner Device."); } }; void read(); }} /></label>
    {recipient ? <><p>Recipient Runner Device: {recipient.authority.machine_id}</p>{route.data && !route.data.route ? <label>Initial Worker profile<select disabled={pending} value={profile} onChange={event => setProfile(event.target.value)}><option value="">Direct</option>{profiles.map(row => <option key={row.id} value={row.id}>{resourceName(row)} · Revision {row.revision.toString()}</option>)}</select></label> : null}<button disabled={pending || !route.data || Boolean(route.error) || profile !== "" && !selectedProfile} onClick={() => { if (!route.data || pending) return; const current = route.data.route; void exporting.send({ mutation: { requestId: newRequestId(), id: current?.id ?? "", expectedRevision: current?.revision ?? 0n }, machineId: recipient.authority.machine_id, deviceId: recipient.authority.device_id, pairingId: recipient.authority.pairing_id, endpoint: authority.endpoint, recipient: recipient.recipient, keyId: recipient.key_id, desiredGeneration: current?.revision ?? 0n, ...(!current ? { profileId: profile, profileRevision: selectedProfile?.revision ?? 0n } : {}) }); }}>Export current encrypted configuration</button></> : null}
    {output ? <><p>Exported generation {output.generation}. Import within five minutes.</p><label>Encrypted bundle (Base64)<textarea readOnly rows={4} spellCheck={false} value={output.ciphertext} onFocus={event => event.target.select()} /></label><label>Authenticated ciphertext digest<input readOnly value={output.digest} onFocus={event => event.target.select()} /></label><p>Copy the ciphertext to the intended Worker. Supply this digest independently to its import confirmation.</p></> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={route.error || exporting.error} />{exporting.uncertain ? <button disabled={exporting.busy} onClick={exporting.retry}>Retry original encrypted export</button> : null}
  </section>;
}
