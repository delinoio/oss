// SPDX-License-Identifier: Apache-2.0
// Synthetic Connect fixtures exercise presentation without native or account authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, SubscriptionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SubscriptionQuotaControls } from "./subscription-quota";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
document.documentElement.dataset.theme = args.get("theme") ?? "light";
if(args.get("zoom")==="2") document.body.style.zoom="2";
await i18n.changeLanguage(args.get("language") ?? "en");
const machine=newRequestId();
const account=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,revision:1n,schemaVersion:2,documentJson:encode({alias:"Synthetic reset credits",type:"subscription",subscription_service:"chatgpt",health:"ready",connection:{id:newRequestId()},subscription:{generation:newRequestId(),owner_machine_id:machine,reset_credits:{observation_id:newRequestId(),observed_at:new Date().toISOString(),available_count:"2",credits:["a","b"].map(suffix=>({id:`RateLimitResetCredit_${"long_original_identity_".repeat(4)}${suffix}`,reset_type:"codexRateLimits",status:"available",expires_at:"2099-01-01T00:00:00Z"}))}}})});
const transport=createRouterTransport(router=>{
 router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1,SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2]})});
 router.service(SubscriptionService,{requestSubscriptionObservation:()=>{throw new Error("Presentation fixture must not consume a credit");}});
});
createRoot(document.getElementById("root")!).render(<main style={{width:"100%",padding:8,boxSizing:"border-box"}}><QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><TransportProvider transport={transport}><MutationIntents><SubscriptionQuotaControls current={account} machine={machine} active accepted={()=>{throw new Error("No fixture mutation acceptance");}} busyChanged={()=>{}} /></MutationIntents></TransportProvider></QueryClientProvider></main>);
