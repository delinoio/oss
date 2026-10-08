// SPDX-License-Identifier: Apache-2.0
// Synthetic real row/controller evidence. No native command or external account.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AccountService, ProviderService, UsageService, EntityKind, ResourceSchema, GetUsageSummaryResponseSchema, UsageAccountingProfile, UsageCostState, newRequestId } from "@delinoio/delidev-api-client";
import { SettingsTasks, SettingsTaskBackground } from "./settings-task";
import { ApiEntryRow } from "./api-entry-row";
import { ApiVerification } from "./api-verification";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";
import { i18n, SupportedLanguage } from "./localization";
import type { UsageEntry } from "./usage-entry";
import "./themes.css";
import "./styles.css";
import "./api-account.css";
import "./api-verification-layout.fixture.css";

const args = new URLSearchParams(location.search), scenario = args.get("scenario") ?? "accepted";
const language = args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English;
await i18n.changeLanguage(language);
documentElementTheme();
function documentElementTheme() { globalThis.document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light"; }
const provider = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROVIDER, schemaVersion: 1, revision: 1n, documentJson: encode({ enabled: true, discovery: true }) });
const connection = newRequestId(), observed = "2026-10-08T00:08:14Z";
const alias = scenario === "long" ? "OpenRouter — " + "LongAlias한글".repeat(14) : "OpenRouter";
let row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 3, revision: 1n, documentJson: encode({ alias, type: "api", provider_id: provider.id, api_protocol: "openai-responses", enabled: true, health: "ready", connection: scenario === "disconnected" ? undefined : { id: connection }, quota: [], validation: { connection_id: connection, state: scenario === "failed" ? "failed" : scenario === "unsupported" ? "unsupported" : "observed", authentication: "credential-accepted", observed_at: observed }, catalog: { connection_id: connection, state: scenario === "failed" ? "failed" : "observed", received: 467, observed_at: observed, last_success_at: observed } }) });
const summary = create(GetUsageSummaryResponseSchema, { fromUnixMs: 1780000000123n, untilUnixMs: 1782592000123n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, totals: { total: {} }, estimatedCost: UsageCostState.UNAVAILABLE });
const counters = { usage: 0, validate: [] as { id: string; expectedRevision: string; requestId: string }[], discover: [] as { id: string; expectedRevision: string; requestId: string }[], manage: 0, edit: 0, remove: 0, changed: 0, navigation: [] as { accountId: string; from: string; until: string }[] };
let release: (() => void) | undefined;
const transport = createRouterTransport(router => {
  router.service(UsageService, { getUsageSummary: request => { counters.usage++; if (request.accountId !== row.id) throw new Error("Foreign usage identity"); return summary; } });
  router.service(AccountService, { validateAccount: async request => {
    const mutation = request.mutation!;
    counters.validate.push({ id: mutation.id, expectedRevision: String(mutation.expectedRevision), requestId: mutation.requestId });
    if (scenario === "pending") await new Promise<void>(done => { release = done; });
    if (scenario === "uncertain" && counters.validate.length === 1) throw new ConnectError("Synthetic lost authentication receipt with complete original recovery guidance.", Code.Unavailable);
    const validation = { request_id: mutation.requestId, connection_id: connection, state: "observed", authentication: "credential-accepted", observed_at: observed };
    row = create(ResourceSchema, { ...row, revision: 2n, documentJson: encode({ ...document(row), validation }) });
    return { account: row, requestId: mutation.requestId, validationJson: encode(validation) };
  } });
  router.service(ProviderService, { discoverModels: request => {
    const mutation = request.mutation!;
    counters.discover.push({ id: mutation.id, expectedRevision: String(mutation.expectedRevision), requestId: mutation.requestId });
    const catalog = { request_id: mutation.requestId, connection_id: connection, state: "observed", received: 467, observed_at: observed };
    row = create(ResourceSchema, { ...row, revision: 3n, documentJson: encode({ ...document(row), catalog }) });
    return { account: row, requestId: mutation.requestId, observationJson: encode(catalog) };
  } });
});
Object.defineProperty(window, "__apiVerificationFixture", { value: { counters, accountId: row.id, from: String(summary.fromUnixMs), until: String(summary.untilUnixMs), release: () => release?.(), language: (next: string) => i18n.changeLanguage(next) } });
function Surface() {
  const [current, update] = useState(row);
  const changed = () => { counters.changed++; update(row); };
  const openUsage = (entry: UsageEntry) => counters.navigation.push({ accountId: entry.accountId!, from: String(entry.fromUnixMs), until: String(entry.untilUnixMs) });
  return <div className="api-fixture-shell"><aside className="api-fixture-sidebar" aria-label="Synthetic sidebar">DeliDev</aside><main className="api-fixture-main settings-api-keys"><div className="settings-content-column api-keys-view"><header className="api-entry-heading"><h1>AI API Keys</h1><p>Synthetic original account presentation</p></header><div className="api-usage-list"><div className="api-entry-rows"><ApiEntryRow row={current} provider={{ displayName: scenario === "long" ? "ProviderName긴이름".repeat(12) : "OpenRouter", enabled: true, protocol: "openai-responses" }} active manage={() => counters.manage++} edit={() => counters.edit++} remove={() => counters.remove++} openUsage={openUsage} verification={<ApiVerification row={current} provider={provider} active changed={changed} />} /></div></div></div></main></div>;
}
const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
createRoot(globalThis.document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SettingsTasks><SettingsTaskBackground><Surface /></SettingsTaskBackground></SettingsTasks></MutationIntents></QueryClientProvider></TransportProvider>);
