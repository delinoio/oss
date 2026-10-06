import type { UsageEntry } from "./usage-entry";
import { providerPresetNames } from "@delinoio/delidev-api-client";
import { useOpenRouterOAuth } from "./account-oauth";
import { SubscriptionAccounts } from "./subscription-accounts";
import { Backups } from "./backups";
import { RepositoryGitHubAccess } from "./integration-access";
import { RepositoryGitHubItems } from "./github-items";
import { Integrations } from "./integrations";
import { ConfigurationTransfer } from "./configuration-transfer";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { NotificationSettings } from "./notification-settings";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ConfigurationQuery, configurationSchemaVersion, supportsResourceSchema, EntityKind, ProviderInventoryCapability, ProviderConnectionMethod, ProviderPresetId, ProviderQuery, ResourceQuery, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { ConfigurationFields, editableKinds, kindNames, newConfiguration, ServerPreferenceSection } from "./configuration-fields";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { JobState, TrackedJob } from "./jobs";
import { LocalWorkerControls, LocalWorkerPresentation, type ControlLocalWorker } from "./local-worker-controls";
import { SSHSetup } from "./ssh-setup";
import { MachineSettings } from "./machine-settings";
import { NetworkSettings } from "./network-settings";
import { DeviceAction, DeviceRow, DeviceRevocation, DeviceRevocationExit, Doctor } from "./device-settings";
import { DoctorTitle } from "./doctor";
import { AccountConnection } from "./account-connection";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { PairingGrant, type PairingAuthority } from "./pairing-grant";
import { AccountSettings, AccountSettingsSection, type AccountProviderPicker, type AccountProviderSummary } from "./account-settings";
import { ApiProviderSettings, providerInventoryReady, type ProviderListState } from "./provider-model-settings";
import { revealAgentInvalidControl } from "./agent-configuration";
import { RepositoryRegistration, type ChooseRepositoryFolder } from "./repository-registration";
import type { ReadLocalWorkerProof } from "./local-worker";
import { SettingsLifetime } from "./settings-lifetime";
import { AppearanceSettings } from "./appearance";
import { readableServerPreferences, revealServerPreferenceInvalidControl, ServerPreferencesEmpty, ServerPreferencesSummary, serverPreferenceLabel } from "./server-preferences";
import { SettingsLoading } from "./settings-presentation";
import { ToastKind, useNotifications } from "./toast-notifications";
import "./api-account.css";
import { SettingsTasks, SettingsTaskBackground, SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useRetainSettingsTask, useSettingsTaskVisible, useInSettingsTask, useCloseSettingsTask } from "./settings-task-context";

