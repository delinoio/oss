// SPDX-License-Identifier: Apache-2.0
import { DisclosureButton, DisclosureContent, DisclosureDensity, Disclosure, DisclosureSummary } from "./disclosure";
import { createPortal } from "react-dom";
import { SettingsTaskDismissButton } from "./settings-task";
import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale } from "./localization";
import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useSettingsTaskDismiss } from "./settings-task-context";
import { useLayoutEffect, useRef, useState, useId } from "react";
import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, NetworkQuery, ResourceQuery, SystemCapability, SystemQuery, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text } from "./documents";
import { MutationIntents, useRetainedMutation } from "./mutation";
import type { PairingAuthority } from "./pairing-grant";
import { SettingsLifetime, useSettingsOpening } from "./settings-lifetime";
import { Failure, Problem } from "./ui";
import { WorkerNetworkNative } from "./worker-network-native";
import { NativeRouteState, ProxyMode, ciphertextBase64, workerRecipient, workerRouteStatus, type WorkerRecipient } from "./worker-network";

import { ResourceChoice } from "./configuration-fields";
import { useResourceScrollQuery } from "./resource-scroll-query";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import "./network-settings.css";

// Reads and write-only drafts live only in this Settings visit. Closing the
// presentation never cancels an accepted server/native operation or retries it.
export function NetworkSettings({ active, machine = "", authority, onPresentationChange }: { active: boolean; machine?: string; authority?: PairingAuthority; onPresentationChange?: (open: boolean) => void }) {
  useLocale();
  const [open, setOpen] = useState(false);
  const workspaceId = useId();
  const disclosure = useRef<HTMLButtonElement>(null), workspace = useRef<HTMLDivElement>(null);
  const [headerActions, setHeaderActions] = useState<HTMLDivElement | null>(null);
  // Only the Runner Device modal makes the containing inventory inactive.
  useLayoutEffect(() => { onPresentationChange?.(Boolean(machine && open)); return () => onPresentationChange?.(false); }, [open, machine, onPresentationChange]);
  const close = () => {
    if (workspace.current?.contains(globalThis.document.activeElement)) disclosure.current?.focus({ preventScroll: true });
    setOpen(false);
  };
  const Trigger = machine ? "button" : DisclosureButton;
  const trigger = <Trigger density={machine ? undefined : DisclosureDensity.Settings} ref={disclosure} type="button" aria-label={machine && open ? copy("network-settings.hideNetworkSettings_b1aa7f") : copy("network-settings.networkSettings_600f22")} aria-expanded={open} aria-controls={machine ? undefined : workspaceId} onClick={() => open ? close() : setOpen(true)}>{machine && open ? copy("network-settings.hideNetworkSettings_b1aa7f") : copy("network-settings.networkSettings_600f22")}</Trigger>;
  return <section data-settings-search-target={machine ? undefined : "network"} className={machine ? undefined : "network-inline"} aria-label={machine ? copy("network-settings.runnerDeviceNetwork_1f2f36") : copy("network-settings.networkSettings_600f22")}>
    {machine ? trigger : <div className="network-disclosure-header">{trigger}<div ref={setHeaderActions} /></div>}
    {open ? machine ? <SettingsTaskDialog title={copy("network-settings.runnerDeviceNetwork_1f2f36")} size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={close}><NetworkWorkspace active={active} machine={machine} authority={authority} /></SettingsTaskDialog>
      // A plain nested lifetime keeps each profile dialog independently disposable.
      : <DisclosureContent id={workspaceId} ref={workspace}><SettingsLifetime>{() => <MutationIntents><NetworkWorkspace active={active} machine="" authority={authority} inline headerActions={headerActions} /></MutationIntents>}</SettingsLifetime></DisclosureContent> : null}
  </section>;
}
function NetworkWorkspace({ active, machine, authority, inline = false, headerActions }: { active: boolean; machine: string; authority?: PairingAuthority; inline?: boolean; headerActions?: HTMLElement | null }) {
  useLocale();
  const [draft, setDraft] = useState<Resource | "new">();
  const [deleting, setDeleting] = useState<Resource>();
  const content = useRef<HTMLDivElement>(null), root = useScrollRoot(content);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const ready = status.data?.capabilities.includes(SystemCapability.SERVER_OUTBOUND_PROXY_V1) === true;
  const bootstrap = status.data?.capabilities.includes(SystemCapability.WORKER_NETWORK_BOOTSTRAP_V1) === true;
  const inventoryActive = active && ready && !draft && !deleting;
  const profiles = useResourceScrollQuery(EntityKind.NETWORK_PROFILE, inventoryActive, machine, false, undefined, undefined, undefined, true);
  const route = useQuery(NetworkQuery.getNetworkRoute, { machineId: machine }, { enabled: active && ready });
  const observation = useQuery(NetworkQuery.getWorkerNetworkStatus, { machineId: machine }, { enabled: active && bootstrap && Boolean(machine), refetchInterval: active && bootstrap && machine ? 5000 : false });
  const [selection, setSelection] = useState<Resource>();
  const [selected, setSelected] = useState<string>("");
  const current = selection && (!route.data?.route || selection.revision >= route.data.route.revision) ? selection : route.data?.route;
  const currentData = document(current), currentProfile = object(currentData.profile);
  const refresh = () => { void status.refetch(); if (ready) changed(); };
  const changed = () => { profiles.refreshExplicit(); void route.refetch(); if (machine) void observation.refetch(); };
  const select = useRetainedMutation(`network-select:${machine || "server"}`, NetworkQuery.selectNetworkProfile, result => { if (result.resource) setSelection(result.resource); changed(); });
  const pending = select.busy || select.uncertain;
  const observed = observation.data ? workerRouteStatus(observation.data.statusJson, machine) : undefined;
  const [selectedRow, setSelectedRow] = useState<Resource>();
  const transport = useTransport(), client = useQueryClient(), opening = useSettingsOpening();
  const choiceGeneration = useRef(0);
  const [choiceBusy, setChoiceBusy] = useState(false), [choiceError, setChoiceError] = useState<unknown>();
  const chooseProfile = async (id: string) => {
    const generation = ++choiceGeneration.current;
    setSelected(id); setSelectedRow(undefined); setChoiceError(undefined);
    if (!id) { setChoiceBusy(false); return; }
    setChoiceBusy(true);
    try {
      const response = await client.fetchQuery({ ...createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.NETWORK_PROFILE, id }, { transport }), staleTime: 0, retry: false });
      if (opening?.disposed || choiceGeneration.current !== generation) return;
      const row = response.resource;
      if (!row || row.id !== id || row.kind !== EntityKind.NETWORK_PROFILE || !supportsResourceSchema(row)) throw new ConnectError("Selected network profile is unavailable", Code.NotFound);
      setSelectedRow(row);
    } catch (error) { if (!opening?.disposed && choiceGeneration.current === generation) setChoiceError(error); }
    finally { if (!opening?.disposed && choiceGeneration.current === generation) setChoiceBusy(false); }
  };
  return <div ref={content} className={inline ? "network-workspace" : undefined}>
    {inline && headerActions ? createPortal(<button disabled={status.isFetching || Boolean(profiles.loading) || route.isFetching} onClick={refresh}>{copy("network-settings.refreshRouting_8a54e5")}</button>, headerActions) : null}
    {inline ? <h3>{copy("network-settings.currentRoute")}</h3> : null}
    {!inline ? <h3>{machine ? copy("network-settings.runnerDeviceRouting_47784f") : copy("network-settings.serverOutboundRouting_dcedb5")}</h3> : null}
    {!inline ? <p>{copy("network-settings.eachSelectionFreezesAProfileRevision_f3a800")}</p> : null}
    {status.data && !ready ? <p role="status">{copy("network-settings.updateTheSelectedServerToUse_bda069")}</p> : null}
    <Problem error={status.error} summary={copy("network-settings.capabilityReadHelp")} />
    <Problem error={route.error} summary={copy("network-settings.routeReadHelp")} />
    <Problem error={observation.error} summary={copy("network-settings.workerStatusHelp")} />
    <Problem error={select.error} summary={copy("network-settings.selectionHelp")} />
    <Problem error={choiceError} summary={copy("network-settings.choiceReadHelp")} />
    <Failure failure={profiles.error?.failure} summary={copy("network-settings.profileReadHelp")} />
    {ready ? <>
      {current || route.data ? <p><LocalizedText id="network-settings.selectedRouteGeneration_b6c542" components={{ s0: <>{text(currentProfile.name) || copy("network-settings.extra.002c7c68468b")}</>, s1: <>{current?.revision.toString() ?? "0"}</>, s2: <>{(current || route.data) && route.error ? copy("network-settings.lastSuccessfulRead_759dd4") : ""}</> }} /></p> : <p role="status">{copy(route.error ? "network-settings.routeUnavailable" : "network-settings.readingRoute")}</p>}
      {machine && bootstrap ? observed ? <dl><dt>{copy("network-settings.desiredGeneration_f15f38")}</dt><dd>{observed.desired_generation}</dd><dt>{copy("network-settings.effectiveControlGeneration_e8082d")}</dt><dd>{observed.effective_generation} · {observed.control_state}</dd><dt>{copy("network-settings.nativeApiGeneration_02dc37")}</dt><dd>{observed.native_generation} · {observed.native_state}</dd></dl> : observation.data ? <div role="alert"><p>{copy("network-settings.workerRoutingStatusIsUnreadableUpdate_1920c7")}</p><p>{copy("network-settings.workerStatusHelp")}</p></div> : <p role="status">{copy("network-settings.readingWorkerRouteStatus_71a350")}</p> : null}
      {observed?.control_state === NativeRouteState.Stale ? <><p role="status">{copy("network-settings.thisWorkerMustReconcileItsEncrypted_476d45")}</p><p>{copy("network-settings.workerStaleHelp")}</p></> : null}
      {machine ? <p>{copy("network-settings.nativeStatusConcernsSupportedCodexApi_0d7631")}</p> : null}
      {inline ? <><h3>{copy("network-settings.serverOutboundRouting_dcedb5")}</h3><p>{copy("network-settings.eachSelectionFreezesAProfileRevision_f3a800")}</p></> : null}
      <form className={inline ? "network-selection" : undefined} onSubmit={event => { event.preventDefault(); if (pending || choiceBusy || choiceError || route.error || !route.data || selected && !selectedRow) return; void select.send({ mutation: { requestId: newRequestId(), id: current?.id ?? "", expectedRevision: current?.revision ?? 0n }, machineId: machine, profileId: selected, profileRevision: selectedRow?.revision ?? 0n }); }}>
        <fieldset disabled={pending || choiceBusy || !route.data || Boolean(profiles.loading) || route.isFetching || Boolean(profiles.error || route.error)}>{inline ? <label>{copy("network-settings.profileToSelect_7e2a31")}<select value={selected} disabled={!inventoryActive || pending} onChange={event => { void chooseProfile(event.target.value); }}><option value="">{copy("network-settings.direct_002c7c")}</option>{selectedRow && !profiles.rows.some(row => row.id === selectedRow.id) ? <option value={selectedRow.id}>{resourceName(selectedRow)} · {selectedRow.revision.toString()}</option> : null}{profiles.rows.map(row => <option key={row.id} value={row.id}>{selectedRow?.id === row.id ? resourceName(selectedRow) : row.name} · {(selectedRow?.id === row.id ? selectedRow.revision : row.revision).toString()}</option>)}</select></label> : <ResourceChoice kind={EntityKind.NETWORK_PROFILE} emptyLabel={copy("network-settings.direct_002c7c")} active={inventoryActive} label={copy("network-settings.profileToSelect_7e2a31")} value={selected} change={(id, _data, resource) => { setSelected(id); setSelectedRow(resource); }} disabled={pending} />}<button type="submit" disabled={Boolean(choiceError) || Boolean(selected && !selectedRow)}>{copy("network-settings.selectThisRevision_928177")}</button></fieldset>
      </form>
      <div className={inline ? "network-header" : undefined}>{inline ? <h3>{copy("network-settings.profiles")}</h3> : null}<button type="button" disabled={pending} onClick={() => setDraft("new")}>{copy("network-settings.newNetworkProfile_100d40")}</button></div>
      {inline && profiles.loaded && profiles.pages.length === 1 && !profiles.pages[0].token && !profiles.rows.length && !profiles.nextPageToken && !profiles.error ? <div className="network-empty"><p><strong>{copy("network-settings.noProfiles")}</strong></p><p>{copy("network-settings.createProfileHelp")}</p></div> : null}
      <ScrollPayloadWindow query={profiles} root={root} active={inventoryActive} identity={paginationIdentity} revision={paginationRevision}>{rows => rows.map(row => <article className={inline ? "network-profile-row" : "result"} key={row.id}><h4>{resourceName(row)}</h4><p><LocalizedText id="network-settings.revision_5209bc" components={{ s0: <>{text(document(row).mode)}</>, s1: <>{row.revision.toString()}</>, s2: <>{text(document(row).credential_generation) ? copy("network-settings.protectedCredentialConfigured_cf2bda") : ""}</> }} /></p><button disabled={pending} onClick={() => setDraft(row)}>{copy("network-settings.editProfile_15c4aa")}</button><button disabled={pending || text(currentData.profile_id) === row.id} onClick={() => setDeleting(row)}>{copy("network-settings.deleteProfile_47311a")}</button></article>)}</ScrollPayloadWindow>
      {deleting ? <SettingsTaskDialog title={copy("network-settings.deleteProfile_47311a")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setDeleting(undefined)}><ProfileDeletion row={deleting} machine={machine} close={() => setDeleting(undefined)} deleted={() => { setDeleting(undefined); changed(); }} /></SettingsTaskDialog> : null}
      <ScrollContinuation query={profiles} root={root} active={inventoryActive} label={copy("network-settings.networkProfilePages_752e3c")} />
      {draft ? <SettingsTaskDialog key={draft === "new" ? "new" : draft.id} title={draft === "new" ? copy("network-settings.newNetworkProfile_100d40") : copy("network-settings.editNetworkProfile_14453e")} size={SettingsDialogSize.Form} close={() => setDraft(undefined)}><ProfileEditor initial={draft === "new" ? undefined : draft} saved={() => { setDraft(undefined); changed(); }} close={() => setDraft(undefined)} /></SettingsTaskDialog> : null}
      {!inline ? <button disabled={Boolean(profiles.loading) || route.isFetching || observation.isFetching} onClick={changed}>{copy("network-settings.refreshRouting_8a54e5")}</button> : null}
      {select.uncertain ? <button disabled={select.busy} onClick={select.retry}>{copy("network-settings.retryOriginalRouteSelection_3cdea3")}</button> : null}
      {authority ? inline ? <Disclosure density={DisclosureDensity.Settings} className="network-transfer"><DisclosureSummary>{copy("network-settings.workerTransfer")}</DisclosureSummary>{bootstrap ? <EncryptedWorkerExport active={active} choicesActive={inventoryActive} authority={authority} machine={machine} /> : <p>{copy("network-settings.updateTheSelectedServerToExport_9ba35a")}</p>}</Disclosure> : bootstrap ? <EncryptedWorkerExport active={active} choicesActive={inventoryActive} authority={authority} machine={machine} /> : <p>{copy("network-settings.updateTheSelectedServerToExport_9ba35a")}</p> : null}
    </> : null}
  </div>;
}
function ProfileDeletion({ row, machine, close, deleted }: { row: Resource; machine: string; close: () => void; deleted: () => void }) {
  useLocale();
  // The confirmation owns its exact request. Dismissal never leaves a retry in
  // the still-open inline workspace or cancels an accepted deletion.
  const remove = useRetainedMutation(`network-delete:${machine || "server"}`, NetworkQuery.deleteNetworkProfile, deleted);
  const pending = remove.busy || remove.uncertain;
  return <section aria-label={copy("network-settings.confirmNetworkProfileDeletion_ef7d43")}><p><LocalizedText id="network-settings.deleteAtRevisionProfilesSelectedBy_d3d3f3" components={{ s0: <>{resourceName(row)}</>, s1: <>{row.revision.toString()}</> }} /></p><SettingsTaskActions><button disabled={pending} onClick={() => void remove.send({ mutation: { requestId: newRequestId(), id: row.id, expectedRevision: row.revision } })}>{copy("network-settings.confirmProfileDeletion_079ac8")}</button><SettingsTaskDismissButton data-settings-task-cancel disabled={pending} onClick={close}>{copy("network-settings.keepProfile_8e76f0")}</SettingsTaskDismissButton></SettingsTaskActions><Problem error={remove.error} summary={copy("network-settings.profileDeleteHelp")} />{remove.uncertain ? <button disabled={remove.busy} onClick={remove.retry}>{copy("network-settings.retryOriginalProfileDeletion_861363")}</button> : null}</section>;
}

