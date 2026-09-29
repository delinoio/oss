import { RepositoryGitHubAccess } from "./integration-access";
import { RepositoryGitHubItems } from "./github-items";
import { Integrations } from "./integrations";
import { ConfigurationTransfer } from "./configuration-transfer";
import { ModelPricing } from "./pricing";
import { NotificationSettings } from "./notification-settings";
import { useCallback, useEffect, useState } from "react";
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
import { Modal, ModalLayout, Problem } from "./ui";
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

export enum SettingsEntryDestination { Repositories = "repositories" }
enum SettingsArea { Configuration, Diagnostics, Notifications, Transfer, Integrations }
enum SettingsWorkflow { Integrations = "integrations", Notifications = "notifications", Transfer = "transfer" }
enum SettingsCategory {
  Providers = "providers", Models = "models", Accounts = "accounts", AgentWorkers = "agent-workers", Instructions = "instructions",
  Projects = "projects", Repositories = "repositories", ExecutionWorkers = "execution-workers", PairedDevices = "paired-devices",
  ServerPreferences = "server-preferences", Integrations = "integrations", Diagnostics = "diagnostics", Notifications = "notifications", Transfer = "transfer",
}
enum SettingsGroup { AiAgents = "AI & agents", Workspace = "Workspace", System = "System" }

const settingsCategories: Record<SettingsCategory, { label: string; description: string; kind?: EntityKind; area: SettingsArea }> = {
  [SettingsCategory.Providers]: { label: "Providers", description: "Saved on the selected server.", kind: EntityKind.PROVIDER, area: SettingsArea.Configuration },
  [SettingsCategory.Models]: { label: "Models", description: "Saved on the selected server.", kind: EntityKind.MODEL, area: SettingsArea.Configuration },
  [SettingsCategory.Accounts]: { label: "AI accounts", description: "Saved on the selected server.", kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.AgentWorkers]: { label: "Agent Workers", description: "Saved on the selected server.", kind: EntityKind.AGENT, area: SettingsArea.Configuration },
  [SettingsCategory.Instructions]: { label: "Instructions", description: "Saved on the selected server.", kind: EntityKind.TEMPLATE, area: SettingsArea.Configuration },
  [SettingsCategory.Projects]: { label: "Projects", description: "Saved on the selected server.", kind: EntityKind.PROJECT, area: SettingsArea.Configuration },
  [SettingsCategory.Repositories]: { label: "Repositories", description: "Saved on the selected server.", kind: EntityKind.REPOSITORY, area: SettingsArea.Configuration },
  [SettingsCategory.ExecutionWorkers]: { label: "Execution Workers", description: "Saved on the selected server.", kind: EntityKind.MACHINE, area: SettingsArea.Configuration },
  [SettingsCategory.PairedDevices]: { label: "Paired devices", description: "Saved on the selected server.", kind: EntityKind.DEVICE, area: SettingsArea.Configuration },
  [SettingsCategory.ServerPreferences]: { label: "Server preferences", description: "Saved on the selected server.", kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.Integrations]: { label: "Integrations", description: "Named GitHub.com PAT profiles are separate from AI accounts. Each repository selects its profile explicitly.", area: SettingsArea.Integrations },
  [SettingsCategory.Diagnostics]: { label: "Diagnostics", description: "Read-only observations from the selected server. This check does not repair state, connect an account or run model inference.", area: SettingsArea.Diagnostics },
  [SettingsCategory.Notifications]: { label: "Notifications", description: "These preferences belong to this client on the selected server. Inbox requests stay available when notifications are disabled or cannot be delivered.", area: SettingsArea.Notifications },
  [SettingsCategory.Transfer]: { label: "Import / Export", description: "Transfer providers, models, account preferences, Agent Workers, instructions, repositories, projects and server preferences. Accounts are imported disconnected and require a new connection. Device registrations, observed quotas, discovered model evidence and session history are excluded.", area: SettingsArea.Transfer },
};

