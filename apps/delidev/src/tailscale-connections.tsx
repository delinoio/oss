// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { DeviceType, SystemCapability, SystemQuery, TailscaleQuery, TailscaleWorkerState as WorkerState, TailscaleDiscoveryState as Discovery, TailscaleOwnership as Ownership, TailscalePeerState as PeerState, TailscaleApprovalState as Approval, TailscaleAccessState as Access, newRequestId, type TailscalePeer, type TailscaleConnection } from "@delinoio/delidev-api-client";
import { copy, useLocale, type MessageKey } from "./localization";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { SettingsTaskDialog, SettingsTaskScope, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { SSHSetup } from "./ssh-setup";
import { SavedConnectionEvent } from "./connection-events";

function useSupported(active = true) { return useQuery(SystemQuery.getStatus, {}, { enabled: active }).data?.capabilities.includes(SystemCapability.TAILSCALE_CONNECTION_APPROVAL_V1) === true; }
const discoveryLabels: Partial<Record<Discovery, MessageKey>> = { [Discovery.MISSING]: "tailscale.missing", [Discovery.STOPPED]: "tailscale.stopped", [Discovery.LOGGED_OUT]: "tailscale.loggedOut", [Discovery.PERMISSION_DENIED]: "tailscale.permission", [Discovery.MALFORMED]: "tailscale.malformed", [Discovery.INCOMPLETE]: "tailscale.incomplete" };
const ownershipLabels: Record<Ownership, MessageKey> = { [Ownership.UNSPECIFIED]: "tailscale.unknown", [Ownership.OWN]: "tailscale.own", [Ownership.SHARED]: "tailscale.shared", [Ownership.TAGGED]: "tailscale.tagged", [Ownership.UNKNOWN]: "tailscale.unknown" };
const peerLabels: Record<PeerState, MessageKey> = { [PeerState.UNSPECIFIED]: "tailscale.unchecked", [PeerState.NOT_CHECKED]: "tailscale.unchecked", [PeerState.ACCEPTING]: "tailscale.accepting", [PeerState.OFFLINE]: "tailscale.offline", [PeerState.TLS_FAILURE]: "tailscale.tls", [PeerState.BLOCKED]: "tailscale.blocked", [PeerState.UNSUPPORTED]: "tailscale.unsupported" };
const approvalLabels: Record<Approval, MessageKey> = { [Approval.UNSPECIFIED]: "tailscale.uncertain", [Approval.PENDING]: "tailscale.pending", [Approval.APPROVED]: "tailscale.approved", [Approval.DENIED]: "tailscale.denied", [Approval.EXPIRED]: "tailscale.expired", [Approval.CANCELED]: "tailscale.canceled" };
const accessLabels: Record<Access, MessageKey> = { [Access.UNSPECIFIED]: "tailscale.unavailable", [Access.OFF]: "tailscale.off", [Access.STARTING]: "tailscale.starting", [Access.READY]: "tailscale.ready", [Access.UNAVAILABLE]: "tailscale.unavailable", [Access.CLEANUP_REQUIRED]: "tailscale.cleanup" };

export function TailscaleAccessControls() {
 useLocale(); const supported = useSupported();
 const devices = useQuery(TailscaleQuery.readTailscaleDevices, {}, { enabled: supported, retry: false, gcTime: 0 });
 const access = useQuery(TailscaleQuery.getTailscaleAccess, {}, { enabled: supported, retry: false });
 const server = useQuery(SystemQuery.getStatus, {});
 const [confirm, setConfirm] = useState(false);
 const mutation = useRetainedMutation("tailscale:access", TailscaleQuery.setTailscaleAccess, () => { setConfirm(false); void access.refetch(); });
 if (!supported) return null;
 const origin = access.data?.origin || devices.data?.self?.origin || "";
 const enabled = access.data?.enabled === true;
 return <section className="tailscale-access"><h3>{copy("tailscale.access")}</h3><p>{copy("tailscale.accessHelp")}</p><p role="status">{copy(accessLabels[access.data?.state ?? Access.UNSPECIFIED])}</p><button type="button" disabled={mutation.busy || access.isFetching || !origin || (!enabled && !devices.data?.httpsReady)} onClick={() => setConfirm(true)}>{copy(enabled ? "tailscale.disable" : "tailscale.enable")}</button>
 {mutation.uncertain ? <><p role="alert">{copy("tailscale.uncertain")}</p><button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("tailscale.poll")}</button></> : null}<Problem error={access.error || mutation.error} />
 {confirm ? <SettingsTaskScope><SettingsTaskDialog title={copy("tailscale.confirmAccess")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setConfirm(false)}><p>{copy("tailscale.confirmHelp")}</p><p>{origin}</p><div className="actions"><button type="button" disabled={mutation.busy} onClick={() => { if (server.data) void mutation.send({ requestId: newRequestId(), enable: !enabled, serverId: server.data.serverId, origin }); }}>{copy("tailscale.confirm")}</button><button type="button" data-settings-task-cancel onClick={() => setConfirm(false)}>{copy("tailscale.cancel")}</button></div></SettingsTaskDialog></SettingsTaskScope> : null}
 <TailscaleOwnedWorkers /></section>;
}
export function TailscaleDevices() {
 useLocale(); const supported = useSupported();
 const devices = useQuery(TailscaleQuery.readTailscaleDevices, {}, { enabled: supported, retry: false, gcTime: 0 });
 const access = useQuery(TailscaleQuery.getTailscaleAccess, {}, { enabled: supported, retry: false });
 const outgoing = useQuery(TailscaleQuery.listTailscalePeerConnections, {}, { enabled: supported, retry: false });
 const [selected, setSelected] = useState<TailscaleConnection>();
 if (!supported) return null;
 const state = devices.isError ? undefined : devices.data?.state;
 return <section aria-label={copy("tailscale.devices")}><div className="connections-section-heading"><h2>{copy("tailscale.devices")}</h2><button type="button" disabled={devices.isFetching} onClick={() => { void devices.refetch(); void access.refetch(); void outgoing.refetch(); }}>{copy("tailscale.refresh")}</button></div>
 {devices.isFetching ? <p role="status">{copy("tailscale.loading")}</p> : null}
 {state !== Discovery.READY ? <p role="status">{copy(devices.isError ? "tailscale.readFailed" : discoveryLabels[state ?? Discovery.UNSPECIFIED] ?? "tailscale.incomplete")}</p> : devices.data?.peers.length ? <ul className="connections-saved-list">{devices.data.peers.map(peer => <PeerRow key={peer.id} peer={peer} currentOrigin={access.data?.state === Access.READY ? access.data.origin : ""} changed={() => void outgoing.refetch()} />)}</ul> : <p>{copy("tailscale.empty")}</p>}
 <Problem error={devices.error || access.error || outgoing.error} />
 {outgoing.data?.connections.length ? <section><h3>{copy("tailscale.originalRequests")}</h3><ul className="connections-saved-list">{outgoing.data.connections.map(request => <li className="connections-saved-row" key={request.requestId}><div><p>{request.origin}</p><p>{copy(approvalLabels[request.state])}</p></div><button type="button" onClick={() => setSelected(request)}>{copy("tailscale.resume")}</button></li>)}</ul></section> : null}
 {selected ? <SettingsTaskScope><PeerConnectionTask key={selected.requestId} original={selected} close={() => { setSelected(undefined); void outgoing.refetch(); }} /></SettingsTaskScope> : null}
 </section>;
}
function PeerRow({ peer, currentOrigin, changed }: { peer: TailscalePeer; currentOrigin: string; changed: () => void }) {
 const check = useQuery(TailscaleQuery.checkTailscaleDevice, { peerId: peer.id, expectedOrigin:peer.origin }, { enabled: false, retry: false, gcTime:0 });
 const [role, setRole] = useState<DeviceType>();
 const checked = check.data?.state === PeerState.ACCEPTING;
 return <li className="connections-saved-row"><div><h3>{peer.name}</h3><p>{copy(ownershipLabels[peer.ownership])} · {copy(peer.online ? "tailscale.online" : "tailscale.offline")}</p><p>{peer.origin}</p><p role="status">{copy(peerLabels[check.data?.state ?? PeerState.NOT_CHECKED])}</p><Problem error={check.error} /></div><div className="connections-row-actions"><button type="button" disabled={!peer.online || check.isFetching} onClick={() => void check.refetch()}>{copy("tailscale.check")}</button>{checked ? <><button type="button" onClick={() => setRole(DeviceType.CLIENT)}>{copy("tailscale.connect")}</button><button type="button" disabled={!currentOrigin} onClick={() => setRole(DeviceType.WORKER)}>{copy("tailscale.worker")}</button></> : null}{peer.online && currentOrigin ? <SSHSetup active targetHost={new URL(peer.origin).hostname} serverOrigin={currentOrigin} /> : null}</div>
 {role !== undefined && checked ? <SettingsTaskScope><PeerConnectionTask peer={peer} serverId={check.data!.serverId} role={role} currentOrigin={currentOrigin} close={() => { setRole(undefined); changed(); }} /></SettingsTaskScope> : null}</li>;
}
function PeerConnectionTask({ peer, serverId, role = DeviceType.CLIENT, currentOrigin = "", original, close }: { peer?: TailscalePeer; serverId?: string; role?: DeviceType; currentOrigin?: string; original?: TailscaleConnection; close: () => void }) {
 const [id] = useState(() => original?.requestId ?? newRequestId());
 const [request, setRequest] = useState(original); const [code, setCode] = useState(""); const [done, setDone] = useState(false);
 const isWorker = (request?.role ?? role) === DeviceType.WORKER;
 const start = useRetainedMutation(`tailscale:start:${id}`, TailscaleQuery.startTailscalePeerConnection, result => setRequest(result.connection));
 const poll = useRetainedMutation(`tailscale:poll:${id}`, TailscaleQuery.pollTailscalePeerConnection, result => setRequest(result.connection));
 const cancel = useRetainedMutation(`tailscale:cancel:${id}`, TailscaleQuery.cancelTailscalePeerConnection, result => setRequest(result.connection));
 const finish = useRetainedMutation(`tailscale:complete:${id}`, TailscaleQuery.completeTailscalePeerConnection, result => { if (result.completed) { setDone(true); window.dispatchEvent(new Event(SavedConnectionEvent.Changed)); } });
 const busy = start.busy || poll.busy || cancel.busy || finish.busy;
 return <SettingsTaskDialog title={copy(isWorker ? "tailscale.workerTitle" : "tailscale.clientTitle")} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} close={close}>
 <p>{copy("tailscale.target")}: {peer?.name ?? request?.origin}</p>{isWorker ? <><p>{copy("tailscale.currentServer")}: {request?.workerServerOrigin || currentOrigin}</p><p>{copy("tailscale.workerHelp")}</p></> : null}
 {done ? <p role="status">{copy("tailscale.done")}</p> : <>
 {!request ? <button type="button" disabled={busy || !peer || !serverId} onClick={() => { if (peer && serverId) void start.send({ requestId: id, peerId: peer.id, role, targetOrigin: peer.origin, targetServerId: serverId }); }}>{copy("tailscale.request")}</button> : <><p role="status">{copy(approvalLabels[request.state])}</p>{request.confirmationCode ? <><p>{copy("tailscale.match")}: <strong className="tailscale-code">{request.confirmationCode}</strong></p><p>{copy("tailscale.matchHelp")}</p></> : null}<button type="button" disabled={busy} onClick={() => void (poll.uncertain ? poll.retry() : poll.send({ requestId: id }))}>{copy("tailscale.poll")}</button><button type="button" disabled={busy || request.state !== Approval.PENDING} onClick={() => void (cancel.uncertain ? cancel.retry() : cancel.send({ requestId: id }))}>{copy("tailscale.cancel")}</button>{request.state === Approval.APPROVED ? <form onSubmit={event => { event.preventDefault(); void (finish.uncertain ? finish.retry() : finish.send({ requestId: id, confirmationCode: code })); }}><label>{copy("tailscale.enteredCode")}<input autoFocus inputMode="numeric" autoComplete="off" maxLength={6} pattern="[0-9]{6}" required value={code} onChange={event => setCode(event.target.value)} /></label><button type="submit" disabled={busy || code !== request.confirmationCode}>{copy("tailscale.continue")}</button></form> : null}</>}
 {start.uncertain || poll.uncertain || finish.uncertain || cancel.uncertain ? <><p role="alert">{copy("tailscale.uncertain")}</p>{!request ? <button type="button" disabled={busy} onClick={() => void start.retry()}>{copy("tailscale.poll")}</button> : null}</> : null}
 </>}<Problem error={start.error || poll.error || finish.error || cancel.error} /><button type="button" onClick={close}>{copy("tailscale.close")}</button>
 </SettingsTaskDialog>;
}
export function TailscaleApprovalObserver({ active }: { active: boolean }) {
 useLocale(); const supported = useSupported(active);
 const pending = useQuery(TailscaleQuery.listTailscaleConnections, {}, { enabled: active && supported, retry: false, refetchInterval: active && supported ? 5000 : false });
 const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());
 const request = pending.data?.connections.find(value => (value.state === Approval.PENDING || value.state === Approval.EXPIRED && Boolean(value.decisionId)) && !dismissed.has(value.requestId));
 if (!request) return pending.data?.connections.some(value => (value.state === Approval.PENDING || value.state === Approval.EXPIRED && Boolean(value.decisionId))) ? <aside className="tailscale-approval-notice" role="status"><span>{copy("tailscale.approvalTitle")}</span><button type="button" onClick={() => setDismissed(new Set())}>{copy("tailscale.resume")}</button></aside> : null;
 return <SettingsTaskScope><TargetApprovalTask key={request.requestId} request={request} close={() => { setDismissed(value => new Set([...value, request.requestId])); void pending.refetch(); }} /></SettingsTaskScope>;
}
function TargetApprovalTask({ request, close }: { request: TailscaleConnection; close: () => void }) {
 const [code, setCode] = useState(""); const [decision] = useState(() => request.decisionId || newRequestId());
 const mutation = useRetainedMutation(`tailscale:decision:${request.requestId}`, TailscaleQuery.decideTailscaleConnection, close);
 return <SettingsTaskDialog title={copy("tailscale.approvalTitle")} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} close={close}><p>{request.requesterName}</p><p>{copy("tailscale.target")}: {request.origin}</p>{request.role === DeviceType.WORKER ? <><p>{copy("tailscale.currentServer")}: {request.workerServerOrigin}</p><p>{copy("tailscale.workerHelp")}</p></> : <p>{copy("tailscale.connect")}</p>}<p>{copy("tailscale.match")}: <strong className="tailscale-code">{request.confirmationCode}</strong></p><p>{copy("tailscale.matchHelp")}</p>{request.decisionId ? <><p role="status">{copy("tailscale.uncertain")}</p><button type="button" disabled={mutation.busy} onClick={() => void (mutation.uncertain ? mutation.retry() : mutation.send({ requestId:request.requestId,decisionId:request.decisionId,confirmationCode:request.confirmationCode,allow:request.decisionAllow }))}>{copy("tailscale.resume")}</button></> : <form onSubmit={event => { event.preventDefault(); void mutation.send({ requestId: request.requestId, decisionId: decision, confirmationCode: code, allow: true }); }}><label>{copy("tailscale.enteredCode")}<input autoFocus inputMode="numeric" autoComplete="off" maxLength={6} pattern="[0-9]{6}" required value={code} onChange={event => setCode(event.target.value)} /></label><button type="submit" disabled={mutation.busy || code !== request.confirmationCode}>{copy("tailscale.approve")}</button></form>}<button type="button" disabled={mutation.busy || Boolean(request.decisionId) || mutation.uncertain} onClick={() => void mutation.send({ requestId: request.requestId, decisionId: decision, confirmationCode: request.confirmationCode, allow: false })}>{copy("tailscale.deny")}</button>{mutation.uncertain ? <><p role="alert">{copy("tailscale.uncertain")}</p><button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("tailscale.poll")}</button></> : null}<Problem error={mutation.error} /></SettingsTaskDialog>;
}

