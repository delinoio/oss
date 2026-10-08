// SPDX-License-Identifier: Apache-2.0
// Synthetic saved-resource reader; this entry has no native or account authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, SystemCapability, SystemService } from "@delinoio/delidev-api-client";
import { SubscriptionRail } from "./subscription-rail";
import { encode } from "./documents";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
const args=new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language")==="ko"?"ko":"en");
document.documentElement.dataset.theme=args.get("theme")==="dark"?"dark":"light";
document.documentElement.dataset.railFixture="__subscriptionRailFixture";
let hold=false, finish:(()=>void)|undefined, reads=0;
Object.assign(window,{fixtureHoldRail:()=>{hold=true;},fixtureReleaseRail:()=>{hold=false;finish?.();finish=undefined;},fixtureTimerRail:()=>timerRefresh?.()});
let timerRefresh:(()=>void)|undefined;
const originalInterval=window.setInterval.bind(window);
window.setInterval=((handler:TimerHandler, delay?:number,...values:unknown[])=>{if(delay===60000&&typeof handler==="function")timerRefresh=handler as ()=>void;return originalInterval(handler,delay,...values);}) as typeof window.setInterval;
const names=args.get("tail")==="true"?Array.from({length:10},(_,index)=>`Account ${index+1}`):["Personal","Work"];
const rows=names.map((name,index)=>create(ResourceSchema,{id:`0195c9c0-7b13-7000-8000-${String(index+1).padStart(12,"0")}`,kind:EntityKind.ACCOUNT,revision:1n,schemaVersion:2,documentJson:encode({type:"subscription",name,subscription_service:"chatgpt",connection:{id:`0195c9c0-7b13-7000-8000-${String(index+20).padStart(12,"0")}`},quota:[{id:"weekly",remaining:.28,state:"observed",observed_at:new Date().toISOString(),comparison_group:"weekly",blocking:true}]})}));
const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1]})});router.service(ResourceService,{listResources:async()=>{document.documentElement.dataset.reads=String(++reads);if(hold){document.documentElement.dataset.pending="true";await new Promise<void>(done=>finish=done);document.documentElement.dataset.pending="false";}return{resources:rows,nextPageToken:args.get("tail")==="true"?"tail":""};}});});
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false,refetchOnWindowFocus:false}}})}><TransportProvider transport={transport}><><style>{`.subscription-rail-scroll{max-height:96px;}`}</style><nav className="sidebar-rail" style={{height:"100dvh"}}><button className="sidebar-rail-button">Home</button><div className="sidebar-rail-spacer"/><SubscriptionRail enabled manage={()=>{}} focusFallback={()=>document.querySelector<HTMLButtonElement>(".fixture-help")?.focus()}/><button className="sidebar-rail-button fixture-help">Help</button><button className="sidebar-rail-button">Settings</button></nav></></TransportProvider></QueryClientProvider>);
