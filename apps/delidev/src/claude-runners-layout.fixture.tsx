// SPDX-License-Identifier: Apache-2.0
// Synthetic original Claude workflow. No server, native command or account is used.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfigurationService, EntityKind, ErrorDetailSchema, ResourceSchema, ResourceService, SubscriptionService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { AppearanceProvider, Theme } from "./appearance";
import { OAuthNativeProvider } from "./account-oauth";
import { useClaudeSubscriptionLogin } from "./claude-subscription-login";
import { RunnerRemediationProvider } from "./runner-remediation";
import { SettingsTaskDialog, SettingsTaskScope, SettingsTasks, SettingsDialogSize } from "./settings-task";
import { encode } from "./documents";
import { copy, i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";

const args = new URLSearchParams(location.search), scenario = args.get("scenario") ?? "mixed";
const korean = args.get("language") === "ko", theme = args.get("theme") === "dark" ? Theme.Dark : Theme.Light;
void i18n.changeLanguage(korean ? SupportedLanguage.Korean : SupportedLanguage.English);
const counters = { list: 0, exact: 0, save: 0, login: 0, native: 0 };
Object.defineProperty(window, "__claudeRunnersFixture", { value: counters });
const installation = { harness: "claude-code", state: "detected", version: "2.1.236", protocol_verified: true, protocol: { state: "verified" } };
function row(name: string, patch: Record<string, unknown> = {}): Resource {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name, disabled: false, worker_capabilities: ["native-claude-subscriptions-v1"], installations: [installation], ...patch }) });
}
const eligible = row(korean ? "사용 가능한 Runner — 긴 이름과 자세한 설치 관측을 줄 바꿈으로 표시합니다" : "Eligible Runner — a deliberately long name for wrapping the installation observation");
const excluded = [
  row("Disabled Runner", { disabled: true }),
  row("Capability unavailable", { worker_capabilities: [] }),
  row("Missing installation", { installations: [] }),
  row("Duplicate installation", { installations: [installation, installation] }),
  row("Unsupported version", { installations: [{ ...installation, version: "2.1.235" }] }),
  row("Execution denied", { installations: [{ ...installation, state: "permission-denied", version: "", problem: { message: "/private/native-secret-sentinel", guidance: "native-secret-guidance" } }] }),
  row("Installation failed", { installations: [{ ...installation, state: "failed", version: "" }] }),
  row("Protocol unchecked", { installations: [{ ...installation, protocol_verified: false, protocol: undefined }] }),
  row("Protocol failed", { installations: [{ ...installation, protocol_verified: false, protocol: { state: "failed", problem: { message: "native-secret-protocol" } } }] }),
];
const malformed = row("Malformed observation", { disabled: "false", installations: [{ ...installation, version: "/private/native-secret-sentinel" }] });
const all = [eligible, ...excluded, malformed];
const transport = createRouterTransport(router => {
  router.service(ResourceService, {
    listResources: async request => {
      counters.list++;
      if (scenario === "failure" && counters.list === 1) throw new ConnectError("Synthetic access denied", Code.PermissionDenied, undefined, [{ desc: ErrorDetailSchema, value: { code: "permission_denied", guidance: "Check the original connection access.", correlationId: "0195c9c0-7b13-7000-8000-000000000001" } }]);
      if (scenario === "stale" && counters.list === 2) throw new ConnectError("native-secret-refresh", Code.Unavailable);
      if (scenario === "stale" && counters.list > 2) await new Promise(resolve => setTimeout(resolve, 180));
      if (scenario === "empty") return { resources: [] };
      if (scenario === "partial" && !request.filter?.pageToken) return { resources: [], nextPageToken: "synthetic-next-page" };
      return { resources: scenario === "mixed" ? all : scenario === "malformed" ? [malformed] : [eligible] };
    },
    getResource: request => { counters.exact++; return { resource: all.find(resource => resource.id === request.id) }; },
  });
  router.service(ConfigurationService, { saveConfiguration: () => { counters.save++; throw new ConnectError("Forbidden synthetic account write", Code.PermissionDenied); } });
  router.service(SubscriptionService, { requestSubscription: () => { counters.login++; throw new ConnectError("Forbidden synthetic login", Code.PermissionDenied); } });
});
function Surface() {
  const flow = useClaudeSubscriptionLogin(true, () => {});
  return <main id="main" className="settings-content"><button data-fixture-open onClick={() => flow.begin()}>{copy("claude-subscription.title")}</button>{flow.body ? <SettingsTaskDialog title={copy("claude-subscription.title")} size={SettingsDialogSize.Wide} close={flow.hide}>{flow.body}</SettingsTaskDialog> : null}</main>;
}
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
createRoot(document.getElementById("root")!).render(<AppearanceProvider bridge={{ read: async () => ({ revision: 1, theme, problem: null }), update: async next => ({ revision: 2, theme: next, problem: null }), subscribe: async () => () => {} }}><TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><OAuthNativeProvider control={async () => { counters.native++; throw new Error("Forbidden synthetic native operation"); }}><SettingsTasks><SettingsTaskScope><Surface /></SettingsTaskScope></SettingsTasks></OAuthNativeProvider></RunnerRemediationProvider></QueryClientProvider></TransportProvider></AppearanceProvider>);
