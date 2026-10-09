// SPDX-License-Identifier: Apache-2.0
// Isolated synthetic saved accounts, without native or mutation authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
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
const count = Number(args.get("count") ?? 70);
if (![0, 1, 2, 3, 4, 5, 6, 70].includes(count)) throw new Error("Invalid synthetic account count");
const windows = args.get("windows") === "many" ? 12 : 1;
const quota = (length:number) => Array.from({length},(_,window)=>({id:window===0 ? "codex:primary" : `native:window:${window}:long-original-window-identity`,state:"observed",remaining:window===0?.57:.28,comparison_group:"weekly",blocking:true,observed_at:new Date().toISOString(),reset_at:new Date(Date.now()+86400000).toISOString()}));
let reads=0;
const rows = Array.from({ length: count }, (_, index) => create(ResourceSchema, { id: `0195c9c0-7b13-7000-8000-${String(index + 1).padStart(12, "0")}`, kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode({ type: "subscription", name: index === 0 ? "personal@example.test" : `Synthetic ${index + 1} ${"Long wrapping alias ".repeat(6)}`, subscription_service: index % 4 === 2 ? "claude" : index % 4 === 3 ? "grok" : "chatgpt", connection: { id: "synthetic" }, quota: quota(windows) }) }));
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] }) });
 router.service(ResourceService, { listResources: request => { if(request.filter?.kind !== EntityKind.ACCOUNT)return {resources:[]};if(args.get("readProblem") === "true")throw new ConnectError("Synthetic saved read failure",Code.Unavailable);document.documentElement.dataset.accountReads=String(++reads);document.documentElement.dataset.accountRead = JSON.stringify({ pageSize: request.filter.pageSize, pageToken: request.filter.pageToken });const offset=Number(request.filter.pageToken)||0;return {resources:rows.slice(offset,offset+50).map(row=>args.get("grow")==="true"&&reads>1&&row.id===rows[0].id?create(ResourceSchema,{...row,revision:2n,documentJson:encode({...JSON.parse(new TextDecoder().decode(row.documentJson)),quota:quota(12)})}):row),nextPageToken:offset+50<rows.length?String(offset+50):""}; } });
 router.service(SessionService, { listSessions: () => ({sessions:[]}) });
});
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient()}><TransportProvider transport={transport}><div className="app"><Sidebar surface={Surface.Sessions} selectedSessionId="" navigate={()=>{}} openSession={()=>{}} newSession={()=>{}} newGeneralChat={()=>{}} newProject={()=>{}} openSettings={()=>{}} /><main>Synthetic saved quota rail</main></div></TransportProvider></QueryClientProvider>);
