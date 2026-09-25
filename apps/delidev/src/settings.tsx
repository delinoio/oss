import { ModelPricing } from "./pricing";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ConfigurationQuery, EntityKind, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, resourceName, text, type Document } from "./documents";
import { ConfigurationFields, editableKinds, kindNames, newConfiguration } from "./configuration-fields";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { JobState, TrackedJob } from "./jobs";
import { LocalWorkerControls, type ControlLocalWorker } from "./local-worker-controls";
import { MachineSettings } from "./machine-settings";
import { DeviceDetails, DeviceRevocation, Doctor } from "./device-settings";
import { AccountConnection } from "./account-connection";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import { PairingGrant, type PairingAuthority } from "./pairing-grant";

export function ConfigurationEditor({ kind, initial, active, saved, cancel }: { kind: EntityKind; initial?: Resource; active: boolean; saved: () => void; cancel: () => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : newConfiguration(kind));
  const [job, setJob] = useState<Resource | "unknown">();
  const [childPending, setChildPending] = useState(false);
  const [problem, setProblem] = useState("");
  const current = useQuery(ResourceQuery.getResource, { kind, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`configuration:${kind}:${initial?.id ?? "new"}`, ConfigurationQuery.saveConfiguration, (result) => { if (result.job) setJob(result.job); else if (result.resource) saved(); else setJob("unknown"); });
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = mutation.busy || mutation.uncertain;
  const change = (value: Document) => {
    if (encode(value).byteLength > 1 << 20 || (kind === EntityKind.TEMPLATE && new TextEncoder().encode(text(value.contents)).byteLength > 128 << 10)) { setProblem("This configuration is too large. Shorten the text before adding more content."); return; }
    setData(value); setProblem("");
  };
  if (job) return <section><h3>{kindNames[kind]} save accepted</h3>{job === "unknown" ? <p role="alert">The server acknowledged this request without a readable result. Inspect its receipt before starting another save.</p> : <TrackedJob initial={job} active={active}>{(state) => state === JobState.Succeeded ? <><p>Configuration saved after Worker validation.</p><button onClick={saved}>Done</button></> : state === JobState.Failed || state === JobState.Canceled ? <button onClick={() => setJob(undefined)}>Return to retained draft</button> : null}</TrackedJob>}</section>;
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || childPending || stale || (initial && current.error)) return; void mutation.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, kind, schemaVersion: 1, documentJson: encode(data) }); }}>
    <h3>{initial ? "Edit" : "New"} {kindNames[kind]}</h3>
    <fieldset disabled={blocked}><ConfigurationFields kind={kind} data={data} change={change} active={active} existing={Boolean(initial)} pendingOperation={setChildPending} /></fieldset>
    {stale ? <p role="alert">This entry changed elsewhere. Your draft is retained. Cancel this edit and reopen the latest entry before saving.</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={current.error || mutation.error} />
    <div className="actions"><button className="primary" disabled={blocked || childPending || stale || Boolean(initial && current.error)}>Save {kindNames[kind]}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same configuration</button> : null}<button type="button" disabled={blocked || childPending} onClick={cancel}>Cancel edit</button></div>
  </form>;
}

enum SettingsArea { Configuration, Diagnostics }

export function Settings({ close, visible = true, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; close: () => void; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string }) {
  const [kind, setKind] = useState(EntityKind.PROVIDER);
  const [area, setArea] = useState(SettingsArea.Configuration);
  const [device, setDevice] = useState<Resource>();
  const [page, setPage] = useState("");
  const [editing, setEditing] = useState<{ initial?: Resource; key: string }>();
  const [machine, setMachine] = useState<Resource>();
  const [deleting, setDeleting] = useState<Resource>();
  const [routing, setRouting] = useState<Resource>();
  const [account, setAccount] = useState<Resource>();
 const [pricing,setPricing]=useState<Resource>();
  const client = useQueryClient();
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: page } }, { enabled: visible && area === SettingsArea.Configuration });
  const tabs = [[EntityKind.PROVIDER, "Providers"], [EntityKind.MODEL, "Models"], [EntityKind.ACCOUNT, "AI accounts"], [EntityKind.AGENT, "Agent Workers"], [EntityKind.TEMPLATE, "Instructions"], [EntityKind.PROJECT, "Projects"], [EntityKind.REPOSITORY, "Repositories"], [EntityKind.MACHINE, "Execution Workers"], [EntityKind.DEVICE, "Paired devices"], [EntityKind.SETTINGS, "Server preferences"]] as const;
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  return <Modal title="Settings" close={close} visible={visible}><nav aria-label="Settings categories">{tabs.map(([value, label]) => <button key={value} disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Configuration && kind === value} onClick={() => { setKind(value); setArea(SettingsArea.Configuration); setPage(""); }}>{label}</button>)}<button disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Diagnostics} onClick={() => setArea(SettingsArea.Diagnostics)}>Diagnostics</button></nav>
    <div hidden={area !== SettingsArea.Diagnostics}><Doctor active={visible && area === SettingsArea.Diagnostics} /></div>
    <div hidden={area !== SettingsArea.Configuration}>
    {controlLocalWorker ? <div hidden={kind !== EntityKind.MACHINE || Boolean(machine || editing || deleting || routing || account)}><LocalWorkerControls control={controlLocalWorker} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
    {pairingAuthority ? <div hidden={kind !== EntityKind.DEVICE || Boolean(device)}><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} /></div> : null}
    {pricing ? <ModelPricing model={pricing} active={visible} close={() => {setPricing(undefined);void result.refetch();}} /> : device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={() => { setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing ? <ConfigurationEditor key={editing.key} kind={kind} initial={editing.initial} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : <>
      <header><p>Saved on the selected server.</p><button onClick={() => void result.refetch()}>Refresh settings</button></header>
      {editableKinds.includes(kind) && (kind !== EntityKind.SETTINGS || result.data?.resources.length === 0) ? <button className="primary" disabled={kind === EntityKind.SETTINGS && Boolean(result.error || result.isFetching)} onClick={() => setEditing({ key: newRequestId() })}>New {kindNames[kind]}</button> : kind === EntityKind.DEVICE ? <p>Pair devices using a short-lived document. Local Worker registration is available in Execution Workers.</p> : kind === EntityKind.MACHINE && !controlLocalWorker ? <p>Configure these entries through the DeliDev CLI.</p> : null}
      <Problem error={result.error} />{result.data?.resources.map((row) => { const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</h3>{kind === EntityKind.DEVICE ? <DeviceDetails resource={row} currentDeviceId={currentDeviceId} /> : null}{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small><div className="actions">{kind === EntityKind.DEVICE && data.revoked === false ? <button disabled={row.schemaVersion !== 1} onClick={() => setDevice(row)}>Revoke {resourceName(row)}</button> : null}{editableKinds.includes(kind) ? <button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Edit {kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</button> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <button disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}>Delete {resourceName(row)}</button> : null}{kind === EntityKind.MODEL ? <button disabled={row.schemaVersion !== 1} onClick={() => setPricing(row)}>Token pricing</button> : null}{kind === EntityKind.AGENT ? <button disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>Preview routing</button> : null}{kind === EntityKind.MACHINE ? <button disabled={row.schemaVersion !== 1} onClick={() => setMachine(row)}>Inspect installed harnesses</button> : null}{kind === EntityKind.ACCOUNT ? <button disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>Manage connection</button> : null}</div></article>; })}
      {result.data?.resources.length === 0 ? <p>No saved entries.</p> : null}<nav aria-label="Settings pages"><button disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next page</button></nav>
    </>}
    </div>
  </Modal>;
}
