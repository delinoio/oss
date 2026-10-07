// SPDX-License-Identifier: Apache-2.0
// Synthetic read-only App fixture; no native, account or inference authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { AccountingUnitKind, EntityKind, GetUsageSummaryResponseSchema, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, UsageAccountingProfile, UsageCostState, UsageCoverage, UsageService, UsageTimeGranularity } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { AppearanceProvider, Theme } from "./appearance";
import { encode } from "./documents";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";

const args = new URLSearchParams(location.search);
const empty = args.get("empty") === "true";
void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const theme = args.get("theme") === "dark" ? Theme.Dark : Theme.Light;
const ids = { session: "0195c9c0-7b13-7000-8000-000000000001", project: "0195c9c0-7b13-7000-8000-000000000002", account: "0195c9c0-7b13-7000-8000-000000000003", provider: "0195c9c0-7b13-7000-8000-000000000004", model: "0195c9c0-7b13-7000-8000-000000000005" };
const measure = { knownTotal: empty ? "" : "9007199254740993", measuredResponses: empty ? 0 : 1, unavailableResponses: 0 };
const missing = { knownTotal: "", measuredResponses: 0, unavailableResponses: empty ? 0 : 1 };
const totals = { responses: empty ? 0 : 1, total: measure, input: measure, output: { ...measure, knownTotal: empty ? "" : "0" }, cachedInput: missing, cacheWriteInput: missing, reasoningOutput: missing };
const data = create(GetUsageSummaryResponseSchema, {
  fromUnixMs: 1788220800000n, untilUnixMs: 1788307200000n, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES,
  actualCost: UsageCostState.UNAVAILABLE, totals, acceptedExecutionsWithoutResponse: 2, acceptedCompactionsWithoutResponse: 1,
  groups: empty ? [] : [{ sessionId: ids.session, sessionName: "Synthetic original session with a complete long display name".repeat(2), projectId: ids.project, projectName: "Synthetic project", accountId: ids.account, accountName: "Synthetic original account", providerId: ids.provider, providerName: "Synthetic API", modelId: ids.model, modelName: "Synthetic recorded model", totals, estimates: { currencies: [{ currency: "USD", knownAmount: "0.002", partialResponses: 1 }], unpricedResponses: 1 } }],
  analytics: { granularity: UsageTimeGranularity.DAY, timeZone: "UTC", days: [{ fromUnixMs: 1788220800000n, untilUnixMs: 1788307200000n, totals }], models: empty ? [] : [{ providerId: ids.provider, modelId: ids.model, providerName: "Synthetic API", modelName: "Synthetic recorded model", totals }] },
  nativeAccounting: [{ totals: { kind: AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT } }, { totals: { kind: AccountingUnitKind.OPENCODE_STEP, units: empty ? 0 : 1, input: empty ? undefined : { knownTotal: "9007199254740993", measuredUnits: 1 } } }],
  estimates: { currencies: empty ? [] : [{ currency: "USD", knownAmount: "0.002", partialResponses: 1 }, { currency: "EUR", knownAmount: "0", completeResponses: 1 }], unpricedResponses: empty ? 0 : 1 },
});
const requests = { summary: 0, writes: 0 };
Object.defineProperty(window, "__usageFixture", { value: requests });
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ protocolVersion: 1, capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] }) });
  router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
  router.service(InboxService, { listInbox: () => ({ entries: [] }), getNotificationPreferences: () => ({ preferences: create(NotificationPreferencesSchema, { revision: 1n }) }) });
  router.service(ResourceService, { listResources: () => ({ resources: [] }), getResource: request => ({ resource: create(ResourceSchema, { id: request.id, kind: request.kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Synthetic record", native_id: "synthetic-model", provider_id: ids.provider, source_kind: "api" }) }) }) });
  router.service(UsageService, { getUsageSummary: () => { requests.summary++; return data; } });
});
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><App transport={transport} /></AppearanceProvider>);
