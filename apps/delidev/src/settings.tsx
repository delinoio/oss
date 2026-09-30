import { Backups } from "./backups";
import { RepositoryGitHubAccess } from "./integration-access";
import { RepositoryGitHubItems } from "./github-items";
import { Integrations } from "./integrations";
import { ConfigurationTransfer } from "./configuration-transfer";
import { ModelPricing } from "./pricing";
import { NotificationSettings } from "./notification-settings";
import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ConfigurationQuery, EntityKind, ProviderInventoryCapability, ProviderPresetId, ProviderQuery, ResourceQuery, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { ConfigurationFields, editableKinds, kindNames, newConfiguration } from "./configuration-fields";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { JobState, TrackedJob } from "./jobs";
import { LocalWorkerControls, type ControlLocalWorker } from "./local-worker-controls";
import { MachineSettings } from "./machine-settings";
import { DeviceDetails, DeviceRevocation, Doctor } from "./device-settings";
import { DoctorTitle } from "./doctor";
import { AccountConnection } from "./account-connection";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { Modal, ModalLayout, Problem } from "./ui";
import { PairingGrant, type PairingAuthority } from "./pairing-grant";
import { AccountSettings, AccountSettingsSection, type AccountProviderPicker, type AccountProviderSummary } from "./account-settings";
import { ActiveModelSettings, ApiProviderSettings, providerInventoryReady } from "./provider-model-settings";
import { revealAgentInvalidControl } from "./agent-configuration";
import { SettingsLifetime } from "./settings-lifetime";

