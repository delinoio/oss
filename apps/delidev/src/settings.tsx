// SPDX-License-Identifier: Apache-2.0
import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import type { UsageEntry } from "./usage-entry";
import { providerPresetNames } from "@delinoio/delidev-api-client";
import { SubscriptionAccounts } from "./subscription-accounts";
import { Backups } from "./backups";
import { Integrations } from "./integrations";
import { ConfigurationTransfer } from "./configuration-transfer";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { ProjectCreationWizard } from "./project-creation";
import { NotificationSettings } from "./notification-settings";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ConfigurationQuery, configurationSchemaVersion, supportsResourceSchema, apiFormat, clientFailure, FailureCode, EntityKind, ProviderInventoryCapability, ProviderConnectionMethod, ProviderPresetId, ProviderQuery, ResourceQuery, SystemQuery, SystemCapability, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { accountPreferencesDocument } from "./account-preferences";
import { ConfigurationFields, editableKinds, kindNames, newConfiguration, ServerPreferenceSection } from "./configuration-fields";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { JobState, TrackedJob } from "./jobs";
import { LocalWorkerControls, LocalWorkerPresentation, type ControlLocalWorker, type LocalWorkerAction } from "./local-worker-controls";
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
import { LanguageSettings } from "./language";
import { statusLabel } from "./product-status";
import { latestServerPreferences, readableServerPreferences, revealServerPreferenceInvalidControl, ServerPreferencesEmpty, ServerPreferencesSummary, ServerPreferencesUnavailable, serverPreferenceLabel, type ServerPreferencesObservation } from "./server-preferences";
import { SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { RepositoryIcon, RepositoryRow } from "./repository-list";
import { ToastKind, useNotifications } from "./toast-notifications";
import "./api-account.css";
import { SettingsTasks, SettingsTaskBackground, SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useSettingsTaskVisible, useInSettingsTask, useCloseSettingsTask } from "./settings-task-context";

export enum ConfigurationEditorPresentation { Workflow, InlineServerPreferences }

export function ConfigurationEditor({ kind, initial, initialData, subscriptionOnly = false, serverPreferenceSection = ServerPreferenceSection.All, active, saved, cancel, presentation = ConfigurationEditorPresentation.Workflow, preferencesObservation }: { kind: EntityKind; initial?: Resource; initialData?: Document; subscriptionOnly?: boolean; serverPreferenceSection?: ServerPreferenceSection; active: boolean; saved: () => void; cancel: () => void; presentation?: ConfigurationEditorPresentation; preferencesObservation?: ServerPreferencesObservation }) {
  useLocale();
  const notifications = useNotifications();
  const inline = kind === EntityKind.SETTINGS && presentation === ConfigurationEditorPresentation.InlineServerPreferences;
  const [baseline, setBaseline] = useState(initial);
  const source = inline ? baseline : initial;
  const [data, setData] = useState<Document>(() => initial ? document(initial) : initialData ?? newConfiguration(kind));
  const [job, setJob] = useState<Resource | "unknown">();
  const [childPending, setChildPending] = useState(false);
  const [fieldsBlocked, setFieldsBlocked] = useState(false);
  const [problem, setProblem] = useProductMessage("");
  const [conflict, setConflict] = useState(false);
  const observedConflict = useRef<unknown>(undefined);
  const form = useRef<HTMLFormElement>(null);
  const formId = useId(), taskVisible = useSettingsTaskVisible(), inTask = useInSettingsTask(), cancelTask = useCloseSettingsTask(cancel);
  const current = useQuery(ResourceQuery.getResource, { kind, id: source?.id ?? "" }, { enabled: active && Boolean(source), refetchInterval: active ? 5000 : false });
  const repositoryStatus = useQuery(SystemQuery.getStatus, {}, { enabled: active && kind === EntityKind.REPOSITORY, retry: false });
  const savedKind = { [EntityKind.AGENT]: "settings.savedKind.AGENT" as const, [EntityKind.TEMPLATE]: "settings.savedKind.TEMPLATE" as const, [EntityKind.PROJECT]: "settings.savedKind.PROJECT" as const, [EntityKind.REPOSITORY]: "settings.savedKind.REPOSITORY" as const, [EntityKind.ACCOUNT]: "settings.savedKind.ACCOUNT" as const, [EntityKind.MACHINE]: "settings.savedKind.MACHINE" as const, [EntityKind.PROVIDER]: "settings.savedKind.PROVIDER" as const, [EntityKind.MODEL]: "settings.savedKind.MODEL" as const, [EntityKind.SETTINGS]: "settings.savedKind.SETTINGS" as const };
  const mutation = useRetainedMutation(`configuration:${kind}:${source?.id ?? "new"}`, ConfigurationQuery.saveConfiguration, (result, request) => {
    if (result.job) setJob(result.job);
    else if (result.resource) {
      if (inline) { setBaseline(result.resource); setData(document(result.resource)); setProblem(""); }
      notifications.notify({ kind: ToastKind.Success, message: ownedMessage(savedKind[kind as keyof typeof savedKind] ?? "settings.savedKind.fallback"), id: request.mutation?.requestId }); saved();
    } else setJob("unknown");
  }, inline ? (result, request) => !result.resource || Boolean(result.resource.id && readableServerPreferences(result.resource)
    && (!request.mutation?.id || result.resource.id === request.mutation.id)
    && result.resource.revision > (request.mutation?.expectedRevision ?? 0n)) : undefined);
  const polled = current.data?.resource;
  const latest = inline ? latestServerPreferences(source, preferencesObservation?.resource, polled?.id === source?.id ? polled : undefined) : undefined;
  const dirty = inline && JSON.stringify(data) !== JSON.stringify(source ? document(source) : initialData ?? newConfiguration(kind));
  // An older in-flight read cannot replace the resource returned by Save. The
  // revision-bound document remains authoritative across query invalidation.
  const currentUnavailable = inline && Boolean(source && current.data && (!polled || polled.id !== source.id ||
    (polled.revision >= source.revision && !readableServerPreferences(polled))));
  const stale = inline ? Boolean(latest && (!source || latest.id !== source.id || latest.revision > source.revision))
    : Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = mutation.busy || mutation.uncertain;
  const inlineReadBlocked = inline && Boolean(!preferencesObservation?.complete || preferencesObservation.fetching || preferencesObservation.error || currentUnavailable);
  useEffect(() => {
    if (!inline || !mutation.error || observedConflict.current === mutation.error || clientFailure(mutation.error).code !== FailureCode.Conflict) return;
    observedConflict.current = mutation.error; setConflict(true);
    // A rejected first save may reveal a concurrently created singleton. Read
    // both inventory and current revision without replaying the rejected write.
    saved();
  }, [inline, mutation.error, saved]);
  useEffect(() => {
    if (!inline || dirty || blocked || conflict || !stale || !latest || !readableServerPreferences(latest) || !preferencesObservation?.complete || currentUnavailable) return;
    setBaseline(latest); setData(document(latest)); setProblem("");
  }, [inline, dirty, blocked, conflict, stale, latest, preferencesObservation?.complete, currentUnavailable]);
  const discard = () => {
    const adopted = latest && readableServerPreferences(latest) ? latest : source;
    setBaseline(adopted); setData(adopted ? document(adopted) : initialData ?? newConfiguration(kind)); setProblem(""); setConflict(false);
  };
  const change = (value: Document) => {
    if (encode(value).byteLength > 1 << 20 || (kind === EntityKind.TEMPLATE && new TextEncoder().encode(text(value.contents)).byteLength > 128 << 10)) { setProblem(ownedMessage("settings.extra.0097158d86f7")); return; }
    setData(value); setProblem("");
  };
  const isApiEntry = kind === EntityKind.ACCOUNT && data.type === "api";
  const kindLabel = isApiEntry ? copy("settings.extra.1ebd6d7b3aeb") : kind === EntityKind.SETTINGS ? serverPreferenceLabel(serverPreferenceSection) : kindNames[kind];
  if (job) return <section className={isApiEntry ? "api-entry-workflow" : undefined}>{isApiEntry ? <header className="api-entry-heading"><h1><LocalizedText id="settings.saveAccepted_51c311" components={{ s0: <>{kindLabel}</> }} /></h1><p className="api-entry-scope">{copy("settings.savedOnTheSelectedServer_93dbee")}</p></header> : <h3><LocalizedText id="settings.saveAccepted_51c311" components={{ s0: <>{kindLabel}</> }} /></h3>}{job === "unknown" ? <p role="alert">{copy("settings.theServerAcknowledgedThisRequestWithout_061fa2")}</p> : <TrackedJob initial={job} active={active}>{(state) => state === JobState.Succeeded ? <><p>{copy("settings.configurationSavedAfterWorkerValidation_d2b875")}</p><button onClick={saved}>{copy("settings.done_11a676")}</button></> : state === JobState.Failed || state === JobState.Canceled ? <button onClick={() => setJob(undefined)}>{copy("settings.returnToRetainedDraft_213f1b")}</button> : null}</TrackedJob>}</section>;
  const validSubscriptionProvider = !subscriptionOnly || (kind === EntityKind.PROVIDER && data.protocol === "native-subscription" && data.authentication === "subscription" && text(data.endpoint) === "");
  const repositoryNeedsRemoteCapability = kind === EntityKind.REPOSITORY && typeof data.remote_url === "string" && data.remote_url !== "";
  const repositoryStatusPending = repositoryNeedsRemoteCapability && repositoryStatus.data === undefined && !repositoryStatus.error;
  const repositoryStatusFailed = repositoryNeedsRemoteCapability && Boolean(repositoryStatus.error);
  const repositoryUnsupported = repositoryNeedsRemoteCapability && repositoryStatus.data !== undefined && !repositoryStatus.data.capabilities.includes(SystemCapability.REMOTE_REPOSITORIES_V1);
  const saveDisabled = fieldsBlocked || repositoryStatusPending || repositoryStatusFailed || repositoryUnsupported || blocked || childPending || stale || inlineReadBlocked || (inline && (!dirty || conflict)) || data.reconfiguration_required === true || !validSubscriptionProvider || Boolean(source && current.error);
  const submit = () => {
    if (saveDisabled) return;
    let documentJson: Uint8Array;
    try {
      documentJson = kind === EntityKind.ACCOUNT && source ? accountPreferencesDocument(source, { ...(data.type === "api" && apiFormat(data.api_protocol) ? { api_protocol: apiFormat(data.api_protocol)! } : {}), alias: text(data.alias), enabled: data.enabled === true, exclude_automatic: data.exclude_automatic === true, recovery_notifications: data.recovery_notifications === true }) : encode(data);
    } catch {
      setProblem(copy("settings.accountPreferencesCouldNotBeSavedReopen_4a7c1b")); return;
    }
    void mutation.send({ mutation: { id: source?.id ?? "", expectedRevision: source?.revision ?? 0n, requestId: newRequestId() }, kind, schemaVersion: configurationSchemaVersion(kind, data), documentJson });
  };
  if (kind === EntityKind.PROJECT && !source) return <ProjectCreationWizard data={data} change={change} active={active} visible={taskVisible} blocked={blocked || childPending} busy={mutation.busy} saveDisabled={saveDisabled} submit={submit} cancel={cancelTask} cancelDisabled={!inTask && (blocked || childPending)} uncertain={mutation.uncertain} retry={mutation.retry}>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
  </ProjectCreationWizard>;
  return <form id={formId} ref={form} aria-label={inline ? (serverPreferenceSection === ServerPreferenceSection.GitWorkflow ? "Git workflow form" : "Server preferences form") : undefined} className={kind === EntityKind.PROJECT ? "project-editor" : kind === EntityKind.AGENT ? "agent-configuration" : kind === EntityKind.SETTINGS ? "server-preferences-editor" : isApiEntry ? "api-entry-workflow api-entry-preferences" : undefined} onInvalidCapture={kind === EntityKind.AGENT ? revealAgentInvalidControl : kind === EntityKind.SETTINGS ? revealServerPreferenceInvalidControl : undefined} onSubmit={(event) => { event.preventDefault(); submit(); }}>
    {inline ? null : isApiEntry ? <header className="api-entry-heading"><h1 hidden={inTask}>{initial ? copy("settings.editPreferences_00b4cc") : copy("settings.newAiApiKeyEntry_5f978c")}</h1><p>{resourceName(initial)}</p><p className="api-entry-scope">{copy("settings.savedOnTheSelectedServer_93dbee")}</p></header> : <h3 hidden={inTask}>{initial ? copy("settings.edit_464c4f") : copy("settings.new_18fdd5")} {kindLabel}</h3>}
    {kind === EntityKind.AGENT && !initial ? <p className="agent-subtitle">{copy("settings.configureTheEssentialsThenCustomizeOnly_a8beda")}</p> : null}
    <fieldset disabled={blocked || (inline && (!preferencesObservation?.complete || currentUnavailable))}><ConfigurationFields initial={source} saveBlocked={setFieldsBlocked} kind={kind} data={data} change={change} active={active} existing={Boolean(source)} pendingOperation={setChildPending} subscriptionOnly={subscriptionOnly} serverPreferenceSection={serverPreferenceSection} /></fieldset>
    {repositoryUnsupported ? <p role="status">Update the selected server before saving a repository.</p> : null}
    {stale || (inline && conflict) ? <p role="alert">{inline ? copy("settings.inlinePreferencesChangedElsewhereDraftRetained", { v0: kindLabel }) : copy("settings.thisEntryChangedElsewhereYourDraft_106fa0")}</p> : null}{currentUnavailable ? <p role="status">{inline ? copy("settings.inlinePreferencesUnavailableDraftRetained", { v0: kindLabel }) : copy("settings.serverPreferencesUnavailableDraftRetained")}</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={current.error || mutation.error || (repositoryNeedsRemoteCapability ? repositoryStatus.error : undefined)} />{repositoryStatusFailed ? <button type="button" disabled={repositoryStatus.isFetching} onClick={() => void repositoryStatus.refetch()}>Retry repository capability check</button> : null}
    {subscriptionOnly && !validSubscriptionProvider ? <p role="alert">{copy("settings.subscriptionProvidersMustUseNativeSubscription_8e47b2")}</p> : null}
    {inline ? <div className="actions server-preferences-actions"><span className="server-preferences-status" role="status">{mutation.busy ? copy("settings.savingChanges") : mutation.uncertain ? copy("settings.saveOutcomeUnknown") : dirty ? copy("settings.unsavedChanges") : ""}</span><button type="button" disabled={blocked || childPending || (!dirty && !stale && !conflict)} onClick={discard}>{copy("settings.discardChanges")}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</button> : null}<button className="primary" disabled={saveDisabled}>{copy("settings.saveChanges")}</button></div> : null}
    {!inline && (kind === EntityKind.AGENT || kind === EntityKind.SETTINGS) ? <SettingsTaskActions form={formId} className={kind === EntityKind.AGENT ? "agent-footer" : "server-preferences-actions"}><button type="button" data-settings-task-cancel disabled={!inTask && (blocked || childPending)} onClick={cancelTask}>{copy("settings.cancelEdit_6fa271")}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</button> : null}<button className="primary" disabled={saveDisabled}><LocalizedText id="settings.save_cdb68b" components={{ s0: <>{kindLabel}</> }} /></button></SettingsTaskActions>
      : !inline ? <SettingsTaskActions form={formId}><button type="button" data-settings-task-cancel disabled={!inTask && (blocked || childPending)} onClick={cancelTask}>{copy("settings.cancelEdit_6fa271")}</button><button className="primary" disabled={saveDisabled}><LocalizedText id="settings.save_cdb68b" components={{ s0: <>{kindLabel}</> }} /></button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</button> : null}</SettingsTaskActions> : null}

  </form>;
}

