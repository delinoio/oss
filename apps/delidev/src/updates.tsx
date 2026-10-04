// SPDX-License-Identifier: Apache-2.0
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
  return <section aria-label="Original update"><p>DeliDev {text(data.version)} · {text(data.target)} · {text(data.state)}</p><p>Original update: <code>{row.id}</code></p>
    {worker ? <><p>Updates wait for active work, terminals, forwarding and cleanup. The original registration and workspaces are preserved.</p>{text(data.state) === "OBSERVED" ? <button disabled={!active || request.busy || request.uncertain} onClick={() => void request.send({ mutation: mutation() })}>Update Worker when idle</button> : null}{["OBSERVED", "WAITING_FOR_IDLE"].includes(text(data.state)) ? <button disabled={!active || cancel.busy || cancel.uncertain} onClick={() => void cancel.send({ mutation: mutation() })}>Cancel unclaimed update</button> : null}</> : <><p>Installation requires confirmation on this computer. Restart the desktop after installation; running servers and sessions retain their processes.</p>{!local && !uncertain ? <button disabled={busy || !active} onClick={() => void native(NativeUpdateAction.Prepare)}>Download verified desktop update</button> : null}{local?.phase === NativeUpdatePhase.Prepared && !uncertain ? <button disabled={busy || !active} onClick={() => void native(NativeUpdateAction.Install)}>Review and install desktop update</button> : null}{local ? <p role="status">Desktop installation: {local.phase}{local.phase === NativeUpdatePhase.Installed ? " · Restart DeliDev when ready." : ""}</p> : null}{uncertain ? <p role="alert">The original installation response is unconfirmed. Inspect it before taking another action.</p> : null}<button disabled={busy || !active} onClick={() => void native(NativeUpdateAction.Inspect)}>Inspect original desktop installation</button></>}
    {request.uncertain ? <button disabled={request.busy || !active} onClick={request.retry}>Inspect the same Worker update request</button> : null}{cancel.uncertain ? <button disabled={cancel.busy || !active} onClick={cancel.retry}>Inspect the same cancellation</button> : null}<Problem error={error || read.error || request.error || cancel.error} />
  </section>;
}
export function Updates({ active, machine, controls }: { active: boolean; machine?: Resource; controls?: DesktopUpdateControls }) {
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
  return <details onToggle={event => setExpanded(event.currentTarget.open)}><summary>{machine ? "Worker updates" : "Desktop updates"}</summary>{expanded ? <>{!supported || !knownWorker ? <p>{status.isPending ? "Checking update support…" : "This server or Worker requires an update to support signed updates."}</p> : <>
    <p>Only signed stable DeliDev releases are accepted. If production signing is unavailable, the installed version is preserved.</p><button disabled={!active || !targets[target] || !version || check.busy || check.uncertain} onClick={() => void check.send({ requestId: newRequestId(), component: machine ? UpdateComponent.WORKER : UpdateComponent.DESKTOP, target: targets[target], currentVersion: version, machineId: machine?.id ?? "", expectedMachineRevision: machine?.revision ?? 0n })}>Check for {machine ? "Worker" : "desktop"} update</button>{check.uncertain ? <button disabled={check.busy || !active} onClick={check.retry}>Retry the same update check</button> : null}
    <form onSubmit={event => { event.preventDefault(); void read.refetch(); }}><label>Original update ID<input value={id} maxLength={36} onChange={event => { setId(event.target.value); setCandidate(undefined); }} /></label><button disabled={!active || !id}>Inspect original update</button></form>
    {selected ? <UpdateCandidate key={selected.id} current={selected} active={active && expanded} worker={Boolean(machine)} controls={controls} /> : null}<Problem error={check.error || read.error || error} />
  </>}</> : null}</details>;
}
