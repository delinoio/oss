// SPDX-License-Identifier: Apache-2.0
// Browser-only synthetic inventory. Never included in the product entry point.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { AccountService, AccountTypeFilter, BackupCreationState, ConfigurationService, EntityKind, GitHubTokenIdentityState, InboxService, IntegrationService, ProviderInventoryCapability, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, UsageService, UsageAccountingProfile, UsageCostState, newRequestId } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { AppearanceProvider, Theme } from "./appearance";
import { LanguagePreference, LanguageProblem, LanguageProvider, type LanguageBridge, type LanguageSnapshot } from "./language";
import { document as resourceDocument, encode } from "./documents";
import { LocalWorkerState, LocalWorkerManagementState, type LocalWorkerStatus } from "./local-worker-controls";
import { ToastKind, useNotifications } from "./toast-notifications";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";

const args = new URLSearchParams(location.search);
const hiddenWorkerChoices = args.get("hiddenWorkerChoices") === "true";
void i18n.changeLanguage(args.get("language") === SupportedLanguage.Korean ? SupportedLanguage.Korean : SupportedLanguage.English);
function ToastFixtureControls() {
  const notifications = useNotifications();
  const messages = { [ToastKind.Success]: "Fixture save completed.", [ToastKind.Info]: `A long informational notification wraps within the card, including this uninterrupted word: ${"longword".repeat(30)}.`, [ToastKind.Warning]: "Review the selected settings before continuing.", [ToastKind.Error]: "The fixture operation failed. Its detailed error remains available." };
  return <div><button>Connection controls</button>{Object.values(ToastKind).map(kind => <button type="button" key={kind} onClick={() => notifications.notify({ id: `fixture-${kind}`, kind, message: messages[kind], durationMs: 0 })}>Show {kind} toast</button>)}</div>;
}
const populated = args.get("populated") === "true";
const githubOnboarding = args.get("github-onboarding") === "true";
const apiUsage = args.get("apiUsage") === "true";
const longNames = args.get("longNames") === "true";
const subscriptionBackground = args.get("subscriptionBackground") === "true";
const theme = Object.values(Theme).find(value => value === args.get("theme")) ?? Theme.System;
const hoverFixture = args.get("sessionHover") === "true";
if (hoverFixture) void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const hoverProject = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "oss" }) });
const hoverSessions = hoverFixture ? Array.from({ length: 20 }, (_, index) => {
  const id = newRequestId();
  return create(ResourceSchema, { id, sessionId: id, projectId: hoverProject.id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: index === 0 ? "New session" : index === 1 ? "Complete-session-name-".repeat(12) : `Synthetic session ${index + 1}`, workspace: "worktree", outcome: index === 0 ? "not-started" : "running", archive: "active", name_mode: "automatic", title_state: index === 1 ? "skipped" : "waiting", ...(index === 1 ? { title_reason: "budget-reached" } : {}) }) });
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
const serverId = newRequestId(), currentDeviceId = newRequestId(), machineId = newRequestId();
const automaticWorker = args.get("automaticWorker");
const fixtureWorker: LocalWorkerStatus = {
  machine_id: machineId, generation: newRequestId(),
  state: automaticWorker === "paused" ? LocalWorkerState.Exited : automaticWorker === "blocked" ? LocalWorkerState.Uncertain : automaticWorker === "running" ? LocalWorkerState.Running : LocalWorkerState.Starting,
  controller_active: automaticWorker !== "paused" && automaticWorker !== "blocked",
  ...(automaticWorker ? { management: { state: automaticWorker === "paused" ? LocalWorkerManagementState.Paused : automaticWorker === "blocked" ? LocalWorkerManagementState.Blocked : LocalWorkerManagementState.Running, attempts: 0, retry_ms: 0, owned_by_app: true, ...(automaticWorker === "blocked" ? { failure: "invalid-evidence" } : {}) } } : {}),
};

