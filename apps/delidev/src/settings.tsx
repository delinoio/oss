import { defaultBranchPrefix, validBranchPrefix } from "./session-defaults";
// SPDX-License-Identifier: Apache-2.0
import { SettingsActionScope, SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { DisclosureDensity, DisclosureDensityScope } from "./disclosure";
import { SettingsCategory } from "./settings-category";
import { SettingsSearch, SettingsSearchFocus, SettingsSearchTarget, type SettingsSearchRequest } from "./settings-search";
import { AgentWorkerRow } from "./agent-worker-row";
import { AgentWorkerMetadataProvider } from "./agent-worker-models";
import { useRunnerRemediation } from "./runner-remediation";
import { RunnerWorkflow, useRunnerPreference } from "./runner-device-preferences";
import { SettingsTaskDismissButton } from "./settings-task";
import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import type { UsageEntry } from "./usage-entry";
import { providerPresetNames } from "@delinoio/delidev-api-client";
import { SubscriptionAccounts } from "./subscription-accounts";
import { Backups } from "./backups";
import { Integrations } from "./integrations";
import { ConfigurationTransfer } from "./configuration-transfer";
import { SourceKind } from "./worker-source";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { ProjectCreationWizard, type ProjectRegistrationAdapters } from "./project-creation";
import { ProjectList } from "./project-list";
import { projectRepositoryIds, useProjectListMetadata } from "./project-list-metadata";
import { NotificationSettings } from "./notification-settings";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { AccountQuery, apiFormatToWire, ConfigurationQuery, configurationSchemaVersion, supportsResourceSchema, apiFormat, apiFormatProfileFromWire, clientFailure, FailureCode, EntityKind, ProviderInventoryCapability, ProviderConnectionMethod, ProviderPresetId, ProviderQuery, ResourceQuery, SystemQuery, SystemCapability, isEntityId, newRequestId, type ProviderInventoryEntry, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { accountPreferencesDocument } from "./account-preferences";
import { revealRepositoryInvalidControl } from "./repository-editor";
import { ConfigurationFields, ResourceSelectionPending, editableKinds, kindNames, newConfiguration, ServerPreferenceSection } from "./configuration-fields";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { JobState, TrackedJob } from "./jobs";
import { LocalWorkerControls, LocalWorkerPresentation, type ControlLocalWorker, type LocalWorkerAction } from "./local-worker-controls";
import { SSHSetup } from "./ssh-setup";
import { MachineSettings } from "./machine-settings";
import { NetworkSettings } from "./network-settings";
import { DeviceAction, DeviceRow, DeviceRevocation, DeviceRevocationExit } from "./device-settings";
import { AccountConnection } from "./account-connection";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { Failure, Problem } from "./ui";
import { inventoryIdentity, useProviderPages } from "./model-pagination";
import { useResourceScrollQuery } from "./resource-scroll-query";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { PairingGrant, type PairingAuthority } from "./pairing-grant";
import { AccountSettings, AccountSettingsSection, type AccountProviderPicker, type AccountProviderSummary } from "./account-settings";
import { ApiProviderSettings, providerInventoryReady, type ProviderListState } from "./provider-model-settings";
import { revealAgentInvalidControl } from "./agent-configuration";
import { RepositoryRegistration, type ChooseRepositoryFolder } from "./repository-registration";
import type { ReadLocalWorkerProof } from "./local-worker";
import { SettingsLifetime } from "./settings-lifetime";
import { ShortcutSettings } from "./shortcut-settings";
import { DateFormatSettings } from "./date-format";
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

export function ConfigurationEditor({ kind, initial, initialData, subscriptionOnly = false, serverPreferenceSection = ServerPreferenceSection.All, active, saved, cancel, presentation = ConfigurationEditorPresentation.Workflow, preferencesObservation, registrationAdapters }: { kind: EntityKind; initial?: Resource; initialData?: Document; subscriptionOnly?: boolean; serverPreferenceSection?: ServerPreferenceSection; active: boolean; saved: () => void; cancel: () => void; presentation?: ConfigurationEditorPresentation; preferencesObservation?: ServerPreferencesObservation; registrationAdapters?: ProjectRegistrationAdapters }) {
  useLocale();
  const checkoutPreference = useRunnerPreference(RunnerWorkflow.Checkout, false);
  const remediationPreference = useRunnerPreference(kind === EntityKind.SETTINGS ? RunnerWorkflow.ServerRemediation : RunnerWorkflow.RepositoryRemediation, false);
  const notifications = useNotifications();
  const inline = kind === EntityKind.SETTINGS && presentation === ConfigurationEditorPresentation.InlineServerPreferences;
  const [baseline, setBaseline] = useState(initial);
  const apiEditor = kind === EntityKind.ACCOUNT && Boolean(initial && document(initial).type === "api");
  const source = inline || apiEditor ? baseline : initial;
  const [data, setData] = useState<Document>(() => initial ? document(initial) : initialData ?? newConfiguration(kind));
  const [job, setJob] = useState<Resource | "unknown">();
  const [childPending, setChildPending] = useState(false);
  const pendingSelections = useRef(new Set<string>());
  const [selectionPending, setSelectionPending] = useState(false);
  const reportSelectionPending = useCallback((identity: string, pending: boolean) => {
    if (pending) pendingSelections.current.add(identity); else pendingSelections.current.delete(identity);
    setSelectionPending(pendingSelections.current.size > 0);
  }, []);
  const [keepsFormatKey, setKeepsFormatKey] = useState(false);
  const [fieldsBlocked, setFieldsBlocked] = useState(false);
  const [problem, setProblem] = useProductMessage("");
  const [conflict, setConflict] = useState(false);
  const observedConflict = useRef<unknown>(undefined);
  const form = useRef<HTMLFormElement>(null);
  const formId = useId(), taskVisible = useSettingsTaskVisible(), inTask = useInSettingsTask(), cancelTask = useCloseSettingsTask(cancel);
  const current = useQuery(ResourceQuery.getResource, { kind, id: source?.id ?? "" }, { enabled: active && Boolean(source), refetchInterval: active ? 5000 : false });
  const repositoryStatus = useQuery(SystemQuery.getStatus, {}, { enabled: active && [EntityKind.REPOSITORY, EntityKind.PROJECT, EntityKind.SETTINGS].includes(kind), retry: false });
  const savedKind = { [EntityKind.AGENT]: "settings.savedKind.AGENT" as const, [EntityKind.TEMPLATE]: "settings.savedKind.TEMPLATE" as const, [EntityKind.PROJECT]: "settings.savedKind.PROJECT" as const, [EntityKind.REPOSITORY]: "settings.savedKind.REPOSITORY" as const, [EntityKind.ACCOUNT]: "settings.savedKind.ACCOUNT" as const, [EntityKind.MACHINE]: "settings.savedKind.MACHINE" as const, [EntityKind.PROVIDER]: "settings.savedKind.PROVIDER" as const, [EntityKind.MODEL]: "settings.savedKind.MODEL" as const, [EntityKind.SETTINGS]: "settings.savedKind.SETTINGS" as const };
  const mutation = useRetainedMutation(`configuration:${kind}:${source?.id ?? "new"}`, ConfigurationQuery.saveConfiguration, (result, request) => {
    if (result.requestId === request.mutation?.requestId && (result.resource?.kind === kind && isEntityId(result.resource.id) && supportsResourceSchema(result.resource) && result.resource.revision > 0n || result.job?.kind === EntityKind.JOB && isEntityId(result.job.id) && supportsResourceSchema(result.job) && result.job.revision > 0n)) {
      const submitted = object(JSON.parse(new TextDecoder().decode(request.documentJson)));
      if (kind === EntityKind.SETTINGS || kind === EntityKind.REPOSITORY) remediationPreference.remember(text(object(submitted.remediation).machine_id));
      if (kind === EntityKind.REPOSITORY) {
        const original = items(document(initial).checkouts).map(object);
        const added = items(submitted.checkouts).map(object).filter(row => !original.some(old => old.machine_id === row.machine_id && old.path === row.path));
        if (added.length) checkoutPreference.remember(text(added[added.length - 1].machine_id));
      }
    }
    if (result.job) setJob(result.job);
    else if (result.resource) {
      if (inline) { setBaseline(result.resource); setData(document(result.resource)); setProblem(""); }
      notifications.notify({ kind: ToastKind.Success, message: ownedMessage(savedKind[kind as keyof typeof savedKind] ?? "settings.savedKind.fallback"), id: request.mutation?.requestId }); saved();
    } else setJob("unknown");
  }, inline ? (result, request) => !result.resource || Boolean(result.resource.id && readableServerPreferences(result.resource)
    && (!request.mutation?.id || result.resource.id === request.mutation.id)
    && result.resource.revision > (request.mutation?.expectedRevision ?? 0n)) : undefined);
  const formatMutation = useRetainedMutation(`account-change-format:${source?.id ?? "new"}`, AccountQuery.changeAccountApiFormat, (result, request) => {
    notifications.notify({ kind: ToastKind.Success, message: ownedMessage("settings.savedKind.ACCOUNT"), id: request.mutation?.requestId }); saved();
  }, (result, request) => Boolean(result.account && result.account.kind === EntityKind.ACCOUNT && result.account.id === request.mutation?.id && result.account.revision > (request.mutation?.expectedRevision ?? 0n) && Boolean(apiFormat(document(result.account).api_protocol)) && (result.replayed || apiFormatToWire(apiFormat(document(result.account).api_protocol)!) === request.apiProtocol)));
  const polled = current.data?.resource;
  const latest = inline ? latestServerPreferences(source, preferencesObservation?.resource, polled?.id === source?.id ? polled : undefined) : undefined;
  const dirty = inline && JSON.stringify(data) !== JSON.stringify(source ? document(source) : initialData ?? newConfiguration(kind));
  // An older in-flight read cannot replace the resource returned by Save. The
  // revision-bound document remains authoritative across query invalidation.
  const currentUnavailable = inline && Boolean(source && current.data && (!polled || polled.id !== source.id ||
    (polled.revision >= source.revision && !readableServerPreferences(polled))));
  const stale = inline ? Boolean(latest && (!source || latest.id !== source.id || latest.revision > source.revision))
    : apiEditor ? Boolean(source && polled && polled.revision > source.revision && accountEditingIdentity(document(polled)) !== accountEditingIdentity(document(source))) : Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = mutation.busy || mutation.uncertain || formatMutation.busy || formatMutation.uncertain;
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
  useEffect(() => {
    if (!apiEditor || !source || !polled || polled.id !== source.id || polled.kind !== source.kind || polled.revision <= source.revision || stale || blocked) return;
    // Adopt only server-owned observation changes. Keep every editable draft
    // and use the exact latest resource bytes for the next revision-bound save.
    setBaseline(polled);
    setData(draft => ({ ...document(polled), ...accountDraftPreferences(draft) }));
  }, [apiEditor, source, polled, stale, blocked]);
  const change = (value: Document) => {
    if (encode(value).byteLength > 1 << 20 || (kind === EntityKind.TEMPLATE && new TextEncoder().encode(text(value.contents)).byteLength > 128 << 10)) { setProblem(ownedMessage("settings.extra.0097158d86f7")); return; }
    setData(value); setProblem("");
  };
  const isApiEntry = kind === EntityKind.ACCOUNT && data.type === "api";
  const kindLabel = isApiEntry ? copy("settings.extra.1ebd6d7b3aeb") : kind === EntityKind.SETTINGS ? serverPreferenceLabel(serverPreferenceSection) : kindNames[kind];
  const apiEntryHeading = isApiEntry ? <header className="api-entry-heading"><h1 hidden={inTask}>{initial ? copy("settings.editPreferences_00b4cc") : copy("settings.newAiApiKeyEntry_5f978c")}</h1><p>{resourceName(initial)}</p><p className="api-entry-scope">{copy("settings.savedOnTheSelectedServer_93dbee")}</p></header> : null;
  if (job) return <section className={isApiEntry ? "api-entry-workflow" : undefined}>{apiEntryHeading}{job === "unknown" ? <p role="alert">{copy("settings.theServerAcknowledgedThisRequestWithout_061fa2")}</p> : <TrackedJob initial={job} active={active}>{(state) => state === JobState.Succeeded ? <><p>{copy("settings.configurationSavedAfterWorkerValidation_d2b875")}</p><SettingsActionButton icon={SettingsActionIcon.Cancel} onClick={saved}>{copy("settings.done_11a676")}</SettingsActionButton></> : state === JobState.Failed || state === JobState.Canceled ? <SettingsActionButton icon={SettingsActionIcon.Back} onClick={() => setJob(undefined)}>{copy("settings.returnToRetainedDraft_213f1b")}</SettingsActionButton> : null}</TrackedJob>}</section>;
  const validSubscriptionProvider = !subscriptionOnly || (kind === EntityKind.PROVIDER && data.protocol === "native-subscription" && data.authentication === "subscription" && text(data.endpoint) === "");
  const repositoryNeedsRemoteCapability = kind === EntityKind.REPOSITORY && typeof data.remote_url === "string" && data.remote_url !== "";
  const repositoryStatusPending = repositoryNeedsRemoteCapability && repositoryStatus.data === undefined && !repositoryStatus.error;
  const repositoryStatusFailed = repositoryNeedsRemoteCapability && Boolean(repositoryStatus.error);
  const repositoryUnsupported = repositoryNeedsRemoteCapability && repositoryStatus.data !== undefined && !repositoryStatus.data.capabilities.includes(SystemCapability.REMOTE_REPOSITORIES_V1);
  const supportsProjectBehavior = Boolean(repositoryStatus.data?.capabilities.includes(SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1));
  const supportsSessionDefaults = Boolean(repositoryStatus.data?.capabilities.includes(SystemCapability.SESSION_DEFAULTS_V1));
 const defaultsUnsupported = [EntityKind.PROJECT, EntityKind.SETTINGS].includes(kind) && source?.schemaVersion === 3 && !supportsSessionDefaults;
 const prefixValue = kind === EntityKind.SETTINGS ? data.branch_prefix : kind === EntityKind.PROJECT ? object(data.settings).branch_prefix : undefined;
 const prefixInvalid = prefixValue !== undefined && (typeof prefixValue !== "string" || !validBranchPrefix(prefixValue));
 const behaviorUnsupported = [EntityKind.PROJECT, EntityKind.SETTINGS].includes(kind) && source?.schemaVersion === 2 && !supportsProjectBehavior;
  const saveDisabled = defaultsUnsupported || prefixInvalid || behaviorUnsupported || selectionPending || fieldsBlocked || repositoryStatusPending || repositoryStatusFailed || repositoryUnsupported || blocked || childPending || stale || inlineReadBlocked || (inline && (!dirty || conflict)) || data.reconfiguration_required === true || !validSubscriptionProvider || Boolean(source && current.error);
  const submit = () => {
    // The ref also fences a submit dispatched before React commits the disabled button.
    if (saveDisabled || pendingSelections.current.size) return;
    const selectedFormat = apiFormat(data.api_protocol);
    if (apiEditor && keepsFormatKey && source && selectedFormat && selectedFormat !== document(source).api_protocol) {
      void formatMutation.send({ mutation: { id: source.id, expectedRevision: source.revision, requestId: newRequestId() }, apiProtocol: apiFormatToWire(selectedFormat), alias: text(data.alias), enabled: data.enabled === true, excludeAutomatic: data.exclude_automatic === true, recoveryNotifications: data.recovery_notifications === true });
      return;
    }
    const submittedData = { ...data };
 if ([EntityKind.PROJECT, EntityKind.SETTINGS].includes(kind)) {
 if (supportsSessionDefaults) {
 if (kind === EntityKind.PROJECT) submittedData.settings = { ...object(data.settings), plan_mode_default: text(object(data.settings).plan_mode_default) || "inherit" };
 else { submittedData.plan_mode_default = data.plan_mode_default === true; submittedData.branch_prefix = typeof data.branch_prefix === "string" ? data.branch_prefix : defaultBranchPrefix; }
 } else { delete submittedData.plan_mode_default; delete submittedData.branch_prefix; if (kind === EntityKind.PROJECT) { const settings = { ...object(data.settings) }; delete settings.plan_mode_default; delete settings.branch_prefix; submittedData.settings = settings; } }
 }
    if ([EntityKind.PROJECT, EntityKind.SETTINGS].includes(kind)) {
      if (supportsProjectBehavior) {
        if (kind === EntityKind.PROJECT) submittedData.settings = object(submittedData.settings);
        else submittedData.automatic_plan_approval = data.automatic_plan_approval === true;
      } else {
        delete submittedData.settings; delete submittedData.automatic_plan_approval;
      }
    }
    let documentJson: Uint8Array;
    try {
      documentJson = kind === EntityKind.ACCOUNT && source ? accountPreferencesDocument(source, { ...(data.type === "api" && apiFormat(data.api_protocol) ? { api_protocol: apiFormat(data.api_protocol)! } : {}), alias: text(data.alias), enabled: data.enabled === true, exclude_automatic: data.exclude_automatic === true, recovery_notifications: data.recovery_notifications === true }) : encode(submittedData);
    } catch {
      setProblem(copy("settings.accountPreferencesCouldNotBeSavedReopen_4a7c1b")); return;
    }
    void mutation.send({ mutation: { id: source?.id ?? "", expectedRevision: source?.revision ?? 0n, requestId: newRequestId() }, kind, schemaVersion: [EntityKind.PROJECT, EntityKind.SETTINGS].includes(kind) && !supportsProjectBehavior ? 1 : configurationSchemaVersion(kind, submittedData), documentJson });
  };
  if (kind === EntityKind.PROJECT && !source) return <ResourceSelectionPending.Provider value={reportSelectionPending}><ProjectCreationWizard registrationAdapters={registrationAdapters} data={data} change={change} active={active} visible={taskVisible} blocked={blocked || childPending} busy={mutation.busy} saveDisabled={saveDisabled} submit={submit} cancel={cancelTask} cancelDisabled={!inTask && (blocked || childPending)} uncertain={mutation.uncertain} retry={mutation.retry}>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
  </ProjectCreationWizard></ResourceSelectionPending.Provider>;
  return <ResourceSelectionPending.Provider value={reportSelectionPending}><form id={formId} ref={form} aria-label={inline ? (serverPreferenceSection === ServerPreferenceSection.GitWorkflow ? "Git workflow form" : "Server preferences form") : undefined} className={kind === EntityKind.REPOSITORY && source ? "repository-editor" : kind === EntityKind.PROJECT ? "project-editor" : kind === EntityKind.AGENT ? "agent-configuration" : kind === EntityKind.SETTINGS ? "server-preferences-editor" : isApiEntry ? "api-entry-workflow api-entry-preferences" : undefined} onInvalidCapture={kind === EntityKind.REPOSITORY && source ? revealRepositoryInvalidControl : kind === EntityKind.AGENT ? revealAgentInvalidControl : kind === EntityKind.SETTINGS ? revealServerPreferenceInvalidControl : undefined} onSubmit={(event) => { event.preventDefault(); submit(); }}>
    {inline ? null : isApiEntry ? apiEntryHeading : <h3 hidden={inTask}>{initial ? copy("settings.edit_464c4f") : copy("settings.new_18fdd5")} {kindLabel}</h3>}
    {kind === EntityKind.AGENT && !initial ? <p className="agent-subtitle">{copy("settings.configureTheEssentialsThenCustomizeOnly_a8beda")}</p> : null}
    <fieldset disabled={blocked || (inline && (!preferencesObservation?.complete || currentUnavailable))}><ConfigurationFields disabled={blocked || childPending} supportsProjectBehavior={supportsProjectBehavior} supportsSessionDefaults={supportsSessionDefaults} movementActive={active && taskVisible && !blocked && !childPending} keepsFormatKey={setKeepsFormatKey} initial={source} saveBlocked={setFieldsBlocked} kind={kind} data={data} change={change} active={active} existing={Boolean(source)} pendingOperation={setChildPending} subscriptionOnly={subscriptionOnly} serverPreferenceSection={serverPreferenceSection} /></fieldset>
    {repositoryUnsupported ? <p role="status">{copy("settings.repositoryServerUpdateRequired")}</p> : null}
    {behaviorUnsupported ? <p role="status">{copy("configuration-fields.behaviorUnsupported")}</p> : null}
    {stale || (inline && conflict) ? <p role="alert">{inline ? copy("settings.inlinePreferencesChangedElsewhereDraftRetained", { v0: kindLabel }) : copy("settings.thisEntryChangedElsewhereYourDraft_106fa0")}</p> : null}{currentUnavailable ? <p role="status">{inline ? copy("settings.inlinePreferencesUnavailableDraftRetained", { v0: kindLabel }) : copy("settings.serverPreferencesUnavailableDraftRetained")}</p> : null}{problem ? <p role="alert">{problem}</p> : null}<Problem error={current.error} actions={<SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || blocked || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</SettingsActionButton>} /><Problem error={mutation.error || formatMutation.error || (repositoryNeedsRemoteCapability ? repositoryStatus.error : undefined)} />{repositoryStatusFailed ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={repositoryStatus.isFetching} onClick={() => void repositoryStatus.refetch()}>{copy("settings.retryRepositoryCapability")}</SettingsActionButton> : null}
    {subscriptionOnly && !validSubscriptionProvider ? <p role="alert">{copy("settings.subscriptionProvidersMustUseNativeSubscription_8e47b2")}</p> : null}
    {inline ? <div className="actions server-preferences-actions"><span className="server-preferences-status" role="status">{mutation.busy ? copy("settings.savingChanges") : mutation.uncertain ? copy("settings.saveOutcomeUnknown") : dirty ? copy("settings.unsavedChanges") : ""}</span><SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" disabled={blocked || childPending || (!dirty && !stale && !conflict)} onClick={discard}>{copy("settings.discardChanges")}</SettingsActionButton>{formatMutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={formatMutation.busy} onClick={formatMutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</SettingsActionButton> : null}{mutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</SettingsActionButton> : null}<SettingsActionButton icon={SettingsActionIcon.Save} className="primary" disabled={saveDisabled}>{copy("settings.saveChanges")}</SettingsActionButton></div> : null}
    {!inline && (kind === EntityKind.AGENT || kind === EntityKind.SETTINGS) ? <SettingsTaskActions form={formId} className={kind === EntityKind.AGENT ? "agent-footer" : "server-preferences-actions"}><SettingsTaskDismissButton type="button" data-settings-task-cancel disabled={!inTask && (blocked || childPending)} onClick={cancelTask}>{copy("settings.cancelEdit_6fa271")}</SettingsTaskDismissButton>{formatMutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={formatMutation.busy} onClick={formatMutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</SettingsActionButton> : null}{mutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</SettingsActionButton> : null}<SettingsActionButton icon={SettingsActionIcon.Save} className="primary" disabled={saveDisabled}>{apiEditor && keepsFormatKey ? copy("settings.saveChanges") : <LocalizedText id="settings.save_cdb68b" components={{ s0: <>{kindLabel}</> }} />}</SettingsActionButton></SettingsTaskActions>
      : !inline ? <SettingsTaskActions form={formId}>{kind === EntityKind.REPOSITORY && source && inTask ? null : <SettingsTaskDismissButton type="button" data-settings-task-cancel disabled={!inTask && (blocked || childPending)} onClick={cancelTask}>{copy("settings.cancelEdit_6fa271")}</SettingsTaskDismissButton>}<SettingsActionButton icon={SettingsActionIcon.Save} className="primary" disabled={saveDisabled}>{apiEditor && keepsFormatKey ? copy("settings.saveChanges") : <LocalizedText id="settings.save_cdb68b" components={{ s0: <>{kindLabel}</> }} />}</SettingsActionButton>{formatMutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={formatMutation.busy} onClick={formatMutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</SettingsActionButton> : null}{mutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("settings.retryTheSameConfiguration_630088")}</SettingsActionButton> : null}</SettingsTaskActions> : null}

  </form></ResourceSelectionPending.Provider>;
}

function ServerPreferencesWorkspace({ resources, nextPageToken, page, fetching, error, section, active, saved, authority, onNetworkPresentationChange }: { resources?: Resource[]; nextPageToken?: string; page: string; fetching: boolean; error?: unknown; section: ServerPreferenceSection; active: boolean; saved: () => void; authority?: PairingAuthority; onNetworkPresentationChange?: (open: boolean) => void }) {
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
    <span data-settings-search-pending={!resources && !error ? "true" : undefined} hidden />
    {initial !== undefined ? <ConfigurationEditor kind={EntityKind.SETTINGS} initial={initial ?? undefined} serverPreferenceSection={section} active={active} saved={saved} cancel={() => {}} presentation={ConfigurationEditorPresentation.InlineServerPreferences} preferencesObservation={{ complete, resource: complete ? resources?.[0] : undefined, fetching, error }} /> : null}
    {section !== ServerPreferenceSection.GitWorkflow ? <div className="server-preferences-network"><NetworkSettings active={active} authority={authority} onPresentationChange={onNetworkPresentationChange} /></div> : null}
  </>;
}

export type SettingsNavigationEntry = SettingsEntryDestination | { category: SettingsCategory; target?: SettingsSearchTarget; generation: string; resourceId?: string; resourceKind?: EntityKind };
export enum SettingsEntryDestination { ConnectionDiagnostics="connection-diagnostics", Repositories = "repositories", NewProject = "new-project", RunnerDevices = "runner-devices", GitProfiles = "git-profiles" }
enum SettingsArea { Configuration, Diagnostics, Notifications, Transfer, Integrations, Backups, Appearance, KeyboardShortcuts }

enum SettingsGroup { Ai = "AI", Coding = "Coding", Devices = "Device management", System = "System" }

export const settingsCategories: Record<SettingsCategory, { label: string; description: string; kind?: EntityKind; area: SettingsArea }> = {
  [SettingsCategory.KeyboardShortcuts]: { get label() { return copy("shortcuts.title"); }, get description() { return copy("shortcut-settings.scope"); }, area: SettingsArea.KeyboardShortcuts },
  [SettingsCategory.Appearance]: { get label() { return copy("settings.appearance_3907fa"); }, get description() { return copy("settings.savedOnThisComputer_11cb50"); }, area: SettingsArea.Appearance },
  [SettingsCategory.Backups]: { get label() { return copy("settings.backups_3334fe"); }, get description() { return copy("settings.inspectManagedDatabaseImagesAndFollow_b28b20"); }, area: SettingsArea.Backups },
  [SettingsCategory.SubscriptionAccounts]: { get label() { return copy("settings.aiSubscription_ec8b7a"); }, get description() { return copy("settings.manageYourSubscriptionsAndConnectMore_f0b9fe"); }, kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.ApiAccounts]: { get label() { return copy("settings.aiApiKeys_da1a0f"); }, get description() { return copy("settings.manageAiApiKeysAndKeyless_372629"); }, kind: EntityKind.ACCOUNT, area: SettingsArea.Configuration },
  [SettingsCategory.Providers]: { get label() { return copy("settings.apiProviders_376855"); }, get description() { return copy("settings.providerAvailabilityIsSavedOnThe_11e7c8"); }, kind: EntityKind.PROVIDER, area: SettingsArea.Configuration },
  [SettingsCategory.AgentWorkers]: { get label() { return copy("settings.agentWorkers_e60c23"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.AGENT, area: SettingsArea.Configuration },
  [SettingsCategory.Instructions]: { get label() { return copy("settings.instructions_934652"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.TEMPLATE, area: SettingsArea.Configuration },
  [SettingsCategory.ProjectDefaults]: { get label() { return copy("configuration-fields.projectDefaults"); }, get description() { return copy("configuration-fields.inheritanceHelp"); }, kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.Projects]: { get label() { return copy("settings.projects_04e2a9"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.PROJECT, area: SettingsArea.Configuration },
  [SettingsCategory.Repositories]: { get label() { return copy("settings.repositories_1e32af"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.REPOSITORY, area: SettingsArea.Configuration },
  [SettingsCategory.ExecutionWorkers]: { get label() { return copy("settings.runnerDevices_a176a8"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.MACHINE, area: SettingsArea.Configuration },
  [SettingsCategory.PairedDevices]: { get label() { return copy("settings.pairedDevices_f72c6a"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.DEVICE, area: SettingsArea.Configuration },
  [SettingsCategory.ServerPreferences]: { get label() { return copy("settings.serverPreferences_eba66b"); }, get description() { return copy("settings.savedOnTheSelectedServer_93dbee"); }, kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.GitWorkflow]: { get label() { return copy("settings.gitWorkflow"); }, get description() { return copy("settings.gitWorkflowDescription"); }, kind: EntityKind.SETTINGS, area: SettingsArea.Configuration },
  [SettingsCategory.Integrations]: { get label() { return copy("settings.integrations_090512"); }, get description() { return copy("settings.manageGithubProfilesForRepositoryAccess_42adb1"); }, area: SettingsArea.Integrations },
  [SettingsCategory.Diagnostics]: { get label() { return copy("settings.connectionDiagnostics_b30b0d"); }, get description() { return copy("settings.connectionDescription"); }, area: SettingsArea.Diagnostics },
  [SettingsCategory.Notifications]: { get label() { return copy("settings.notifications_788011"); }, get description() { return copy("settings.thesePreferencesBelongToThisClient_082e1e"); }, area: SettingsArea.Notifications },
  [SettingsCategory.Transfer]: { get label() { return copy("settings.importExport_6e061f"); }, get description() { return copy("settings.moveConfigurationBetweenDelidevServers_749ed7"); }, area: SettingsArea.Transfer },
};

export const settingsGroups: { label: SettingsGroup; categories: SettingsCategory[] }[] = [
  { label: SettingsGroup.Ai, categories: [SettingsCategory.SubscriptionAccounts, SettingsCategory.ApiAccounts, SettingsCategory.Providers, SettingsCategory.AgentWorkers, SettingsCategory.Instructions] },
  { label: SettingsGroup.Coding, categories: [SettingsCategory.ProjectDefaults, SettingsCategory.Projects, SettingsCategory.Repositories, SettingsCategory.Integrations, SettingsCategory.GitWorkflow] },
  { label: SettingsGroup.Devices, categories: [SettingsCategory.ExecutionWorkers, SettingsCategory.PairedDevices] },
  { label: SettingsGroup.System, categories: [SettingsCategory.Appearance, SettingsCategory.KeyboardShortcuts, SettingsCategory.ServerPreferences, SettingsCategory.Diagnostics, SettingsCategory.Notifications, SettingsCategory.Transfer, SettingsCategory.Backups] },
];

const settingsIcons: Record<SettingsCategory, string> = {
 [SettingsCategory.ProjectDefaults]: "M4 6h16M4 12h16M4 18h16",

  [SettingsCategory.KeyboardShortcuts]: "M3 6h18v12H3zM6 9h1m3 0h1m3 0h1m3 0h1M7 15h10",
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


interface SettingsProps { openUsage?: (entry: UsageEntry) => void; readLocalWorker?: ReadLocalWorkerProof; chooseRepositoryFolder?: ChooseRepositoryFolder; connectionSettings?: React.ReactNode; pairingAuthority?: PairingAuthority; visible?: boolean; controlLocalWorker?: ControlLocalWorker; currentDeviceId?: string; entryDestination?: SettingsNavigationEntry; destinationConsumed?: () => void }
enum SettingsEntryKind { NewProject, ManageAccounts, AddAccount, NotificationTarget }
type SettingsCategoryEntry = {kind:SettingsEntryKind.NotificationTarget;resourceId:string;resourceKind:EntityKind} | { kind: SettingsEntryKind.NewProject } | { kind: SettingsEntryKind.ManageAccounts | SettingsEntryKind.AddAccount; providerId: string; provider?: AccountProviderSummary; startOAuth?: boolean };
interface SettingsSelection { category: SettingsCategory; key: string; entry?: SettingsCategoryEntry }
type NavigateSettings = (category: SettingsCategory, entry?: SettingsCategoryEntry) => void;

function entrySelection(destination?: SettingsNavigationEntry): SettingsSelection {
  if (typeof destination === "object") return { category: destination.category, key: newRequestId(),entry:destination.resourceId&&destination.resourceKind?{kind:SettingsEntryKind.NotificationTarget,resourceId:destination.resourceId,resourceKind:destination.resourceKind}:undefined };
  return { category: destination === SettingsEntryDestination.ConnectionDiagnostics ? SettingsCategory.Diagnostics : destination === SettingsEntryDestination.GitProfiles ? SettingsCategory.Integrations : destination === SettingsEntryDestination.RunnerDevices ? SettingsCategory.ExecutionWorkers : destination === SettingsEntryDestination.Repositories ? SettingsCategory.Repositories : destination === SettingsEntryDestination.NewProject ? SettingsCategory.Projects : SettingsCategory.SubscriptionAccounts, key: newRequestId(), entry: destination === SettingsEntryDestination.NewProject ? { kind: SettingsEntryKind.NewProject } : undefined };
}

export function Settings({ visible = true, ...props }: SettingsProps) {
  useLocale();
  return visible ? <DisclosureDensityScope density={DisclosureDensity.Settings}><SettingsActionScope><SettingsVisit {...props} /></SettingsActionScope></DisclosureDensityScope> : null;
}

function SettingsVisit({ entryDestination, destinationConsumed, ...props }: SettingsProps) {
  useLocale();
  const closeDrawer = useCloseSidebarDrawer();
  const [searchRequest, setSearchRequest] = useState<SettingsSearchRequest>();
  const [selection, setSelection] = useState(() => entrySelection(entryDestination));
  const initialDestination = useRef(entryDestination);
  const handledDestination = useRef<SettingsNavigationEntry | undefined>(undefined);
  const navigate = useCallback<NavigateSettings>((category, entry) => {
    setSearchRequest(undefined);
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
    if (typeof entryDestination === "object") setSearchRequest({ category: entryDestination.category, target: entryDestination.target ?? SettingsSearchTarget.Category, generation: entryDestination.generation });
    destinationConsumed?.();
  }, [destinationConsumed, entryDestination, navigate]);
  return <>
    <SidebarSurface active title={copy("settings.settings_74a883")} className="settings-navigation" showHeading={false}>
      <SettingsSearch categories={settingsGroups.flatMap(group => group.categories.map(category => ({ category, label: settingsCategories[category].label, help: settingsCategories[category].description })))} select={(target) => { navigate(target.category); setSearchRequest({ ...target, generation: newRequestId() }); }}>
      <nav aria-label={copy("settings.settingsCategories_b9ed95")} data-settings-groups>
        {settingsGroups.map(group => <section className="settings-nav-group" key={group.label}>
          <h2>{copy(group.label === SettingsGroup.Ai ? "settings.group.aiAgents" : group.label === SettingsGroup.Coding ? "settings.group.coding" : group.label === SettingsGroup.Devices ? "settings.group.devices" : "settings.group.system")}</h2>
          {group.categories.map(category => <button type="button" className="settings-category-button" key={category} data-settings-category={category} aria-current={selection.category === category ? "page" : undefined} aria-pressed={selection.category === category} onClick={() => navigate(category)}>
            <SettingsIcon category={category} /><span>{settingsCategories[category].label}</span>
          </button>)}
        </section>)}
      </nav>
      </SettingsSearch>
    </SidebarSurface>
    {/* Each category owns its waits and drafts. Disposal rejects late results
        without canceling or replaying already accepted server/native work. */}
    <SettingsLifetime key={selection.key}>{opening => <MutationIntents><SettingsWorkspace {...props} searchRequest={searchRequest} selectedCategory={selection.category} entry={selection.entry} navigate={navigate} controlLocalWorker={props.controlLocalWorker ? Object.assign((action: LocalWorkerAction, generation?: string) => opening.native(() => props.controlLocalWorker!(action, generation)), { automatic: props.controlLocalWorker.automatic }) : undefined} /></MutationIntents>}</SettingsLifetime>
  </>;
}

function SettingsWorkspace({ openUsage, connectionSettings, visible = true, controlLocalWorker, readLocalWorker, chooseRepositoryFolder, currentDeviceId, pairingAuthority, selectedCategory, searchRequest, entry, navigate }: SettingsProps & { searchRequest?: SettingsSearchRequest; selectedCategory: SettingsCategory; entry?: SettingsCategoryEntry; navigate: NavigateSettings }) {
  useLocale();

  const [device, setDevice] = useState<Resource>();
  const [expandedDevices, setExpandedDevices] = useState<ReadonlySet<string>>(() => new Set());
  const pairedPageIds = useRef<ReadonlySet<string>>(new Set());
  const [pairingTriggerContainer, setPairingTriggerContainer] = useState<HTMLSpanElement | null>(null);
  const deviceContent = useRef<HTMLDivElement>(null), refreshDevices = useRef<HTMLButtonElement>(null);
  const deviceReturnFocus = useRef<{ id: string; exit: DeviceRevocationExit } | undefined>(undefined);
  const page = "";
  const [editing, setEditing] = useState<{ kind?: EntityKind; initial?: Resource; initialData?: Document; key: string; subscriptionOnly?: boolean } | undefined>(() => entry?.kind === SettingsEntryKind.NewProject ? { key: newRequestId() } : undefined);
  const [machine, setMachine] = useState<Resource>();
  const runnerInspection = useRunnerRemediation({ compact: false, authority: pairingAuthority, active: visible });
  const inspectMachine = (row: Resource) => runnerInspection ? runnerInspection(row) : setMachine(row);
  const [deleting, setDeleting] = useState<Resource>();
  const [routing, setRouting] = useState<Resource>();
  const [account, setAccount] = useState<Resource>();
  const notificationTarget=entry?.kind===SettingsEntryKind.NotificationTarget?entry:undefined;
  const notificationResource=useQuery(ResourceQuery.getResource,{kind:notificationTarget?.resourceKind??EntityKind.UNSPECIFIED,id:notificationTarget?.resourceId??""},{enabled:visible&&Boolean(notificationTarget),retry:false});
  const appliedNotification=useRef(false);
  useEffect(()=>{
    if(!visible||!notificationTarget||appliedNotification.current||notificationResource.error||notificationResource.isFetching)return;
    const value=notificationResource.data?.resource;
    if(value?.id!==notificationTarget.resourceId||value.kind!==notificationTarget.resourceKind)return;
    appliedNotification.current=true;
    if(value.kind===EntityKind.MACHINE&&selectedCategory===SettingsCategory.ExecutionWorkers)setMachine(value);
    if(value.kind===EntityKind.ACCOUNT&&selectedCategory===SettingsCategory.SubscriptionAccounts)setAccount(value);
  },[visible,notificationTarget,notificationResource.data,notificationResource.error,notificationResource.isFetching,selectedCategory]);

  const [providerList, setProviderList] = useState<ProviderListState>({ query: "", page: "" });

  const [startApiWizard, setStartApiWizard] = useState<{ key: string; providerId: string; provider?: AccountProviderSummary; startOAuth?: boolean } | undefined>(() => entry?.kind === SettingsEntryKind.AddAccount ? { key: newRequestId(), providerId: entry.providerId, provider: entry.provider, startOAuth: entry.startOAuth } : undefined);
  const [apiProviderID, setApiProviderID] = useState(() => entry && entry.kind !== SettingsEntryKind.NewProject && entry.kind!==SettingsEntryKind.NotificationTarget ? entry.providerId : "");
  const [apiProviderHint, setApiProviderHint] = useState<AccountProviderSummary | undefined>(() => entry && entry.kind !== SettingsEntryKind.NewProject && entry.kind!==SettingsEntryKind.NotificationTarget ? entry.provider : undefined);

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
  const isProjectDefaults = selectedCategory === SettingsCategory.ProjectDefaults;
  const isPreferenceCategory = isServerPreferences || isGitWorkflow || isProjectDefaults;
  const preferenceSection = isProjectDefaults ? ServerPreferenceSection.ProjectDefaults : isGitWorkflow ? ServerPreferenceSection.GitWorkflow : ServerPreferenceSection.AccountRouting;
  const preferenceLabel = serverPreferenceLabel(preferenceSection);
  const isPairedDevices = selectedCategory === SettingsCategory.PairedDevices;
  const isRunnerDevices = selectedCategory === SettingsCategory.ExecutionWorkers;
  const hasSpecializedPanel = isAccountCategory || isApiProviders;
  const hasOverlay = Boolean(editing || account || deleting || routing || machine || device);
  const [networkPresentationOpen, setNetworkPresentationOpen] = useState(false);
  const categoryDescription = kind === EntityKind.DEVICE
    ? copy("settings.extra.302fcd6cacc5")
    : kind === EntityKind.MACHINE && !controlLocalWorker ? copy("settings.extra.bd632b65e652")
    : selected.description;
  const inventoryActive = visible && area === SettingsArea.Configuration && !hasSpecializedPanel && !hasOverlay && !networkPresentationOpen;
  const [modelRefresh, refreshModels] = useState(0);
  const inventory = useResourceScrollQuery(kind, inventoryActive, selectedCategory, false, undefined, undefined, undefined, true);
  const root = useScrollRoot(deviceContent);
  const resident = new Map<string, Resource>();
  for (const value of inventory.payloadPages) for (const row of value.payload) if (!resident.has(row.id) || resident.get(row.id)!.revision < row.revision) resident.set(row.id, row);
  const resources = inventory.rows.flatMap(row => resident.has(row.id) ? [resident.get(row.id)!] : []);
  const projectMetadata = useProjectListMetadata(isProjects ? resources.flatMap(projectRepositoryIds) : [], visible && isProjects);
  const result = { data: inventory.loaded ? { resources, nextPageToken: inventory.nextPageToken } : undefined,
    error: inventory.error?.failure, isFetching: Boolean(inventory.loading), isPending: !inventory.loaded && !inventory.error,
    isSuccess: inventory.loaded && !inventory.error && !inventory.loading,
    refetch: () => inventory.refreshExplicit() };
  // Within this category, only a successful paired page can prune retained
  // identities. Leaving the category disposes every disclosure with its scope.
  useEffect(() => {
    if (!isPairedDevices || !result.isSuccess || !result.data) return;
    const ids = new Set(inventory.rows.map((row) => row.id));
    pairedPageIds.current = ids;
    setExpandedDevices((current) => {
      const retained = new Set([...current].filter((id) => ids.has(id)));
      return retained.size === current.size ? current : retained;
    });
  }, [isPairedDevices, inventory.rows, result.isSuccess]);
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
  const apiInventory = useProviderPages("", visible && isApiAccounts && !hasOverlay, false, true);
  const eligibleInventory = useProviderPages("", visible && isApiAccounts && !hasOverlay, true, true);
  const providerPicker: AccountProviderPicker = {
    ready: Boolean(!eligibleInventory.error && eligibleInventory.data && providerInventoryReady(eligibleInventory.data.capabilities) && eligibleInventory.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER)),
    loaded: eligibleInventory.loaded, fetching: eligibleInventory.isFetching, failure: eligibleInventory.error?.failure,
    pageToken: "", nextPageToken: eligibleInventory.nextPageToken, retry: eligibleInventory.refreshExplicit,
    next: eligibleInventory.append, first: eligibleInventory.reload, query: eligibleInventory,
    renderOptions: (render, root, active) => <ScrollPayloadWindow query={eligibleInventory} root={root} active={active}>{(responses, rows) => responses.flatMap(response => response.entries.filter(entry => rows.some(row => row.id === inventoryIdentity(entry))).flatMap(entry => { const provider = providerSummary(entry, response.capabilities); return provider && provider.enabled ? [render(provider)] : []; }))}</ScrollPayloadWindow>,
  };
  const providerPresets = useQuery(ProviderQuery.listProviderPresets, {}, { enabled: visible && area === SettingsArea.Configuration && isApiAccounts });
  const apiAccountTypeFilteringReady = Boolean(!apiInventory.error && apiInventory.data && providerInventoryReady(apiInventory.data.capabilities) && apiInventory.data.capabilities.includes(ProviderInventoryCapability.ACCOUNT_TYPE_FILTER));
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
    apiFormats: capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) ? entry.apiFormats.flatMap(profile => {
      const normalized = apiFormatProfileFromWire(profile);
      return normalized ? [normalized] : [];
    }) : undefined,
    oauthFormatSelectingAvailable: capabilities.includes(ProviderInventoryCapability.ACCOUNT_OAUTH_API_PROTOCOL_V1) && capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1),
    oauthAvailable: capabilities.includes(entry.presetId === ProviderPresetId.OPENROUTER ? ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1 : ProviderInventoryCapability.ACCOUNT_OAUTH_V1) && entry.enabled && (entry.connectionMethod === ProviderConnectionMethod.OAUTH_PKCE || entry.presetId === ProviderPresetId.BASETEN && entry.connectionMethod === ProviderConnectionMethod.OAUTH_DEVICE),
  } : undefined;
  const apiProviders = apiInventory.payloadPages.flatMap(page => page.payload.flatMap(response => response.entries.map(entry => providerSummary(entry, response.capabilities)))).filter((value): value is AccountProviderSummary => value !== undefined);
  const eligibleProviders = eligibleInventory.payloadPages.flatMap(page => page.payload.flatMap(response => response.entries.map(entry => providerSummary(entry, response.capabilities)))).filter((value): value is AccountProviderSummary => value !== undefined && value.enabled);
  const configurationList = area === SettingsArea.Configuration && !hasSpecializedPanel;
  const successfulEmptyFirstPage = Boolean(inventory.loaded && inventory.rows.length === 0 && !result.error && !inventory.nextPageToken);
  const retainedServerEmpty = isPreferenceCategory && inventory.loaded && inventory.rows.length === 0 && !inventory.nextPageToken;
  const done = () => { setEditing(undefined); setDeleting(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  const providerEntrySummary = (entry?: ProviderInventoryEntry, capabilities: readonly ProviderInventoryCapability[] = []) => entry ? providerSummary(entry, capabilities) : undefined;
  const closeTask = () => { setDevice(undefined); setMachine(undefined); setDeleting(undefined); setRouting(undefined); setEditing(undefined); setAccount(undefined); };
  const taskResource = editing?.initial ?? device ?? machine ?? deleting ?? routing ?? account;
  const taskKind = editing?.kind ?? kind;
  const taskTitle = device ? "Revoke device" : machine ? "Runner Device details" : deleting ? deleting.kind === EntityKind.ACCOUNT && document(deleting).type === "api" ? copy("account-deletion.api.title") : "Delete configuration" : routing ? copy("settings.previewRouting_02d4d9") : account ? "Manage connection" : editing && taskKind === EntityKind.REPOSITORY && !editing.initial ? copy("settings.addRepository_2eda4d") : editing && taskKind === EntityKind.PROJECT && !editing.initial ? copy("project-creation.title") : editing ? `${editing.initial ? copy("settings.edit_464c4f") : copy("settings.new_18fdd5")} ${taskKind === EntityKind.SETTINGS ? preferenceLabel : kindNames[taskKind]}` : "Settings task";
  const taskSize = deleting || device ? SettingsDialogSize.Confirmation : routing ? SettingsDialogSize.Form : machine || account || [EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.REPOSITORY].includes(taskKind) ? SettingsDialogSize.Wide : SettingsDialogSize.Form;
  return <SettingsTasks>
      <SettingsSearchFocus request={searchRequest} root={deviceContent} category={selectedCategory} />
      <section className={selectedCategory === SettingsCategory.Repositories ? "settings-content settings-repositories" : isProjects ? "settings-content settings-projects" : isPreferenceCategory ? `settings-content settings-server-preferences${isGitWorkflow ? " settings-git-workflow" : ""}` : isApiAccounts ? "settings-content settings-api-keys" : isRunnerDevices ? "settings-content settings-runner-devices" : area === SettingsArea.Diagnostics ? "settings-content settings-connections" : "settings-content"} aria-label={copy("settings.settingsContent_e4dcd3")}>
        <SettingsTaskBackground><div className="settings-content-column">
        <div ref={deviceContent} className={isAgentWorkers ? "settings-agent-column" : isPairedDevices ? "settings-paired-column" : isRunnerDevices ? "settings-runner-column" : area === SettingsArea.Transfer ? "settings-transfer-column" : undefined}>
        {isGitWorkflow ? <p className="settings-breadcrumb">{copy("settings.gitWorkflow")}</p> : null}
        {area !== SettingsArea.Backups && !isApiAccounts && !isApiProviders ? <div className="settings-category-heading">
          <div className="settings-category-title"><h1 data-settings-search-target="category" aria-live="polite" aria-atomic="true">{selected.label}</h1>{selectedCategory === SettingsCategory.Repositories ? <p>{copy("settings.repositoryDescription")}</p> : isAgentWorkers ? <p className="settings-agent-summary">{copy("settings.reusableConfigurationsForYourAgents_5ba1a2")}</p> : isPreferenceCategory ? <p>{isGitWorkflow ? copy("settings.gitWorkflowDescription") : copy("settings.defaultRoutingWorktreeFetchAndPull_e19cf8")}</p> : null}<p className={isAgentWorkers ? "settings-scope settings-agent-scope" : isPreferenceCategory ? "settings-scope server-preferences-scope" : isPairedDevices ? "settings-scope paired-device-summary" : "settings-scope"}>{categoryDescription}</p>{isPairedDevices ? <p className="paired-device-scope">{copy("settings.savedOnTheSelectedServer_93dbee")}</p> : null}</div>
          {configurationList ? <div className="settings-toolbar">
            <SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} type="button" ref={isPairedDevices ? refreshDevices : undefined} aria-label={isGitWorkflow ? copy("settings.refreshGitWorkflow") : undefined} onClick={() => { if (isProjects) projectMetadata.refresh(); if (isAgentWorkers) refreshModels(value => value + 1); void result.refetch(); }}>{isGitWorkflow ? copy("settings.refresh_0e9161") : copy("settings.refreshSettings_65dbd6")}</SettingsActionButton>

            {isPairedDevices && pairingAuthority ? <span ref={setPairingTriggerContainer} /> : null}
            {editableKinds.includes(kind) && kind !== EntityKind.SETTINGS
              ? <SettingsActionButton icon={SettingsActionIcon.Add} type="button" className="primary" onClick={() => setEditing({ key: newRequestId() })}>{kind === EntityKind.REPOSITORY ? copy("settings.addRepository_2eda4d") : kind === EntityKind.PROJECT ? copy("project-creation.title") : copy("settings.new_077d61", { v0: kindNames[kind] })}</SettingsActionButton>
              : null}
          </div> : null}
        </div> : null}
        <div className="settings-panels">
          {area === SettingsArea.KeyboardShortcuts ? <ShortcutSettings /> : null}
          {area === SettingsArea.Appearance ? <div><AppearanceSettings /><LanguageSettings /><DateFormatSettings /></div> : null}
          {area === SettingsArea.Backups ? <div><Backups active={visible} /></div> : null}
          {area === SettingsArea.Integrations ? <div><Integrations active={visible} showCategoryIntro={false} /></div> : null}
          {area === SettingsArea.Transfer ? <div><ConfigurationTransfer active={visible} showCategoryIntro={false} /></div> : null}
          {notificationTarget&&notificationResource.error?<><Problem error={notificationResource.error}/><p role="status">{copy("inbox.operational.unavailable")}</p></>:null}
          {area === SettingsArea.Notifications ? <div><NotificationSettings active={visible} showCategoryIntro={false} openSubscriptions={()=>navigate(SettingsCategory.SubscriptionAccounts)} /></div> : null}
          {area === SettingsArea.Diagnostics ? <div>{connectionSettings ?? <section data-settings-search-target="current-connection" aria-label={copy("settings.connections.current")}><h2>{copy("settings.connections.current")}</h2><p>{copy("settings.connectionUnavailable")}</p></section>}</div> : null}
          {area === SettingsArea.Configuration ? <div>
            {controlLocalWorker && isRunnerDevices ? <div data-settings-search-target="local-worker"><LocalWorkerControls control={controlLocalWorker} presentation={LocalWorkerPresentation.RunnerDevices} active={visible && area === SettingsArea.Configuration && kind === EntityKind.MACHINE} changed={() => void client.invalidateQueries({ refetchType: "active" })} /></div> : null}
            {pairingAuthority && isPairedDevices ? <div><PairingGrant authority={pairingAuthority} active={visible && area === SettingsArea.Configuration && kind === EntityKind.DEVICE && !device} triggerContainer={pairingTriggerContainer} /></div> : null}
            {isApiAccounts ? <div>
              <AccountSettings apiFormatSelectingReady={Boolean(apiInventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) && eligibleInventory.data?.capabilities.includes(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1) && !apiInventory.error && !eligibleInventory.error)} openUsage={openUsage} section={AccountSettingsSection.Api} active={visible && isApiAccounts && !hasOverlay} accountTypeFilteringReady={apiAccountTypeFilteringReady} providerInventoryFailure={apiInventory.error?.failure} accountTypeFilteringLoading={apiInventory.isLoading} accountTypeFilteringFetching={apiInventory.isFetching} retryAccountCapabilities={() => { apiInventory.refreshExplicit(); }} providerIdFilter={apiProviderID} clearProviderFilter={() => { setApiProviderID(""); setApiProviderHint(undefined); setStartApiWizard(undefined); }} setProviderFilter={(providerId, provider) => { setApiProviderID(providerId); setApiProviderHint(provider); }} providers={apiProviders} eligibleProviders={eligibleProviders} providerSearch="" setProviderSearch={() => {}} providerSearchLoading={apiInventory.isFetching} providerSearchError={undefined} providerPicker={providerPicker} subscriptionProviderResources={[]} subscriptionProviderManagement={null} openApiProviders={() => navigate(SettingsCategory.Providers)} manageAccount={setAccount} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} startApiWizard={startApiWizard} providerHint={apiProviderHint} />
            </div> : null}
            {isSubscriptionAccounts ? <div>
              <SubscriptionAccounts active={visible && isSubscriptionAccounts && !hasOverlay} editAccount={(resource) => setEditing({ kind: EntityKind.ACCOUNT, initial: resource, key: newRequestId() })} deleteAccount={setDeleting} />
            </div> : null}

            {isPreferenceCategory ? <><ServerPreferencesWorkspace resources={result.data?.resources} nextPageToken={result.data?.nextPageToken} page={page} fetching={result.isFetching} error={result.error} section={preferenceSection} active={visible} saved={done} authority={pairingAuthority} onNetworkPresentationChange={setNetworkPresentationOpen} /><div hidden={!inventoryActive}><ScrollContinuation showInitial={false} showErrors={false} query={inventory} root={root} active={inventoryActive} label={copy("settings.settingsPages_05ec05")} /></div></> : isApiProviders ? <ApiProviderSettings active={visible && isApiProviders && !hasOverlay} state={providerList} changeState={setProviderList} changed={done} createCustom={(initialData) => setEditing({ kind: EntityKind.PROVIDER, initialData, key: newRequestId() })} editCustom={(initial) => setEditing({ kind: EntityKind.PROVIDER, initial, key: newRequestId() })} manageAccounts={(providerID, entry) => navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.ManageAccounts, providerId: providerID, provider: providerEntrySummary(entry) })} addAccount={(providerID, entry, oauthSupported) => navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.AddAccount, providerId: providerID, provider: providerEntrySummary(entry, oauthSupported) })} deleteCustom={setDeleting} /> : isRunnerDevices ? <><SSHSetup active={visible} /><RunnerDeviceInventory query={inventory} root={root} active={inventoryActive} resources={result.data?.resources} error={result.error} loading={result.isPending && !result.data} page={page} nextPage={result.data?.nextPageToken ?? ""} inspect={inspectMachine} /><div hidden={!inventoryActive}><ScrollContinuation showInitial={false} showErrors={false} query={inventory} root={root} active={inventoryActive} label={copy("settings.settingsPages_05ec05")} /></div></> : hasSpecializedPanel ? null : <>
              {result.isPending && !result.data ? <SettingsLoading label={copy("settings.loading_e6401a", { v0: (isPreferenceCategory ? preferenceLabel : selected.label).toLowerCase() })} /> : null}
              {isServerPreferences ? <NetworkSettings active={visible && isServerPreferences} authority={pairingAuthority} onPresentationChange={setNetworkPresentationOpen} /> : null}
              {isPreferenceCategory && result.isFetching && result.data ? <p role="status">{copy("settings.refreshingServerPreferences_a3769f")}</p> : null}
              <Failure failure={result.error} />
              {result.error && result.data ? <p className="notice" role="status">{copy("settings.refreshFailedShowingTheLastSuccessfully_df6f1e")}</p> : null}
              {isPairedDevices ? <p className="paired-device-explanation">{copy("settings.authorizationDoesNotMeanThisDevice_cfecd2")}</p> : null}
              <div className={selectedCategory === SettingsCategory.Repositories ? "repository-list" : isAgentWorkers && result.data?.resources.length ? "settings-agent-list" : isPairedDevices && result.data?.resources.length ? "paired-device-list" : undefined}>
              <AgentWorkerMetadataProvider active={isAgentWorkers && inventoryActive} refresh={modelRefresh}><ScrollPayloadWindow query={inventory} root={root} active={inventoryActive} identity={paginationIdentity} revision={paginationRevision}>{rows => isProjects ? <ProjectList resources={rows} metadata={projectMetadata.rows} edit={(row) => setEditing({ initial: row, key: newRequestId() })} remove={setDeleting} /> : rows.map((row) => { if (kind === EntityKind.REPOSITORY) return <RepositoryRow key={row.id} row={row} edit={() => setEditing({ initial: row, key: newRequestId() })} remove={() => setDeleting(row)} />; if (isPreferenceCategory) return <ServerPreferencesSummary key={row.id} row={row} section={preferenceSection} />; if (isPairedDevices) return <DeviceRow key={row.id} resource={row} currentDeviceId={currentDeviceId} expanded={expandedDevices.has(row.id)} toggle={() => toggleDevice(row.id)} revoke={() => setDevice(row)} />; if (isAgentWorkers) return <AgentWorkerRow key={row.id} row={row} edit={() => setEditing({ initial: row, key: newRequestId() })} preview={() => setRouting(row)} remove={() => setDeleting(row)} />; const data = document(row); return <article className="result" key={row.id}><h3>{kind === EntityKind.SETTINGS ? preferenceLabel : resourceName(row)}</h3>{text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}{text(data.harness) ? <p><LocalizedText id="settings.harness_db1faa" components={{ s0: <>{text(data.harness)}</> }} /></p> : null}{kind === EntityKind.TEMPLATE ? <pre>{text(data.contents)}</pre> : null}<small>{row.id}</small><div className="actions">{editableKinds.includes(kind) ? <SettingsActionButton icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} targetId={row.id} aria-label={copy("settings.edit_f1be7e", { v0: kind === EntityKind.SETTINGS ? preferenceLabel : resourceName(row) })} disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}><LocalizedText id="settings.edit_4ec92a" components={{ s0: <>{kind === EntityKind.SETTINGS ? preferenceLabel : resourceName(row)}</> }} /></SettingsActionButton> : null}{editableKinds.includes(kind) && kind !== EntityKind.SETTINGS ? <SettingsActionButton icon={SettingsActionIcon.Delete} presentation={SettingsActionPresentation.Icon} targetId={row.id} aria-label={copy("settings.delete_cd822e", { v0: resourceName(row) })} disabled={row.schemaVersion !== 1} onClick={() => setDeleting(row)}><LocalizedText id="settings.delete_a1b98e" components={{ s0: <>{resourceName(row)}</> }} /></SettingsActionButton> : null}{kind === EntityKind.AGENT ? <SettingsActionButton icon={SettingsActionIcon.Inspect} disabled={row.schemaVersion !== 1} onClick={() => setRouting(row)}>{copy("settings.previewRouting_02d4d9")}</SettingsActionButton> : null}{kind === EntityKind.MACHINE ? <SettingsActionButton icon={SettingsActionIcon.Inspect} disabled={row.schemaVersion !== 1} onClick={() => inspectMachine(row)}>{copy("settings.inspectInstalledHarnesses_45e943")}</SettingsActionButton> : null}{kind === EntityKind.ACCOUNT ? <SettingsActionButton icon={SettingsActionIcon.Connect} disabled={row.schemaVersion !== 1} onClick={() => setAccount(row)}>{copy("settings.manageConnection_ad2892")}</SettingsActionButton> : null}</div></article>; })}</ScrollPayloadWindow></AgentWorkerMetadataProvider>


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
                ? successfulEmptyFirstPage ? <section className="project-empty" aria-label={copy("settings.noProjectsYet_f83c80")}><div className="project-empty-icon" aria-hidden="true"><SettingsIcon category={SettingsCategory.Projects} /></div><h2>{copy("settings.noProjectsYet_f83c80")}</h2><p>{copy("settings.groupRepositoriesAndChooseWhichAgent_facff7")}</p><p>{copy("settings.chooseNewProjectToGetStarted_5f012f")}</p></section>
                  : result.data?.resources.length === 0 && !result.error ? <p>{copy("settings.noProjectsOnThisPage_c9b8ee")}</p> : null
                : kind === EntityKind.PROVIDER && page === "" && result.data?.resources.length === 0 && !result.error && !result.data.nextPageToken
                ? <section className="provider-empty" aria-label={copy("settings.noProvidersYet_553273")}><SettingsIcon category={SettingsCategory.Providers} /><h2>{copy("settings.noProvidersYet_553273")}</h2><p>{copy("settings.addAProviderToConfigureYour_e9b95c")}</p></section>
                : result.data?.resources.length === 0 ? <p>{isAgentWorkers ? copy("settings.noAgentWorkersOnThisPage_624e5b") : kind === EntityKind.PROVIDER ? copy("settings.noProvidersOnThisPage_ea9e7e") : copy("settings.noSavedEntries_a59b83")}</p> : null}
              <div hidden={!inventoryActive}><ScrollContinuation showInitial={false} showErrors={false} query={inventory} root={root} active={inventoryActive} label={copy("settings.settingsPages_05ec05")} /></div>
              {isPairedDevices ? <p className="paired-device-guidance">{copy("settings.localWorkerRegistrationIsAvailableIn_09876c")}</p> : null}
            </>}
          </div> : null}
        </div>
        </div>
        </div></SettingsTaskBackground>
      </section>
      {runnerInspection?.body}
      {hasOverlay ? <SettingsTaskDialog key={routing ? `routing:${routing.id}` : editing?.key ?? `${taskTitle}:${taskResource?.id}`} title={taskTitle} subtitle={routing ? copy("routing-preview.scope", { v0: resourceName(routing) }) : undefined} size={taskSize} focus={deleting ? SettingsDialogFocus.Close : device ? SettingsDialogFocus.Cancel : routing || machine || account ? SettingsDialogFocus.Heading : SettingsDialogFocus.Input} close={closeTask}>{device ? <DeviceRevocation initial={device} currentDeviceId={currentDeviceId} active={visible} close={(exit) => { deviceReturnFocus.current = { id: device.id, exit }; setDevice(undefined); void result.refetch(); }} revoked={() => void client.invalidateQueries({ refetchType: "active" })} /> : machine ? <MachineSettings initial={machine} active={visible} authority={pairingAuthority} close={() => { setMachine(undefined); void result.refetch(); }} /> : deleting ? <ConfigurationDeletion initial={deleting} deleted={done} close={() => setDeleting(undefined)} /> : routing ? <RoutingPreview agent={routing} active={visible} close={() => setRouting(undefined)} /> : editing && (editing.kind ?? kind) === EntityKind.REPOSITORY && !editing.initial ? <RepositoryRegistration key={editing.key} active={visible} readLocalWorker={readLocalWorker} controlLocalWorker={controlLocalWorker} chooseFolder={chooseRepositoryFolder} saved={done} cancel={() => setEditing(undefined)} /> : editing && (editing.kind ?? kind) === EntityKind.AGENT ? <AgentWorkerWizard key={editing.key} initial={editing.initial} active={visible} saved={done} cancel={() => setEditing(undefined)} openAccounts={source => source.kind === SourceKind.Api ? navigate(SettingsCategory.ApiAccounts, { kind: SettingsEntryKind.AddAccount, providerId: source.id, startOAuth: false }) : navigate(SettingsCategory.SubscriptionAccounts)} /> : editing ? <ConfigurationEditor registrationAdapters={{ readLocalWorker, controlLocalWorker, chooseFolder: chooseRepositoryFolder }} key={editing.key} kind={editing.kind ?? kind} initial={editing.initial} initialData={editing.initialData} subscriptionOnly={editing.subscriptionOnly} serverPreferenceSection={isPreferenceCategory ? preferenceSection : ServerPreferenceSection.All} active={visible} saved={done} cancel={() => setEditing(undefined)} /> : account ? <AccountConnection initial={account} active={visible} close={() => { setAccount(undefined); void result.refetch(); }} /> : null}</SettingsTaskDialog> : null}
  </SettingsTasks>;
}

function RunnerDeviceInventory({ query, root, active, resources, error, loading, page, nextPage, inspect }: { query: ReturnType<typeof useResourceScrollQuery>; root: import("react").RefObject<HTMLElement | null>; active: boolean; resources?: Resource[]; error: import("@delinoio/delidev-api-client").ClientFailure | undefined; loading: boolean; page: string; nextPage: string; inspect: (row: Resource) => void }) {
  useLocale();
  return <section data-settings-search-target="runner-inventory" className="settings-runner-inventory" aria-label={copy("settings.savedRunnerDevices_9e6092")}>
    <h2>{copy("settings.savedRunnerDevices_9e6092")}</h2>
    {loading ? <><p role="status">{copy("settings.loadingRunnerDevices_a75d73")}</p><div aria-hidden="true" aria-busy="true" className="settings-runner-skeletons">{[0, 1].map((row) => <div aria-hidden="true" className="settings-runner-skeleton-row" key={row}><span /><span /><span /></div>)}</div></> : null}
    <Failure failure={error} />
    {error && resources ? <p role="status">{copy("settings.refreshFailedShowingTheLastSuccessfully_df6f1e")}</p> : null}
    <ScrollPayloadWindow query={query} root={root} active={active} identity={paginationIdentity} revision={paginationRevision}>{rows => rows.map((row) => {
      const data = document(row);
      return <article className="settings-runner-row" key={row.id}>
        <div className="settings-runner-identity"><h3>{resourceName(row)}</h3>{text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}{text(data.harness) ? <p><LocalizedText id="settings.harness_db1faa" components={{ s0: <>{text(data.harness)}</> }} /></p> : null}<small>{row.id}</small></div>
        <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={row.schemaVersion !== 1} onClick={() => inspect(row)}>{copy("settings.inspectInstalledHarnesses_45e943")}</SettingsActionButton></div>
      </article>;
    })}</ScrollPayloadWindow>
    {resources?.length === 0 ? <p>{page || nextPage ? copy("settings.noSavedEntriesOnThisPage_ea1b6b") : copy("settings.noSavedEntries_a59b83")}</p> : null}

  </section>;
}

function accountDraftPreferences(data: Document): Document {
 return Object.fromEntries(["alias", "enabled", "exclude_automatic", "recovery_notifications", "api_protocol"].filter(key => Object.hasOwn(data, key)).map(key => [key, data[key]]));
}
function accountEditingIdentity(data: Document): string {
 return JSON.stringify({ ...accountDraftPreferences(data), provider_id: data.provider_id, type: data.type });
}
