// SPDX-License-Identifier: Apache-2.0
// Browser-only synthetic inventory. Never included in the product entry point.
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { NotificationPreferencesSchema, SituationNotificationPreferencesSchema, NetworkService, BudgetState, SubscriptionService, FailedSubscriptionCleanupState, FailedSubscriptionCleanupOutcome, FailedSubscriptionCleanupReason, AccountService, AccountTypeFilter, BackupCreationState, ConfigurationService, EntityKind, GitHubTokenIdentityState, InboxService, IntegrationService, ProviderInventoryCapability, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, UsageService, UsageAccountingProfile, UsageCostState, newRequestId } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { NotificationLayoutFixture, notificationFixtureCounts, prepareNotificationLayoutFixture } from "./notification-settings-layout.fixture";
import { DateFormatProvider, DateFormatPreference, type DateFormatBridge, type DateFormatSnapshot } from "./date-format";
import { AppearanceProvider, Theme } from "./appearance";
import { LanguagePreference, LanguageProblem, LanguageProvider, type LanguageBridge, type LanguageSnapshot } from "./language";
import { document as resourceDocument, object, text, encode } from "./documents";
import { LocalWorkerState, LocalWorkerManagementState, type LocalWorkerStatus } from "./local-worker-controls";
import { ToastKind, useNotifications } from "./toast-notifications";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";

const args = new URLSearchParams(location.search);
const hiddenWorkerChoices = args.get("hiddenWorkerChoices") === "true";
function WizardFixtureControls() {
 const client = useQueryClient();
 useEffect(() => { const host = window as typeof window & { refreshWizardAccounts?: () => Promise<void> }; host.refreshWizardAccounts = async () => { await client.invalidateQueries({ refetchType: "active" }); }; return () => { delete host.refreshWizardAccounts; }; }, [client]);
 return null;
}

