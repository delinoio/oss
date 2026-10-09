// SPDX-License-Identifier: Apache-2.0
// Isolated synthetic saved accounts, without native or mutation authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService } from "@delinoio/delidev-api-client";
import { Sidebar } from "./sidebar";
import { Surface } from "./views";
import { encode } from "./documents";
import { i18n, SupportedLanguage } from "./localization";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
void i18n.changeLanguage(args.get("language") === "ko" ? SupportedLanguage.Korean : SupportedLanguage.English);
const rows = Array.from({ length: 70 }, (_, index) => create(ResourceSchema, { id: `0195c9c0-7b13-7000-8000-${String(index + 1).padStart(12, "0")}`, kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode({ type: "subscription", name: `Synthetic ${index + 1}`, subscription_service: index % 4 === 2 ? "claude" : index % 4 === 3 ? "grok" : "chatgpt", connection: { id: "synthetic" }, quota: index % 4 < 2 ? [{id:"weekly",state:"observed",remaining:.28,comparison_group:"weekly",blocking:true,observed_at:new Date().toISOString()}] : [] }) }));
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] }) });
 router.service(ResourceService, { listResources: request => { if(request.filter?.kind !== EntityKind.ACCOUNT)return {resources:[]};const offset=Number(request.filter.pageToken)||0;return {resources:rows.slice(offset,offset+50),nextPageToken:offset+50<rows.length?String(offset+50):""}; } });
 router.service(SessionService, { listSessions: () => ({sessions:[]}) });
});
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient()}><TransportProvider transport={transport}><div className="app"><Sidebar surface={Surface.Sessions} selectedSessionId="" navigate={()=>{}} openSession={()=>{}} newSession={()=>{}} newGeneralChat={()=>{}} newProject={()=>{}} openSettings={()=>{}} /><main>Synthetic saved quota rail</main></div></TransportProvider></QueryClientProvider>);
