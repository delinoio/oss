import { RepositoryGitHubAccess } from "./integration-access";
import { RepositoryGitHubItems } from "./github-items";
import { Integrations } from "./integrations";
import { ConfigurationTransfer } from "./configuration-transfer";
import { ModelPricing } from "./pricing";
import { NotificationSettings } from "./notification-settings";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ConfigurationQuery, EntityKind, ProviderInventoryCapability, ProviderQuery, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
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
import { ActiveModelSettings, ApiProviderSettings } from "./provider-model-settings";

export function ConfigurationEditor({ kind, initial, initialData, active, saved, cancel }: { kind: EntityKind; initial?: Resource; initialData?: Document; active: boolean; saved: () => void; cancel: () => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : initialData ?? newConfiguration(kind));
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

enum SettingsArea { Configuration, Diagnostics, Notifications, Transfer, Integrations }

export function Settings({ close, visible = true, controlLocalWorker, currentDeviceId, pairingAuthority }: { pairingAuthority?: PairingAuthority; close: () => void; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string }) {
  const [kind, setKind] = useState(EntityKind.PROVIDER);
  const [area, setArea] = useState(SettingsArea.Configuration);
  const [device, setDevice] = useState<Resource>();
  const [page, setPage] = useState("");
  const [accountProviderID, setAccountProviderID] = useState("");
  const [providerFilterQuery, setProviderFilterQuery] = useState("");
  const [providerFilterPage, setProviderFilterPage] = useState("");
  const [editing, setEditing] = useState<{ initial?: Resource; initialData?: Document; key: string }>();
  const [machine, setMachine] = useState<Resource>();
  const [deleting, setDeleting] = useState<Resource>();
  const [routing, setRouting] = useState<Resource>();
  const [account, setAccount] = useState<Resource>();
  const [pricing, setPricing] = useState<Resource>();
  const client = useQueryClient();
  const customInventory = useQuery(ProviderQuery.listProviderInventory, { query: providerFilterQuery, enabledOnly: false, pageSize: 50, pageToken: providerFilterPage }, { enabled: visible && area === SettingsArea.Configuration && kind === EntityKind.ACCOUNT });
  const accountFilterReady = Boolean(customInventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER));
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: page }, providerId: kind === EntityKind.ACCOUNT && accountFilterReady ? accountProviderID : "" }, { enabled: visible && area === SettingsArea.Configuration && kind !== EntityKind.PROVIDER && kind !== EntityKind.MODEL });
  const tabs = [[EntityKind.PROVIDER, "API Providers"], [EntityKind.MODEL, "Models"], [EntityKind.ACCOUNT, "AI accounts"], [EntityKind.AGENT, "Agent Workers"], [EntityKind.TEMPLATE, "Instructions"], [EntityKind.PROJECT, "Projects"], [EntityKind.REPOSITORY, "Repositories"], [EntityKind.MACHINE, "Execution Workers"], [EntityKind.DEVICE, "Paired devices"], [EntityKind.SETTINGS, "Server preferences"]] as const;
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  const providerFilterEntries = customInventory.data?.entries.filter((entry) => entry.providerId) ?? [];
  return <Modal title="Settings" close={close} visible={visible}><nav aria-label="Settings categories">{tabs.map(([value, label]) => <button key={value} disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Configuration && kind === value} onClick={() => { setKind(value); setArea(SettingsArea.Configuration); setPage(""); }}>{label}</button>)}<button disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Integrations} onClick={() => setArea(SettingsArea.Integrations)}>Integrations</button><button disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Diagnostics} onClick={() => setArea(SettingsArea.Diagnostics)}>Diagnostics</button><button disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Notifications} onClick={() => setArea(SettingsArea.Notifications)}>Notifications</button><button disabled={Boolean(editing || account || deleting || routing || machine || device || pricing)} aria-pressed={area === SettingsArea.Transfer} onClick={() => setArea(SettingsArea.Transfer)}>Import / Export</button></nav>
    <div hidden={area !== SettingsArea.Integrations}><Integrations active={visible && area === SettingsArea.Integrations} /></div>
    <div hidden={area !== SettingsArea.Transfer}><ConfigurationTransfer active={visible && area === SettingsArea.Transfer} /></div>
    <div hidden={area !== SettingsArea.Notifications}><NotificationSettings active={visible && area === SettingsArea.Notifications} /></div>
    <div hidden={area !== SettingsArea.Diagnostics}><Doctor active={visible && area === SettingsArea.Diagnostics} /></div>
    <div hidden={area !== SettingsArea.Configuration}>
      {controlLocalWorker ? <div hidden={kind !== EntityKind.MACHINE || Boolean(machine || editing || deleting || routing || account)}><LocalWorkerControls control={controlLocalWorker} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
    {pairingAuthority ? <div hidden={kind !== EntityKind.DEVICE || Boolean(device)}><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} /></div> : null}
    {pricing ? <ModelPricing model={pricing} active={visible} close={() => { setPricing(undefined); void result.refetch(); }} /> : device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={() => { setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing ? <ConfigurationEditor key={editing.key} kind={kind} initial={editing.initial} initialData={editing.initialData} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : kind === EntityKind.PROVIDER ? <ApiProviderSettings active={visible && area === SettingsArea.Configuration} changed={done} createCustom={(initialData) => setEditing({ initialData, key: newRequestId() })} editCustom={(initial) => setEditing({ initial, key: newRequestId() })} manageAccounts={(providerID) => { setKind(EntityKind.ACCOUNT); setAccountProviderID(providerID); setPage(""); }} addAccount={(providerID) => { const data = newConfiguration(EntityKind.ACCOUNT); data.provider_id = providerID; setKind(EntityKind.ACCOUNT); setAccountProviderID(providerID); setEditing({ initialData: data, key: newRequestId() }); }} deleteCustom={(resource) => setDeleting(resource)} /> : kind === EntityKind.MODEL ? <ActiveModelSettings active={visible && area === SettingsArea.Configuration} createModel={() => setEditing({ key: newRequestId() })} editModel={(initial) => setEditing({ initial, key: newRequestId() })} priceModel={(model) => setPricing(model)} /> : <>
      <header><p>Saved on the selected server.</p><button onClick={() => void result.refetch()}>Refresh settings</button></header>
      {kind === EntityKind.ACCOUNT ? <><Problem error={customInventory.error} />{!customInventory.error && customInventory.data && !accountFilterReady ? <p role="status">This server does not support account provider filtering. Showing all accounts.</p> : null}<label>Search providers<input value={providerFilterQuery} maxLength={256} disabled={!accountFilterReady} onChange={(event) => { setProviderFilterQuery(event.target.value); setProviderFilterPage(""); }} /></label><label>Filter AI accounts by provider<select value={accountFilterReady ? accountProviderID : ""} disabled={!accountFilterReady} onChange={(event) => { setAccountProviderID(event.target.value); setPage(""); }}><option value="">All providers</option>{accountProviderID && !providerFilterEntries.some((entry) => entry.providerId === accountProviderID) ? <option value={accountProviderID}>Selected provider · {accountProviderID}</option> : null}{providerFilterEntries.map((entry) => <option key={entry.providerId} value={entry.providerId}>{entry.displayName}{entry.enabled ? " · On" : " · Off"}</option>)}</select></label><nav aria-label="Account provider pages"><button disabled={!providerFilterPage || customInventory.isFetching} onClick={() => setProviderFilterPage("")}>First provider page</button><button disabled={!customInventory.data?.nextPageToken || customInventory.isFetching} onClick={() => setProviderFilterPage(customInventory.data!.nextPageToken)}>More providers</button></nav></> : null}
      {editableKinds.includes(kind) && (kind !== EntityKind.SETTINGS || result.data?.resources.length === 0) ? <button className="primary" disabled={kind === EntityKind.SETTINGS && Boolean(result.error || result.isFetching)} onClick={() => setEditing({ key: newRequestId() })}>New {kindNames[kind]}</button> : kind === EntityKind.DEVICE ? <p>Pair devices using a short-lived document. Local Worker registration is available in Execution Workers.</p> : kind === EntityKind.MACHINE && !controlLocalWorker ? <p>Configure these entries through the DeliDev CLI.</p> : null}
      <Problem error={result.error} />{result.data?.resources.map((row) => { const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</h3>{kind === EntityKind.DEVICE ? <DeviceDetails resource={row} currentDeviceId={currentDeviceId} /> : null}{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small>{kind === EntityKind.REPOSITORY ? <><RepositoryGitHubAccess selected={row} active={visible && area === SettingsArea.Configuration} /><RepositoryGitHubItems selected={row} active={visible && area === SettingsArea.Configuration} /></> : null}<div className="actions">{kind === EntityKind.DEVICE && data.revoked === false ? <button disabled={row.schemaVersion !== 1} onClick={() => setDevice(row)}>Revoke {resourceName(row)}</button> : null}{editableKinds.includes(kind) ? <button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Edit {kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</button> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <button disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}>Delete {resourceName(row)}</button> : null}{kind === EntityKind.AGENT ? <button disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>Preview routing</button> : null}{kind === EntityKind.MACHINE ? <button disabled={row.schemaVersion !== 1} onClick={() => setMachine(row)}>Inspect installed harnesses</button> : null}{kind === EntityKind.ACCOUNT ? <button disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>Manage connection</button> : null}</div></article>; })}
      {result.data?.resources.length === 0 ? <p>No saved entries.</p> : null}<nav aria-label="Settings pages"><button disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next page</button></nav>
    </>}
    </div>
  </Modal>;
}