function TailscaleOwnedWorkers() {
 const supported=useSupported();
 const owned=useQuery(TailscaleQuery.listTailscaleConnections, {}, {enabled:supported,retry:false});
 const workers=owned.data?.connections.filter(value=>value.role===DeviceType.WORKER && value.state===Approval.APPROVED) ?? [];
 return workers.length ? <div><h3>{copy("tailscale.ownedWorkers")}</h3><ul className="connections-list">{workers.map(value=><OwnedWorkerRow key={value.requestId} value={value} />)}</ul><Problem error={owned.error}/></div> : null;
}
function OwnedWorkerRow({value}:{value:TailscaleConnection}) {
 const status=useQuery(TailscaleQuery.getTailscaleWorker,{approvalRequestId:value.requestId},{retry:false});
 const start=useRetainedMutation(`tailscale:worker-start:${value.requestId}`,TailscaleQuery.startTailscaleWorker,()=>void status.refetch());
 const stop=useRetainedMutation(`tailscale:worker-stop:${value.requestId}`,TailscaleQuery.stopTailscaleWorker,()=>void status.refetch());
 const [confirm,setConfirm]=useState<"start"|"stop">();
 const busy=start.busy||stop.busy;
 const state=status.data?.state;
 const canStart=state===WorkerState.NOT_STARTED||state===WorkerState.STOPPED||state===WorkerState.EXITED;
 const canStop=Boolean(status.data?.generation)&&state!==WorkerState.STOPPED&&state!==WorkerState.EXITED;
 const labels:Record<WorkerState,MessageKey>={ [WorkerState.UNSPECIFIED]:"tailscale.uncertain",[WorkerState.NOT_STARTED]:"tailscale.workerIdle",[WorkerState.STARTING]:"tailscale.starting",[WorkerState.RUNNING]:"tailscale.workerRunning",[WorkerState.STOPPING]:"tailscale.workerStopping",[WorkerState.STOPPED]:"tailscale.workerStopped",[WorkerState.UNCERTAIN]:"tailscale.uncertain",[WorkerState.EXITED]:"tailscale.workerExited" };
 return <li className="connections-saved-row"><div><h4>{copy("tailscale.workerTitle")}</h4><p>{copy("tailscale.currentServer")}: {value.workerServerOrigin}</p><p role="status">{copy(labels[state??WorkerState.UNSPECIFIED])}</p><Problem error={status.error||start.error||stop.error}/></div><div className="connections-row-actions"><button type="button" disabled={busy||status.isFetching} onClick={()=>void status.refetch()}>{copy("tailscale.refresh")}</button><button type="button" disabled={busy||!canStart||start.uncertain||stop.uncertain} onClick={()=>setConfirm("start")}>{copy("tailscale.workerStart")}</button><button type="button" disabled={busy||!canStop||start.uncertain||stop.uncertain} onClick={()=>setConfirm("stop")}>{copy("tailscale.workerStop")}</button>{start.uncertain||stop.uncertain?<button type="button" disabled={busy} onClick={()=>void(start.uncertain?start.retry():stop.retry())}>{copy("tailscale.resume")}</button>:null}</div>{confirm?<SettingsTaskScope><SettingsTaskDialog title={copy(confirm==="start"?"tailscale.workerStart":"tailscale.workerStop")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={()=>setConfirm(undefined)}><p>{copy("tailscale.workerScopeHelp")}</p><p>{value.workerServerOrigin}</p><button type="button" disabled={busy||!status.data} onClick={()=>{if(status.data){const input={approvalRequestId:value.requestId,requestId:newRequestId(),expectedGeneration:status.data.generation};void(confirm==="start"?start.send(input):stop.send(input));setConfirm(undefined);}}}>{copy("tailscale.confirm")}</button><button type="button" data-settings-task-cancel onClick={()=>setConfirm(undefined)}>{copy("tailscale.cancel")}</button></SettingsTaskDialog></SettingsTaskScope>:null}</li>;
}
