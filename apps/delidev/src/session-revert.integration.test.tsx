// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code,ConnectError,createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient,QueryClientProvider } from "@tanstack/react-query";
import { fireEvent,render,screen,waitFor,within } from "@testing-library/react";
import { useRef,useState } from "react";
import { expect,it,vi } from "vitest";
import { EntityKind,ResourceSchema,ResourceService,SessionService,SystemService,SystemCapability,newRequestId,type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { useSessionRevert } from "./session-revert";

it.each([false,true])("keeps one original Revert request and restores only an unsent guarded draft (changed=%s)",async changed=>{
 const id=newRequestId(),machine=newRequestId(),turn=newRequestId(),thread=newRequestId(),message=newRequestId(),input=newRequestId(),job=newRequestId();
 const body={machine_id:machine,initial_execution:{configuration:{harness:"codex"}},archive:"active",recovery:"none",execution:{cleanup_verified:true,native_thread_id:thread}};
 const original=create(ResourceSchema,{kind:EntityKind.SESSION,id,sessionId:id,revision:2n,schemaVersion:1,documentJson:encode(body)});
 const target=create(ResourceSchema,{kind:EntityKind.MESSAGE,id:message,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({role:"user",state:"complete",text:"earlier prompt",input_id:input,native_thread_id:thread,native_turn_id:turn})});
 const requests:any[]=[];
 const revert=vi.fn(async request=>{requests.push(request);if(requests.length===1)throw new ConnectError("lost original acknowledgment",Code.Unavailable);return{requestId:request.mutation.requestId,replayed:true,job:create(ResourceSchema,{kind:EntityKind.JOB,id:job,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({input:{version:4,action_id:request.mutation.requestId,revert:{message_id:message,native_turn_id:turn,context_revision:0}}})})};});
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.CODEX_SESSION_REVERT_V1]})});
  router.service(ResourceService,{getResource:request=>({resource:request.kind===EntityKind.MACHINE?create(ResourceSchema,{kind:EntityKind.MACHINE,id:machine,revision:1n,schemaVersion:1,documentJson:encode({worker_capabilities:["codex-session-revert-v1"]})}):original})});
  router.service(SessionService,{revertSession:revert});
 });
 function Harness({session}:{session:Resource}){const[draft,setDraft]=useState("current draft");const composer=useRef<HTMLTextAreaElement>(null);const state=useSessionRevert({session,active:true,draft,composer,blocked:false,changed:()=>{},restore:(text)=>setDraft(text)});return<><textarea aria-label="Unsent prompt" ref={composer} value={draft} onChange={e=>setDraft(e.target.value)}/>{state.action(target)}{state.content}</>;}
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(session:Resource)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Harness session={session}/></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered=render(view(original));await waitFor(()=>expect(screen.getByRole("button",{name:"Revert and edit"})).toHaveProperty("disabled",false));
 fireEvent.click(screen.getByRole("button",{name:"Revert and edit"}));const dialog=screen.getByRole("dialog");expect(within(dialog).getByText(/Your current draft will be replaced/)).toBeDefined();fireEvent.click(within(dialog).getByRole("button",{name:"Revert and edit"}));
 fireEvent.click(await screen.findByRole("button",{name:"Retry the same Revert request"}));await waitFor(()=>expect(revert).toHaveBeenCalledTimes(2));expect(requests[1]).toEqual(requests[0]);
 if(changed)fireEvent.change(screen.getByRole("textbox",{name:"Unsent prompt"}),{target:{value:"edited during Revert"}});
 const completed=create(ResourceSchema,{...original,revision:4n,documentJson:encode({...body,context_revision:1,revert:{action_id:requests[0].mutation.requestId,job_id:job,result:{context_revision:1,retained_turn_ids:[],target:{message_id:message,native_turn_id:turn,context_revision:0,prompt:{prompt:"earlier prompt",mode:"execute"}}}}})});
 rendered.rerender(view(completed));
 if(changed){await screen.findByText(/Your edited draft was preserved/);expect(screen.getByRole("textbox",{name:"Unsent prompt"})).toHaveProperty("value","edited during Revert");fireEvent.click(screen.getByRole("button",{name:"Restore earlier prompt"}));}
 await waitFor(()=>expect(screen.getByRole("textbox",{name:"Unsent prompt"})).toHaveProperty("value","earlier prompt"));expect(document.activeElement).toBe(screen.getByRole("textbox",{name:"Unsent prompt"}));expect(revert).toHaveBeenCalledTimes(2);
});
