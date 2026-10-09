// SPDX-License-Identifier: Apache-2.0
// Isolated synthetic adapters. No real browser/profile/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserCapability, BrowserProfileSchema, BrowserProfileState, BrowserService, BudgetState, EntityKind, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { BrowserHostProvider } from "./host-capabilities";
import { SessionView } from "./session";
import "./themes.css";
import "./styles.css";
import "./session-browser-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
if (args.get("theme") !== "system") document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("zoom") === "2") document.body.style.zoom = "2";
const sessionId = newRequestId(), accountId = newRequestId(), profileId = newRequestId();
const session = create(ResourceSchema, { id: sessionId, sessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ name: "Synthetic Browser session", workspace: "general-chat", archive: "active", outcome: "stopped", dispatch: "paused", recovery: "none", initial_execution: { id: newRequestId(), initial_account_id: accountId } }) });
const profile = create(BrowserProfileSchema, { id: profileId, accountId, revision: 1n, state: BrowserProfileState.ACTIVE, serverId: newRequestId(), deviceId: newRequestId() });
const messages = ["user", "assistant"].map((role, index) => create(ResourceSchema, { id: newRequestId(), sessionId, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role, state: "complete", text: `Synthetic ${role} message ${index}` }) }));
const tabs = Array.from({ length: args.get("tabs") === "16" ? 16 : 2 }, (_, n) => ({ id: newRequestId(), url: n ? `https://docs.example.com/${"long-path/".repeat(40)}` : "https://example.com/" }));
let local = { tabs: { tabs, selected: tabs[0].id }, removal_pending: args.get("removal") === "pending" };
const evidence = { registrations: [] as { requestId: string; accountId: string }[], native: [] as { operation: string; action?: string; viewId?: string; profileId?: string; bounds?: { x: number; y: number; width: number; height: number } }[], lastBounds: undefined as { x: number; y: number; width: number; height: number } | undefined };
Object.assign(window, { browserFixture: evidence, __TAURI_INTERNALS__: { invoke: async (operation: string, input: { action?: string; viewId?: string; profileId?: string; tabId?: string; url?: string; bounds?: typeof evidence.lastBounds }) => {
  evidence.native.push({ operation, action: input.action, profileId: input.profileId, viewId: input.viewId, bounds: input.bounds });
  if (input.bounds) evidence.lastBounds = input.bounds;
  if (args.get("native") === "failure" && operation === "open_browser") throw new Error("Synthetic original native failure");
  if (input.action === "select-tab") local = { ...local, tabs: { ...local.tabs, selected: input.tabId! } };
  if (input.action === "close-tab") { const tabs = local.tabs.tabs.filter(tab => tab.id !== input.tabId); local = { ...local, tabs: { tabs, selected: tabs.some(tab => tab.id === local.tabs.selected) ? local.tabs.selected : tabs[0]?.id ?? "" } }; }
  if (input.action === "new-tab") { const tab = { id: newRequestId(), url: input.url! }; local = { ...local, tabs: { tabs: [...local.tabs.tabs, tab], selected: tab.id } }; }
  if (input.action === "navigate") local = { ...local, tabs: { ...local.tabs, tabs: local.tabs.tabs.map(tab => tab.id === local.tabs.selected ? { ...tab, url: input.url! } : tab) } };
  return local;
} } });
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
  router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: () => ({ view: { session, state: BudgetState.ALLOW_INCOMPLETE } }) });
  router.service(BrowserService, { getBrowserCapabilities: async () => { if (args.get("capabilities") === "loading") await new Promise<void>(() => {}); if (args.get("capabilities") === "failure") throw new ConnectError("Synthetic capability unavailable", Code.Unavailable); return { capabilities: args.get("capabilities") === "unsupported" ? [] : [BrowserCapability.PROTECTED_DEVICE_PROFILE_V1] }; }, registerBrowserProfile: request => { evidence.registrations.push({ requestId: request.session!.requestId, accountId: request.accountId }); if (args.get("registration") === "uncertain" && evidence.registrations.length === 1) throw new ConnectError("Synthetic original receipt unavailable", Code.Unavailable); return { profile }; } });
  router.service(ResourceService, { getSnapshot: () => ({ resources: [session], cursor: "fixture" }), listResources: request => ({ resources: request.filter?.kind === EntityKind.MESSAGE ? messages : [] }), async *watchEvents(_request, context) { if (!context.signal.aborted) await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true })); } });
});
function Fixture() {
  const [draft, setDraft] = useState("Retained synthetic Browser draft"), [mounted, setMounted] = useState(true);
  return <div className="app browser-layout-fixture" data-fixture="__browserSplitFixture"><aside><h1>DeliDev</h1><button onClick={() => setMounted(value => !value)}>Fixture remount</button></aside><main><div className="session-container">{mounted ? <SessionView id={sessionId} draft={draft} setDraft={setDraft} /> : null}</div></main></div>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MutationIntents><BrowserHostProvider available={args.get("host") !== "unsupported"}><Fixture /></BrowserHostProvider></MutationIntents></QueryClientProvider></TransportProvider>);
