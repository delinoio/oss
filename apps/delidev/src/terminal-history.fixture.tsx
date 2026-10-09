// SPDX-License-Identifier: Apache-2.0
// Isolated original-source terminal geometry; synthetic services grant no native authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, TerminalService, TerminalAction, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTerminals } from "./session-terminals";
import "./themes.css";
import "./styles.css";
import "./session.css";
import "./terminal-history.fixture.css";
const args=new URLSearchParams(location.search), exited=args.get("exited")==="true";
const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({archive:"active"})});
let terminal=create(ResourceSchema,{id:newRequestId(),sessionId:session.id,kind:EntityKind.TERMINAL,schemaVersion:1,revision:4n,documentJson:encode({state:exited?"exited":"running",cleanup_verified:exited})});
const metrics={reads:0,controls:[] as {action:TerminalAction;rows:number;columns:number;input:number[]}[],watches:0,watchClosed:0,requests:[] as {epoch:string;afterSequence:string}[],screens:0,removedScreens:0};
let inputRelease:(()=>void)|undefined;
const pause=(ms:number)=>new Promise<void>(resolve=>setTimeout(resolve,ms));
const transport=createRouterTransport(router=>{
 router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SESSION_TERMINALS_V1]})});
 router.service(ResourceService,{listResources:async()=>{metrics.reads++;await pause(180);return {resources:[terminal]};}});
 router.service(TerminalService,{
  controlTerminal:async request=>{if(request.mutation?.id!==terminal.id||request.mutation.expectedRevision!==terminal.revision||![TerminalAction.INPUT,TerminalAction.RESIZE].includes(request.action))throw new Error("Synthetic terminal control ownership changed.");metrics.controls.push({action:request.action,rows:request.rows,columns:request.columns,input:[...request.input]});
   if(request.action===TerminalAction.INPUT)await new Promise<void>(resolve=>{inputRelease=resolve;});else await pause(30);
   terminal=create(ResourceSchema,{...terminal,revision:terminal.revision+1n});return {terminal};},
  watchTerminalOutput:async function*(request,context){metrics.watches++;metrics.requests.push({epoch:request.epoch,afterSequence:request.afterSequence.toString()});
   try {yield {epoch:newRequestId(),sequence:1n,data:new TextEncoder().encode(Array.from({length:80},(_,index)=>`unchanged synthetic line ${index}\r\n`).join("")),terminal};
    if(exited)return;
    await new Promise<void>(resolve=>{if(context.signal.aborted)resolve();else context.signal.addEventListener("abort",()=>resolve(),{once:true});});
   }finally{metrics.watchClosed++;}
  }
 });
});
Object.assign(window,{__terminalHistoryFixture:{metrics,releaseInput:()=>{inputRelease?.();inputRelease=undefined;}}});
const observer=new MutationObserver(changes=>{for(const change of changes)for(const [nodes,key] of [[change.addedNodes,"screens"],[change.removedNodes,"removedScreens"]] as const)for(const node of nodes)if(node instanceof Element)metrics[key]+=Number(node.matches(".xterm"))+node.querySelectorAll(".xterm").length;});
observer.observe(document.body,{subtree:true,childList:true});
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><TransportProvider transport={transport}><MutationIntents><section className="session-workspace terminal-open terminal-history-fixture"><div className="session-content"><div className="session-upper-content"/><div className="session-terminal-slot"><SessionTerminals session={session} close={()=>{}} /></div></div></section></MutationIntents></TransportProvider></QueryClientProvider>);
