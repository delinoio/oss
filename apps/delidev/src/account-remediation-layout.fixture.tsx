// SPDX-License-Identifier: Apache-2.0
// Synthetic read-only presentation; no native login or account mutation authority.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AccountService, CodexDiagnosticPhase, CodexDiagnosticSchema, EntityKind, ErrorDetailSchema, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService } from "@delinoio/delidev-api-client";
import { Sidebar } from "./sidebar";
import { Surface } from "./views";
import { Modal } from "./ui";
import { LocalConnectionPresentationProvider } from "./local-connection-presentation";
import { LocalServerControls, LocalServerState } from "./local-server";
import { AccountConnection } from "./account-connection";
import { SubscriptionOnboarding, SubscriptionOnboardingStage } from "./subscription-onboarding";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const counters = { inventory: 0, account: 0, provider: 0, inspect: 0, unsupportedMutation: 0 };
Object.defineProperty(window, "__accountRemediationFixture", { value: counters });
const id = "0195c9c0-7b13-7000-8000-000000000001", providerId = "0195c9c0-7b13-7000-8000-000000000002";
const denied = () => new ConnectError("Synthetic permission denial", Code.PermissionDenied, undefined, [{desc:ErrorDetailSchema,value:{code:"permission_denied",correlationId:"0195c9c0-7b13-7000-8000-000000000003",guidance:"Synthetic server account permission is unavailable."}}]);
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] }) });
 router.service(ResourceService, { listResources: request => { if (request.filter?.kind === EntityKind.ACCOUNT) { counters.inventory++; throw denied(); } return {resources:[]}; }, getResource: () => { counters.provider++; throw denied(); } });
 router.service(AccountService, { getAccountStatus: () => { counters.account++; throw denied(); }, connectAccount:()=>{counters.unsupportedMutation++;throw denied();} });
 router.service(SessionService, { listSessions: () => ({sessions:[]}) });
});
const initial = create(ResourceSchema, {id,kind:EntityKind.ACCOUNT,schemaVersion:1,revision:1n,documentJson:encode({alias:"Synthetic original account",type:"api",provider_id:providerId,health:"disconnected"})});
const diagnostic = create(CodexDiagnosticSchema,{phase:CodexDiagnosticPhase.CLEANUP,code:"recovery_required",detectedVersion:"0.151.0",message:"private-native-sentinel",guidance:"private-path-sentinel"});
function OriginalAccountTask() {
 const [open, setOpen] = useState(false);
 return <LocalConnectionPresentationProvider inline={false}><button onClick={()=>setOpen(true)}>Open original account task</button>{open ? <Modal title="Original account task" close={()=>setOpen(false)}><AccountConnection initial={initial} active close={()=>setOpen(false)} /></Modal> : null}<LocalServerControls status={{state:LocalServerState.Ready,attempts:0,retry_ms:0}} busy={false} restart={()=>{counters.unsupportedMutation++;}} /></LocalConnectionPresentationProvider>;
}
const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><div className="app"><Sidebar surface={Surface.Sessions} selectedSessionId="" navigate={()=>{}} openSession={()=>{}} newSession={()=>{}} newGeneralChat={()=>{}} newProject={()=>{}} openSettings={()=>{}} /><main className="page settings-screen"><SubscriptionOnboarding serviceName="ChatGPT" stage={SubscriptionOnboardingStage.Recovery} active name="Synthetic original account" suggested={false} busy={false} diagnostic={diagnostic} canReopen={false} canCancel={false} changeName={()=>{}} saveName={()=>{}} reopen={()=>{}} cancel={()=>{}} leave={()=>{}} inspect={()=>{counters.inspect++;}} />{args.get("mode") === "modal" ? <OriginalAccountTask /> : <AccountConnection initial={initial} active close={()=>{}} />}</main></div></MutationIntents></TransportProvider></QueryClientProvider>);