export function ConfigurationEditor({ kind, initial, initialData, subscriptionOnly = false, active, saved, cancel, focusName = false, nameFocused }: { kind: EntityKind; initial?: Resource; initialData?: Document; subscriptionOnly?: boolean; active: boolean; saved: () => void; cancel: () => void; focusName?: boolean; nameFocused?: () => void }) {
  const [data, setData] = useState<Document>(() => initial ? document(initial) : initialData ?? newConfiguration(kind));
  const [job, setJob] = useState<Resource | "unknown">();
  const [childPending, setChildPending] = useState(false);
  const [problem, setProblem] = useState("");
  const form = useRef<HTMLFormElement>(null);
  const current = useQuery(ResourceQuery.getResource, { kind, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`configuration:${kind}:${initial?.id ?? "new"}`, ConfigurationQuery.saveConfiguration, (result) => { if (result.job) setJob(result.job); else if (result.resource) saved(); else setJob("unknown"); });
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = mutation.busy || mutation.uncertain;
  useEffect(() => {
    if (!active || !focusName || kind !== EntityKind.PROJECT || initial || blocked) return;
    const frame = window.requestAnimationFrame(() => {
      const name = form.current?.querySelector<HTMLInputElement>("input");
      if (!name || name.disabled) return;
      name.focus();
      nameFocused?.();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [active, blocked, focusName, initial, kind, nameFocused]);
  const change = (value: Document) => {
    if (encode(value).byteLength > 1 << 20 || (kind === EntityKind.TEMPLATE && new TextEncoder().encode(text(value.contents)).byteLength > 128 << 10)) { setProblem("This configuration is too large. Shorten the text before adding more content."); return; }
    setData(value); setProblem("");
  };
  const kindLabel = kind === EntityKind.ACCOUNT && data.type === "api" ? "AI API key entry" : kindNames[kind];
  if (job) return <section><h3>{kindLabel} save accepted</h3>{job === "unknown" ? <p role="alert">The server acknowledged this request without a readable result. Inspect its receipt before starting another save.</p> : <TrackedJob initial={job} active={active}>{(state) => state === JobState.Succeeded ? <><p>Configuration saved after Worker validation.</p><button onClick={saved}>Done</button></> : state === JobState.Failed || state === JobState.Canceled ? <button onClick={() => setJob(undefined)}>Return to retained draft</button> : null}</TrackedJob>}</section>;
  const validSubscriptionProvider = !subscriptionOnly || (kind === EntityKind.PROVIDER && data.protocol === "native-subscription" && data.authentication === "subscription" && text(data.endpoint) === "");
  return <form ref={form} className={kind === EntityKind.AGENT ? "agent-configuration" : undefined} onInvalidCapture={kind === EntityKind.AGENT ? revealAgentInvalidControl : undefined} onSubmit={(event) => { event.preventDefault(); if (blocked || childPending || stale || !validSubscriptionProvider || (initial && current.error)) return; void mutation.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, kind, schemaVersion: 1, documentJson: encode(data) }); }}>
    <h3>{initial ? "Edit" : "New"} {kindLabel}</h3>
    {kind === EntityKind.AGENT && !initial ? <p className="agent-subtitle">Configure the essentials, then customize only what you need.</p> : null}
    <fieldset disabled={blocked}><ConfigurationFields kind={kind} data={data} change={change} active={active} existing={Boolean(initial)} pendingOperation={setChildPending} subscriptionOnly={subscriptionOnly} /></fieldset>
    {stale ? <p role="alert">This entry changed elsewhere. Your draft is retained. Cancel this edit and reopen the latest entry before saving.</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={current.error || mutation.error} />
    {subscriptionOnly && !validSubscriptionProvider ? <p role="alert">Subscription providers must use native-subscription protocol, subscription authentication and an empty endpoint.</p> : null}
    {kind === EntityKind.AGENT ? <div className="actions agent-footer"><button type="button" disabled={blocked || childPending} onClick={cancel}>Cancel edit</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same configuration</button> : null}<button className="primary" disabled={blocked || childPending || stale || !validSubscriptionProvider || Boolean(initial && current.error)}>Save {kindLabel}</button></div>
      : <div className="actions"><button className="primary" disabled={blocked || childPending || stale || !validSubscriptionProvider || Boolean(initial && current.error)}>Save {kindLabel}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same configuration</button> : null}<button type="button" disabled={blocked || childPending} onClick={cancel}>Cancel edit</button></div>}

  </form>;
}

export enum SettingsEntryDestination { Repositories = "repositories", NewProject = "new-project" }
enum SettingsArea { Configuration, Diagnostics, Notifications, Transfer, Integrations, Backups }
enum SettingsWorkflow { Integrations = "integrations", Notifications = "notifications", Transfer = "transfer", Accounts = "accounts" }
enum SettingsCategory {
  SubscriptionAccounts = "subscription-accounts", ApiAccounts = "api-accounts", Providers = "providers", Models = "models", AgentWorkers = "agent-workers", Instructions = "instructions",
  Projects = "projects", Repositories = "repositories", ExecutionWorkers = "execution-workers", PairedDevices = "paired-devices",
  ServerPreferences = "server-preferences", Integrations = "integrations", Diagnostics = "diagnostics", Notifications = "notifications", Transfer = "transfer", Backups = "backups",
}
enum SettingsGroup { AiAgents = "AI & agents", Workspace = "Workspace", System = "System" }

const settingsCategories: Record<SettingsCategory, { label: string; description: string; kind?: EntityKind; area: SettingsArea }> = {
  [SettingsCategory.Backups]: { label: "Backups", description: "Inspect managed database images and follow durable creation and deletion jobs on the selected server.", area: SettingsArea.Backups },
  [SettingsCategory.SubscriptionAccounts]: { label: "AI Subscription", description: "Existing subscription account metadata is managed here; subscription login is not available.", kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.ApiAccounts]: { label: "AI API Keys", description: "Manage AI API keys and keyless local connections. Connection and health are separate states.", kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.Providers]: { label: "API Providers", description: "Provider availability is saved on the selected server.", kind: EntityKind.PROVIDER, area: SettingsArea.Configuration },
  [SettingsCategory.Models]: { label: "Models", description: "Saved on the selected server.", kind: EntityKind.MODEL, area: SettingsArea.Configuration },
  [SettingsCategory.AgentWorkers]: { label: "Agent Workers", description: "Saved on the selected server.", kind: EntityKind.AGENT, area: SettingsArea.Configuration },
  [SettingsCategory.Instructions]: { label: "Instructions", description: "Saved on the selected server.", kind: EntityKind.TEMPLATE, area: SettingsArea.Configuration },
  [SettingsCategory.Projects]: { label: "Projects", description: "Saved on the selected server.", kind: EntityKind.PROJECT, area: SettingsArea.Configuration },
  [SettingsCategory.Repositories]: { label: "Repositories", description: "Saved on the selected server.", kind: EntityKind.REPOSITORY, area: SettingsArea.Configuration },
  [SettingsCategory.ExecutionWorkers]: { label: "Execution Workers", description: "Saved on the selected server.", kind: EntityKind.MACHINE, area: SettingsArea.Configuration },
  [SettingsCategory.PairedDevices]: { label: "Paired devices", description: "Saved on the selected server.", kind: EntityKind.DEVICE, area: SettingsArea.Configuration },
  [SettingsCategory.ServerPreferences]: { label: "Server preferences", description: "Saved on the selected server.", kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.Integrations]: { label: "Integrations", description: "Manage GitHub profiles for repository access. AI accounts are configured separately.", area: SettingsArea.Integrations },
  [SettingsCategory.Diagnostics]: { label: "Connection & diagnostics", description: "Read-only observations from the selected server. This check does not repair state, connect an account or run model inference.", area: SettingsArea.Diagnostics },
  [SettingsCategory.Notifications]: { label: "Notifications", description: "These preferences belong to this client on the selected server. Inbox requests stay available when notifications are disabled or cannot be delivered.", area: SettingsArea.Notifications },
  [SettingsCategory.Transfer]: { label: "Import / Export", description: "Transfer providers, models, account preferences, Agent Workers, instructions, repositories, projects and server preferences. Accounts are imported disconnected and require a new connection. Device registrations, observed quotas, discovered model evidence and session history are excluded.", area: SettingsArea.Transfer },
};

const settingsGroups: { label: SettingsGroup; categories: SettingsCategory[] }[] = [
  { label: SettingsGroup.AiAgents, categories: [SettingsCategory.SubscriptionAccounts, SettingsCategory.ApiAccounts, SettingsCategory.Providers, SettingsCategory.Models, SettingsCategory.AgentWorkers, SettingsCategory.Instructions] },
  { label: SettingsGroup.Workspace, categories: [SettingsCategory.Projects, SettingsCategory.Repositories, SettingsCategory.ExecutionWorkers] },
  { label: SettingsGroup.System, categories: [SettingsCategory.PairedDevices, SettingsCategory.ServerPreferences, SettingsCategory.Integrations, SettingsCategory.Diagnostics, SettingsCategory.Notifications, SettingsCategory.Transfer, SettingsCategory.Backups] },
];

const settingsIcons: Record<SettingsCategory, string> = {
  [SettingsCategory.Backups]: "M4 4h16v16H4zM8 4v6h8V4M8 20v-6h8v6",
  [SettingsCategory.Providers]: "M7 18a4 4 0 1 1 .9-7.9A5.5 5.5 0 0 1 18 9.5 3.5 3.5 0 0 1 18 18z",
  [SettingsCategory.Models]: "M7 7h10v10H7zM4 4h2m12 0h2M4 20h2m12 0h2",
  [SettingsCategory.SubscriptionAccounts]: "M16 20v-1a4 4 0 0 0-4-4h-1a4 4 0 0 0-4 4v1m4-9a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7",
  [SettingsCategory.ApiAccounts]: "M16 20v-1a4 4 0 0 0-4-4h-1a4 4 0 0 0-4 4v1m4-9a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7",
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

interface SettingsProps { connectionSettings?: React.ReactNode; pairingAuthority?: PairingAuthority; close: () => void; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string; entryDestination?: SettingsEntryDestination; destinationConsumed?: () => void }
export function Settings({ visible = true, ...props }: SettingsProps) {
  const client = useQueryClient();
  return visible ? <SettingsLifetime>{(opening) => <MutationIntents><SettingsWorkspace {...props} close={() => { opening.dispose(client); props.close(); }} controlLocalWorker={props.controlLocalWorker ? (action, generation) => opening.native(() => props.controlLocalWorker!(action, generation)) : undefined} /></MutationIntents>}</SettingsLifetime> : null;
}

function SettingsWorkspace({ connectionSettings, close, visible = true, controlLocalWorker, currentDeviceId, pairingAuthority, entryDestination, destinationConsumed }: SettingsProps) {
  const [selectedCategory, setSelectedCategory] = useState(() => entryDestination === SettingsEntryDestination.Repositories ? SettingsCategory.Repositories : entryDestination === SettingsEntryDestination.NewProject ? SettingsCategory.Projects : SettingsCategory.SubscriptionAccounts);
  const [device, setDevice] = useState<Resource>();
  const [page, setPage] = useState("");
  const [editing, setEditing] = useState<{ kind?: EntityKind; initial?: Resource; initialData?: Document; key: string; subscriptionOnly?: boolean }>();
  const [machine, setMachine] = useState<Resource>();
  const [deleting, setDeleting] = useState<Resource>();
  const [routing, setRouting] = useState<Resource>();
  const [account, setAccount] = useState<Resource>();
  const [pricing, setPricing] = useState<Resource>();
  const [accountProviderID, setAccountProviderID] = useState("");
  const [providerSearch, setProviderSearch] = useState("");
  const [providerFilterPage, setProviderFilterPage] = useState("");
  const [providerChoicePage, setProviderChoicePage] = useState("");
  const [startApiWizard, setStartApiWizard] = useState<{ key: string; providerId: string; provider?: AccountProviderSummary }>();
  const [accountProviderHint, setAccountProviderHint] = useState<AccountProviderSummary>();
  const [childWorkflows, setChildWorkflows] = useState<ReadonlySet<SettingsWorkflow>>(() => new Set());
  const [focusNewProjectName, setFocusNewProjectName] = useState(false);
  const handledDestination = useRef<SettingsEntryDestination | undefined>(undefined);
  const client = useQueryClient();
  const selected = settingsCategories[selectedCategory];
  const area = selected.area;
  const kind = selected.kind ?? EntityKind.PROVIDER;
  const isApiAccounts = selectedCategory === SettingsCategory.ApiAccounts;
  const isSubscriptionAccounts = selectedCategory === SettingsCategory.SubscriptionAccounts;
  const isAccountCategory = isApiAccounts || isSubscriptionAccounts;
  const isApiProviders = selectedCategory === SettingsCategory.Providers;
  const isModels = selectedCategory === SettingsCategory.Models;
  const hasSpecializedPanel = isAccountCategory || isApiProviders || isModels;
  const hasOverlay = Boolean(editing || account || deleting || routing || machine || device || pricing);
  const categoryDescription = kind === EntityKind.DEVICE
    ? "Pair devices using a short-lived document. Local Worker registration is available in Execution Workers."
    : kind === EntityKind.MACHINE && !controlLocalWorker ? "Configure these entries through the DeliDev CLI."
    : selected.description;
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: page } }, { enabled: visible && area === SettingsArea.Configuration && !hasSpecializedPanel });
  const accountInventory = useQuery(ProviderQuery.listProviderInventory, { query: providerSearch, enabledOnly: false, pageSize: 50, pageToken: providerFilterPage }, { enabled: visible && area === SettingsArea.Configuration && isAccountCategory });
  const eligibleInventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 50, pageToken: providerChoicePage }, { enabled: visible && area === SettingsArea.Configuration && isApiAccounts });
  const providerPicker: AccountProviderPicker = { ready: Boolean(eligibleInventory.data && providerInventoryReady(eligibleInventory.data.capabilities) && eligibleInventory.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER)), loaded: Boolean(eligibleInventory.data), fetching: eligibleInventory.isFetching, error: eligibleInventory.error, pageToken: providerChoicePage, nextPageToken: eligibleInventory.data?.nextPageToken ?? "", retry: () => { void eligibleInventory.refetch(); }, next: () => setProviderChoicePage(eligibleInventory.data?.nextPageToken ?? ""), first: () => setProviderChoicePage("") };
  const providerPresets = useQuery(ProviderQuery.listProviderPresets, {}, { enabled: visible && area === SettingsArea.Configuration && isAccountCategory });
  const accountTypeFilteringReady = Boolean(accountInventory.data && providerInventoryReady(accountInventory.data.capabilities) && accountInventory.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER));
  const presets = new Map<string, Document>();
  try {
    if (providerPresets.data) {
      const parsed = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(providerPresets.data.presetsJson));
      for (const preset of items(parsed).map(object)) presets.set(text(preset.id), preset);
    }
  } catch { /* Preset-specific help stays unavailable when the server response is malformed. */ }
  const presetNames = new Map<ProviderPresetId, string>([
    [ProviderPresetId.VERCEL_AI_GATEWAY, "vercel-ai-gateway"], [ProviderPresetId.OPENROUTER, "openrouter"],
    [ProviderPresetId.OPENAI, "openai"], [ProviderPresetId.ANTHROPIC, "anthropic"], [ProviderPresetId.XAI, "xai"],
    [ProviderPresetId.DEEPSEEK, "deepseek"], [ProviderPresetId.OLLAMA, "ollama"], [ProviderPresetId.LM_STUDIO, "lm-studio"],
    [ProviderPresetId.VLLM, "vllm"],
  ]);
  const providerSummary = (entry: ProviderInventoryEntry): AccountProviderSummary | undefined => entry.providerId && entry.provider?.kind === EntityKind.PROVIDER ? {
    providerId: entry.providerId,
    displayName: entry.displayName,
    enabled: entry.enabled,
    provider: entry.provider,
    keyGuidance: text(presets.get(presetNames.get(entry.presetId) ?? "")?.key_guidance) || "Use the provider's documented API key flow.",
    documentationUrl: text(presets.get(presetNames.get(entry.presetId) ?? "")?.documentation),
  } : undefined;
  const accountProviders = (accountInventory.data?.entries ?? []).map(providerSummary).filter((value): value is AccountProviderSummary => value !== undefined);
  const eligibleProviders = (eligibleInventory.data?.entries ?? []).map(providerSummary).filter((value): value is AccountProviderSummary => value !== undefined && value.enabled);
  useEffect(() => {
    setProviderFilterPage("");
  }, [providerSearch]);
  const reportChildWorkflow = useCallback((workflow: SettingsWorkflow, active: boolean) => setChildWorkflows((current) => {
    if (current.has(workflow) === active) return current;
    const next = new Set(current);
    if (active) next.add(workflow); else next.delete(workflow);
    return next;
  }), []);
  const reportIntegrationWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Integrations, active), [reportChildWorkflow]);
  const reportTransferWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Transfer, active), [reportChildWorkflow]);
  const reportNotificationWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Notifications, active), [reportChildWorkflow]);
  const reportAccountWorkflow = useCallback((active: boolean) => reportChildWorkflow(SettingsWorkflow.Accounts, active), [reportChildWorkflow]);
  const parentWorkflow = Boolean(editing || account || deleting || routing || machine || device || pricing);
  const workflowProtected = parentWorkflow || childWorkflows.size > 0;
  const categoryLocked = workflowProtected;
  const configurationList = area === SettingsArea.Configuration && !categoryLocked && !hasSpecializedPanel;
  const successfulEmptyFirstPage = !page && Boolean(result.data && result.data.resources.length === 0 && !result.error && !result.data.nextPageToken);
  const hidePagination = successfulEmptyFirstPage;
  useEffect(() => {
    if (!entryDestination) {
      handledDestination.current = undefined;
      return;
    }
    if (!visible || handledDestination.current === entryDestination) return;
    if (entryDestination === SettingsEntryDestination.NewProject && editing && kind === EntityKind.PROJECT && !editing.initial) {
      handledDestination.current = entryDestination;
      destinationConsumed?.();
      return;
    }
    if (workflowProtected) return;
    setSelectedCategory(entryDestination === SettingsEntryDestination.Repositories ? SettingsCategory.Repositories : SettingsCategory.Projects);
    setPage("");
    if (entryDestination === SettingsEntryDestination.NewProject) {
      setEditing({ key: newRequestId() });
      setFocusNewProjectName(true);
    }
    handledDestination.current = entryDestination;
    destinationConsumed?.();
  }, [destinationConsumed, editing, entryDestination, kind, visible, workflowProtected]);
  const projectNameFocused = useCallback(() => setFocusNewProjectName(false), []);
  const chooseCategory = (category: SettingsCategory) => {
    setSelectedCategory(category);
    if (settingsCategories[category].area === SettingsArea.Configuration) {
      setPage("");
      if (category === SettingsCategory.ApiAccounts || category === SettingsCategory.SubscriptionAccounts) {
        setAccountProviderID("");
        setAccountProviderHint(undefined);
        setProviderSearch("");
      }
    }
  };
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  const providerEntrySummary = (entry?: ProviderInventoryEntry) => entry ? providerSummary(entry) : undefined;
  const subscriptionProviders = accountProviders.map((entry) => entry.provider).filter((resource): resource is Resource => {
    if (!resource) return false;
    const data = document(resource);
    return data.protocol === "native-subscription" && data.authentication === "subscription" && text(data.endpoint) === "";
  });
  const subscriptionProviderManagement = <section className="subscription-provider-management">
    <p>Only native subscription providers with subscription authentication and an empty endpoint are available for subscription account metadata.</p>
    <button type="button" onClick={() => setEditing({ kind: EntityKind.PROVIDER, initialData: { ...newConfiguration(EntityKind.PROVIDER), protocol: "native-subscription", authentication: "subscription", endpoint: "", discovery: false }, key: newRequestId(), subscriptionOnly: true })}>Add native subscription provider</button>
    <Problem error={accountInventory.error} />
    {subscriptionProviders.map((provider) => <article className="result" key={provider.id}><h4>{resourceName(provider)}</h4><p>Native subscription · no endpoint · subscription login unavailable</p><div className="actions"><button type="button" disabled={provider.schemaVersion !== 1} onClick={() => setEditing({ kind: EntityKind.PROVIDER, initial: provider, key: newRequestId(), subscriptionOnly: true })}>Edit subscription provider</button><button type="button" disabled={provider.schemaVersion !== 1} onClick={() => setDeleting(provider)}>Delete subscription provider</button></div></article>)}
    {accountInventory.isLoading ? <p role="status">Loading subscription providers…</p> : null}
    {!accountInventory.isLoading && subscriptionProviders.length === 0 ? <p>No eligible subscription provider configurations are available yet.</p> : null}
  </section>;
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
        {area !== SettingsArea.Diagnostics ? <div className="settings-category-heading">
          <div className="settings-category-title"><h1 aria-live="polite" aria-atomic="true">{selected.label}</h1><p>{categoryDescription}</p></div>
          {configurationList ? <div className="settings-toolbar">
            <button type="button" onClick={() => void result.refetch()}>Refresh settings</button>
            {editableKinds.includes(kind) && (kind !== EntityKind.SETTINGS || result.data?.resources.length === 0)
              ? <button type="button" className="primary" disabled={kind === EntityKind.SETTINGS && (!result.data || Boolean(result.error || result.isFetching))} onClick={() => setEditing({ key: newRequestId() })}><span className="settings-action-icon" aria-hidden="true">+</span>New {kindNames[kind]}</button>
              : null}
          </div> : null}
        </div> : null}
        <div className="settings-panels">
          <div hidden={area !== SettingsArea.Backups}><Backups active={visible && area === SettingsArea.Backups} /></div>
          <div hidden={area !== SettingsArea.Integrations}><Integrations active={visible && area === SettingsArea.Integrations} showCategoryIntro={false} onWorkflowReadyChange={reportIntegrationWorkflow} /></div>
          <div hidden={area !== SettingsArea.Transfer}><ConfigurationTransfer active={visible && area === SettingsArea.Transfer} showCategoryIntro={false} onWorkflowReadyChange={reportTransferWorkflow} /></div>
          <div hidden={area !== SettingsArea.Notifications}><NotificationSettings active={visible && area === SettingsArea.Notifications} showCategoryIntro={false} onWorkflowReadyChange={reportNotificationWorkflow} /></div>
          <div hidden={area !== SettingsArea.Diagnostics}>{connectionSettings ? <section aria-label="Connection"><h2>Connection</h2>{connectionSettings}</section> : null}<Doctor title={DoctorTitle.ConnectionDiagnostics} active={visible && area === SettingsArea.Diagnostics} visible={visible} /></div>
          <div hidden={area !== SettingsArea.Configuration}>
            {controlLocalWorker ? <div hidden={kind !== EntityKind.MACHINE || Boolean(machine || editing || deleting || routing || account)}><LocalWorkerControls control={controlLocalWorker} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
            {pairingAuthority ? <div hidden={kind !== EntityKind.DEVICE || Boolean(device)}><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} /></div> : null}
            <div hidden={!isApiAccounts || hasOverlay}>
              <AccountSettings section={AccountSettingsSection.Api} active={visible && isApiAccounts && !hasOverlay} accountTypeFilteringReady={accountTypeFilteringReady} accountTypeFilteringProblem={accountInventory.error} providerIdFilter={accountProviderID} clearProviderFilter={() => { setAccountProviderID(""); setAccountProviderHint(undefined); }} setProviderFilter={(providerId, provider) => { setAccountProviderID(providerId); setAccountProviderHint(provider); setPage(""); }} providers={accountProviders} eligibleProviders={eligibleProviders} providerSearch={providerSearch} setProviderSearch={setProviderSearch} providerSearchLoading={accountInventory.isFetching} providerSearchError={accountInventory.error} providerPicker={providerPicker} providerFilterHasMore={Boolean(accountInventory.data?.nextPageToken)} loadMoreProviderFilters={() => setProviderFilterPage(accountInventory.data?.nextPageToken ?? "")} subscriptionProviderResources={accountProviders.map((provider) => provider.provider)} subscriptionProviderManagement={subscriptionProviderManagement} openApiProviders={() => { setSelectedCategory(SettingsCategory.Providers); setPage(""); }} manageAccount={setAccount} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} onWorkflowReadyChange={reportAccountWorkflow} startApiWizard={startApiWizard} providerHint={accountProviderHint} />
            </div>
            <div hidden={!isSubscriptionAccounts || hasOverlay}>
              <AccountSettings section={AccountSettingsSection.Subscription} active={visible && isSubscriptionAccounts && !hasOverlay} accountTypeFilteringReady={accountTypeFilteringReady} accountTypeFilteringProblem={accountInventory.error} providerIdFilter={accountProviderID} clearProviderFilter={() => { setAccountProviderID(""); setAccountProviderHint(undefined); }} setProviderFilter={(providerId, provider) => { setAccountProviderID(providerId); setAccountProviderHint(provider); setPage(""); }} providers={accountProviders} eligibleProviders={eligibleProviders} providerSearch={providerSearch} setProviderSearch={setProviderSearch} providerSearchLoading={accountInventory.isFetching} providerSearchError={accountInventory.error} providerPicker={providerPicker} providerFilterHasMore={Boolean(accountInventory.data?.nextPageToken)} loadMoreProviderFilters={() => setProviderFilterPage(accountInventory.data?.nextPageToken ?? "")} subscriptionProviderResources={accountProviders.map((provider) => provider.provider)} subscriptionProviderManagement={subscriptionProviderManagement} openApiProviders={() => { setSelectedCategory(SettingsCategory.Providers); setPage(""); }} manageAccount={setAccount} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} onWorkflowReadyChange={reportAccountWorkflow} providerHint={accountProviderHint} />
            </div>
            {pricing ? <ModelPricing model={pricing} active={visible} close={() => { setPricing(undefined); void client.invalidateQueries({ refetchType: "active" }); }} /> : device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={() => { setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing ? <ConfigurationEditor key={editing.key} kind={editing.kind ?? kind} initial={editing.initial} initialData={editing.initialData} subscriptionOnly={editing.subscriptionOnly} active={visible} saved={done} cancel={() => setEditing(undefined)} focusName={focusNewProjectName} nameFocused={projectNameFocused} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : isApiProviders ? <ApiProviderSettings active={visible && isApiProviders} changed={done} createCustom={(initialData) => setEditing({ kind: EntityKind.PROVIDER, initialData, key: newRequestId() })} editCustom={(initial) => setEditing({ kind: EntityKind.PROVIDER, initial, key: newRequestId() })} manageAccounts={(providerID, entry) => { const provider = providerEntrySummary(entry); setSelectedCategory(SettingsCategory.ApiAccounts); setAccountProviderID(providerID); setAccountProviderHint(provider); setProviderSearch(entry.displayName); setPage(""); }} addAccount={(providerID, entry) => { const provider = providerEntrySummary(entry); setSelectedCategory(SettingsCategory.ApiAccounts); setAccountProviderID(providerID); setAccountProviderHint(provider); setStartApiWizard({ key: newRequestId(), providerId: providerID, provider }); setPage(""); }} deleteCustom={setDeleting} /> : isModels ? <ActiveModelSettings active={visible && isModels} createModel={() => setEditing({ kind: EntityKind.MODEL, key: newRequestId() })} editModel={(initial) => setEditing({ kind: EntityKind.MODEL, initial, key: newRequestId() })} priceModel={setPricing} /> : hasSpecializedPanel ? null : <>
              {result.isPending && !result.data ? <p role="status">Loading {selected.label.toLowerCase()}…</p> : null}
              <Problem error={result.error} />
              {result.error && result.data ? <p className="notice" role="status">Refresh failed. Showing the last successfully loaded results.</p> : null}
              {result.data?.resources.map((row) => { const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</h3>{kind === EntityKind.DEVICE ? <DeviceDetails resource={row} currentDeviceId={currentDeviceId} /> : null}{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small>{kind === EntityKind.REPOSITORY ? <><RepositoryGitHubAccess selected={row} active={visible && area === SettingsArea.Configuration} /><RepositoryGitHubItems selected={row} active={visible && area === SettingsArea.Configuration} /></> : null}<div className="actions">{kind === EntityKind.DEVICE && row.id === currentDeviceId ? <p>This desktop client cannot revoke its own registration.</p> : null}{kind === EntityKind.DEVICE && data.revoked === false && row.id !== currentDeviceId ? <button disabled={row.schemaVersion !== 1} onClick={() => setDevice(row)}>Revoke {resourceName(row)}</button> : null}{editableKinds.includes(kind) ? <button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Edit {kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</button> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <button disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}>Delete {resourceName(row)}</button> : null}{kind === EntityKind.MODEL ? <button disabled={row.schemaVersion !== 1} onClick={() => setPricing(row)}>Token pricing</button> : null}{kind === EntityKind.AGENT ? <button disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>Preview routing</button> : null}{kind === EntityKind.MACHINE ? <button disabled={row.schemaVersion !== 1} onClick={() => setMachine(row)}>Inspect installed harnesses</button> : null}{kind === EntityKind.ACCOUNT ? <button disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>Manage connection</button> : null}</div></article>; })}
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