const subscription = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 1n, documentJson: encode({ alias: "ChatGPT fixture", type: "subscription", subscription_service: "chatgpt", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [] }) });
const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, schemaVersion: 1, revision: 1n, documentJson: encode({ name: apiUsage ? "OpenRouter" : "Fixture provider", enabled: true, endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: false }) });
const model = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Fixture model with a complete long identity", native_id: "example-model-native-".repeat(12), alias: "example-model", provider_id: provider.id, new: true, hidden: false, harnesses: ["codex"] }) });
const extraModels = args.get("manyModels") === "true" ? Array.from({ length: 20 }, (_, index) => create(ResourceSchema, { ...model, id: newRequestId(), documentJson: encode({ ...resourceDocument(model), name: `Fixture model ${index + 2}`, native_id: `example-model-${index + 2}` }) })) : [];
const backup = { id: newRequestId(), revision: 1n, sizeBytes: 9007199254740993n, modifiedAt: "2026-09-29T00:00:00.123Z" };
const creationId = newRequestId();
const records = populated ? [
  ...[EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.PROJECT, EntityKind.REPOSITORY, EntityKind.MACHINE].map(kind => create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Example ${EntityKind[kind]} ${"long-name-".repeat(15)}`, harness: "codex", contents: "Complete fixture instruction text.\nSecond line retained.", repositories: [], accounts: [], templates: [] }) })),
  model, ...extraModels,
  ...["Personal API", "Team API", "Backup API"].map((alias, index) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias, type: "api", provider_id: provider.id, enabled: true, health: "unverified", ...(index < 2 && !hiddenWorkerChoices ? { connection: { authentication: "keyless" } } : {}) }) })),
  ...["api", "subscription"].map(type => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias: apiUsage && type === "api" ? longNames ? "OpenRouter-long-identity-".repeat(10) : "OpenRouter" : `Fixture ${type} entry`, type, provider_id: provider.id, connection: apiUsage && type === "api" ? { id: newRequestId() } : "disconnected", health: "unverified", enabled: true, quota: [] }) })),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SETTINGS, schemaVersion: 1, revision: 1n, documentJson: encode({ default_routing: "priority", automatic_fetch: false, notifications: false, remediation: { ci_failure: false, review_feedback: false, merge_conflict: false, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 3 } }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTEGRATION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Fixture GitHub profile", provider: "github.com", token_kind: "fine-grained", resource_owner: "fixture-owner", ...(args.get("repository-pat") === "true" ? { connection: { generation_id: newRequestId() } } : {}) }) }),
  create(ResourceSchema, { id: currentDeviceId, kind: EntityKind.DEVICE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Example desktop", type: "client", paired_at: "2026-10-01T08:00:00Z", revoked: false }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.DEVICE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Example runner", type: "worker", machine_id: machineId, paired_at: "2026-10-01T07:30:00Z", revoked: false }) }),
] : [];
const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
let notificationPreferences = { revision: 1n, interactions: true, terminals: false };
const fixtureTransport = createRouterTransport(router => {
  router.service(IntegrationService, { listGitHubRepositories: request => {
    const profile = records.find(row => row.id === request.profileId)!;
    const repositories = request.page === 1 ? [
      { repository: { provider: "github.com", id: "123", node_id: "R_fixture_123", owner: "example", name: "desktop", private: true }, archived: false, https_url: "https://github.com/example/desktop.git", ssh_url: "git@github.com:example/desktop.git" },
      { repository: { provider: "github.com", id: "124", node_id: "R_fixture_124", owner: "example", name: "repository-with-a-long-name-".repeat(3), private: false }, archived: true, https_url: `https://github.com/example/${"repository-with-a-long-name-".repeat(3)}.git`, ssh_url: `git@github.com:example/${"repository-with-a-long-name-".repeat(3)}.git` },
    ] : [];
    return { schemaVersion: 1, documentJson: encode({ profile_id: profile.id, profile_revision: String(request.expectedRevision), generation_id: resourceDocument(profile).connection && (resourceDocument(profile).connection as { generation_id: string }).generation_id, observed_at: "2026-10-07T00:00:00Z", page: request.page, page_size: 50, next_page: request.page === 1 ? 2 : 0, repositories }) };
  } });
  router.service(UsageService, { getUsageSummary: () => ({ fromUnixMs: 1788642000000n, untilUnixMs: 1791234000000n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals: { responses: 126, total: { knownTotal: "1284920", measuredResponses: 126 } }, estimatedCost: UsageCostState.KNOWN_SUBTOTAL, estimates: { currencies: [{ currency: "USD", knownAmount: "12.48", completeResponses: 126 }] } }) });
  router.service(ResourceService, { listResources: request => { return { resources: hoverFixture && request.filter?.kind === EntityKind.PROJECT ? [hoverProject] : subscriptionBackground && request.filter?.kind === EntityKind.ACCOUNT && request.accountType === AccountTypeFilter.SUBSCRIPTION ? [subscription] : records.filter(row => row.kind === request.filter?.kind && (row.kind !== EntityKind.ACCOUNT || resourceDocument(row).type === (request.accountType === AccountTypeFilter.API ? "api" : "subscription"))) }; }, getResource: request => ({ resource: [provider, ...(subscriptionBackground ? [subscription] : []), ...hoverSessions, ...records].find(row => row.id === request.id) }) });
  router.service(ProviderService, { listProviderInventory: () => ({ entries: [{ provider, providerId: provider.id, presetId: ProviderPresetId.OLLAMA, displayName: apiUsage ? "OpenRouter" : "Fixture provider", enabled: true, accountCountsAvailable: true, totalAccounts: 0n, connectedAccounts: 0n }], capabilities }), listProviderPresets: () => ({ presetsJson: encode([]) }), searchModels: () => ({ models: populated ? [model, ...extraModels] : [], providers: [provider] }) });
  router.service(SessionService, { listSessions: () => ({ sessions: hoverSessions }) });
  router.service(InboxService, { getNotificationPreferences: () => ({ preferences: notificationPreferences }), setNotificationPreferences: request => { notificationPreferences = { revision: notificationPreferences.revision + 1n, interactions: request.preferences!.interactions, terminals: request.preferences!.terminals }; return { preferences: notificationPreferences }; }, listInbox: () => ({ entries: [] }) });
  router.service(AccountService, { getAccountStatus: () => ({}) });
  if (githubOnboarding) router.service(IntegrationService, { inspectGitHubToken: request => ({ requestId: request.requestId, state: GitHubTokenIdentityState.VERIFIED, identity: { id: "17", nodeId: "U_17", login: "fixture-user" } }) });
  router.service(ConfigurationService, { previewRouting: () => ({ routeJson: encode({ policy: "fixed", selected: "", candidates: [] }) }) });
  router.service(SystemService, { getStatus: () => ({ serverId, protocolVersion: 1, capabilities: [...(subscriptionBackground ? [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] : []), SystemCapability.AGENT_WORKER_WIZARD_V1, SystemCapability.REMOTE_REPOSITORIES_V1, ...(githubOnboarding ? [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1] : []), ...(args.get("repository-pat") === "true" ? [SystemCapability.REPOSITORY_CLONE_V1, SystemCapability.GITHUB_REPOSITORY_PICKER_V1] : [])] }), listBackups: () => ({ backups: populated ? [backup] : [] }), inspectBackup: () => ({ backup, sha256: "a".repeat(64), schemaVersion: 30, serverId }), listBackupCreations: () => ({ jobs: populated ? [{ id: creationId, backupId: backup.id, revision: 1n, state: BackupCreationState.SUCCEEDED }] : [] }), listBackupDeletions: () => ({ jobs: [] }), getDoctor: () => ({ reportJson: encode({ schema_version: 2, server_id: serverId, version: "0.1.0", os: "darwin", architecture: "arm64", protocol_version: 1, database_schema_version: 24, listener: "http://127.0.0.1:46310", observed_at: "2026-10-01T08:00:00.000Z", database: "ready", credential_store: "owner-credential-ready", inference_probes: false, storage: { result: { state: "observed" }, database_bytes: "9007199254740993", wal_bytes: "391432", logical_database_bytes: "561152", volume_capacity_bytes: "18446744073709551615", volume_available_bytes: "950436651008", resources: [] }, machines: [], credentials: [], more_machines: false, more_credentials: false }) }) });
});
const transport = fixtureTransport;
createRoot(document.getElementById("root")!).render(<LanguageProvider bridge={languageBridge}><AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><App transport={transport} currentDeviceId={currentDeviceId} connectionSettings={args.get("toast-controls") === "true" ? <ToastFixtureControls /> : <button>Connection controls</button>} controlLocalWorker={Object.assign(async () => fixtureWorker, { automatic: Boolean(automaticWorker) })} /></AppearanceProvider></LanguageProvider>);