function ProfileEditor({ initial, saved, close }: { initial?: Resource; saved: () => void; close: () => void }) {
  useLocale();
  const formId = useId();
  const data = document(initial);
  const [name, setName] = useState(text(data.name)), [mode, setMode] = useState((data.mode ?? ProxyMode.Direct) as ProxyMode);
  const [host, setHost] = useState(text(data.host)), [port, setPort] = useState(String(data.port ?? ""));
  const [bypasses, setBypasses] = useState(items(data.bypass).map(object).map(row => ({ host: text(row.host), port: String(row.port ?? "") })));
  const [username, setUsername] = useState(""), [password, setPassword] = useState(""), [clear, setClear] = useState(false);
  const save = useRetainedMutation(`network-save:${initial?.id ?? "new"}`, NetworkQuery.saveNetworkProfile, () => { setUsername(""); setPassword(""); saved(); });
  const pending = save.busy || save.uncertain;
  useSettingsTaskDismiss(() => { setUsername(""); setPassword(""); });
  return <form id={formId} aria-label={copy("network-settings.networkProfileEditor_838f1e")} onSubmit={event => { event.preventDefault(); if (pending) return; const definition = { name, mode, ...(mode === ProxyMode.Direct ? {} : { host, port: Number(port), bypass: bypasses.map(row => ({ host: row.host, ...(row.port ? { port: Number(row.port) } : {}) })) }) }; void save.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode(definition), ...(mode !== ProxyMode.Direct && (username || password) ? { credentialJson: encode({ username, password }) } : {}), clearCredential: mode !== ProxyMode.Direct && clear }); }}>
    <h4>{initial ? copy("network-settings.editNetworkProfile_14453e") : copy("network-settings.newNetworkProfile_100d40")}</h4><fieldset disabled={pending}>
      <label>{copy("network-settings.profileName_d36632")}<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} /></label>
      <label>{copy("network-settings.connectionMode_72c094")}<select value={mode} onChange={event => setMode(event.target.value as ProxyMode)}>{Object.values(ProxyMode).map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      {mode !== ProxyMode.Direct ? <><label>{copy("network-settings.proxyHost_669c36")}<input required maxLength={253} value={host} onChange={event => setHost(event.target.value)} /></label><label>{copy("network-settings.proxyPort_cec8ad")}<input required type="number" min={1} max={65535} value={port} onChange={event => setPort(event.target.value)} /></label><p>{copy("network-settings.useACanonicalHostOrIp_fcd2d6")}</p>
        {bypasses.map((row, index) => <div key={index}><label>{copy("network-settings.bypassHostOrCidr_def46c")}<input required maxLength={253} value={row.host} onChange={event => setBypasses(values => values.map((value, position) => position === index ? { ...value, host: event.target.value } : value))} /></label><label>{copy("network-settings.bypassPortOptional_5bae3d")}<input type="number" min={1} max={65535} value={row.port} onChange={event => setBypasses(values => values.map((value, position) => position === index ? { ...value, port: event.target.value } : value))} /></label><button type="button" onClick={() => setBypasses(values => values.filter((_, position) => position !== index))}>{copy("network-settings.removeBypass_37bd44")}</button></div>)}
        <button type="button" disabled={bypasses.length >= 128} onClick={() => setBypasses(values => [...values, { host: "", port: "" }])}>{copy("network-settings.addExactBypass_abaaf1")}</button>
        <p>{copy("network-settings.blankCredentialsPreserveAnExistingCredential_1227b5")}</p>
        <label>{copy("network-settings.proxyUsername_4d4135")}<input autoComplete="off" maxLength={255} value={username} disabled={clear} onChange={event => setUsername(event.target.value)} /></label><label>{copy("network-settings.proxyPassword_3978a1")}<input type="password" autoComplete="new-password" maxLength={255} value={password} disabled={clear} onChange={event => setPassword(event.target.value)} /></label><label><input type="checkbox" checked={clear} onChange={event => { setClear(event.target.checked); if (event.target.checked) { setUsername(""); setPassword(""); } }} />{copy("network-settings.clearProtectedCredentialAssociation_dc20a8")}</label>
      </> : null}
      <SettingsTaskActions form={formId}><SettingsTaskDismissButton type="button" data-settings-task-cancel disabled={pending} onClick={close}>{copy("network-settings.cancelProfileEdit_389c37")}</SettingsTaskDismissButton><button type="submit" className="primary" disabled={pending}>{copy("network-settings.saveProfile_0c8209")}</button></SettingsTaskActions>
    </fieldset><Problem error={save.error} summary={copy("network-settings.profileSaveHelp")} />{save.uncertain ? <><p role="status">{copy("network-settings.theSaveOutcomeIsUnknownKeep_27ff5b")}</p><button disabled={save.busy} onClick={save.retry}>{copy("network-settings.retryOriginalProfileSave_5f117b")}</button></> : null}
  </form>;
}
function EncryptedWorkerExport({ active, choicesActive, authority, machine }: { active: boolean; choicesActive: boolean; authority: PairingAuthority; machine: string }) {
  useLocale();
  const opening = useSettingsOpening();
  const [recipient, setRecipient] = useState<WorkerRecipient>(), [problem, setProblem] = useProductMessage("");
  const [profile, setProfile] = useState(""), [output, setOutput] = useState<{ ciphertext: string; digest: string; generation: string }>();
  const readGeneration = useRef(0);
  const selectedMachine = recipient?.authority.machine_id ?? "";
  const route = useQuery(NetworkQuery.getNetworkRoute, { machineId: selectedMachine }, { enabled: active && Boolean(recipient) });
  const [selectedProfile, setSelectedProfile] = useState<Resource>();
  const exporting = useRetainedMutation(`network-export:${machine || "pending"}`, NetworkQuery.exportWorkerNetworkBundle, response => { if (!response.route || !response.ciphertext.length || response.ciphertext.length > 96 << 10 || !/^[0-9a-f]{64}$/.test(response.ciphertextDigest)) { setProblem(ownedMessage("network-settings.extra.167cab7952f7")); return; } setOutput({ ciphertext: ciphertextBase64(response.ciphertext), digest: response.ciphertextDigest, generation: response.route.revision.toString() }); void route.refetch(); });
  const pending = exporting.busy || exporting.uncertain;
  return <section onInvalidCapture={event => { const details = event.currentTarget.closest("details"); if (details) details.open = true; }} aria-label={copy("network-settings.encryptedWorkerConfiguration_a60696")}><h4>{copy("network-settings.recipientEncryptedWorkerConfiguration_bfdb97")}</h4><p>{copy("network-settings.prepareThePublicRecipientOnThe_018e6f")}</p>
    {machine ? <WorkerNetworkNative machine={machine} authority={authority} prepared={value => { ++readGeneration.current; setRecipient(value); setOutput(undefined); setProblem(""); }} /> : null}
    {recipient ? <label>{copy("network-settings.originalPublicRecipientJson_3365ca")}<textarea readOnly rows={4} value={JSON.stringify(recipient, null, 2)} onFocus={event => event.target.select()} /></label> : null}
    <label>{copy("network-settings.workerPublicRecipientDocument_680bad")}<input type="file" accept="application/json,.json" disabled={pending} onChange={event => { const file = event.target.files?.[0]; const generation = ++readGeneration.current; setRecipient(undefined); setOutput(undefined); setProblem(""); if (!file) return; if (file.size > 16384) { setProblem(ownedMessage("network-settings.extra.65c55c94d0e0")); return; } const read = async () => { try { const raw = opening ? await opening.native(() => file.text()) : await file.text(); if (opening?.disposed || generation !== readGeneration.current) return; const result = workerRecipient(JSON.parse(raw), authority, machine || undefined); if (!result) throw new Error("scope"); setRecipient(result); } catch { if (!opening?.disposed && generation === readGeneration.current) setProblem(ownedMessage("network-settings.extra.8264d06ae667")); } }; void read(); }} /></label>
    {recipient ? <><p><LocalizedText id="network-settings.recipientRunnerDevice_cc7e62" components={{ s0: <>{recipient.authority.machine_id}</> }} /></p>{route.data && !route.data.route ? <ResourceChoice kind={EntityKind.NETWORK_PROFILE} emptyLabel={copy("network-settings.direct_002c7c")} active={choicesActive} disabled={pending} label={copy("network-settings.initialWorkerProfile_234823")} value={profile} change={(id, _data, resource) => { setProfile(id); setSelectedProfile(resource); }} /> : null}<button disabled={pending || !route.data || Boolean(route.error) || profile !== "" && !selectedProfile} onClick={() => { if (!route.data || pending) return; const current = route.data.route; void exporting.send({ mutation: { requestId: newRequestId(), id: current?.id ?? "", expectedRevision: current?.revision ?? 0n }, machineId: recipient.authority.machine_id, deviceId: recipient.authority.device_id, pairingId: recipient.authority.pairing_id, endpoint: authority.endpoint, recipient: recipient.recipient, keyId: recipient.key_id, desiredGeneration: current?.revision ?? 0n, ...(!current ? { profileId: profile, profileRevision: selectedProfile?.revision ?? 0n } : {}) }); }}>{copy("network-settings.exportCurrentEncryptedConfiguration_3dc0e1")}</button></> : null}
    {output ? <><p><LocalizedText id="network-settings.exportedGenerationImportWithinFiveMinutes_569eb9" components={{ s0: <>{output.generation}</> }} /></p><label>{copy("network-settings.encryptedBundleBase64_81471f")}<textarea readOnly rows={4} spellCheck={false} value={output.ciphertext} onFocus={event => event.target.select()} /></label><label>{copy("network-settings.authenticatedCiphertextDigest_3026f9")}<input readOnly value={output.digest} onFocus={event => event.target.select()} /></label><p>{copy("network-settings.copyTheCiphertextToTheIntended_37e10e")}</p></> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={route.error} summary={copy("network-settings.recipientRouteHelp")} />{recipient && route.error ? <button disabled={!active || pending || route.isFetching} onClick={() => void route.refetch()}>{copy("network-settings.recheckRecipientRoute")}</button> : null}<Problem error={exporting.error} summary={copy("network-settings.exportHelp")} />{exporting.uncertain ? <button disabled={exporting.busy} onClick={exporting.retry}>{copy("network-settings.retryOriginalEncryptedExport_e22883")}</button> : null}
  </section>;
}