function ServerPreferencesWorkspace({ resources, nextPageToken, page, fetching, error, section, active, saved, authority }: { resources?: Resource[]; nextPageToken?: string; page: string; fetching: boolean; error?: unknown; section: ServerPreferenceSection; active: boolean; saved: () => void; authority?: PairingAuthority }) {
  const label = serverPreferenceLabel(section).toLowerCase();
  const complete = Boolean(resources && !page && !nextPageToken && resources.length <= 1 && (!resources.length || readableServerPreferences(resources[0])));
  // Admit this mounted editor only from a complete read. Once admitted, retain
  // it through refresh failures and unavailable snapshots without inventing data.
  const [initial, setInitial] = useState<Resource | null | undefined>(() => complete && !error ? resources?.[0] ?? null : undefined);
  if (initial === undefined && complete && !error) setInitial(resources?.[0] ?? null);
  return <>
    {!resources && !error ? <SettingsLoading label={copy("settings.loading_e6401a", { v0: label })} /> : null}
    {fetching && resources ? <p role="status">{section === ServerPreferenceSection.GitWorkflow ? copy("settings.refreshingGitWorkflow") : copy("settings.refreshingServerPreferences_a3769f")}</p> : null}
    <Problem error={error} />
    {error && resources ? <p className="notice" role="status">{copy("settings.refreshFailedShowingTheLastSuccessfully_df6f1e")}</p> : null}
    {resources && !complete ? <ServerPreferencesUnavailable rows={resources} section={section} /> : null}
    {initial !== undefined ? <ConfigurationEditor kind={EntityKind.SETTINGS} initial={initial ?? undefined} serverPreferenceSection={section} active={active} saved={saved} cancel={() => {}} presentation={ConfigurationEditorPresentation.InlineServerPreferences} preferencesObservation={{ complete, resource: complete ? resources?.[0] : undefined, fetching, error }} /> : null}
    {section !== ServerPreferenceSection.GitWorkflow ? <div className="server-preferences-network"><NetworkSettings active={active} authority={authority} /></div> : null}
  </>;
}