export function ConfigurationEditor({ kind, initial, initialData, subscriptionOnly = false, serverPreferenceSection = ServerPreferenceSection.All, active, saved, cancel, focusName = false, nameFocused }: { kind: EntityKind; initial?: Resource; initialData?: Document; subscriptionOnly?: boolean; serverPreferenceSection?: ServerPreferenceSection; active: boolean; saved: () => void; cancel: () => void; focusName?: boolean; nameFocused?: () => void }) {
  const notifications = useNotifications();
  const [data, setData] = useState<Document>(() => initial ? document(initial) : initialData ?? newConfiguration(kind));
  const [job, setJob] = useState<Resource | "unknown">();
  const [childPending, setChildPending] = useState(false);
  const [problem, setProblem] = useState("");
  const form = useRef<HTMLFormElement>(null);
  const formId = useId(), taskVisible = useSettingsTaskVisible(), inTask = useInSettingsTask(), cancelTask = useCloseSettingsTask(cancel);
  useRetainSettingsTask(Boolean(job) || childPending);
  const current = useQuery(ResourceQuery.getResource, { kind, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`configuration:${kind}:${initial?.id ?? "new"}`, ConfigurationQuery.saveConfiguration, (result, request) => { if (result.job) setJob(result.job); else if (result.resource) { notifications.notify({ kind: ToastKind.Success, message: `${kind === EntityKind.SETTINGS ? serverPreferenceLabel(serverPreferenceSection) : kindNames[kind]} saved.`, id: request.mutation?.requestId }); saved(); } else setJob("unknown"); });
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = mutation.busy || mutation.uncertain;
  useEffect(() => {
    if (!active || !taskVisible || !focusName || kind !== EntityKind.PROJECT || initial || blocked) return;
    const frame = window.requestAnimationFrame(() => {
      const name = form.current?.querySelector<HTMLInputElement>("input");
      if (!name || name.disabled) return;
      name.focus();
      nameFocused?.();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [active, blocked, focusName, initial, kind, nameFocused, taskVisible]);
  const change = (value: Document) => {
    if (encode(value).byteLength > 1 << 20 || (kind === EntityKind.TEMPLATE && new TextEncoder().encode(text(value.contents)).byteLength > 128 << 10)) { setProblem("This configuration is too large. Shorten the text before adding more content."); return; }
    setData(value); setProblem("");
  };
  const isApiEntry = kind === EntityKind.ACCOUNT && data.type === "api";
  const kindLabel = isApiEntry ? "AI API key entry" : kind === EntityKind.SETTINGS ? serverPreferenceLabel(serverPreferenceSection) : kindNames[kind];
  if (job) return <section className={isApiEntry ? "api-entry-workflow" : undefined}>{isApiEntry ? <header className="api-entry-heading"><h1>{kindLabel} save accepted</h1><p className="api-entry-scope">Saved on the selected server.</p></header> : <h3>{kindLabel} save accepted</h3>}{job === "unknown" ? <p role="alert">The server acknowledged this request without a readable result. Inspect its receipt before starting another save.</p> : <TrackedJob initial={job} active={active}>{(state) => state === JobState.Succeeded ? <><p>Configuration saved after Worker validation.</p><button onClick={saved}>Done</button></> : state === JobState.Failed || state === JobState.Canceled ? <button onClick={() => setJob(undefined)}>Return to retained draft</button> : null}</TrackedJob>}</section>;
  const validSubscriptionProvider = !subscriptionOnly || (kind === EntityKind.PROVIDER && data.protocol === "native-subscription" && data.authentication === "subscription" && text(data.endpoint) === "");
  return <form id={formId} ref={form} className={kind === EntityKind.PROJECT ? "project-editor" : kind === EntityKind.AGENT ? "agent-configuration" : kind === EntityKind.SETTINGS ? "server-preferences-editor" : isApiEntry ? "api-entry-workflow api-entry-preferences" : undefined} onInvalidCapture={kind === EntityKind.AGENT ? revealAgentInvalidControl : kind === EntityKind.SETTINGS ? revealServerPreferenceInvalidControl : undefined} onSubmit={(event) => { event.preventDefault(); if (blocked || childPending || stale || data.reconfiguration_required === true || !validSubscriptionProvider || (initial && current.error)) return; void mutation.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, kind, schemaVersion: configurationSchemaVersion(kind, data), documentJson: encode(data) }); }}>
    {isApiEntry ? <header className="api-entry-heading"><h1 hidden={inTask}>{initial ? "Edit preferences" : "New AI API key entry"}</h1><p>{resourceName(initial)}</p><p className="api-entry-scope">Saved on the selected server.</p></header> : <h3 hidden={inTask}>{initial ? "Edit" : "New"} {kindLabel}</h3>}
    {kind === EntityKind.AGENT && !initial ? <p className="agent-subtitle">Configure the essentials, then customize only what you need.</p> : null}
    <fieldset disabled={blocked}><ConfigurationFields kind={kind} data={data} change={change} active={active} existing={Boolean(initial)} pendingOperation={setChildPending} subscriptionOnly={subscriptionOnly} serverPreferenceSection={serverPreferenceSection} /></fieldset>
    {stale ? <p role="alert">This entry changed elsewhere. Your draft is retained. Cancel this edit and reopen the latest entry before saving.</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={current.error || mutation.error} />
    {subscriptionOnly && !validSubscriptionProvider ? <p role="alert">Subscription providers must use native-subscription protocol, subscription authentication and an empty endpoint.</p> : null}
    {kind === EntityKind.AGENT || kind === EntityKind.SETTINGS ? <SettingsTaskActions form={formId} className={kind === EntityKind.AGENT ? "agent-footer" : "server-preferences-actions"}><button type="button" data-settings-task-cancel disabled={!inTask && (blocked || childPending)} onClick={cancelTask}>Cancel edit</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same configuration</button> : null}<button className="primary" disabled={blocked || childPending || stale || data.reconfiguration_required === true || !validSubscriptionProvider || Boolean(initial && current.error)}>Save {kindLabel}</button></SettingsTaskActions>
      : <SettingsTaskActions form={formId}><button type="button" data-settings-task-cancel disabled={!inTask && (blocked || childPending)} onClick={cancelTask}>Cancel edit</button><button className="primary" disabled={blocked || childPending || stale || data.reconfiguration_required === true || !validSubscriptionProvider || Boolean(initial && current.error)}>Save {kindLabel}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same configuration</button> : null}</SettingsTaskActions>}

  </form>;
}

export enum SettingsEntryDestination { Repositories = "repositories", NewProject = "new-project" }
enum SettingsArea { Configuration, Diagnostics, Notifications, Transfer, Integrations, Backups, Appearance }
enum SettingsCategory {
  Appearance = "appearance",
  SubscriptionAccounts = "subscription-accounts", ApiAccounts = "api-accounts", Providers = "providers", AgentWorkers = "agent-workers", Instructions = "instructions",
  Projects = "projects", Repositories = "repositories", ExecutionWorkers = "execution-workers", PairedDevices = "paired-devices",
  ServerPreferences = "server-preferences", GitWorkflow = "git-workflow", Integrations = "integrations", Diagnostics = "diagnostics", Notifications = "notifications", Transfer = "transfer", Backups = "backups",
}
enum SettingsGroup { Ai = "AI", Coding = "Coding", Devices = "Device management", System = "System" }

const settingsCategories: Record<SettingsCategory, { label: string; description: string; kind?: EntityKind; area: SettingsArea }> = {
  [SettingsCategory.Appearance]: { label: "Appearance", description: "Saved on this computer.", area: SettingsArea.Appearance },
  [SettingsCategory.Backups]: { label: "Backups", description: "Inspect managed database images and follow durable creation and deletion jobs on the selected server.", area: SettingsArea.Backups },
  [SettingsCategory.SubscriptionAccounts]: { label: "AI Subscription", description: "Manage your subscriptions and connect more accounts.", kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.ApiAccounts]: { label: "AI API Keys", description: "Manage AI API keys and keyless local connections. Connection and health are separate states.", kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.Providers]: { label: "API Providers", description: "Provider availability is saved on the selected server.", kind: EntityKind.PROVIDER, area: SettingsArea.Configuration },
  [SettingsCategory.AgentWorkers]: { label: "Agent Workers", description: "Saved on the selected server.", kind: EntityKind.AGENT, area: SettingsArea.Configuration },
  [SettingsCategory.Instructions]: { label: "Instructions", description: "Saved on the selected server.", kind: EntityKind.TEMPLATE, area: SettingsArea.Configuration },
  [SettingsCategory.Projects]: { label: "Projects", description: "Saved on the selected server.", kind: EntityKind.PROJECT, area: SettingsArea.Configuration },
  [SettingsCategory.Repositories]: { label: "Repositories", description: "Saved on the selected server.", kind: EntityKind.REPOSITORY, area: SettingsArea.Configuration },
  [SettingsCategory.ExecutionWorkers]: { label: "Runner Devices", description: "Saved on the selected server.", kind: EntityKind.MACHINE, area: SettingsArea.Configuration },
  [SettingsCategory.PairedDevices]: { label: "Paired devices", description: "Saved on the selected server.", kind: EntityKind.DEVICE, area: SettingsArea.Configuration },
  [SettingsCategory.ServerPreferences]: { label: "Server preferences", description: "Saved on the selected server.", kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.GitWorkflow]: { label: "Git", description: "Saved on the selected server.", kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.Integrations]: { label: "Git Profiles", description: "Manage GitHub profiles for repository access. AI accounts are configured separately.", area: SettingsArea.Integrations },
  [SettingsCategory.Diagnostics]: { label: "Connection & diagnostics", description: "Read-only observations from the selected server. This check does not repair state, connect an account or run model inference.", area: SettingsArea.Diagnostics },
  [SettingsCategory.Notifications]: { label: "Notifications", description: "These preferences belong to this client on the selected server. Inbox requests stay available when notifications are disabled or cannot be delivered.", area: SettingsArea.Notifications },
  [SettingsCategory.Transfer]: { label: "Import / Export", description: "Move configuration between DeliDev servers.", area: SettingsArea.Transfer },
};

const settingsGroups: { label: SettingsGroup; categories: SettingsCategory[] }[] = [
  { label: SettingsGroup.Ai, categories: [SettingsCategory.SubscriptionAccounts, SettingsCategory.ApiAccounts, SettingsCategory.Providers, SettingsCategory.AgentWorkers, SettingsCategory.Instructions] },
  { label: SettingsGroup.Coding, categories: [SettingsCategory.Projects, SettingsCategory.Repositories, SettingsCategory.Integrations, SettingsCategory.GitWorkflow] },
  { label: SettingsGroup.Devices, categories: [SettingsCategory.ExecutionWorkers, SettingsCategory.PairedDevices] },
  { label: SettingsGroup.System, categories: [SettingsCategory.Appearance, SettingsCategory.ServerPreferences, SettingsCategory.Diagnostics, SettingsCategory.Notifications, SettingsCategory.Transfer, SettingsCategory.Backups] },
];

const settingsIcons: Record<SettingsCategory, string> = {
  [SettingsCategory.Appearance]: "M12 3a9 9 0 1 0 0 18V3zM12 3a9 9 0 0 1 0 18",
  [SettingsCategory.Backups]: "M4 4h16v16H4zM8 4v6h8V4M8 20v-6h8v6",
  [SettingsCategory.Providers]: "M7 18a4 4 0 1 1 .9-7.9A5.5 5.5 0 0 1 18 9.5 3.5 3.5 0 0 1 18 18z",
  [SettingsCategory.SubscriptionAccounts]: "M16 20v-1a4 4 0 0 0-4-4h-1a4 4 0 0 0-4 4v1m4-9a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7",
  [SettingsCategory.ApiAccounts]: "M16 20v-1a4 4 0 0 0-4-4h-1a4 4 0 0 0-4 4v1m4-9a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7",
  [SettingsCategory.AgentWorkers]: "M4 8h16v12H4zM8 8V5h8v3m-8 5h.01M16 13h.01M9 16h6",
  [SettingsCategory.Instructions]: "M6 3h8l4 4v14H6zM14 3v5h5M9 12h6m-6 4h6",
  [SettingsCategory.Projects]: "M3 7h7l2 2h9v11H3zM3 7V5h7l2 2",
  [SettingsCategory.Repositories]: "M6 4v6m0 0a3 3 0 1 0 0 6m0-6h7a3 3 0 1 1 0 6h5m-12 0v4",
  [SettingsCategory.ExecutionWorkers]: "M3 4h18v13H3zM8 21h8m-4-4v4M7 8h4m-4 4h10",
  [SettingsCategory.PairedDevices]: "M7 3h10v18H7zM10 6h4m-4 12h4",
  [SettingsCategory.GitWorkflow]: "M6 4v6m0 0a3 3 0 1 0 0 6m0-6h7a3 3 0 1 1 0 6h5m-12 0v4",
  [SettingsCategory.ServerPreferences]: "M4 6h16M4 12h16M4 18h16M9 4v4m6 2v4m-3 2v4",
  [SettingsCategory.Integrations]: "M9 15l6-6m-8 9H5a4 4 0 0 1 0-8h4m6-4h4a4 4 0 0 1 0 8h-4",
  [SettingsCategory.Diagnostics]: "M3 12h4l3-7 4 14 3-7h4",
  [SettingsCategory.Notifications]: "M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9m-8 12h4",
  [SettingsCategory.Transfer]: "M7 7h13m0 0-4-4m4 4-4 4M17 17H4m0 0 4 4m-4-4 4-4",
};

function SettingsIcon({ category }: { category: SettingsCategory }) {
  return <svg className="settings-category-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={settingsIcons[category]} /></svg>;
}

function AgentWorkerRow({ row, edit, preview, remove }: { row: Resource; edit: () => void; preview: () => void; remove: () => void }) {
  const data = document(row);
  let name = resourceName(row);
  // Unsupported schemas stay non-actionable. Only bounded inert name text is
  // projected within the Agent name's 256-byte UTF-8 limit for identifying the
  // row; it never enters an editor or request.
  // Remove this projection once the shared document parser supports that schema.
  if (!supportsResourceSchema(row) && row.documentJson.byteLength <= 1 << 20) {
    try {
      const display = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(row.documentJson)));
      const projectedName = text(display.name) || text(display.alias);
      // Check code units first so malformed large values never allocate an
      // equally large UTF-8 buffer merely to validate display text.
      if (projectedName.length <= 256 && new TextEncoder().encode(projectedName).byteLength <= 256) name = projectedName || name;
    } catch { /* Malformed display text keeps the existing unnamed fallback. */ }
  }
  return <article className="settings-agent-row">
    <div className="settings-agent-details">
      <h3>{name}</h3>
      {text(data.health) ? <p>Status: {text(data.health)}</p> : null}
      {text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}
      {data.reconfiguration_required === true ? <p role="status">Reconfiguration required</p> : null}
      <small>{row.id}</small>
    </div>
    <div className="actions settings-agent-actions">
      <button type="button" disabled={!supportsResourceSchema(row)} aria-label={`Edit ${name}`} onClick={edit}>Edit</button>
      <button type="button" disabled={!supportsResourceSchema(row) || data.reconfiguration_required === true} aria-label={`Preview routing for ${name}`} onClick={preview}>Preview routing</button>
      <button type="button" disabled={!supportsResourceSchema(row)} aria-label={`Delete ${name}`} onClick={remove}>Delete</button>
    </div>
  </article>;
}