const settingsGroups: { label: SettingsGroup; categories: SettingsCategory[] }[] = [
  { label: SettingsGroup.AiAgents, categories: [SettingsCategory.Providers, SettingsCategory.Models, SettingsCategory.Accounts, SettingsCategory.AgentWorkers, SettingsCategory.Instructions] },
  { label: SettingsGroup.Workspace, categories: [SettingsCategory.Projects, SettingsCategory.Repositories, SettingsCategory.ExecutionWorkers] },
  { label: SettingsGroup.System, categories: [SettingsCategory.PairedDevices, SettingsCategory.ServerPreferences, SettingsCategory.Integrations, SettingsCategory.Diagnostics, SettingsCategory.Notifications, SettingsCategory.Transfer] },
];

const settingsIcons: Record<SettingsCategory, string> = {
  [SettingsCategory.Providers]: "M7 18a4 4 0 1 1 .9-7.9A5.5 5.5 0 0 1 18 9.5 3.5 3.5 0 0 1 18 18z",
  [SettingsCategory.Models]: "M7 7h10v10H7zM4 4h2m12 0h2M4 20h2m12 0h2",
  [SettingsCategory.Accounts]: "M16 20v-1a4 4 0 0 0-4-4h-1a4 4 0 0 0-4 4v1m4-9a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7",
  [SettingsCategory.AgentWorkers]: "M4 8h16v12H4zM8 8V5h8v3m-8 5h.01M16 13h.01M9 16h6",
  [SettingsCategory.Instructions]: "M6 3h8l4 4v14H6zM14 3v5h5M9 12h6m-6 4h6",
  [SettingsCategory.Projects]: "M3 7h7l2 2h9v11H3zM3 7V5h7l2 2",
  [SettingsCategory.Repositories]: "M6 4v6m0 0a3 3 0 1 0 0 6m0-6h7a3 3 0 1 1 0 6h5m-12 0v4",
  [SettingsCategory.ExecutionWorkers]: "M3 4h18v13H3zM8 21h8m-4-4v4M7 8h4m-4 4h10",
  [SettingsCategory.PairedDevices]: "M7 3h10v18H7zM10 6h4m-4 12h4",
  [SettingsCategory.ServerPreferences]: "M4 6h16M4 12h16M4 18h16M9 4v4m6 2v4m-3 2v4",
  [SettingsCategory.Integrations]: "M9 15l6-6m-8 9H5a4 4 0 0 1 0-8h4m6-4h4a4 4 0 0 1 0 8h-4",
  [SettingsCategory.Diagnostics]: "M3 12h4l3-7 4 14 3-7h4",
  [SettingsCategory.Notifications]: "M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9m-8 12h4",
  [SettingsCategory.Transfer]: "M7 7h13m0 0-4-4m4 4-4 4M17 17H4m0 0 4 4m-4-4 4-4",
};

function SettingsIcon({ category }: { category: SettingsCategory }) {
  return <svg className="settings-category-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={settingsIcons[category]} /></svg>;
}