export enum SettingsEntryDestination { Repositories = "repositories", NewProject = "new-project", RunnerDevices = "runner-devices" }
enum SettingsArea { Configuration, Diagnostics, Notifications, Transfer, Integrations, Backups, Appearance }
enum SettingsCategory {
  Appearance = "appearance",
  SubscriptionAccounts = "subscription-accounts", ApiAccounts = "api-accounts", Providers = "providers", AgentWorkers = "agent-workers", Instructions = "instructions",
  Projects = "projects", Repositories = "repositories", ExecutionWorkers = "execution-workers", PairedDevices = "paired-devices",
  ServerPreferences = "server-preferences", GitWorkflow = "git-workflow", Integrations = "integrations", Diagnostics = "diagnostics", Notifications = "notifications", Transfer = "transfer", Backups = "backups",
}
enum SettingsGroup { Ai = "AI", Coding = "Coding", Devices = "Device management", System = "System" }

const settingsCategories: Record<SettingsCategory, { label: string; description: string; kind?: EntityKind; area: SettingsArea }> = {
  [SettingsCategory.Appearance]: { get label() { return copy("settings.appearance_3907fa"); }, get description() { return copy("settings.savedOnThisComputer_11cb50"); }, area: SettingsArea.Appearance },
  [SettingsCategory.Backups]: { get label() { return copy("settings.backups_3334fe"); }, get description() { return copy("settings.inspectManagedDatabaseImagesAndFollow_b28b20"); }, area: SettingsArea.Backups },
  [SettingsCategory.SubscriptionAccounts]: { get label() { return copy("settings.aiSubscription_ec8b7a"); }, get description() { return copy("settings.manageYourSubscriptionsAndConnectMore_f0b9fe"); }, kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.ApiAccounts]: { get label() { return copy("settings.aiApiKeys_da1a0f"); }, get description() { return copy("settings.manageAiApiKeysAndKeyless_372629"); }, kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.Providers]: { get label() { return copy("settings.apiProviders_376855"); }, get description() { return copy("settings.providerAvailabilityIsSavedOnThe_11e7c8"); }, kind: EntityKind.PROVIDER, area: SettingsArea.Configuration },
  [SettingsCategory.AgentWorkers]: { get label() { return copy("settings.agentWorkers_e60c23"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.AGENT, area: SettingsArea.Configuration },
  [SettingsCategory.Instructions]: { get label() { return copy("settings.instructions_934652"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.TEMPLATE, area: SettingsArea.Configuration },
  [SettingsCategory.Projects]: { get label() { return copy("settings.projects_04e2a9"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.PROJECT, area: SettingsArea.Configuration },
  [SettingsCategory.Repositories]: { get label() { return copy("settings.repositories_1e32af"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.REPOSITORY, area: SettingsArea.Configuration },
  [SettingsCategory.ExecutionWorkers]: { get label() { return copy("settings.runnerDevices_a176a8"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.MACHINE, area: SettingsArea.Configuration },
  [SettingsCategory.PairedDevices]: { get label() { return copy("settings.pairedDevices_f72c6a"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.DEVICE, area: SettingsArea.Configuration },
  [SettingsCategory.ServerPreferences]: { get label() { return copy("settings.serverPreferences_eba66b"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.GitWorkflow]: { get label() { return copy("settings.gitWorkflow"); }, get description() { return copy("settings.gitWorkflowDescription"); }, kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.Integrations]: { get label() { return copy("settings.integrations_090512"); }, get description() { return copy("settings.manageGithubProfilesForRepositoryAccess_42adb1"); }, area: SettingsArea.Integrations },
  [SettingsCategory.Diagnostics]: { get label() { return copy("settings.connectionDiagnostics_b30b0d"); }, get description() { return copy("settings.readOnlyObservationsFromTheSelected_92a18a"); }, area: SettingsArea.Diagnostics },
  [SettingsCategory.Notifications]: { get label() { return copy("settings.notifications_788011"); }, get description() { return copy("settings.thesePreferencesBelongToThisClient_082e1e"); }, area: SettingsArea.Notifications },
  [SettingsCategory.Transfer]: { get label() { return copy("settings.importExport_6e061f"); }, get description() { return copy("settings.moveConfigurationBetweenDelidevServers_749ed7"); }, area: SettingsArea.Transfer },
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
  useLocale();
  return <svg className="settings-category-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={settingsIcons[category]} /></svg>;
}

function AgentWorkerRow({ row, edit, preview, remove }: { row: Resource; edit: () => void; preview: () => void; remove: () => void }) {
  useLocale();
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
      {text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}
      {text(data.harness) ? <p><LocalizedText id="settings.harness_db1faa" components={{ s0: <>{text(data.harness)}</> }} /></p> : null}
      {data.reconfiguration_required === true ? <p role="status">{copy("settings.reconfigurationRequired_a84a37")}</p> : null}
      <small>{row.id}</small>
    </div>
    <div className="actions settings-agent-actions">
      <button type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.edit_f1be7e", { v0: name })} onClick={edit}>{copy("settings.edit_464c4f")}</button>
      <button type="button" disabled={!supportsResourceSchema(row) || data.reconfiguration_required === true} aria-label={copy("settings.previewRoutingFor_ee49d7", { v0: name })} onClick={preview}>{copy("settings.previewRouting_02d4d9")}</button>
      <button type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.delete_cd822e", { v0: name })} onClick={remove}>{copy("settings.delete_e2d0a5")}</button>
    </div>
  </article>;
}

interface SettingsProps { openUsage?: (entry: UsageEntry) => void; readLocalWorker?: ReadLocalWorkerProof; chooseRepositoryFolder?: ChooseRepositoryFolder; connectionSettings?: React.ReactNode; pairingAuthority?: PairingAuthority; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string; entryDestination?: SettingsEntryDestination; destinationConsumed?: () => void }
enum SettingsEntryKind { NewProject, ManageAccounts, AddAccount }
type SettingsCategoryEntry = { kind: SettingsEntryKind.NewProject } | { kind: SettingsEntryKind.ManageAccounts | SettingsEntryKind.AddAccount; providerId: string; provider?: AccountProviderSummary };
interface SettingsSelection { category: SettingsCategory; key: string; entry?: SettingsCategoryEntry }
type NavigateSettings = (category: SettingsCategory, entry?: SettingsCategoryEntry) => void;

function entrySelection(destination?: SettingsEntryDestination): SettingsSelection {
  return { category: destination === SettingsEntryDestination.RunnerDevices ? SettingsCategory.ExecutionWorkers : destination === SettingsEntryDestination.Repositories ? SettingsCategory.Repositories : destination === SettingsEntryDestination.NewProject ? SettingsCategory.Projects : SettingsCategory.SubscriptionAccounts, key: newRequestId(), entry: destination === SettingsEntryDestination.NewProject ? { kind: SettingsEntryKind.NewProject } : undefined };
}

export function Settings({ visible = true, ...props }: SettingsProps) {
  useLocale();
  return visible ? <SettingsVisit {...props} /> : null;
}

function SettingsVisit({ entryDestination, destinationConsumed, ...props }: SettingsProps) {
  useLocale();
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
    <SidebarSurface active title={copy("settings.settings_74a883")} className="settings-navigation">
      <nav aria-label={copy("settings.settingsCategories_b9ed95")}>
        {settingsGroups.map(group => <section className="settings-nav-group" key={group.label}>
          <h2>{copy(group.label === SettingsGroup.Ai ? "settings.group.aiAgents" : group.label === SettingsGroup.Coding ? "settings.group.coding" : group.label === SettingsGroup.Devices ? "settings.group.devices" : "settings.group.system")}</h2>
          {group.categories.map(category => <button type="button" className="settings-category-button" key={category} data-settings-category={category} aria-current={selection.category === category ? "page" : undefined} aria-pressed={selection.category === category} onClick={() => navigate(category)}>
            <SettingsIcon category={category} /><span>{settingsCategories[category].label}</span>
          </button>)}
        </section>)}
      </nav>
    </SidebarSurface>
    {/* Each category owns its waits and drafts. Disposal rejects late results
        without canceling or replaying already accepted server/native work. */}
    <SettingsLifetime key={selection.key}>{opening => <MutationIntents><SettingsWorkspace {...props} selectedCategory={selection.category} entry={selection.entry} navigate={navigate} controlLocalWorker={props.controlLocalWorker ? Object.assign((action: LocalWorkerAction, generation?: string) => opening.native(() => props.controlLocalWorker!(action, generation)), { automatic: props.controlLocalWorker.automatic }) : undefined} /></MutationIntents>}</SettingsLifetime>
  </>;
}

function SettingsWorkspace({ openUsage, connectionSettings, visible = true, controlLocalWorker, readLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority, selectedCategory, entry, navigate }: SettingsProps & { selectedCategory: SettingsCategory; entry?: SettingsCategoryEntry; navigate: NavigateSettings }) {
  useLocale();
  const openConnectionDiagnostics = () => {
    // Keep focus in the persistent main region when this category is disposed.
    window.document.getElementById("main")?.focus({ preventScroll: true });
    navigate(SettingsCategory.Diagnostics);
  };
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
    ? copy("settings.extra.302fcd6cacc5")
    : kind === EntityKind.MACHINE && !controlLocalWorker ? copy("settings.extra.bd632b65e652")
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
  const providerSummary = (entry: ProviderInventoryEntry, capabilities: readonly ProviderInventoryCapability[] = []): AccountProviderSummary | undefined => entry.providerId && entry.provider?.kind === EntityKind.PROVIDER ? {
    providerId: entry.providerId,
    displayName: entry.displayName,
    enabled: entry.enabled,
    provider: entry.provider,
    keyGuidance: text(presets.get(providerPresetNames.get(entry.presetId) ?? "")?.key_guidance) || copy("settings.extra.7a611b78ccfc"),
    documentationUrl: text(presets.get(providerPresetNames.get(entry.presetId) ?? "")?.documentation),
    presetId: providerPresetNames.get(entry.presetId),
    keyCreationUrl: text(presets.get(providerPresetNames.get(entry.presetId) ?? "")?.key_creation_url),
    oauthAvailable: capabilities.includes(entry.presetId === ProviderPresetId.OPENROUTER ? ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1 : ProviderInventoryCapability.ACCOUNT_OAUTH_V1) && entry.enabled && (entry.connectionMethod === ProviderConnectionMethod.OAUTH_PKCE || entry.presetId === ProviderPresetId.BASETEN && entry.connectionMethod === ProviderConnectionMethod.OAUTH_DEVICE),
  } : undefined;
  const apiProviders = (apiInventory.data?.entries ?? []).map(entry => providerSummary(entry, apiInventory.data?.capabilities ?? [])).filter((value): value is AccountProviderSummary => value !== undefined);
  const eligibleProviders = (eligibleInventory.data?.entries ?? []).map(entry => providerSummary(entry, eligibleInventory.data?.capabilities ?? [])).filter((value): value is AccountProviderSummary => value !== undefined && value.enabled);
  const configurationList = area === SettingsArea.Configuration && !hasSpecializedPanel;
  const successfulEmptyFirstPage = !page && Boolean(result.data && result.data.resources.length === 0 && !result.error && !result.data.nextPageToken);
  const retainedServerEmpty = isPreferenceCategory && !page && Boolean(result.data && result.data.resources.length === 0 && !result.data.nextPageToken);
  const hidePagination = successfulEmptyFirstPage || (isPreferenceCategory && !page && Boolean(result.data && !result.data.nextPageToken));
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  const providerEntrySummary = (entry?: ProviderInventoryEntry, capabilities: readonly ProviderInventoryCapability[] = []) => entry ? providerSummary(entry, capabilities) : undefined;
  const closeTask = () => { setDevice(undefined); setMachine(undefined); setDeleting(undefined); setRouting(undefined); setEditing(undefined); setAccount(undefined); };
  const taskResource = editing?.initial ?? device ?? machine ?? deleting ?? routing ?? account;
  const taskKind = editing?.kind ?? kind;
  const taskTitle = device ? "Revoke device" : machine ? "Runner Device details" : deleting ? deleting.kind === EntityKind.ACCOUNT && document(deleting).type === "api" ? copy("account-deletion.api.title") : "Delete configuration" : routing ? "Preview routing" : account ? "Manage connection" : editing && taskKind === EntityKind.REPOSITORY && !editing.initial ? copy("settings.addRepository_2eda4d") : editing && taskKind === EntityKind.PROJECT && !editing.initial ? copy("project-creation.title") : editing ? `${editing.initial ? copy("settings.edit_464c4f") : copy("settings.new_18fdd5")} ${taskKind === EntityKind.SETTINGS ? preferenceLabel : kindNames[taskKind]}` : "Settings task";
  const taskSize = deleting || device ? SettingsDialogSize.Confirmation : routing ? SettingsDialogSize.Form : machine || account || [EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.REPOSITORY].includes(taskKind) ? SettingsDialogSize.Wide : SettingsDialogSize.Form;
  return <SettingsTasks>
      <section className={selectedCategory === SettingsCategory.Repositories ? "settings-content settings-repositories" : isProjects ? "settings-content settings-projects" : isPreferenceCategory ? `settings-content settings-server-preferences${isGitWorkflow ? " settings-git-workflow" : ""}` : isApiAccounts ? "settings-content settings-api-keys" : isRunnerDevices ? "settings-content settings-runner-devices" : "settings-content"} aria-label={copy("settings.settingsContent_e4dcd3")}>
        <SettingsTaskBackground><div className="settings-content-column">
        <div ref={deviceContent} className={isAgentWorkers ? "settings-agent-column" : isPairedDevices ? "settings-paired-column" : isRunnerDevices ? "settings-runner-column" : area === SettingsArea.Transfer ? "settings-transfer-column" : undefined}>
        {isGitWorkflow ? <p className="settings-breadcrumb">{copy("settings.gitWorkflow")}</p> : null}
        {area !== SettingsArea.Diagnostics && area !== SettingsArea.Backups && !isApiAccounts && !isApiProviders ? <div className="settings-category-heading">
          <div className="settings-category-title"><h1 aria-live="polite" aria-atomic="true">{selected.label}</h1>{selectedCategory === SettingsCategory.Repositories ? <p>{copy("settings.repositoryDescription")}</p> : isAgentWorkers ? <p className="settings-agent-summary">{copy("settings.reusableConfigurationsForYourAgents_5ba1a2")}</p> : isPreferenceCategory ? <p>{isGitWorkflow ? copy("settings.gitWorkflowDescription") : copy("settings.defaultRoutingWorktreeFetchAndPull_e19cf8")}</p> : null}<p className={isAgentWorkers ? "settings-scope settings-agent-scope" : isPreferenceCategory ? "settings-scope server-preferences-scope" : isPairedDevices ? "settings-scope paired-device-summary" : "settings-scope"}>{categoryDescription}</p>{isPairedDevices ? <p className="paired-device-scope">{copy("settings.savedOnTheSelectedServer_93dbee")}</p> : null}</div>
          {configurationList ? <div className="settings-toolbar">
            <button type="button" ref={isPairedDevices ? refreshDevices : undefined} aria-label={isGitWorkflow ? "Refresh Git workflow" : undefined} onClick={() => void result.refetch()}>{isGitWorkflow ? copy("settings.refresh_0e9161") : copy("settings.refreshSettings_65dbd6")}</button>
            {isPairedDevices && pairingAuthority ? <span ref={setPairingTriggerContainer} /> : null}
            {editableKinds.includes(kind) && kind !== EntityKind.SETTINGS
              ? <button type="button" className="primary" onClick={() => setEditing({ key: newRequestId() })}><span className="settings-action-icon" aria-hidden="true">+</span>{kind === EntityKind.REPOSITORY ? copy("settings.addRepository_2eda4d") : kind === EntityKind.PROJECT ? copy("project-creation.title") : copy("settings.new_077d61", { v0: kindNames[kind] })}</button>
              : null}
          </div> : null}
        </div> : null}
        <div className="settings-panels">
          {area === SettingsArea.Appearance ? <div><AppearanceSettings /><LanguageSettings /></div> : null}
          {area === SettingsArea.Backups ? <div><Backups active={visible} /></div> : null}
          {area === SettingsArea.Integrations ? <div><Integrations active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Transfer ? <div><ConfigurationTransfer active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Notifications ? <div><NotificationSettings active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Diagnostics ? <div>{connectionSettings ? <section aria-label={copy("settings.connection_639a40")}><h2>{copy("settings.connection_639a40")}</h2>{connectionSettings}</section> : null}<Doctor title={DoctorTitle.ConnectionDiagnostics} active={visible && area === SettingsArea.Diagnostics} visible={visible} /></div> : null}
          {area === SettingsArea.Configuration ? <div>
            {controlLocalWorker && isRunnerDevices ? <div><LocalWorkerControls control={controlLocalWorker} presentation={LocalWorkerPresentation.RunnerDevices} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} onDiagnostics={openConnectionDiagnostics} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
            {pairingAuthority && isPairedDevices ? <div><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} triggerContainer={pairingTriggerContainer} /></div> : null}
            {isApiAccounts ? <div>
              <AccountSettings apiFormatSelectingReady={Boolean(apiInventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) && eligibleInventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) && !apiInventory.error && !eligibleInventory.error)} openUsage={openUsage} section={AccountSettingsSection.Api} active={visible && isApiAccounts && !hasOverlay} accountTypeFilteringReady={apiAccountTypeFilteringReady} accountTypeFilteringProblem={apiInventory.error} accountTypeFilteringLoading={apiInventory.isLoading} accountTypeFilteringFetching={apiInventory.isFetching} retryAccountCapabilities={() => { void apiInventory.refetch(); }} providerIdFilter={apiProviderID} clearProviderFilter={() => { setApiProviderID(""); setApiProviderHint(undefined); setApiProviderPage(""); setStartApiWizard(undefined); }} setProviderFilter={(providerId, provider) => { setApiProviderID(providerId); setApiProviderHint(provider); setPage(""); }} providers={apiProviders} eligibleProviders={eligibleProviders} providerSearch="" setProviderSearch={() => {}} providerSearchLoading={apiInventory.isFetching} providerSearchError={apiInventory.error} providerPicker={providerPicker} subscriptionProviderResources={[]} subscriptionProviderManagement={null} openApiProviders={() => navigate(SettingsCategory.Providers)} manageAccount={setAccount} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} startApiWizard={startApiWizard} providerHint={apiProviderHint} />
            </div> : null}
            {isSubscriptionAccounts ? <div>
              <SubscriptionAccounts active={visible && isSubscriptionAccounts && !hasOverlay} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} />
            </div> : null}

            {isPreferenceCategory ? <><ServerPreferencesWorkspace resources={result.data?.resources} nextPageToken={result.data?.nextPageToken} page={page} fetching={result.isFetching} error={result.error} section={preferenceSection} active={visible} saved={done} authority={pairingAuthority} />{isGitWorkflow && !hidePagination ? <nav className="settings-pages" aria-label={copy("settings.settingsPages_05ec05")}><button type="button" disabled={!page || result.isFetching} onClick={() => choosePage("")}>{copy("settings.firstPage_0bdbb7")}</button><button type="button" disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => choosePage(result.data!.nextPageToken)}>{copy("settings.nextPage_c08ac7")}</button></nav> : null}</> : isApiProviders ? <ApiProviderSettings active={visible && isApiProviders} state={providerList} changeState={setProviderList} changed={done} createCustom={(initialData) => setEditing({ kind: EntityKind.PROVIDER, initialData, key: newRequestId() })} editCustom={(initial) => setEditing({ kind: EntityKind.PROVIDER, initial, key: newRequestId() })} manageAccounts={(providerID, entry) => navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.ManageAccounts, providerId: providerID, provider: providerEntrySummary(entry) })} addAccount={(providerID, entry, oauthSupported) => navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.AddAccount, providerId: providerID, provider: providerEntrySummary(entry, oauthSupported) })} deleteCustom={setDeleting} /> : isRunnerDevices ? <><SSHSetup active={visible} /><RunnerDeviceInventory resources={result.data?.resources} error={result.error} loading={result.isPending && !result.data} fetching={result.isFetching} page={page} nextPage={result.data?.nextPageToken ?? ""} hidePagination={hidePagination} first={() => setPage("")} next={() => setPage(result.data!.nextPageToken)} inspect={setMachine} /></> : hasSpecializedPanel ? null : <>
              {result.isPending && !result.data ? <SettingsLoading label={copy("settings.loading_e6401a", { v0: (isPreferenceCategory ? preferenceLabel : selected.label).toLowerCase() })} /> : null}
              {isServerPreferences ? <NetworkSettings active={visible && isServerPreferences} authority={pairingAuthority} /> : null}
              {isPreferenceCategory && result.isFetching && result.data ? <p role="status">{copy("settings.refreshingServerPreferences_a3769f")}</p> : null}
              <Problem error={result.error} />
              {result.error && result.data ? <p className="notice" role="status">{copy("settings.refreshFailedShowingTheLastSuccessfully_df6f1e")}</p> : null}
              {isPairedDevices ? <p className="paired-device-explanation">{copy("settings.authorizationDoesNotMeanThisDevice_cfecd2")}</p> : null}
              <div className={selectedCategory === SettingsCategory.Repositories ? "repository-list" : isAgentWorkers && result.data?.resources.length ? "settings-agent-list" : isPairedDevices && result.data?.resources.length ? "paired-device-list" : undefined}>
              {isProjects ? <ProjectList resources={result.data?.resources ?? []} edit={(row) => setEditing({ initial: row, key: newRequestId() })} remove={setDeleting} /> : result.data?.resources.map((row) => { if (kind === EntityKind.REPOSITORY) return <RepositoryRow key={row.id} row={row} active={visible && area === SettingsArea.Configuration} edit={() => setEditing({ initial: row, key: newRequestId() })} remove={() => setDeleting(row)} />; if (isPreferenceCategory) return <ServerPreferencesSummary key={row.id} row={row} section={preferenceSection} />; if (isPairedDevices) return <DeviceRow key={row.id} resource={row} currentDeviceId={currentDeviceId} expanded={expandedDevices.has(row.id)} toggle={() => toggleDevice(row.id)} revoke={() => setDevice(row)} />; if (isAgentWorkers) return <AgentWorkerRow key={row.id} row={row} edit={() => setEditing({ initial: row, key: newRequestId() })} preview={() => setRouting(row)} remove={() => setDeleting(row)} />; const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? preferenceLabel : resourceName(row)}</h3>{text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}{text(data.harness) ? <p><LocalizedText id="settings.harness_db1faa" components={{ s0: <>{text(data.harness)}</> }} /></p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small><div className="actions">{editableKinds.includes(kind) ? <button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}><LocalizedText id="settings.edit_4ec92a" components={{ s0: <>{kind === EntityKind.SETTINGS ? preferenceLabel : resourceName(row)}</> }} /></button> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <button disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}><LocalizedText id="settings.delete_a1b98e" components={{ s0: <>{resourceName(row)}</> }} /></button> : null}{kind === EntityKind.AGENT ? <button disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>{copy("settings.previewRouting_02d4d9")}</button> : null}{kind === EntityKind.MACHINE ? <button disabled={row.schemaVersion !== 1} onClick={() => setMachine(row)}>{copy("settings.inspectInstalledHarnesses_45e943")}</button> : null}{kind === EntityKind.ACCOUNT ? <button disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>{copy("settings.manageConnection_ad2892")}</button> : null}</div></article>; })}
              </div>
              {selectedCategory === SettingsCategory.Repositories && successfulEmptyFirstPage ? <SettingsEmpty title={copy("settings.repositoryEmptyTitle")} icon={<RepositoryIcon />}><p>{copy("settings.repositoryEmptyHelp")}</p></SettingsEmpty>
                : selectedCategory === SettingsCategory.Repositories ? result.data?.resources.length === 0 && !result.error ? <p>{copy("settings.repositoryPageEmpty")}</p> : null
                : isPreferenceCategory && (successfulEmptyFirstPage || retainedServerEmpty) ? <ServerPreferencesEmpty section={preferenceSection} />
                : isPreferenceCategory ? result.data?.resources.length === 0 ? <p>{copy("settings.noPreferencesOnThisPage", { v0: preferenceLabel.toLowerCase() })}</p> : null
                : isPairedDevices
                ? successfulEmptyFirstPage ? <section className="paired-device-empty" aria-label={copy("settings.noPairedDevicesYet_8f0b7b")}><h2>{copy("settings.noPairedDevicesYet_8f0b7b")}</h2><p>{pairingAuthority ? copy("settings.chooseCreatePairingDocumentToPair_e6c0b6") : copy("settings.pairingDocumentCreationIsUnavailableFor_90f935")}</p></section>
                  : result.data?.resources.length === 0 && !result.error ? <p>{copy("settings.noPairedDevicesOnThisPage_64cf79")}</p> : null
                : isAgentWorkers && successfulEmptyFirstPage
                ? <section className="settings-agent-empty" aria-label={copy("settings.noAgentWorkersYet_afe8e7")}><div className="settings-agent-icon-tile" aria-hidden="true"><SettingsIcon category={SettingsCategory.AgentWorkers} /></div><h2>{copy("settings.noAgentWorkersYet_afe8e7")}</h2><p>{copy("settings.defineAHarnessModelAccountsAnd_eb5de3")}</p></section>
                : isProjects
                ? successfulEmptyFirstPage ? <section className="project-empty" aria-label={copy("settings.noProjectsYet_f83c80")}><div className="project-empty-icon"><SettingsIcon category={SettingsCategory.Projects} /></div><h2>{copy("settings.noProjectsYet_f83c80")}</h2><p>{copy("settings.groupRepositoriesAndChooseWhichAgent_facff7")}</p><p>{copy("settings.chooseNewProjectToGetStarted_5f012f")}</p></section>
                  : result.data?.resources.length === 0 && !result.error ? <p>{copy("settings.noProjectsOnThisPage_c9b8ee")}</p> : null
                : kind === EntityKind.PROVIDER && page === "" && result.data?.resources.length === 0 && !result.error && !result.data.nextPageToken
                ? <section className="provider-empty" aria-label={copy("settings.noProvidersYet_553273")}><SettingsIcon category={SettingsCategory.Providers} /><h2>{copy("settings.noProvidersYet_553273")}</h2><p>{copy("settings.addAProviderToConfigureYour_e9b95c")}</p></section>
                : result.data?.resources.length === 0 ? <p>{isAgentWorkers ? copy("settings.noAgentWorkersOnThisPage_624e5b") : kind === EntityKind.PROVIDER ? copy("settings.noProvidersOnThisPage_ea9e7e") : copy("settings.noSavedEntries_a59b83")}</p> : null}
              {!hidePagination ? <nav className="settings-pages" aria-label={copy("settings.settingsPages_05ec05")}>{selectedCategory === SettingsCategory.Repositories && result.data ? <span className="repository-page-count">{copy("settings.repositoryPageCount", { count: result.data.resources.length })}</span> : null}<button type="button" disabled={!page || result.isFetching} onClick={() => choosePage("")}>{copy("settings.firstPage_0bdbb7")}</button><button type="button" disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => choosePage(result.data!.nextPageToken)}>{copy("settings.nextPage_c08ac7")}</button></nav> : null}
              {isPairedDevices ? <p className="paired-device-guidance">{copy("settings.localWorkerRegistrationIsAvailableIn_09876c")}</p> : null}
            </>}
          </div> : null}
        </div>
        </div>
        </div></SettingsTaskBackground>
      </section>
      {hasOverlay ? <SettingsTaskDialog key={editing?.key ?? `${taskTitle}:${taskResource?.id}`} title={taskTitle} size={taskSize} focus={deleting || device ? SettingsDialogFocus.Cancel : routing || machine || account ? SettingsDialogFocus.Heading : SettingsDialogFocus.Input} close={closeTask}>{device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={(exit) => { deviceReturnFocus.current = { id: device.id, exit }; setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} authority={pairingAuthority} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing && (editing.kind ?? kind) === EntityKind.REPOSITORY && !editing.initial ? <RepositoryRegistration key={editing.key} active={visible} readLocalWorker={readLocalWorker} controlLocalWorker={controlLocalWorker} chooseFolder={chooseRepositoryFolder} saved={done} cancel={() => setEditing(undefined)} /> : editing && (editing.kind ?? kind) === EntityKind.AGENT ? <AgentWorkerWizard key={editing.key} initial={editing.initial} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : editing ? <ConfigurationEditor key={editing.key} kind={editing.kind ?? kind} initial={editing.initial} initialData={editing.initialData} subscriptionOnly={editing.subscriptionOnly} serverPreferenceSection={isPreferenceCategory ? preferenceSection : ServerPreferenceSection.All} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : null}</SettingsTaskDialog> : null}
  </SettingsTasks>;
}

function ProjectList({ resources, edit, remove }: { resources: Resource[]; edit: (row: Resource) => void; remove: (row: Resource) => void }) {
  useLocale();
  if (resources.length === 0) return null;
  return <section className="project-list" aria-label={copy("settings.savedProjects_f85c7c")}>{resources.map((row) => {
    const name = resourceName(row);
    return <article className="project-row" key={row.id}>
      <div className="project-identity"><h3>{name}</h3><small>{row.id}</small></div>
      <div className="actions"><button type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.edit_f1be7e", { v0: name })} onClick={() => edit(row)}>{copy("settings.edit_464c4f")}</button><button type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.delete_cd822e", { v0: name })} onClick={() => remove(row)}>{copy("settings.delete_e2d0a5")}</button></div>
    </article>;
  })}</section>;
}