interface SettingsProps { openUsage?: (entry: UsageEntry) => void; readLocalWorker?: ReadLocalWorkerProof; chooseRepositoryFolder?: ChooseRepositoryFolder; connectionSettings?: React.ReactNode; pairingAuthority?: PairingAuthority; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string; entryDestination?: SettingsEntryDestination; destinationConsumed?: () => void }
enum SettingsEntryKind { NewProject, ManageAccounts, AddAccount }
type SettingsCategoryEntry = { kind: SettingsEntryKind.NewProject } | { kind: SettingsEntryKind.ManageAccounts | SettingsEntryKind.AddAccount; providerId: string; provider?: AccountProviderSummary };
interface SettingsSelection { category: SettingsCategory; key: string; entry?: SettingsCategoryEntry }
type NavigateSettings = (category: SettingsCategory, entry?: SettingsCategoryEntry) => void;

function entrySelection(destination?: SettingsEntryDestination): SettingsSelection {
  return { category: destination === SettingsEntryDestination.Repositories ? SettingsCategory.Repositories : destination === SettingsEntryDestination.NewProject ? SettingsCategory.Projects : SettingsCategory.SubscriptionAccounts, key: newRequestId(), entry: destination === SettingsEntryDestination.NewProject ? { kind: SettingsEntryKind.NewProject } : undefined };
}