void i18n.changeLanguage(args.get("language") === SupportedLanguage.Korean ? SupportedLanguage.Korean : SupportedLanguage.English);
function ToastFixtureControls() {
  const notifications = useNotifications();
  const messages = { [ToastKind.Success]: "Fixture save completed.", [ToastKind.Info]: `A long informational notification wraps within the card, including this uninterrupted word: ${"longword".repeat(30)}.`, [ToastKind.Warning]: "Review the selected settings before continuing.", [ToastKind.Error]: "The fixture operation failed. Its detailed error remains available." };
  return <div><button>Connection controls</button>{Object.values(ToastKind).map(kind => <button type="button" key={kind} onClick={() => notifications.notify({ id: `fixture-${kind}`, kind, message: messages[kind], durationMs: 0 })}>Show {kind} toast</button>)}</div>;
}
const networkFixture = args.get("networkFixture");
const populated = args.get("populated") === "true";
const routingFixture = args.get("routingFixture");
const githubOnboarding = args.get("github-onboarding") === "true";
const apiFormatEdit = args.get("apiFormatEdit") === "true";
const apiUsage = apiFormatEdit || args.get("apiUsage") === "true";
const longNames = args.get("longNames") === "true";
const projectWizard = args.get("projectWizard") === "true";
const projectRows = args.get("projectRows") === "true";
const projectRowReads = { reads: 0, writes: 0 };
Object.assign(window, { projectRowReads });
const cleanupFixture = args.get("cleanupFixture") === "true";
const accountStorageFixture = args.get("accountStorage") === "true";
const subscriptionDetails = args.get("subscriptionDetails") === "true";
const subscriptionBackground = args.get("subscriptionBackground") === "true" || accountStorageFixture || subscriptionDetails;
const theme = Object.values(Theme).find(value => value === args.get("theme")) ?? Theme.System;
const sessionActionsFixture = args.get("sessionActions") === "true";
const hoverFixture = args.get("sessionHover") === "true" || sessionActionsFixture;
if (hoverFixture) void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const hoverProject = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "oss" }) });
const hoverSessions = hoverFixture ? Array.from({ length: 20 }, (_, index) => {
  const id = newRequestId();
  return create(ResourceSchema, { id, sessionId: id, projectId: hoverProject.id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: index === 0 ? "New session" : index === 1 ? "Complete-session-name-".repeat(12) : `Synthetic session ${index + 1}`, workspace: "worktree", outcome: sessionActionsFixture ? ["not-started", "running", "succeeded", "failed", "stopped", "unknown"][index % 6] : index === 0 ? "not-started" : "running", archive: sessionActionsFixture && index > 5 ? "archived" : "active", name_mode: "automatic", title_state: index === 1 ? "skipped" : "waiting", ...(index === 1 ? { title_reason: "budget-reached" } : {}) }) });
}) : [];
let languageSnapshot: LanguageSnapshot = { revision: 1, language: args.get("language") === "ko" ? LanguagePreference.Korean : LanguagePreference.English, resolved_language: args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English, problem: null };
const languageListeners = new Set<(snapshot: unknown) => void>();
// Synthetic device presentation only: no native storage or server mutation.
const languageBridge: LanguageBridge = {
  read: async () => languageSnapshot,
  update: async (language, revision) => {
    // Keep the synthetic save pending long enough to verify browser focus/locks.
    await new Promise(resolve => setTimeout(resolve, 25));
    if (revision !== languageSnapshot.revision) return { ...languageSnapshot, problem: LanguageProblem.Changed };
    languageSnapshot = { revision: revision + 1, language, resolved_language: language === LanguagePreference.Korean ? SupportedLanguage.Korean : SupportedLanguage.English, problem: null };
    languageListeners.forEach(changed => changed(languageSnapshot));
    return languageSnapshot;
  },
  subscribe: async changed => { languageListeners.add(changed); return () => { languageListeners.delete(changed); }; },
};
// Synthetic device preference: no native storage, account or server effects.
let dateFormatSnapshot: DateFormatSnapshot = { revision: 1, date_format: DateFormatPreference.System, problem: null };
const dateFormatListeners = new Set<(value: unknown) => void>();
const dateFormatBridge: DateFormatBridge = {
  read: async () => dateFormatSnapshot,
  update: async (date_format, revision) => {
    if (revision !== dateFormatSnapshot.revision) throw new Error("Stale fixture preference");
    dateFormatSnapshot = { revision: revision + 1, date_format, problem: null };
    dateFormatListeners.forEach(changed => changed(dateFormatSnapshot));
    return dateFormatSnapshot;
  },
  subscribe: async changed => { dateFormatListeners.add(changed); return () => { dateFormatListeners.delete(changed); }; },
};
const serverId = newRequestId(), currentDeviceId = newRequestId(), machineId = newRequestId();
const automaticWorker = args.get("automaticWorker");
const fixtureWorker: LocalWorkerStatus = {
  machine_id: machineId, generation: newRequestId(),
  state: automaticWorker === "paused" ? LocalWorkerState.Exited : automaticWorker === "blocked" ? LocalWorkerState.Uncertain : automaticWorker === "running" ? LocalWorkerState.Running : LocalWorkerState.Starting,
  controller_active: automaticWorker !== "paused" && automaticWorker !== "blocked",
  ...(automaticWorker ? { management: { state: automaticWorker === "paused" ? LocalWorkerManagementState.Paused : automaticWorker === "blocked" ? LocalWorkerManagementState.Blocked : LocalWorkerManagementState.Running, attempts: 0, retry_ms: 0, owned_by_app: true, ...(automaticWorker === "blocked" ? { failure: "invalid-evidence" } : {}) } } : {}),
};