export function Settings({ close, visible = true, controlLocalWorker, currentDeviceId, pairingAuthority, entryDestination, destinationConsumed }: { pairingAuthority?: PairingAuthority; close: () => void; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string; entryDestination?: SettingsEntryDestination; destinationConsumed?: () => void }) {
  const [selectedCategory, setSelectedCategory] = useState(SettingsCategory.Providers);
  const [device, setDevice] = useState<Resource>();
  const [page, setPage] = useState("");
  const [editing, setEditing] = useState<{ initial?: Resource; key: string }>();
  const [machine, setMachine] = useState<Resource>();
  const [deleting, setDeleting] = useState<Resource>();
  const [routing, setRouting] = useState<Resource>();
 const [account, setAccount] = useState<Resource>();
 const [pricing,setPricing]=useState<Resource>();
  const [childWorkflows, setChildWorkflows] = useState<ReadonlySet<SettingsWorkflow>>(() => new Set());
  const client = useQueryClient();
  const selected = settingsCategories[selectedCategory];
  const area = selected.area;
  const kind = selected.kind ?? EntityKind.PROVIDER;
  const categoryDescription = kind === EntityKind.DEVICE
    ? "Pair devices using a short-lived document. Local Worker registration is available in Execution Workers."
    : kind === EntityKind.MACHINE && !controlLocalWorker ? "Configure these entries through the DeliDev CLI."
    : selected.description;
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: page } }, { enabled: visible && area === SettingsArea.Configuration });
  const reportChildWorkflow = useCallback((workflow: SettingsWorkflow, active: boolean) => setChildWorkflows((current) => {
    if (current.has(workflow) === active) return current;
    const next = new Set(current);
    if (active) next.add(workflow); else next.delete(workflow);
    return next;
  }), []);
  const reportIntegrationWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Integrations, active), [reportChildWorkflow]);
  const reportTransferWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Transfer, active), [reportChildWorkflow]);
  const reportNotificationWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Notifications, active), [reportChildWorkflow]);
  const parentWorkflow = Boolean(editing || account || deleting || routing || machine || device || pricing);
  const workflowProtected = parentWorkflow || childWorkflows.size > 0;
  const categoryLocked = workflowProtected;
  const configurationList = area === SettingsArea.Configuration && !categoryLocked;
  const successfulEmptyFirstPage = !page && Boolean(result.data && result.data.resources.length === 0 && !result.error && !result.data.nextPageToken);
  const hidePagination = successfulEmptyFirstPage;
  useEffect(() => {
    if (!visible || entryDestination !== SettingsEntryDestination.Repositories || workflowProtected) return;
    setSelectedCategory(SettingsCategory.Repositories);
    setPage("");
    destinationConsumed?.();
  }, [destinationConsumed, entryDestination, visible, workflowProtected]);
  const chooseCategory = (category: SettingsCategory) => {
    setSelectedCategory(category);
    if (settingsCategories[category].area === SettingsArea.Configuration) setPage("");
  };
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  return <Modal title="Settings" close={close} visible={visible} layout={ModalLayout.FullWindow}>
    <div className="settings-workspace">
      <aside className="settings-sidebar" aria-label="Settings navigation">
        <nav aria-label="Settings categories">
          {settingsGroups.map((group) => <section className="settings-nav-group" key={group.label}>
            <h2>{group.label}</h2>
            {group.categories.map((category) => <button type="button" className="settings-category-button" key={category} disabled={categoryLocked} aria-current={selectedCategory === category ? "page" : undefined} aria-pressed={selectedCategory === category} onClick={() => chooseCategory(category)}>
              <SettingsIcon category={category} /><span>{settingsCategories[category].label}</span>
            </button>)}
          </section>)}
        </nav>
      </aside>
      <section className="settings-content" aria-label="Settings content">
        <label className="settings-compact-selector">Settings category
          <select aria-label="Settings category" value={selectedCategory} disabled={categoryLocked} onChange={(event) => chooseCategory(event.currentTarget.value as SettingsCategory)}>
            {settingsGroups.map((group) => <optgroup label={group.label} key={group.label}>{group.categories.map((category) => <option key={category} value={category}>{settingsCategories[category].label}</option>)}</optgroup>)}
          </select>
        </label>
        <div className="settings-category-heading">
          <div className="settings-category-title"><h1 aria-live="polite" aria-atomic="true">{selected.label}</h1><p>{categoryDescription}</p></div>
          {configurationList ? <div className="settings-toolbar">
            <button type="button" onClick={() => void result.refetch()}>Refresh settings</button>
            {editableKinds.includes(kind) && (kind !== EntityKind.SETTINGS || result.data?.resources.length === 0)
              ? <button type="button" className="primary" disabled={!result.data || (kind === EntityKind.SETTINGS && Boolean(result.error || result.isFetching))} onClick={() => setEditing({ key: newRequestId() })}><span className="settings-action-icon" aria-hidden="true">+</span>New {kindNames[kind]}</button>
              : null}
          </div> : null}
        </div>
        <div className="settings-panels">
          <div hidden={area !== SettingsArea.Integrations}><Integrations active={visible && area === SettingsArea.Integrations} showCategoryIntro={false} onWorkflowReadyChange={reportIntegrationWorkflow} /></div>
          <div hidden={area !== SettingsArea.Transfer}><ConfigurationTransfer active={visible && area === SettingsArea.Transfer} showCategoryIntro={false} onWorkflowReadyChange={reportTransferWorkflow} /></div>
          <div hidden={area !== SettingsArea.Notifications}><NotificationSettings active={visible && area === SettingsArea.Notifications} showCategoryIntro={false} onWorkflowReadyChange={reportNotificationWorkflow} /></div>
          <div hidden={area !== SettingsArea.Diagnostics}><Doctor active={visible && area === SettingsArea.Diagnostics} showCategoryIntro={false} /></div>
          <div hidden={area !== SettingsArea.Configuration}>
            {controlLocalWorker ? <div hidden={kind !== EntityKind.MACHINE || Boolean(machine || editing || deleting || routing || account)}><LocalWorkerControls control={controlLocalWorker} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
            {pairingAuthority ? <div hidden={kind !== EntityKind.DEVICE || Boolean(device)}><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} /></div> : null}
            {pricing ? <ModelPricing model={pricing} active={visible} close={() => { setPricing(undefined); void result.refetch(); }} /> : device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={() => { setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing ? <ConfigurationEditor key={editing.key} kind={kind} initial={editing.initial} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : <>
              {result.isPending && !result.data ? <p role="status">Loading {selected.label.toLowerCase()}…</p> : null}
              <Problem error={result.error} />
              {result.error && result.data ? <p className="notice" role="status">Refresh failed. Showing the last successfully loaded results.</p> : null}
              {result.data?.resources.map((row) => { const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</h3>{kind === EntityKind.DEVICE ? <DeviceDetails resource={row} currentDeviceId={currentDeviceId} /> : null}{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small>{kind === EntityKind.REPOSITORY ? <><RepositoryGitHubAccess selected={row} active={visible && area === SettingsArea.Configuration} /><RepositoryGitHubItems selected={row} active={visible && area === SettingsArea.Configuration} /></> : null}<div className="actions">{kind === EntityKind.DEVICE && data.revoked === false ? <button disabled={row.schemaVersion !== 1} onClick={() => setDevice(row)}>Revoke {resourceName(row)}</button> : null}{editableKinds.includes(kind) ? <button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Edit {kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</button> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <button disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}>Delete {resourceName(row)}</button> : null}{kind === EntityKind.MODEL ? <button disabled={row.schemaVersion !== 1} onClick={() => setPricing(row)}>Token pricing</button> : null}{kind === EntityKind.AGENT ? <button disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>Preview routing</button> : null}{kind === EntityKind.MACHINE ? <button disabled={row.schemaVersion !== 1} onClick={() => setMachine(row)}>Inspect installed harnesses</button> : null}{kind === EntityKind.ACCOUNT ? <button disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>Manage connection</button> : null}</div></article>; })}
              {kind === EntityKind.PROVIDER && page === "" && result.data?.resources.length === 0 && !result.error && !result.data.nextPageToken
                ? <section className="provider-empty" aria-label="No providers yet"><SettingsIcon category={SettingsCategory.Providers} /><h2>No providers yet</h2><p>Add a provider to configure your models and AI accounts.</p></section>
                : result.data?.resources.length === 0 ? <p>{kind === EntityKind.PROVIDER ? "No providers on this page." : "No saved entries."}</p> : null}
              {!hidePagination ? <nav className="settings-pages" aria-label="Settings pages"><button type="button" disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><button type="button" disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next page</button></nav> : null}
            </>}
          </div>
        </div>
      </section>
    </div>
  </Modal>;
}