export function Settings({ visible = true, ...props }: SettingsProps) {
  return visible ? <SettingsVisit {...props} /> : null;
}

function SettingsVisit({ entryDestination, destinationConsumed, ...props }: SettingsProps) {
  const closeDrawer = useCloseSidebarDrawer();
  const [selection, setSelection] = useState(() => entrySelection(entryDestination));
  const initialDestination = useRef(entryDestination);
  const handledDestination = useRef<SettingsEntryDestination | undefined>(undefined);
  const navigate = useCallback<NavigateSettings>((category, entry) => {
    closeDrawer();
    setSelection(current => current.category === category && !entry ? current : { category, entry, key: newRequestId() });
  }, [closeDrawer]);
  useEffect(() => {
    if (!entryDestination) { handledDestination.current = undefined; return; }
    if (handledDestination.current === entryDestination) return;
    handledDestination.current = entryDestination;
    if (initialDestination.current === entryDestination) initialDestination.current = undefined;
    else {
      const target = entrySelection(entryDestination);
      navigate(target.category, target.entry);
    }
    destinationConsumed?.();
  }, [destinationConsumed, entryDestination, navigate]);
  return <>
    <SidebarSurface active title="Settings" className="settings-navigation">
      <nav aria-label="Settings categories">
        {settingsGroups.map(group => <section className="settings-nav-group" key={group.label}>
          <h2>{group.label}</h2>
          {group.categories.map(category => <button type="button" className="settings-category-button" key={category} data-settings-category={category} aria-current={selection.category === category ? "page" : undefined} aria-pressed={selection.category === category} onClick={() => navigate(category)}>
            <SettingsIcon category={category} /><span>{settingsCategories[category].label}</span>
          </button>)}
        </section>)}
      </nav>
    </SidebarSurface>
    {/* Each category owns its waits and drafts. Disposal rejects late results
        without canceling or replaying already accepted server/native work. */}
    <SettingsLifetime key={selection.key}>{opening => <MutationIntents><SettingsWorkspace {...props} selectedCategory={selection.category} entry={selection.entry} navigate={navigate} controlLocalWorker={props.controlLocalWorker ? (action, generation) => opening.native(() => props.controlLocalWorker!(action, generation)) : undefined} /></MutationIntents>}</SettingsLifetime>
  </>;
}