const subscription = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 1n, documentJson: encode({ alias: "ChatGPT fixture", type: "subscription", subscription_service: "chatgpt", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: subscriptionDetails ? Array.from({length:3}, (_, index) => ({ id: `window-${index}`, state: "observed", remaining: .5, observed_at: new Date().toISOString(), reset_at: new Date(Date.now()+3600000).toISOString() })) : [] }) });
const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, schemaVersion: 1, revision: 1n, documentJson: encode({ name: apiUsage ? "OpenRouter" : "Fixture provider", enabled: true, endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: false }) });
// Opt-in routing fixtures contain display metadata only, never account/native authority.
const routingAccounts = [
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 1n, documentJson: encode({ alias: routingFixture === "compact" ? "Personal" : longNames ? "Complete-account-alias-".repeat(10) : "ChatGPT Personal", type: "subscription", subscription_service: "chatgpt" }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias: routingFixture === "compact" ? "Work" : "Work API", type: "api", provider_id: provider.id }) }),
];
if (apiFormatEdit) {
  const original = resourceDocument(provider);
  provider.schemaVersion = 3;
  provider.documentJson = encode({ ...original, endpoint: "https://openrouter.ai/api/v1", authentication: "bearer", api_formats: ["openai-chat", "openai-responses", "anthropic-messages"].map(protocol => ({ protocol, endpoint: "https://openrouter.ai/api/v1", authentication: "bearer" })) });
}
const model = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Fixture model with a complete long identity", native_id: "example-model-native-".repeat(12), alias: "example-model", provider_id: provider.id, new: true, hidden: false, harnesses: ["codex"] }) });
const extraModels = args.get("manyModels") === "true" ? Array.from({ length: 20 }, (_, index) => create(ResourceSchema, { ...model, id: newRequestId(), documentJson: encode({ ...resourceDocument(model), name: `Fixture model ${index + 2}`, native_id: `example-model-${index + 2}` }) })) : [];
const backup = { id: newRequestId(), revision: 1n, sizeBytes: 9007199254740993n, modifiedAt: "2026-09-29T00:00:00.123Z" };
const creationId = newRequestId();
const records = populated ? [
  ...[EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.PROJECT, EntityKind.REPOSITORY, EntityKind.MACHINE].map(kind => create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision: 1n, documentJson: encode({ name: routingFixture && !longNames && kind === EntityKind.AGENT ? "Luna MAX" : routingFixture && !longNames && kind === EntityKind.PROJECT ? "oss" : `Example ${EntityKind[kind]} ${"long-name-".repeat(15)}`, harness: "codex", contents: "Complete fixture instruction text.\nSecond line retained.", repositories: [], accounts: [], templates: [] }) })),
  model, ...extraModels,
  ...["Personal API", "Team API", "Backup API"].map((alias, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias, type: "api", provider_id: provider.id, enabled: true, health: "unverified", ...(index < 2 && !hiddenWorkerChoices ? { connection: { authentication: "keyless", ...(accountStorageFixture ? { id: newRequestId() } : {}) } } : {}) }) })),
  ...["api", "subscription"].map(type => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias: apiUsage && type === "api" ? longNames ? "OpenRouter-long-identity-".repeat(10) : "OpenRouter" : `Fixture ${type} entry`, type, provider_id: provider.id, connection: apiUsage && type === "api" ? { id: newRequestId() } : "disconnected", health: "unverified", enabled: true, quota: [] }) })),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SETTINGS, schemaVersion: 1, revision: 1n, documentJson: encode({ default_routing: "priority", automatic_fetch: false, notifications: false, remediation: { ci_failure: false, review_feedback: false, merge_conflict: false, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 3 } }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTEGRATION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Fixture GitHub profile", provider: "github.com", token_kind: "fine-grained", resource_owner: "fixture-owner", ...(args.get("repository-pat") === "true" ? { connection: { generation_id: newRequestId() } } : {}) }) }),
  create(ResourceSchema, { id: currentDeviceId, kind: EntityKind.DEVICE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Example desktop", type: "client", paired_at: "2026-10-01T08:00:00Z", revoked: false }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.DEVICE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Example runner", type: "worker", machine_id: machineId, paired_at: "2026-10-01T07:30:00Z", revoked: false }) }),
] : [];
if (apiFormatEdit) {
  const account = records.find(row => row.kind === EntityKind.ACCOUNT && resourceDocument(row).alias === "OpenRouter")!;
  const original = resourceDocument(account);
  account.schemaVersion = 3;
  account.documentJson = encode({ ...original, api_protocol: "openai-chat", exclude_automatic: false, recovery_notifications: true, connection: { id: newRequestId(), authentication: "bearer" } });
}
// Opt-in Projects metadata fixture uses configured source bytes only.
if (args.get("projectList") === "true") {
  const repositories = Array.from({ length: 5 }, (_, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Configured repository ${index + 1} ${"complete-name-".repeat(10)}`, ...(index === 1 ? {} : { remote_url: index === 4 ? "git@example.org:team/complete-source.git" : `https://example.org/team/${"complete-source-".repeat(14)}${index}.git` }) }) }));
  records.push(...repositories);
  const project = records.find(row => row.kind === EntityKind.PROJECT)!;
  project.documentJson = encode({ ...resourceDocument(project), repositories: repositories.map(row => row.id), primary_repository: repositories[4]!.id });
}
if (projectRows) {
  for (let index = records.length - 1; index >= 0; index--) if (records[index]!.kind === EntityKind.PROJECT) records.splice(index, 1);
  const add = (name: string, names: string[]) => {
    const repositories = names.map(name => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n, documentJson: encode({ name, remote_url: name === "oss" ? "https://github.com/delinoio/oss.git" : `https://example.org/team/${"complete-source-".repeat(14)}repository.git` }) }));
    records.push(...repositories, create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name, repositories: repositories.map(row => row.id), primary_repository: repositories[Math.min(3, repositories.length - 1)]?.id }) }));
  };
  add("oss", ["oss"]);
  add("Different project", ["Different repository"]);
  add("Empty project", []);
  add("Multi project", ["web", "api", "worker", "primary fourth", "final repository"]);
}
if (projectWizard) records.push(...["oss", "delidev"].map(name => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n, documentJson: encode({ name }) })));
if (networkFixture === "populated") records.push(create(ResourceSchema, { id: newRequestId(), kind: EntityKind.NETWORK_PROFILE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name: "Complete-proxy-profile-name-".repeat(10), mode: "http", host: "proxy.example", port: 3128, credential_generation: newRequestId() }) }));
const networkReads = { route: 0, profiles: 0, writes: 0 };
// Read counters are synthetic fixture metadata, never product instrumentation.
Object.assign(window, { networkFixtureReads: networkReads });
const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
if (apiFormatEdit) capabilities.push(ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1, ProviderInventoryCapability.ACCOUNT_API_FORMAT_CHANGE_V1);
const notificationLayout = args.get("notificationLayout") === "true";
if (notificationLayout) prepareNotificationLayoutFixture(args);
const notificationTerminals=notificationLayout && args.get("terminals") !== "false";
let notificationPreferences = create(NotificationPreferencesSchema,{ revision: 1n, interactions: true, terminals: notificationTerminals, situations:notificationLayout?create(SituationNotificationPreferencesSchema,{questions:true,approvals:true,succeeded:notificationTerminals,failed:notificationTerminals,stopped:notificationTerminals,serverLost:true,workerUnavailable:true,quotaExhausted:true,scheduleStartFailed:true,scheduleOffline:true}):undefined });
// Read-only synthetic refresh gate; never included in the product entry.
const providerRefresh = args.get("providerRefresh") === "true";
let holdProviders = false;
const providerReplies: Array<() => void> = [];
if (providerRefresh) Object.assign(window, {
  fixtureHoldProviders: () => { holdProviders = true; },
  fixtureReleaseProviders: () => { holdProviders = false; providerReplies.splice(0).forEach(reply => reply()); document.documentElement.dataset.providerRefreshPending = "false"; },
});
// Opt-in read-only routing response gate for browser alignment checks.
let holdRouting = false;
const routingReplies: Array<() => void> = [];
if (args.get("routingHold") === "true") Object.assign(window, {
  fixtureHoldRouting: () => { holdRouting = true; },
  fixtureReleaseRouting: () => { holdRouting = false; routingReplies.splice(0).forEach(reply => reply()); document.documentElement.dataset.routingPending = "false"; },
});
const fixtureTransport = createRouterTransport(router => {
  router.service(IntegrationService, { listGitHubRepositories: request => {
    const profile = records.find(row => row.id === request.profileId)!;
    const repositories = request.page === 1 ? [
      { repository: { provider: "github.com", id: "123", node_id: "R_fixture_123", owner: "example", name: "desktop", private: true }, archived: false, https_url: "https://github.com/example/desktop.git", ssh_url: "git@github.com:example/desktop.git" },
      { repository: { provider: "github.com", id: "124", node_id: "R_fixture_124", owner: "example", name: "repository-with-a-long-name-".repeat(3), private: false }, archived: true, https_url: `https://github.com/example/${"repository-with-a-long-name-".repeat(3)}.git`, ssh_url: `git@github.com:example/${"repository-with-a-long-name-".repeat(3)}.git` },
    ] : [];
    return { schemaVersion: 1, documentJson: encode({ profile_id: profile.id, profile_revision: String(request.expectedRevision), generation_id: resourceDocument(profile).connection && (resourceDocument(profile).connection as { generation_id: string }).generation_id, observed_at: "2026-10-07T00:00:00Z", page: request.page, page_size: 50, next_page: request.page === 1 ? 2 : 0, repositories }) };
  } });
  if (networkFixture) router.service(NetworkService, { getNetworkRoute: () => { networkReads.route++; return {}; }, selectNetworkProfile: request => { networkReads.writes++; return { resource: create(ResourceSchema, { kind: EntityKind.NETWORK_ROUTE, id: newRequestId(), revision: 9007199254740994n, schemaVersion: 1, documentJson: encode({ profile_id: request.profileId, profile: { name: "Fixture selected route", mode: "http" } }) }) }; } });
  router.service(UsageService, { getUsageSummary: () => ({ fromUnixMs: 1788642000000n, untilUnixMs: 1791234000000n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals: { responses: 126, total: { knownTotal: "1284920", measuredResponses: 126 } }, estimatedCost: UsageCostState.KNOWN_SUBTOTAL, estimates: { currencies: [{ currency: "USD", knownAmount: "12.48", completeResponses: 126 }] } }) });
  router.service(ResourceService, { listResources: request => { if (projectRows) projectRowReads.reads++; if (subscriptionDetails && request.filter?.kind === EntityKind.ACCOUNT) document.documentElement.dataset.subscriptionDetailLists = String(Number(document.documentElement.dataset.subscriptionDetailLists ?? "0") + 1); if (request.filter?.kind === EntityKind.NETWORK_PROFILE) networkReads.profiles++; return { resources: hoverFixture && request.filter?.kind === EntityKind.PROJECT ? [hoverProject] : subscriptionBackground && request.filter?.kind === EntityKind.ACCOUNT && request.accountType === AccountTypeFilter.SUBSCRIPTION ? [subscription] : records.filter(row => row.kind === request.filter?.kind && (row.kind !== EntityKind.ACCOUNT || resourceDocument(row).type === (request.accountType === AccountTypeFilter.API ? "api" : "subscription"))) }; }, getResource: request => { if (projectRows) projectRowReads.reads++; if (subscriptionDetails) document.documentElement.dataset.subscriptionDetailReads = String(Number(document.documentElement.dataset.subscriptionDetailReads ?? "0") + 1); return ({ resource: [provider, ...(routingFixture ? routingAccounts : []), ...(subscriptionBackground ? [subscription] : []), ...hoverSessions, ...records].find(row => row.id === request.id) }); } });
  router.service(ProviderService, { listProviderInventory: request => { const response = { entries: [{ provider, providerId: provider.id, presetId: ProviderPresetId.OLLAMA, displayName: apiUsage ? "OpenRouter" : "Fixture provider", enabled: true, accountCountsAvailable: true, totalAccounts: 0n, connectedAccounts: 0n }], capabilities }; if (providerRefresh && holdProviders && !request.enabledOnly) return new Promise(resolve => { providerReplies.push(() => resolve(response)); document.documentElement.dataset.providerRefreshPending = "true"; }); return response; }, listProviderPresets: () => ({ presetsJson: encode([]) }), searchModels: () => ({ models: populated ? [model, ...extraModels] : [], providers: [provider] }) });
  router.service(SessionService, { listSessions: () => ({ sessions: hoverSessions }), getSessionBudget: request => ({ view: { session: hoverSessions.find(row => row.id === request.sessionId), state: BudgetState.ALLOW_INCOMPLETE } }) });
  router.service(InboxService, { getNotificationPreferences: () => { notificationFixtureCounts.preferenceReads++; return { preferences: notificationPreferences }; }, setNotificationPreferences: request => { notificationFixtureCounts.preferenceWrites++; notificationPreferences = create(NotificationPreferencesSchema,{...request.preferences,revision:notificationPreferences.revision+1n}); return { preferences: notificationPreferences }; }, listInbox: () => ({ entries: [] }) });
  let cleanupRead = 0;
  const cleanupId = newRequestId(), retainedCleanupId = newRequestId();
  router.service(SubscriptionService, {
    cleanupFailedSubscriptions: request => { cleanupRead = 0; return { requestId: request.requestId, job: { id: cleanupId, revision: 1n, state: FailedSubscriptionCleanupState.PENDING, total: 5 } }; },
    getFailedSubscriptionCleanup: () => ++cleanupRead < 2 ? { job: { id: cleanupId, revision: 1n, state: FailedSubscriptionCleanupState.PENDING, total: 5 } } : {
      job: { id: cleanupId, revision: 2n, state: FailedSubscriptionCleanupState.COMPLETED, total: 5, processed: 5, deleted: 3, retained: 2 },
      results: [{ accountId: subscription.id, alias: "Retained fixture subscription", outcome: FailedSubscriptionCleanupOutcome.RETAINED, reason: FailedSubscriptionCleanupReason.REFERENCED }, { accountId: retainedCleanupId, alias: "Other retained fixture subscription", outcome: FailedSubscriptionCleanupOutcome.RETAINED, reason: FailedSubscriptionCleanupReason.CLEANUP_UNCONFIRMED }],
    },
  });
  router.service(AccountService, { getAccountStatus: () => ({}) });
  router.service(ConfigurationService, { previewRouting: async () => {
    if (holdRouting) await new Promise<void>(resolve => { routingReplies.push(resolve); document.documentElement.dataset.routingPending = "true"; });
    if (!routingFixture) return { routeJson: encode({ policy: "fixed", selected: "", candidates: [] }) };
    document.documentElement.dataset.fixtureRoutingReads = String(Number(document.documentElement.dataset.fixtureRoutingReads ?? "0") + 1);
    if (routingFixture === "denied") throw new ConnectError("Synthetic routing denial", Code.PermissionDenied);
    if (routingFixture === "invalid") return { routeJson: encode({}) };
    const candidate = (account: typeof subscription, eligibility: string) => ({ id: account.id, weight: 1, eligibility, quota_state: "unknown" });
    const first = { policy: "priority", selected: "", candidates: [candidate(routingAccounts[0], "unauthenticated")] };
    const selected = { policy: "remaining-quota", selected: routingAccounts[1].id, fallback: true, candidates: [{ ...candidate(routingAccounts[1], "eligible"), score: 0.75, reset_at: "2026-10-07T00:00:00Z" }] };
    const compact = { policy: "priority", selected: routingAccounts[0].id, candidates: [{ ...candidate(routingAccounts[0], "eligible"), quota_state: "stale", score: 0, reset_at: "2026-10-07T00:00:00Z" }] };
    if (routingFixture === "compact") return { routeJson: encode({ ...compact, source_index: 0, sources: [{ source: "subscription:chatgpt", model_id: model.id, native_model: "gpt-6", route: compact }] }) };
    return { routeJson: encode(routingFixture === "empty" ? { ...first, candidates: [] } : routingFixture === "selected" ? selected : routingFixture === "sources" ? { ...selected, source_index: 1, sources: [{ source: "subscription:chatgpt", model_id: model.id, native_model: "Fixture subscription model", route: first, problem: { code: "missing_input", message: "Synthetic account state" } }, { source: `api:${provider.id}`, model_id: model.id, native_model: "Fixture API model", route: selected }] } : first) };
  }, saveConfiguration: projectWizard || projectRows ? request => {
    if (args.get("projectRegistration") === "true" && request.kind === EntityKind.REPOSITORY) {
      const repository = create(ResourceSchema, { id: newRequestId(), kind: request.kind, schemaVersion: 1, revision: 1n, documentJson: request.documentJson }); records.push(repository);
      const job = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ state: "succeeded", output: { id: repository.id, revision: 1 } }) }); records.push(job);
      document.documentElement.dataset.fixtureRegistrationCount = String(Number(document.documentElement.dataset.fixtureRegistrationCount ?? "0") + 1); return { job };
    }
    if (projectRows) projectRowReads.writes++;
    // Synthetic acceptance makes accidental writes during Next observable.
    document.documentElement.dataset.fixtureProjectSaveCount = String(Number(document.documentElement.dataset.fixtureProjectSaveCount ?? "0") + 1);
    return { resource: create(ResourceSchema, { id: newRequestId(), kind: request.kind, schemaVersion: request.schemaVersion, revision: 1n, documentJson: request.documentJson }) };
  } : undefined });
  if (githubOnboarding) router.service(IntegrationService, { inspectGitHubToken: request => ({ requestId: request.requestId, state: GitHubTokenIdentityState.VERIFIED, identity: { id: "17", nodeId: "U_17", login: "fixture-user" } }) });
  router.service(SystemService, { getStatus: () => ({ serverId, protocolVersion: 1, capabilities: [...(networkFixture ? [SystemCapability.SERVER_OUTBOUND_PROXY_V1, SystemCapability.WORKER_NETWORK_BOOTSTRAP_V1] : []), ...(subscriptionBackground ? [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1, ...(cleanupFixture ? [SystemCapability.FAILED_SUBSCRIPTION_CLEANUP_V1] : [])] : []), SystemCapability.AGENT_WORKER_WIZARD_V1, ...(args.get("sourceRoutes") === "true" ? [SystemCapability.AGENT_WORKER_SOURCE_ROUTES_V1] : []), SystemCapability.REMOTE_REPOSITORIES_V1, ...(githubOnboarding ? [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] : []), ...(args.get("repository-pat") === "true" ? [SystemCapability.REPOSITORY_CLONE_V1, SystemCapability.GITHUB_REPOSITORY_PICKER_V1] : [])] }), listBackups: () => ({ backups: populated ? [backup] : [] }), inspectBackup: () => ({ backup, sha256: "a".repeat(64), schemaVersion: 30, serverId }), listBackupCreations: () => ({ jobs: populated ? [{ id: creationId, backupId: backup.id, revision: 1n, state: BackupCreationState.SUCCEEDED }] : [] }), listBackupDeletions: () => ({ jobs: [] }), getDoctor: () => { document.documentElement.dataset.fixtureDoctorReads = String(Number(document.documentElement.dataset.fixtureDoctorReads ?? "0") + 1); return { reportJson: encode({ schema_version: 2, server_id: serverId, version: "0.1.0", os: "darwin", architecture: "arm64", protocol_version: 1, database_schema_version: 24, listener: "http://127.0.0.1:46310", observed_at: "2026-10-01T08:00:00.000Z", database: "ready", credential_store: "owner-credential-ready", inference_probes: false, storage: { result: { state: "observed" }, database_bytes: "9007199254740993", wal_bytes: "391432", logical_database_bytes: "561152", volume_capacity_bytes: "18446744073709551615", volume_available_bytes: "950436651008", resources: [] }, machines: [], credentials: accountStorageFixture ? [subscription, ...records.filter(row => row.kind === EntityKind.ACCOUNT && resourceDocument(row).type === "api")].map(row => {
    const data = resourceDocument(row), connectionId = text(object(data.connection).id);
    return { account_id: row.id, connection_id: connectionId || undefined, result: data.type === "subscription" ? { state: "unavailable", code: "unsupported" } : text(data.alias).startsWith("OpenRouter") ? { state: "failed", code: "permission_denied", guidance: `Synthetic protected-store permissions guidance. ${"Original-reference-".repeat(30)}` } : connectionId ? { state: "not-applicable" } : { state: "unconfigured" } };
  }) : [], more_machines: false, more_credentials: false }) }; } });
});
const transport = fixtureTransport;
// Isolated command-palette fixture counters observe ordinary destinations only.
if (args.get("commandPalette") === "true") {
 const counts = { calls: 0, writes: 0 };
 const unary = transport.unary;
 transport.unary = (...parameters) => { counts.calls++; if (/^(create|save|update|submit|confirm)/i.test(parameters[0].name)) counts.writes++; return unary(...parameters); };
 Object.assign(window, { __commandMenuFixture: counts });
}
createRoot(document.getElementById("root")!).render(<LanguageProvider bridge={languageBridge}><AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><DateFormatProvider bridge={dateFormatBridge}>{notificationLayout ? <NotificationLayoutFixture transport={transport} /> : <App localServer={<WizardFixtureControls />} transport={transport} pairingAuthority={networkFixture ? { endpoint: "https://fixture.example", serverId } : undefined} currentDeviceId={currentDeviceId} connectionSettings={args.get("toast-controls") === "true" ? <ToastFixtureControls /> : <button>Connection controls</button>} controlLocalWorker={Object.assign(async () => fixtureWorker, { automatic: Boolean(automaticWorker) })} />}</DateFormatProvider></AppearanceProvider></LanguageProvider>);