function RunnerDeviceInventory({ resources, error, loading, fetching, page, nextPage, hidePagination, first, next, inspect }: { resources?: Resource[]; error: unknown; loading: boolean; fetching: boolean; page: string; nextPage: string; hidePagination: boolean; first: () => void; next: () => void; inspect: (row: Resource) => void }) {
  useLocale();
  return <section className="settings-runner-inventory" aria-label={copy("settings.savedRunnerDevices_9e6092")}>
    <h2>{copy("settings.savedRunnerDevices_9e6092")}</h2>
    {loading ? <><p role="status">{copy("settings.loadingRunnerDevices_a75d73")}</p><div aria-hidden="true" aria-busy="true" className="settings-runner-skeletons">{[0, 1].map((row) => <div aria-hidden="true" className="settings-runner-skeleton-row" key={row}><span /><span /><span /></div>)}</div></> : null}
    <Problem error={error} />
    {error && resources ? <p role="status">{copy("settings.refreshFailedShowingTheLastSuccessfully_df6f1e")}</p> : null}
    {resources?.map((row) => {
      const data = document(row);
      return <article className="settings-runner-row" key={row.id}>
        <div className="settings-runner-identity"><h3>{resourceName(row)}</h3>{text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}{text(data.harness) ? <p><LocalizedText id="settings.harness_db1faa" components={{ s0: <>{text(data.harness)}</> }} /></p> : null}<small>{row.id}</small></div>
        <div className="actions"><button type="button" disabled={row.schemaVersion !== 1} onClick={() => inspect(row)}>{copy("settings.inspectInstalledHarnesses_45e943")}</button></div>
      </article>;
    })}
    {resources?.length === 0 ? <p>{page || nextPage ? copy("settings.noSavedEntriesOnThisPage_ea1b6b") : copy("settings.noSavedEntries_a59b83")}</p> : null}
    {!hidePagination ? <nav className="settings-pages" aria-label={copy("settings.settingsPages_05ec05")}><button type="button" disabled={!page || fetching} onClick={first}>{copy("settings.firstPage_0bdbb7")}</button><button type="button" disabled={!nextPage || fetching} onClick={next}>{copy("settings.nextPage_c08ac7")}</button></nav> : null}
  </section>;
}