function SettingsWorkspace({ openUsage, connectionSettings, visible = true, controlLocalWorker, readLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority, selectedCategory, entry, navigate }: SettingsProps & { selectedCategory: SettingsCategory; entry?: SettingsCategoryEntry; navigate: NavigateSettings }) {
  const oauth = useOpenRouterOAuth();
  const oauthStarted = useRef(false);
  useEffect(() => {
    if (oauthStarted.current || entry?.kind !== SettingsEntryKind.AddAccount || !entry.provider?.oauthAvailable || !oauth.available) return;
    let active = true;
    // Wait until mount effects settle so Strict Mode's setup replay cannot
    // create an OAuth owner that its simulated cleanup immediately disposes.
    queueMicrotask(() => {
      if (!active || oauthStarted.current) return;
      oauthStarted.current = true;
      oauth.start(entry.provider!);
    });
    return () => { active = false; };
  }, [entry, oauth]);
  const [device, setDevice] = useState<Resource>();
  const [expandedDevices, setExpandedDevices] = useState<ReadonlySet<string>>(() => new Set());
  const pairedPageIds = useRef<ReadonlySet<string>>(new Set());
  const [pairingTriggerContainer, setPairingTriggerContainer] = useState<HTMLSpanElement | null>(null);
  const deviceContent = useRef<HTMLDivElement>(null), refreshDevices = useRef<HTMLButtonElement>(null);
  const deviceReturnFocus = useRef<{ id: string; exit: DeviceRevocationExit } | undefined>(undefined);
  const [page, setPage] = useState("");
  const [editing, setEditing] = useState<{ kind?: EntityKind; initial?: Resource; initialData?: Document; key: string; subscriptionOnly?: boolean } | undefined>(() => entry?.kind === SettingsEntryKind.NewProject ? { key: newRequestId() } : undefined);
  const [machine, setMachine] = useState<Resource>();
  const [deleting, setDeleting] = useState<Resource>();
  const [routing, setRouting] = useState<Resource>();
  const [account, setAccount] = useState<Resource>();
  const [providerList, setProviderList] = useState<ProviderListState>({ query: "", page: "" });
  const [providerChoicePage, setProviderChoicePage] = useState("");
  const [startApiWizard, setStartApiWizard] = useState<{ key: string; providerId: string; provider?: AccountProviderSummary } | undefined>(() => entry?.kind === SettingsEntryKind.AddAccount ? { key: newRequestId(), providerId: entry.providerId, provider: entry.provider } : undefined);
  const [apiProviderID, setApiProviderID] = useState(() => entry && entry.kind !== SettingsEntryKind.NewProject ? entry.providerId : "");
  const [apiProviderHint, setApiProviderHint] = useState<AccountProviderSummary | undefined>(() => entry && entry.kind !== SettingsEntryKind.NewProject ? entry.provider : undefined);
  const [apiProviderPage, setApiProviderPage] = useState("");
  const [focusNewProjectName, setFocusNewProjectName] = useState(entry?.kind === SettingsEntryKind.NewProject);
  const client = useQueryClient();
  const selected = settingsCategories[selectedCategory];
  const area = selected.area;
  const kind = selected.kind ?? EntityKind.PROVIDER;
  const isApiAccounts = selectedCategory === SettingsCategory.ApiAccounts;
  const isSubscriptionAccounts = selectedCategory === SettingsCategory.SubscriptionAccounts;
  const isAccountCategory = isApiAccounts || isSubscriptionAccounts;
  const isApiProviders = selectedCategory === SettingsCategory.Providers;
  const isAgentWorkers = selectedCategory === SettingsCategory.AgentWorkers;
  const isProjects = selectedCategory === SettingsCategory.Projects;
  const isServerPreferences = selectedCategory === SettingsCategory.ServerPreferences;
  const isGitWorkflow = selectedCategory === SettingsCategory.GitWorkflow;
  const isPreferenceCategory = isServerPreferences || isGitWorkflow;
  const preferenceSection = isGitWorkflow ? ServerPreferenceSection.GitWorkflow : ServerPreferenceSection.AccountRouting;
  const preferenceLabel = serverPreferenceLabel(preferenceSection);
  const isPairedDevices = selectedCategory === SettingsCategory.PairedDevices;
  const isRunnerDevices = selectedCategory === SettingsCategory.ExecutionWorkers;
  const hasSpecializedPanel = isAccountCategory || isApiProviders;
  const hasOverlay = Boolean(editing || account || deleting || routing || machine || device);
  const categoryDescription = kind === EntityKind.DEVICE
    ? "Pair devices using a short-lived document."
    : kind === EntityKind.MACHINE && !controlLocalWorker ? "Configure these entries through the DeliDev CLI."
    : selected.description;
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: page } }, { enabled: visible && area === SettingsArea.Configuration && !hasSpecializedPanel });
  // Within this category, only a successful paired page can prune retained
  // identities. Leaving the category disposes every disclosure with its scope.
  useEffect(() => {
    if (!isPairedDevices || !result.isSuccess || !result.data) return;
    const ids = new Set(result.data.resources.slice(0, 50).map((row) => row.id));
    pairedPageIds.current = ids;
    setExpandedDevices((current) => {
      const retained = new Set([...current].filter((id) => ids.has(id)));
      return retained.size === current.size ? current : retained;
    });
  }, [isPairedDevices, result.data, result.isSuccess]);
  useLayoutEffect(() => {
    if (device || !isPairedDevices || !deviceReturnFocus.current) return;
    const { id, exit } = deviceReturnFocus.current;
    deviceReturnFocus.current = undefined;
    const action = exit === DeviceRevocationExit.Cancel ? DeviceAction.Revoke : DeviceAction.Details;
    const control = [...(deviceContent.current?.querySelectorAll<HTMLButtonElement>("button[data-device-id]") ?? [])].find((button) => button.dataset.deviceId === id && button.dataset.deviceAction === action && !button.disabled);
    const target = control ?? refreshDevices.current;
    requestAnimationFrame(() => {
      if (target?.isConnected && !globalThis.document.querySelector("dialog[open]:not([role=region])")) target.focus({ preventScroll: true });
    });
  }, [device, isPairedDevices]);
  const toggleDevice = (id: string) => setExpandedDevices((current) => {
    if (!pairedPageIds.current.has(id)) return current;
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else if (next.size < 50) next.add(id);
    return next;
  });
  const choosePage = (token: string) => {
    if (isPairedDevices) setExpandedDevices(new Set());
    setPage(token);
  };
  const apiInventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: false, pageSize: 50, pageToken: apiProviderPage }, { enabled: visible && isApiAccounts });
  const eligibleInventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 50, pageToken: providerChoicePage }, { enabled: visible && area === SettingsArea.Configuration && isApiAccounts });
  const providerPicker: AccountProviderPicker = { ready: Boolean(eligibleInventory.data && providerInventoryReady(eligibleInventory.data.capabilities) && eligibleInventory.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER)), loaded: Boolean(eligibleInventory.data), fetching: eligibleInventory.isFetching, error: eligibleInventory.error, pageToken: providerChoicePage, nextPageToken: eligibleInventory.data?.nextPageToken ?? "", retry: () => { void eligibleInventory.refetch(); }, next: () => setProviderChoicePage(eligibleInventory.data?.nextPageToken ?? ""), first: () => setProviderChoicePage("") };
  const providerPresets = useQuery(ProviderQuery.listProviderPresets, {}, { enabled: visible && area === SettingsArea.Configuration && isApiAccounts });
  const apiAccountTypeFilteringReady = Boolean(apiInventory.data && providerInventoryReady(apiInventory.data.capabilities) && apiInventory.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER));
  const presets = new Map<string, Document>();
  try {
    if (providerPresets.data) {
      const parsed = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(providerPresets.data.presetsJson));
      for (const preset of items(parsed).map(object)) presets.set(text(preset.id), preset);
    }
  } catch { /* Preset-specific help stays unavailable when the server response is malformed. */ }
  const providerSummary = (entry: ProviderInventoryEntry, oauthSupported = false): AccountProviderSummary | undefined => entry.providerId && entry.provider?.kind === EntityKind.PROVIDER ? {
    providerId: entry.providerId,
    displayName: entry.displayName,
    enabled: entry.enabled,
    provider: entry.provider,
    keyGuidance: text(presets.get(providerPresetNames.get(entry.presetId) ?? "")?.key_guidance) || "Use the provider's documented API key flow.",
    documentationUrl: text(presets.get(providerPresetNames.get(entry.presetId) ?? "")?.documentation),
    presetId: providerPresetNames.get(entry.presetId),
    keyCreationUrl: text(presets.get(providerPresetNames.get(entry.presetId) ?? "")?.key_creation_url),
    oauthAvailable: oauthSupported && entry.enabled && entry.connectionMethod === ProviderConnectionMethod.OAUTH_PKCE,
  } : undefined;
  const apiProviders = (apiInventory.data?.entries ?? []).map(entry => providerSummary(entry, Boolean(apiInventory.data?.capabilities.includes(ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1)))).filter((value): value is AccountProviderSummary => value !== undefined);
  const eligibleProviders = (eligibleInventory.data?.entries ?? []).map(entry => providerSummary(entry, Boolean(eligibleInventory.data?.capabilities.includes(ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1)))).filter((value): value is AccountProviderSummary => value !== undefined && value.enabled);
  // Keep the toolbar mounted while a task is open so closing can restore focus
  // to the exact action that opened it. SettingsTaskBackground makes it inert
  // during the task, so this does not expose a concurrent background action.
  const configurationList = area === SettingsArea.Configuration && !hasSpecializedPanel;
  const successfulEmptyFirstPage = !page && Boolean(result.data && result.data.resources.length === 0 && !result.error && !result.data.nextPageToken);
  const retainedServerEmpty = isPreferenceCategory && !page && Boolean(result.data && result.data.resources.length === 0 && !result.data.nextPageToken);
  const hidePagination = successfulEmptyFirstPage || (isPreferenceCategory && !page && Boolean(result.data && !result.data.nextPageToken));
  const serverSingleton = isPreferenceCategory && result.data?.resources.length === 1 ? result.data.resources[0] : undefined;
  const projectNameFocused = useCallback(() => setFocusNewProjectName(false), []);
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  const providerEntrySummary = (entry?: ProviderInventoryEntry, oauthSupported = false) => entry ? providerSummary(entry, oauthSupported) : undefined;
  const closeTask = () => { setDevice(undefined); setMachine(undefined); setDeleting(undefined); setRouting(undefined); setEditing(undefined); setAccount(undefined); };
  const taskResource = editing?.initial ?? device ?? machine ?? deleting ?? routing ?? account;
  const taskKind = editing?.kind ?? kind;
  const taskTitle = device ? "Revoke device" : machine ? "Runner Device details" : deleting ? "Delete configuration" : routing ? "Preview routing" : account ? "Manage connection" : editing && taskKind === EntityKind.REPOSITORY && !editing.initial ? "Add repository" : editing ? `${editing.initial ? "Edit" : "New"} ${taskKind === EntityKind.SETTINGS ? preferenceLabel : kindNames[taskKind]}` : "Settings task";
  const taskSize = deleting || device ? SettingsDialogSize.Confirmation : routing ? SettingsDialogSize.Form : machine || account || [EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.REPOSITORY].includes(taskKind) ? SettingsDialogSize.Wide : SettingsDialogSize.Form;
  return <SettingsTasks>
      <section className={isProjects ? "settings-content settings-projects" : isPreferenceCategory ? `settings-content settings-server-preferences${isGitWorkflow ? " settings-git-workflow" : ""}` : isApiAccounts ? "settings-content settings-api-keys" : isRunnerDevices ? "settings-content settings-runner-devices" : "settings-content"} aria-label="Settings content">
        <SettingsTaskBackground><div className="settings-content-column">
        <div ref={deviceContent} className={isAgentWorkers ? "settings-agent-column" : isPairedDevices ? "settings-paired-column" : isRunnerDevices && !hasOverlay ? "settings-runner-column" : area === SettingsArea.Transfer ? "settings-transfer-column" : undefined}>
        {isGitWorkflow ? <p className="settings-breadcrumb">Coding / Git</p> : null}
        {area !== SettingsArea.Diagnostics && area !== SettingsArea.Backups && !isApiAccounts && !(isApiProviders && !hasOverlay) ? <div className="settings-category-heading">
          <div className="settings-category-title"><h1 aria-live="polite" aria-atomic="true">{selected.label}</h1>{isAgentWorkers ? <p className="settings-agent-summary">Reusable configurations for your agents.</p> : isPreferenceCategory ? <p>{isGitWorkflow ? "Worktree fetch and pull request remediation." : "Default account routing."}</p> : null}<p className={isAgentWorkers ? "settings-scope settings-agent-scope" : isPreferenceCategory ? "settings-scope server-preferences-scope" : isPairedDevices ? "settings-scope paired-device-summary" : "settings-scope"}>{categoryDescription}</p>{isPairedDevices ? <p className="paired-device-scope">Saved on the selected server.</p> : null}</div>
          {configurationList ? <div className="settings-toolbar">
            <button type="button" ref={isPairedDevices ? refreshDevices : undefined} aria-label={isGitWorkflow ? "Refresh Git workflow" : undefined} onClick={() => void result.refetch()}>{isGitWorkflow ? "Refresh" : "Refresh settings"}</button>
            {isPairedDevices && pairingAuthority ? <span ref={setPairingTriggerContainer} /> : null}
            {serverSingleton ? <button type="button" className="primary" disabled={!readableServerPreferences(serverSingleton)} onClick={() => setEditing({ initial: serverSingleton, key: newRequestId() })}>Edit {preferenceLabel}</button> : null}
            {editableKinds.includes(kind) && (kind !== EntityKind.SETTINGS || !result.data?.resources.length)
              ? <button type="button" className="primary" disabled={kind === EntityKind.SETTINGS && (!successfulEmptyFirstPage || result.isFetching)} onClick={() => setEditing({ key: newRequestId() })}><span className="settings-action-icon" aria-hidden="true">+</span>{kind === EntityKind.REPOSITORY ? "Add repository" : `New ${kind === EntityKind.SETTINGS ? preferenceLabel : kindNames[kind]}`}</button>
              : null}
          </div> : null}
        </div> : null}
        <div className="settings-panels">
          {area === SettingsArea.Appearance ? <div><AppearanceSettings /></div> : null}
          {area === SettingsArea.Backups ? <div><Backups active={visible} /></div> : null}
          {area === SettingsArea.Integrations ? <div><Integrations active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Transfer ? <div><ConfigurationTransfer active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Notifications ? <div><NotificationSettings active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Diagnostics ? <div>{connectionSettings ? <section aria-label="Connection"><h2>Connection</h2>{connectionSettings}</section> : null}<Doctor title={DoctorTitle.ConnectionDiagnostics} active={visible && area === SettingsArea.Diagnostics} visible={visible} /></div> : null}
          {area === SettingsArea.Configuration ? <div>
            {controlLocalWorker && isRunnerDevices ? <div hidden={Boolean(machine || editing || deleting || routing || account)}><LocalWorkerControls control={controlLocalWorker} presentation={LocalWorkerPresentation.RunnerDevices} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
            {pairingAuthority && isPairedDevices ? <div hidden={Boolean(device)}><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} triggerContainer={pairingTriggerContainer} /></div> : null}
            {isApiAccounts ? <div hidden={hasOverlay}>
              <AccountSettings openUsage={openUsage} oauth={oauth} section={AccountSettingsSection.Api} active={visible && isApiAccounts && !hasOverlay} accountTypeFilteringReady={apiAccountTypeFilteringReady} accountTypeFilteringProblem={apiInventory.error} accountTypeFilteringLoading={apiInventory.isLoading} accountTypeFilteringFetching={apiInventory.isFetching} retryAccountCapabilities={() => { void apiInventory.refetch(); }} providerIdFilter={apiProviderID} clearProviderFilter={() => { setApiProviderID(""); setApiProviderHint(undefined); setApiProviderPage(""); setStartApiWizard(undefined); }} setProviderFilter={(providerId, provider) => { setApiProviderID(providerId); setApiProviderHint(provider); setPage(""); }} providers={apiProviders} eligibleProviders={eligibleProviders} providerSearch="" setProviderSearch={() => {}} providerSearchLoading={apiInventory.isFetching} providerSearchError={apiInventory.error} providerPicker={providerPicker} subscriptionProviderResources={[]} subscriptionProviderManagement={null} openApiProviders={() => navigate(SettingsCategory.Providers)} manageAccount={setAccount} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} startApiWizard={startApiWizard} providerHint={apiProviderHint} />
            </div> : null}
            {isSubscriptionAccounts ? <div hidden={hasOverlay}>
              <SubscriptionAccounts active={visible && isSubscriptionAccounts && !hasOverlay} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} />
            </div> : null}

            {isApiProviders ? <ApiProviderSettings active={visible && isApiProviders} state={providerList} changeState={setProviderList} changed={done} createCustom={(initialData) => setEditing({ kind: EntityKind.PROVIDER, initialData, key: newRequestId() })} editCustom={(initial) => setEditing({ kind: EntityKind.PROVIDER, initial, key: newRequestId() })} manageAccounts={(providerID, entry) => navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.ManageAccounts, providerId: providerID, provider: providerEntrySummary(entry) })} addAccount={(providerID, entry, oauthSupported) => navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.AddAccount, providerId: providerID, provider: providerEntrySummary(entry, oauthSupported) })} deleteCustom={setDeleting} /> : isRunnerDevices ? <><SSHSetup active={visible} /><RunnerDeviceInventory resources={result.data?.resources} error={result.error} loading={result.isPending && !result.data} fetching={result.isFetching} page={page} nextPage={result.data?.nextPageToken ?? ""} hidePagination={hidePagination} first={() => setPage("")} next={() => setPage(result.data!.nextPageToken)} inspect={setMachine} /></> : hasSpecializedPanel ? null : <>
              {result.isPending && !result.data ? <SettingsLoading label={`Loading ${(isPreferenceCategory ? preferenceLabel : selected.label).toLowerCase()}…`} /> : null}
              {isServerPreferences ? <NetworkSettings active={visible && isServerPreferences} authority={pairingAuthority} /> : null}
              {isPreferenceCategory && result.isFetching && result.data ? <p role="status">Refreshing {preferenceLabel.toLowerCase()}…</p> : null}
              <Problem error={result.error} />
              {result.error && result.data ? <p className="notice" role="status">Refresh failed. Showing the last successfully loaded results.</p> : null}
              {isPairedDevices ? <p className="paired-device-explanation">Authorization does not mean this device is currently connected.</p> : null}
              <div className={isAgentWorkers && result.data?.resources.length ? "settings-agent-list" : isPairedDevices && result.data?.resources.length ? "paired-device-list" : undefined}>
              {isProjects ? <ProjectList resources={result.data?.resources ?? []} edit={(row) => setEditing({ initial: row, key: newRequestId() })} remove={setDeleting} /> : result.data?.resources.map((row) => { if (isPreferenceCategory) return <ServerPreferencesSummary key={row.id} row={row} section={preferenceSection} />; if (isPairedDevices) return <DeviceRow key={row.id} resource={row} currentDeviceId={currentDeviceId} expanded={expandedDevices.has(row.id)} toggle={() => toggleDevice(row.id)} revoke={() => setDevice(row)} />; if (isAgentWorkers) return <AgentWorkerRow key={row.id} row={row} edit={() => setEditing({ initial: row, key: newRequestId() })} preview={() => setRouting(row)} remove={() => setDeleting(row)} />; const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</h3>{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small>{kind === EntityKind.REPOSITORY ? <><RepositoryGitHubAccess selected={row} active={visible && area === SettingsArea.Configuration} /><RepositoryGitHubItems selected={row} active={visible && area === SettingsArea.Configuration} /></> : null}<div className="actions">{editableKinds.includes(kind) ? <button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Edit {kind === EntityKind.SETTINGS ? "Server preferences" : resourceName(row)}</button> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <button disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}>Delete {resourceName(row)}</button> : null}{kind === EntityKind.AGENT ? <button disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>Preview routing</button> : null}{kind === EntityKind.MACHINE ? <button disabled={row.schemaVersion !== 1} onClick={() => setMachine(row)}>Inspect installed harnesses</button> : null}{kind === EntityKind.ACCOUNT ? <button disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>Manage connection</button> : null}</div></article>; })}
              </div>
              {isPreferenceCategory && (successfulEmptyFirstPage || retainedServerEmpty) ? <ServerPreferencesEmpty section={preferenceSection} />
                : isPreferenceCategory ? result.data?.resources.length === 0 ? <p>No {preferenceLabel.toLowerCase()} on this page.</p> : null
                : isPairedDevices
                ? successfulEmptyFirstPage ? <section className="paired-device-empty" aria-label="No paired devices yet"><h2>No paired devices yet</h2><p>{pairingAuthority ? "Choose Create pairing document to pair a desktop client or manually installed Worker." : "Pairing document creation is unavailable for this connection."}</p></section>
                  : result.data?.resources.length === 0 && !result.error ? <p>No paired devices on this page</p> : null
                : isAgentWorkers && successfulEmptyFirstPage
                ? <section className="settings-agent-empty" aria-label="No agent workers yet"><div className="settings-agent-icon-tile" aria-hidden="true"><SettingsIcon category={SettingsCategory.AgentWorkers} /></div><h2>No agent workers yet</h2><p>Define a harness, model, accounts, and instructions, then reuse them in new sessions.</p></section>
                : isProjects
                ? successfulEmptyFirstPage ? <section className="project-empty" aria-label="No projects yet"><div className="project-empty-icon"><SettingsIcon category={SettingsCategory.Projects} /></div><h2>No projects yet</h2><p>Group repositories and choose which Agent Workers and AI accounts a project can use.</p><p>Choose New Project to get started.</p></section>
                  : result.data?.resources.length === 0 && !result.error ? <p>No projects on this page.</p> : null
                : kind === EntityKind.PROVIDER && page === "" && result.data?.resources.length === 0 && !result.error && !result.data.nextPageToken
                ? <section className="provider-empty" aria-label="No providers yet"><SettingsIcon category={SettingsCategory.Providers} /><h2>No providers yet</h2><p>Add a provider to configure your models and AI accounts.</p></section>
                : result.data?.resources.length === 0 ? <p>{isAgentWorkers ? "No agent workers on this page." : kind === EntityKind.PROVIDER ? "No providers on this page." : "No saved entries."}</p> : null}
              {!hidePagination ? <nav className="settings-pages" aria-label="Settings pages"><button type="button" disabled={!page || result.isFetching} onClick={() => choosePage("")}>First page</button><button type="button" disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => choosePage(result.data!.nextPageToken)}>Next page</button></nav> : null}
              {isPairedDevices ? <p className="paired-device-guidance">Local Worker registration is available in Runner Devices.</p> : null}
            </>}
          </div> : null}
        </div>
        </div>
        </div></SettingsTaskBackground>
      </section>
      {hasOverlay ? <SettingsTaskDialog key={editing?.key ?? `${taskTitle}:${taskResource?.id}`} title={taskTitle} size={taskSize} focus={deleting || device ? SettingsDialogFocus.Cancel : routing || machine || account ? SettingsDialogFocus.Heading : SettingsDialogFocus.Input} close={closeTask}>{device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={(exit) => { deviceReturnFocus.current = { id: device.id, exit }; setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} authority={pairingAuthority} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing && (editing.kind ?? kind) === EntityKind.REPOSITORY && !editing.initial ? <RepositoryRegistration key={editing.key} active={visible} readLocalWorker={readLocalWorker} controlLocalWorker={controlLocalWorker} chooseFolder={chooseRepositoryFolder} saved={done} cancel={() => setEditing(undefined)} /> : editing && (editing.kind ?? kind) === EntityKind.AGENT ? <AgentWorkerWizard key={editing.key} initial={editing.initial} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : editing ? <ConfigurationEditor key={editing.key} kind={editing.kind ?? kind} initial={editing.initial} initialData={editing.initialData} subscriptionOnly={editing.subscriptionOnly} serverPreferenceSection={isPreferenceCategory ? preferenceSection : ServerPreferenceSection.All} active={visible} saved={done} cancel={() => setEditing(undefined)} focusName={focusNewProjectName} nameFocused={projectNameFocused} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : null}</SettingsTaskDialog> : null}
  </SettingsTasks>;
}

