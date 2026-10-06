import { LocalizedText, copy, useLocale } from "./localization";
import { useRef, useState } from "react";
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
  useLocale();
  const [open, setOpen] = useState(false);
  return <section aria-label={machine ? copy("network-settings.runnerDeviceNetwork_1f2f36") : copy("network-settings.serverNetwork_1122d2")}><button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>{open ? copy("network-settings.hideNetworkSettings_b1aa7f") : copy("network-settings.networkSettings_600f22")}</button>{open ? <NetworkWorkspace active={active} machine={machine} authority={authority} /> : null}</section>;
}
function NetworkWorkspace({ active, machine, authority }: { active: boolean; machine: string; authority?: PairingAuthority }) {
  useLocale();
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
    <h3>{machine ? copy("network-settings.runnerDeviceRouting_47784f") : copy("network-settings.serverOutboundRouting_dcedb5")}</h3>
    <p>{copy("network-settings.eachSelectionFreezesAProfileRevision_f3a800")}</p>
    {status.data && !ready ? <p role="status">{copy("network-settings.updateTheSelectedServerToUse_bda069")}</p> : null}
    <Problem error={status.error || profiles.error || route.error || observation.error || select.error || remove.error} />
    {ready ? <>
      <p><LocalizedText id="network-settings.selectedRouteGeneration_b6c542" components={{ s0: <>{text(currentProfile.name) || "Direct"}</>, s1: <>{current?.revision.toString() ?? "0"}</>, s2: <>{current && route.error ? copy("network-settings.lastSuccessfulRead_759dd4") : ""}</> }} /></p>
      {machine && bootstrap ? observed ? <dl><dt>{copy("network-settings.desiredGeneration_f15f38")}</dt><dd>{observed.desired_generation}</dd><dt>{copy("network-settings.effectiveControlGeneration_e8082d")}</dt><dd>{observed.effective_generation} · {observed.control_state}</dd><dt>{copy("network-settings.nativeApiGeneration_02dc37")}</dt><dd>{observed.native_generation} · {observed.native_state}</dd></dl> : observation.data ? <p role="alert">{copy("network-settings.workerRoutingStatusIsUnreadableUpdate_1920c7")}</p> : <p role="status">{copy("network-settings.readingWorkerRouteStatus_71a350")}</p> : null}
      {observed?.control_state === NativeRouteState.Stale ? <p role="status">{copy("network-settings.thisWorkerMustReconcileItsEncrypted_476d45")}</p> : null}
      {machine ? <p>{copy("network-settings.nativeStatusConcernsSupportedCodexApi_0d7631")}</p> : null}
      <form onSubmit={event => { event.preventDefault(); if (pending || route.error || !route.data || selected && !selectedRow) return; void select.send({ mutation: { requestId: newRequestId(), id: current?.id ?? "", expectedRevision: current?.revision ?? 0n }, machineId: machine, profileId: selected, profileRevision: selectedRow?.revision ?? 0n }); }}>
        <fieldset disabled={pending || profiles.isFetching || route.isFetching || Boolean(profiles.error || route.error)}><label>{copy("network-settings.profileToSelect_7e2a31")}<select value={selected} onChange={event => setSelected(event.target.value)}><option value="">{copy("network-settings.direct_002c7c")}</option>{rows.map(row => <option key={row.id} value={row.id}><LocalizedText id="network-settings.revision_24230a" components={{ s0: <>{resourceName(row)}</>, s1: <>{row.revision.toString()}</> }} /></option>)}</select></label><button type="submit">{copy("network-settings.selectThisRevision_928177")}</button></fieldset>
      </form>
      <button type="button" disabled={pending} onClick={() => setDraft("new")}>{copy("network-settings.newNetworkProfile_100d40")}</button>
      {rows.map(row => <article className="result" key={row.id}><h4>{resourceName(row)}</h4><p><LocalizedText id="network-settings.revision_5209bc" components={{ s0: <>{text(document(row).mode)}</>, s1: <>{row.revision.toString()}</>, s2: <>{text(document(row).credential_generation) ? copy("network-settings.protectedCredentialConfigured_cf2bda") : ""}</> }} /></p><button disabled={pending} onClick={() => setDraft(row)}>{copy("network-settings.editProfile_15c4aa")}</button><button disabled={pending || text(currentData.profile_id) === row.id} onClick={() => setDeleting(row)}>{copy("network-settings.deleteProfile_47311a")}</button></article>)}
      {deleting ? <section aria-label={copy("network-settings.confirmNetworkProfileDeletion_ef7d43")}><p><LocalizedText id="network-settings.deleteAtRevisionProfilesSelectedBy_d3d3f3" components={{ s0: <>{resourceName(deleting)}</>, s1: <>{deleting.revision.toString()}</> }} /></p><button disabled={pending} onClick={() => void remove.send({ mutation: { requestId: newRequestId(), id: deleting.id, expectedRevision: deleting.revision } })}>{copy("network-settings.confirmProfileDeletion_079ac8")}</button><button disabled={pending} onClick={() => setDeleting(undefined)}>{copy("network-settings.keepProfile_8e76f0")}</button></section> : null}
      <nav aria-label={copy("network-settings.networkProfilePages_752e3c")}><button disabled={!page || profiles.isFetching} onClick={() => { setPage(""); setSelected(""); }}>{copy("network-settings.firstPage_0bdbb7")}</button><button disabled={!profiles.data?.nextPageToken || profiles.isFetching} onClick={() => { setPage(profiles.data!.nextPageToken); setSelected(""); }}>{copy("network-settings.nextPage_c08ac7")}</button></nav>
      {draft ? <ProfileEditor key={draft === "new" ? "new" : draft.id} initial={draft === "new" ? undefined : draft} saved={() => { setDraft(undefined); changed(); }} close={() => setDraft(undefined)} /> : null}
      <button disabled={profiles.isFetching || route.isFetching || observation.isFetching} onClick={changed}>{copy("network-settings.refreshRouting_8a54e5")}</button>
      {select.uncertain ? <button disabled={select.busy} onClick={select.retry}>{copy("network-settings.retryOriginalRouteSelection_3cdea3")}</button> : null}{remove.uncertain ? <button disabled={remove.busy} onClick={remove.retry}>{copy("network-settings.retryOriginalProfileDeletion_861363")}</button> : null}
      {authority && bootstrap ? <EncryptedWorkerExport active={active} authority={authority} machine={machine} profiles={rows} /> : null}
      {authority && !bootstrap ? <p>{copy("network-settings.updateTheSelectedServerToExport_9ba35a")}</p> : null}
    </> : null}
  </>;
}
function ProfileEditor({ initial, saved, close }: { initial?: Resource; saved: () => void; close: () => void }) {
  useLocale();
  const data = document(initial);
  const [name, setName] = useState(text(data.name)), [mode, setMode] = useState((data.mode ?? ProxyMode.Direct) as ProxyMode);
  const [host, setHost] = useState(text(data.host)), [port, setPort] = useState(String(data.port ?? ""));
  const [bypasses, setBypasses] = useState(items(data.bypass).map(object).map(row => ({ host: text(row.host), port: String(row.port ?? "") })));
  const [username, setUsername] = useState(""), [password, setPassword] = useState(""), [clear, setClear] = useState(false);
  const save = useRetainedMutation(`network-save:${initial?.id ?? "new"}`, NetworkQuery.saveNetworkProfile, () => { setUsername(""); setPassword(""); saved(); });
  const pending = save.busy || save.uncertain;
  return <form aria-label={copy("network-settings.networkProfileEditor_838f1e")} onSubmit={event => { event.preventDefault(); if (pending) return; const definition = { name, mode, ...(mode === ProxyMode.Direct ? {} : { host, port: Number(port), bypass: bypasses.map(row => ({ host: row.host, ...(row.port ? { port: Number(row.port) } : {}) })) }) }; void save.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(definition), ...(mode !== ProxyMode.Direct && (username || password) ? { credentialJson: encode({ username, password }) } : {}), clearCredential: mode !== ProxyMode.Direct && clear }); }}>
    <h4>{initial ? copy("network-settings.editNetworkProfile_14453e") : copy("network-settings.newNetworkProfile_100d40")}</h4><fieldset disabled={pending}>
      <label>{copy("network-settings.profileName_d36632")}<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} /></label>
      <label>{copy("network-settings.connectionMode_72c094")}<select value={mode} onChange={event => setMode(event.target.value as ProxyMode)}>{Object.values(ProxyMode).map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      {mode !== ProxyMode.Direct ? <><label>{copy("network-settings.proxyHost_669c36")}<input required maxLength={253} value={host} onChange={event => setHost(event.target.value)} /></label><label>{copy("network-settings.proxyPort_cec8ad")}<input required type="number" min={1} max={65535} value={port} onChange={event => setPort(event.target.value)} /></label><p>{copy("network-settings.useACanonicalHostOrIp_fcd2d6")}</p>
        {bypasses.map((row, index) => <div key={index}><label>{copy("network-settings.bypassHostOrCidr_def46c")}<input required maxLength={253} value={row.host} onChange={event => setBypasses(values => values.map((value, position) => position === index ? { ...value, host: event.target.value } : value))} /></label><label>{copy("network-settings.bypassPortOptional_5bae3d")}<input type="number" min={1} max={65535} value={row.port} onChange={event => setBypasses(values => values.map((value, position) => position === index ? { ...value, port: event.target.value } : value))} /></label><button type="button" onClick={() => setBypasses(values => values.filter((_, position) => position !== index))}>{copy("network-settings.removeBypass_37bd44")}</button></div>)}
        <button type="button" disabled={bypasses.length >= 128} onClick={() => setBypasses(values => [...values, { host: "", port: "" }])}>{copy("network-settings.addExactBypass_abaaf1")}</button>
        <p>{copy("network-settings.blankCredentialsPreserveAnExistingCredential_1227b5")}</p>
        <label>{copy("network-settings.proxyUsername_4d4135")}<input autoComplete="off" maxLength={255} value={username} disabled={clear} onChange={event => setUsername(event.target.value)} /></label><label>{copy("network-settings.proxyPassword_3978a1")}<input type="password" autoComplete="new-password" maxLength={255} value={password} disabled={clear} onChange={event => setPassword(event.target.value)} /></label><label><input type="checkbox" checked={clear} onChange={event => { setClear(event.target.checked); if (event.target.checked) { setUsername(""); setPassword(""); } }} />{copy("network-settings.clearProtectedCredentialAssociation_dc20a8")}</label>
      </> : null}
      <button type="submit">{copy("network-settings.saveProfile_0c8209")}</button><button type="button" onClick={close}>{copy("network-settings.cancelProfileEdit_389c37")}</button>
    </fieldset><Problem error={save.error} />{save.uncertain ? <><p role="status">{copy("network-settings.theSaveOutcomeIsUnknownKeep_27ff5b")}</p><button disabled={save.busy} onClick={save.retry}>{copy("network-settings.retryOriginalProfileSave_5f117b")}</button></> : null}
  </form>;
}
function EncryptedWorkerExport({ active, authority, machine, profiles }: { active: boolean; authority: PairingAuthority; machine: string; profiles: Resource[] }) {
  useLocale();
  const opening = useSettingsOpening();
  const [recipient, setRecipient] = useState<WorkerRecipient>(), [problem, setProblem] = useState("");
  const [profile, setProfile] = useState(""), [output, setOutput] = useState<{ ciphertext: string; digest: string; generation: string }>();
  const readGeneration = useRef(0);
  const selectedMachine = recipient?.authority.machine_id ?? "";
  const route = useQuery(NetworkQuery.getNetworkRoute, { machineId: selectedMachine }, { enabled: active && Boolean(recipient) });
  const selectedProfile = profiles.find(row => row.id === profile);
  const exporting = useRetainedMutation(`network-export:${machine || "pending"}`, NetworkQuery.exportWorkerNetworkBundle, response => { if (!response.route || !response.ciphertext.length || response.ciphertext.length > 96 << 10 || !/^[0-9a-f]{64}$/.test(response.ciphertextDigest)) { setProblem("Export was acknowledged without a valid encrypted result. Inspect the original request; no replacement was created."); return; } setOutput({ ciphertext: ciphertextBase64(response.ciphertext), digest: response.ciphertextDigest, generation: response.route.revision.toString() }); void route.refetch(); });
  const pending = exporting.busy || exporting.uncertain;
  return <section aria-label={copy("network-settings.encryptedWorkerConfiguration_a60696")}><h4>{copy("network-settings.recipientEncryptedWorkerConfiguration_bfdb97")}</h4><p>{copy("network-settings.prepareThePublicRecipientOnThe_018e6f")}</p>
    {machine ? <WorkerNetworkNative machine={machine} authority={authority} prepared={value => { ++readGeneration.current; setRecipient(value); setOutput(undefined); setProblem(""); }} /> : null}
    {recipient ? <label>{copy("network-settings.originalPublicRecipientJson_3365ca")}<textarea readOnly rows={4} value={JSON.stringify(recipient, null, 2)} onFocus={event => event.target.select()} /></label> : null}
    <label>{copy("network-settings.workerPublicRecipientDocument_680bad")}<input type="file" accept="application/json,.json" disabled={pending} onChange={event => { const file = event.target.files?.[0]; const generation = ++readGeneration.current; setRecipient(undefined); setOutput(undefined); setProblem(""); if (!file) return; if (file.size > 16384) { setProblem("The public recipient document exceeds its limit."); return; } const read = async () => { try { const raw = opening ? await opening.native(() => file.text()) : await file.text(); if (opening?.disposed || generation !== readGeneration.current) return; const result = workerRecipient(JSON.parse(raw), authority, machine || undefined); if (!result) throw new Error("scope"); setRecipient(result); } catch { if (!opening?.disposed && generation === readGeneration.current) setProblem("This public recipient does not match the selected server and Runner Device."); } }; void read(); }} /></label>
    {recipient ? <><p><LocalizedText id="network-settings.recipientRunnerDevice_cc7e62" components={{ s0: <>{recipient.authority.machine_id}</> }} /></p>{route.data && !route.data.route ? <label>{copy("network-settings.initialWorkerProfile_234823")}<select disabled={pending} value={profile} onChange={event => setProfile(event.target.value)}><option value="">{copy("network-settings.direct_002c7c")}</option>{profiles.map(row => <option key={row.id} value={row.id}><LocalizedText id="network-settings.revision_24230a" components={{ s0: <>{resourceName(row)}</>, s1: <>{row.revision.toString()}</> }} /></option>)}</select></label> : null}<button disabled={pending || !route.data || Boolean(route.error) || profile !== "" && !selectedProfile} onClick={() => { if (!route.data || pending) return; const current = route.data.route; void exporting.send({ mutation: { requestId: newRequestId(), id: current?.id ?? "", expectedRevision: current?.revision ?? 0n }, machineId: recipient.authority.machine_id, deviceId: recipient.authority.device_id, pairingId: recipient.authority.pairing_id, endpoint: authority.endpoint, recipient: recipient.recipient, keyId: recipient.key_id, desiredGeneration: current?.revision ?? 0n, ...(!current ? { profileId: profile, profileRevision: selectedProfile?.revision ?? 0n } : {}) }); }}>{copy("network-settings.exportCurrentEncryptedConfiguration_3dc0e1")}</button></> : null}
    {output ? <><p><LocalizedText id="network-settings.exportedGenerationImportWithinFiveMinutes_569eb9" components={{ s0: <>{output.generation}</> }} /></p><label>{copy("network-settings.encryptedBundleBase64_81471f")}<textarea readOnly rows={4} spellCheck={false} value={output.ciphertext} onFocus={event => event.target.select()} /></label><label>{copy("network-settings.authenticatedCiphertextDigest_3026f9")}<input readOnly value={output.digest} onFocus={event => event.target.select()} /></label><p>{copy("network-settings.copyTheCiphertextToTheIntended_37e10e")}</p></> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={route.error || exporting.error} />{exporting.uncertain ? <button disabled={exporting.busy} onClick={exporting.retry}>{copy("network-settings.retryOriginalEncryptedExport_e22883")}</button> : null}
  </section>;
}
