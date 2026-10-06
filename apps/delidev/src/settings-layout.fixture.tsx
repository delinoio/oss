// SPDX-License-Identifier: Apache-2.0
// Browser-only synthetic inventory. Never included in the product entry point.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { AccountService, AccountTypeFilter, BackupCreationState, ConfigurationService, EntityKind, InboxService, ProviderInventoryCapability, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, SessionService, SystemService, UsageService, UsageAccountingProfile, UsageCostState, newRequestId } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { AppearanceProvider, Theme } from "./appearance";
import { document as resourceDocument, encode } from "./documents";
import { LocalWorkerState } from "./local-worker-controls";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";

const args = new URLSearchParams(location.search);
const populated = args.get("populated") === "true";
const apiUsage = args.get("apiUsage") === "true";
const longNames = args.get("longNames") === "true";
const theme = Object.values(Theme).find(value => value === args.get("theme")) ?? Theme.System;
const serverId = newRequestId(), currentDeviceId = newRequestId(), machineId = newRequestId();
const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, schemaVersion: 1, revision: 1n, documentJson: encode({ name: apiUsage ? "OpenRouter" : "Fixture provider", enabled: true, endpoint: "http://127.0.0.1:11434/v1", protocol: "openai-chat", authentication: "keyless", discovery: false }) });
const model = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MODEL, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Fixture model with a complete long identity", native_id: "example-model-native-".repeat(12), alias: "example-model", provider_id: provider.id, new: true, hidden: false, harnesses: ["codex"] }) });
const backup = { id: newRequestId(), revision: 1n, sizeBytes: 9007199254740993n, modifiedAt: "2026-09-29T00:00:00.123Z" };
const creationId = newRequestId();
const records = populated ? [
  ...[EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.PROJECT, EntityKind.REPOSITORY, EntityKind.MACHINE].map(kind => create(ResourceSchema, { id: newRequestId(), kind, schemaVersion: 1, revision: 1n, documentJson: encode({ name: `Example ${EntityKind[kind]} ${"long-name-".repeat(15)}`, harness: "codex", contents: "Complete fixture instruction text.\nSecond line retained.", repositories: [], accounts: [], templates: [] }) })),
  model,
  ...["api", "subscription"].map(type => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 1n, documentJson: encode({ alias: apiUsage && type === "api" ? longNames ? "OpenRouter-long-identity-".repeat(10) : "OpenRouter" : `Fixture ${type} entry`, type, provider_id: provider.id, connection: apiUsage && type === "api" ? { id: newRequestId() } : "disconnected", health: "unverified", enabled: true, quota: [] }) })),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SETTINGS, schemaVersion: 1, revision: 1n, documentJson: encode({ default_routing: "priority", automatic_fetch: false, notifications: false, remediation: { ci_failure: false, review_feedback: false, merge_conflict: false, conflict_strategy: "rebase", session_strategy: "dedicated", attempt_limit: 3 } }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTEGRATION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Fixture GitHub profile", provider: "github.com", token_kind: "fine-grained", resource_owner: "fixture-owner" }) }),
  create(ResourceSchema, { id: currentDeviceId, kind: EntityKind.DEVICE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Example desktop", type: "client", paired_at: "2026-10-01T08:00:00Z", revoked: false }) }),
  create(ResourceSchema, { id: newRequestId(), kind: EntityKind.DEVICE, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Example runner", type: "worker", machine_id: machineId, paired_at: "2026-10-01T07:30:00Z", revoked: false }) }),
] : [];
const capabilities = [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER];
const fixtureTransport = createRouterTransport(router => {
  router.service(UsageService, { getUsageSummary: () => ({ fromUnixMs: 1788642000000n, untilUnixMs: 1791234000000n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals: { responses: 126, total: { knownTotal: "1284920", measuredResponses: 126 } }, estimatedCost: UsageCostState.KNOWN_SUBTOTAL, estimates: { currencies: [{ currency: "USD", knownAmount: "12.48", completeResponses: 126 }] } }) });
  router.service(ResourceService, { listResources: request => { return { resources: records.filter(row => row.kind === request.filter?.kind && (row.kind !== EntityKind.ACCOUNT || resourceDocument(row).type === (request.accountType === AccountTypeFilter.API ? "api" : "subscription"))) }; }, getResource: request => ({ resource: [provider, ...records].find(row => row.id === request.id) }) });
  router.service(ProviderService, { listProviderInventory: () => ({ entries: [{ provider, providerId: provider.id, presetId: ProviderPresetId.OLLAMA, displayName: apiUsage ? "OpenRouter" : "Fixture provider", enabled: true, accountCountsAvailable: true, totalAccounts: 0n, connectedAccounts: 0n }], capabilities }), listProviderPresets: () => ({ presetsJson: encode([]) }), searchModels: () => ({ models: populated ? [model] : [], providers: [provider] }) });
  router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
  router.service(InboxService, { getNotificationPreferences: () => ({ preferences: { revision: 1n, interactions: true, terminals: false } }), listInbox: () => ({ entries: [] }) });
  router.service(AccountService, { getAccountStatus: () => ({}) });
  router.service(ConfigurationService, { previewRouting: () => ({ routeJson: encode({ policy: "fixed", selected: "", candidates: [] }) }) });
  router.service(SystemService, { getStatus: () => ({ serverId, protocolVersion: 1 }), listBackups: () => ({ backups: populated ? [backup] : [] }), listBackupCreations: () => ({ jobs: populated ? [{ id: creationId, backupId: backup.id, revision: 1n, state: BackupCreationState.SUCCEEDED }] : [] }), listBackupDeletions: () => ({ jobs: [] }), getDoctor: () => ({ reportJson: encode({ schema_version: 2, server_id: serverId, version: "0.1.0", os: "darwin", architecture: "arm64", protocol_version: 1, database_schema_version: 24, listener: "http://127.0.0.1:46310", observed_at: "2026-10-01T08:00:00.000Z", database: "ready", credential_store: "owner-credential-ready", inference_probes: false, storage: { result: { state: "observed" }, database_bytes: "9007199254740993", wal_bytes: "391432", logical_database_bytes: "561152", volume_capacity_bytes: "18446744073709551615", volume_available_bytes: "950436651008", resources: [] }, machines: [], credentials: [], more_machines: false, more_credentials: false }) }) });
});
const transport = fixtureTransport;
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><App transport={transport} currentDeviceId={currentDeviceId} connectionSettings={<button>Connection controls</button>} controlLocalWorker={async () => ({ state: LocalWorkerState.Starting, machine_id: machineId, controller_active: true })} /></AppearanceProvider>);