function ProjectList({ resources, edit, remove }: { resources: Resource[]; edit: (row: Resource) => void; remove: (row: Resource) => void }) {
  if (resources.length === 0) return null;
  return <section className="project-list" aria-label="Saved projects">{resources.map((row) => {
    const name = resourceName(row);
    return <article className="project-row" key={row.id}>
      <div className="project-identity"><h3>{name}</h3><small>{row.id}</small></div>
      <div className="actions"><button type="button" disabled={!supportsResourceSchema(row)} aria-label={`Edit ${name}`} onClick={() => edit(row)}>Edit</button><button type="button" disabled={!supportsResourceSchema(row)} aria-label={`Delete ${name}`} onClick={() => remove(row)}>Delete</button></div>
    </article>;
  })}</section>;
}

function RunnerDeviceInventory({ resources, error, loading, fetching, page, nextPage, hidePagination, first, next, inspect }: { resources?: Resource[]; error: unknown; loading: boolean; fetching: boolean; page: string; nextPage: string; hidePagination: boolean; first: () => void; next: () => void; inspect: (row: Resource) => void }) {
  return <section className="settings-runner-inventory" aria-label="Saved runner devices">
    <h2>Saved runner devices</h2>
    {loading ? <><p role="status">Loading runner devices...</p><div aria-hidden="true" aria-busy="true" className="settings-runner-skeletons">{[0, 1].map((row) => <div aria-hidden="true" className="settings-runner-skeleton-row" key={row}><span /><span /><span /></div>)}</div></> : null}
    <Problem error={error} />
    {error && resources ? <p role="status">Refresh failed. Showing the last successfully loaded results.</p> : null}
    {resources?.map((row) => {
      const data = document(row);
      return <article className="settings-runner-row" key={row.id}>
        <div className="settings-runner-identity"><h3>{resourceName(row)}</h3>{text(data.health) ? <p>Status: {text(data.health)}</p> : null}{text(data.harness) ? <p>Harness: {text(data.harness)}</p> : null}<small>{row.id}</small></div>
        <div className="actions"><button type="button" disabled={row.schemaVersion !== 1} onClick={() => inspect(row)}>Inspect installed harnesses</button></div>
      </article>;
    })}
    {resources?.length === 0 ? <p>{page || nextPage ? "No saved entries on this page." : "No saved entries."}</p> : null}
    {!hidePagination ? <nav className="settings-pages" aria-label="Settings pages"><button type="button" disabled={!page || fetching} onClick={first}>First page</button><button type="button" disabled={!nextPage || fetching} onClick={next}>Next page</button></nav> : null}
  </section>;
}
