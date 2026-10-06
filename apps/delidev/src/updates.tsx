import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { InstallationQuery, SystemQuery, SystemCapability, UpdateComponent, UpdateTarget, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
export enum NativeUpdateAction { Prepare = "prepare", Install = "install", Inspect = "inspect" }
export enum NativeUpdatePhase { Prepared = "prepared", Installing = "installing", Installed = "installed", Failed = "failed", Uncertain = "uncertain" }
export type NativeUpdateResult = { operation_id: string; release_version: string; phase: NativeUpdatePhase };
export type DesktopUpdateControls = { readContext: () => Promise<{ current_version: string; target: string }>; control: (action: NativeUpdateAction, id: string, revision: bigint) => Promise<NativeUpdateResult> };
const targets: Record<string, UpdateTarget> = { "darwin-amd64": UpdateTarget.DARWIN_AMD64, "darwin-arm64": UpdateTarget.DARWIN_ARM64, "windows-amd64": UpdateTarget.WINDOWS_AMD64, "windows-arm64": UpdateTarget.WINDOWS_ARM64, "linux-amd64": UpdateTarget.LINUX_AMD64, "linux-arm64": UpdateTarget.LINUX_ARM64 };
function UpdateCandidate({ current, active, worker, controls }: { current: Resource; active: boolean; worker?: boolean; controls?: DesktopUpdateControls }) {
  useLocale();
  const [local, setLocal] = useState<NativeUpdateResult>(), [busy, setBusy] = useState(false), [uncertain, setUncertain] = useState(false), [error, setError] = useState<unknown>();
  const alive = useRef(true); useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const read = useQuery(InstallationQuery.getUpdate, { id: current.id }, { enabled: active, refetchInterval: active && worker ? 3000 : false });
  const row = read.data?.update && read.data.update.revision >= current.revision ? read.data.update : current, data = document(row);
  const request = useRetainedMutation(`update:request:${row.id}`, InstallationQuery.requestWorkerUpdate, () => { void read.refetch(); });
  const cancel = useRetainedMutation(`update:cancel:${row.id}`, InstallationQuery.cancelUpdate, () => { void read.refetch(); });
  const native = async (action: NativeUpdateAction) => {
    if (!active || !controls || busy || (uncertain && action !== NativeUpdateAction.Inspect)) return;
    setBusy(true); setError(undefined);
    try { const result = await controls.control(action, row.id, row.revision); if (result.operation_id !== row.id || result.release_version !== text(data.version)) throw new Error("Update identity changed"); if (alive.current) { setLocal(result); setUncertain(false); } }
    catch (error) { if (alive.current) { setError(error); setUncertain(true); } }
    finally { if (alive.current) setBusy(false); }
  };
  const mutation = () => ({ id: row.id, expectedRevision: row.revision, requestId: newRequestId() });
  return <section aria-label={copy("updates.originalUpdate_9f056a")}><p><LocalizedText id="updates.delidev_a214b0" components={{ s0: <>{text(data.version)}</>, s1: <>{text(data.target)}</>, s2: <>{text(data.state)}</> }} /></p><p><LocalizedText id="updates.originalUpdateRevision_bff787" components={{ s0: <code>{row.id}</code>, s1: <>{row.revision.toString()}</> }} /></p>
    {worker ? <><p>{copy("updates.updatesWaitForActiveWorkTerminals_ac7bf1")}</p>{text(data.state) === "OBSERVED" ? <button disabled={!active || request.busy || request.uncertain} onClick={() => void request.send({ mutation: mutation() })}>{copy("updates.updateWorkerWhenIdle_50f590")}</button> : null}{["OBSERVED", "WAITING_FOR_IDLE"].includes(text(data.state)) ? <button disabled={!active || cancel.busy || cancel.uncertain} onClick={() => void cancel.send({ mutation: mutation() })}>{copy("updates.cancelUnclaimedUpdate_d10b89")}</button> : null}</> : <><p>{copy("updates.installationRequiresConfirmationOnThisComputer_828091")}</p>{!local && !uncertain ? <button disabled={busy || !active} onClick={() => void native(NativeUpdateAction.Prepare)}>{copy("updates.downloadVerifiedDesktopUpdate_0dd9ad")}</button> : null}{local?.phase === NativeUpdatePhase.Prepared && !uncertain ? <button disabled={busy || !active} onClick={() => void native(NativeUpdateAction.Install)}>{copy("updates.reviewAndInstallDesktopUpdate_2f844d")}</button> : null}{local ? <p role="status"><LocalizedText id="updates.desktopInstallation_cc279d" components={{ s0: <>{local.phase}</>, s1: <>{local.phase === NativeUpdatePhase.Installed ? copy("updates.restartDelidevWhenReady_f8939d") : ""}</> }} /></p> : null}{uncertain ? <p role="alert">{copy("updates.theOriginalInstallationResponseIsUnconfirmed_34e5ba")}</p> : null}<button disabled={busy || !active} onClick={() => void native(NativeUpdateAction.Inspect)}>{copy("updates.inspectOriginalDesktopInstallation_5849fa")}</button></>}
    {request.uncertain ? <button disabled={request.busy || !active} onClick={request.retry}>{copy("updates.inspectTheSameWorkerUpdateRequest_9c4a50")}</button> : null}{cancel.uncertain ? <button disabled={cancel.busy || !active} onClick={cancel.retry}>{copy("updates.inspectTheSameCancellation_10c4f8")}</button> : null}<Problem error={error || read.error || request.error || cancel.error} />
  </section>;
}
// Read-only recovery stays available independently of live server negotiation.
// The native owner compares the exact local journal and trusted connection; this
// form cannot prepare, install, restart or select an artifact.
function LocalInstallationInspection({ active, controls }: { active: boolean; controls: DesktopUpdateControls }) {
  useLocale();
  const [id, setId] = useState(""), [revision, setRevision] = useState(""), [result, setResult] = useState<NativeUpdateResult>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const alive = useRef(true); useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const valid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id) && /^[1-9][0-9]{0,18}$/.test(revision) && BigInt(revision) < (1n << 63n);
  const inspect = async () => {
    if (!active || busy || !valid) return;
    const original = id; setBusy(true); setError(undefined); setResult(undefined);
    try {
      const observed = await controls.control(NativeUpdateAction.Inspect, original, BigInt(revision));
      if (observed.operation_id !== original) throw new Error("Update identity changed");
      if (alive.current) setResult(observed);
    } catch (error) { if (alive.current) setError(error); }
    finally { if (alive.current) setBusy(false); }
  };
  return <section aria-label={copy("updates.localInstallationRecovery_41dcc5")}><p>{copy("updates.inspectARetainedInstallationOnThis_28e7a3")}</p><form onSubmit={event => { event.preventDefault(); void inspect(); }}><label>{copy("updates.retainedDesktopUpdateId_acb1d6")}<input value={id} maxLength={36} disabled={busy} onChange={event => { setId(event.target.value); setResult(undefined); }} /></label><label>{copy("updates.retainedDesktopUpdateRevision_a8ee1a")}<input value={revision} maxLength={19} inputMode="numeric" disabled={busy} onChange={event => { setRevision(event.target.value); setResult(undefined); }} /></label><button disabled={!active || busy || !valid}>{copy("updates.inspectRetainedLocalInstallation_3d589d")}</button></form>{result ? <p role="status"><LocalizedText id="updates.retainedDesktop_312269" components={{ s0: <>{result.release_version}</>, s1: <>{result.phase}</>, s2: <>{result.phase === NativeUpdatePhase.Installed ? copy("updates.restartDelidevWhenReady_f8939d") : ""}</> }} /></p> : null}<Problem error={error} /></section>;
}
export function Updates({ active, machine, controls }: { active: boolean; machine?: Resource; controls?: DesktopUpdateControls }) {
  useLocale();
  const [expanded, setExpanded] = useState(false), [candidate, setCandidate] = useState<Resource>(), [id, setId] = useState(""), [nativeContext, setNativeContext] = useState<{ current_version: string; target: string }>(), [error, setError] = useState<unknown>();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && expanded });
  const supported = status.data?.capabilities.includes(SystemCapability.SIGNED_UPDATES_V1) === true;
  useEffect(() => { let alive = true; if (expanded && active && controls) void controls.readContext().then(value => { if (alive) setNativeContext(value); }).catch(error => { if (alive) setError(error); }); return () => { alive = false; }; }, [expanded, active, controls]);
  const check = useRetainedMutation(`update:check:${machine?.id ?? "desktop"}`, InstallationQuery.checkUpdate, response => { if (response.update) { setCandidate(response.update); setId(response.update.id); } });
  const read = useQuery(InstallationQuery.getUpdate, { id }, { enabled: active && expanded && supported && Boolean(id) });
  const data = machine ? document(machine) : undefined;
  const target = data ? `${text(data.os)}-${text(data.architecture)}` : nativeContext?.target ?? "", version = data ? text(data.version) : nativeContext?.current_version ?? "";
  const selected = read.data?.update?.id === id ? read.data.update : candidate?.id === id ? candidate : undefined;
  const knownWorker = !machine || (Array.isArray(data?.worker_capabilities) && data.worker_capabilities.includes("signed-worker-updates-v1"));
  return <details onToggle={event => setExpanded(event.currentTarget.open)}><summary>{machine ? copy("updates.workerUpdates_86b3ca") : copy("updates.desktopUpdates_1431d4")}</summary>{expanded ? <>{!supported || !knownWorker ? <p>{status.isPending ? copy("updates.checkingUpdateSupport_9f8283") : copy("updates.thisServerOrWorkerRequiresAn_e35658")}</p> : <>
    <p>{copy("updates.onlySignedStableDelidevReleasesAre_fdf83e")}</p><button disabled={!active || !targets[target] || !version || check.busy || check.uncertain} onClick={() => void check.send({ requestId: newRequestId(), component: machine ? UpdateComponent.WORKER : UpdateComponent.DESKTOP, target: targets[target], currentVersion: version, machineId: machine?.id ?? "", expectedMachineRevision: machine?.revision ?? 0n })}><LocalizedText id="updates.checkForUpdate_8ac71d" components={{ s0: <>{machine ? copy("updates.worker_a67b04") : copy("updates.desktop_68693d")}</> }} /></button>{check.uncertain ? <button disabled={check.busy || !active} onClick={check.retry}>{copy("updates.retryTheSameUpdateCheck_919af6")}</button> : null}
    <form onSubmit={event => { event.preventDefault(); void read.refetch(); }}><label>{copy("updates.originalUpdateId_51ae1b")}<input value={id} maxLength={36} onChange={event => { setId(event.target.value); setCandidate(undefined); }} /></label><button disabled={!active || !id}>{copy("updates.inspectOriginalUpdate_3b2c2e")}</button></form>
    {selected ? <UpdateCandidate key={selected.id} current={selected} active={active && expanded} worker={Boolean(machine)} controls={controls} /> : null}<Problem error={check.error || read.error || error} />
  </>}{controls && !machine ? <LocalInstallationInspection active={active && expanded} controls={controls} /> : null}</> : null}</details>;
}
