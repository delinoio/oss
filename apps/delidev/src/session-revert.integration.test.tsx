// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code,ConnectError,createClient,createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient,QueryClientProvider } from "@tanstack/react-query";
import { fireEvent,render,screen,waitFor,within } from "@testing-library/react";
import { useRef,useState } from "react";
import { expect,it,vi } from "vitest";
import { EntityKind,ResourceSchema,ResourceService,SessionService,SystemService,SystemCapability,newRequestId,type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { useSessionRevert } from "./session-revert";

it.each([{inactive:false,changed:false,undo:false,mode:"plan",historical:"execute"},{inactive:false,changed:true,undo:false,mode:"plan",historical:"execute"},{inactive:false,changed:false,undo:false,mode:"execute",historical:"plan"},{inactive:false,changed:true,undo:false,mode:"execute",historical:"plan"},{inactive:false,changed:true,undo:true,mode:"execute",historical:"plan"},{inactive:true,changed:true,undo:true,mode:"plan",historical:"execute"}])("keeps original Revert and current $mode mode when restoring $historical text (changed=$changed)",async ({inactive,changed,undo,mode,historical})=>{
 const id=newRequestId(),machine=newRequestId(),turn=newRequestId(),thread=newRequestId(),message=newRequestId(),input=newRequestId(),job=newRequestId();
 const body={machine_id:machine,initial_execution:{configuration:{harness:"codex"}},archive:"active",recovery:"none",execution:{cleanup_verified:true,native_thread_id:thread}};
 const original=create(ResourceSchema,{kind:EntityKind.SESSION,id,sessionId:id,revision:2n,schemaVersion:1,documentJson:encode(body)});
 const target=create(ResourceSchema,{kind:EntityKind.MESSAGE,id:message,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({role:"user",state:"complete",text:"earlier prompt",input_id:input,native_thread_id:thread,native_turn_id:turn})});
 const requests:any[]=[];
 const restore=vi.fn();
 const enqueue=vi.fn(async (_request:unknown)=>({}));
 const revert=vi.fn(async request=>{requests.push(request);if(requests.length===1)throw new ConnectError("lost original acknowledgment",Code.Unavailable);return{requestId:request.mutation.requestId,replayed:true,job:create(ResourceSchema,{kind:EntityKind.JOB,id:job,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({input:{version:4,action_id:request.mutation.requestId,revert:{message_id:message,native_turn_id:turn,context_revision:0}}})})};});
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.CODEX_SESSION_REVERT_V1]})});
  router.service(ResourceService,{getResource:request=>({resource:request.kind===EntityKind.MACHINE?create(ResourceSchema,{kind:EntityKind.MACHINE,id:machine,revision:1n,schemaVersion:1,documentJson:encode({worker_capabilities:["codex-session-revert-v1"]})}):original})});
  router.service(SessionService,{revertSession:revert,enqueueInput:enqueue});
 });
 function Harness({session,active}:{session:Resource;active:boolean}){const[draft,setDraft]=useState("current draft");const[generation,setGeneration]=useState(0);const[currentMode,setMode]=useState(mode);const composer=useRef<HTMLTextAreaElement>(null);const state=useSessionRevert({session,active,draft,draftGeneration:generation,composer,blocked:false,changed:()=>{},restore:(...args)=>{restore(...args);setDraft(args[0]);}});return<><textarea aria-label="Unsent prompt" ref={composer} value={draft} onChange={e=>{setGeneration(value=>value+1);setDraft(e.target.value);}}/><label><input type="checkbox" checked={currentMode==="plan"} onChange={e=>setMode(e.target.checked?"plan":"execute")}/>Plan mode</label><button type="button" onClick={()=>void createClient(SessionService,transport).enqueueInput({requestId:newRequestId(),sessionId:id,documentJson:encode({prompt:draft,mode:currentMode})})}>Send restored prompt</button>{state.action(target)}{state.content}</>;}
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(session:Resource,active=true)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Harness session={session} active={active}/></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered=render(view(original));await waitFor(()=>expect(screen.getByRole("button",{name:"Revert and edit"})).toHaveProperty("disabled",false));
 fireEvent.click(screen.getByRole("button",{name:"Revert and edit"}));const dialog=screen.getByRole("dialog");expect(within(dialog).getByText(/Your current draft will be replaced/)).toBeDefined();fireEvent.click(within(dialog).getByRole("button",{name:"Revert and edit"}));
 fireEvent.click(await screen.findByRole("button",{name:"Retry the same Revert request"}));await waitFor(()=>expect(revert).toHaveBeenCalledTimes(2));expect(requests[1]).toEqual(requests[0]);
 if(inactive)rendered.rerender(view(original,false));
 if(changed)fireEvent.change(screen.getByRole("textbox",{name:"Unsent prompt"}),{target:{value:"edited during Revert"}});
 if(undo)fireEvent.change(screen.getByRole("textbox",{name:"Unsent prompt"}),{target:{value:"current draft"}});
 const completed=create(ResourceSchema,{...original,revision:4n,documentJson:encode({...body,context_revision:1,revert:{action_id:requests[0].mutation.requestId,job_id:job,result:{context_revision:1,retained_turn_ids:[],target:{message_id:message,native_turn_id:turn,context_revision:0,prompt:{prompt:"earlier prompt",mode:historical}}}}})});
 rendered.rerender(view(completed,!inactive));
 if(inactive){expect(restore).not.toHaveBeenCalled();rendered.rerender(view(completed));}
 if(changed){await screen.findByText(/Your edited draft was preserved/);expect(screen.getByRole("textbox",{name:"Unsent prompt"})).toHaveProperty("value",undo?"current draft":"edited during Revert");fireEvent.click(screen.getByRole("button",{name:"Restore earlier prompt"}));}
 await waitFor(()=>expect(screen.getByRole("textbox",{name:"Unsent prompt"})).toHaveProperty("value","earlier prompt"));expect(document.activeElement).toBe(screen.getByRole("textbox",{name:"Unsent prompt"}));expect(revert).toHaveBeenCalledTimes(2);
 expect(screen.getByRole("checkbox",{name:"Plan mode"})).toHaveProperty("checked",mode==="plan");
 expect(restore).toHaveBeenCalledExactlyOnceWith("earlier prompt");
 expect(enqueue).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Send restored prompt"}));
 await waitFor(()=>expect(enqueue).toHaveBeenCalledTimes(1));
 const sent=enqueue.mock.calls[0][0] as {documentJson:Uint8Array};
 expect(JSON.parse(new TextDecoder().decode(sent.documentJson))).toEqual({prompt:"earlier prompt",mode});
 expect(revert).toHaveBeenCalledTimes(2);
});


it.each(["accepted", "rejected", "uncertain"] as const)("retains the original fence across A to B to A until %s settlement", async outcome => {
 const id=newRequestId(),other=newRequestId(),machine=newRequestId(),message=newRequestId(),turn=newRequestId(),thread=newRequestId(),job=newRequestId();
 const body={machine_id:machine,initial_execution:{configuration:{harness:"codex"}},archive:"active",recovery:"none",execution:{cleanup_verified:true,native_thread_id:thread}};
 const original=create(ResourceSchema,{kind:EntityKind.SESSION,id,sessionId:id,revision:2n,schemaVersion:1,documentJson:encode(body)});
 const second=create(ResourceSchema,{...original,id:other,sessionId:other});
 const target=create(ResourceSchema,{kind:EntityKind.MESSAGE,id:message,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({role:"user",state:"complete",text:"earlier prompt",input_id:newRequestId(),native_thread_id:thread,native_turn_id:turn})});
 let settle!:()=>void;
 const delayed=new Promise<void>(resolve=>{settle=resolve;});
 const requests:any[]=[];
 const restore=vi.fn(),enqueue=vi.fn();
 const revert=vi.fn(async request=>{
  requests.push(request);
  if(requests.length===1){await delayed;if(outcome!=="accepted")throw new ConnectError("original settlement",outcome==="uncertain"?Code.Unavailable:Code.FailedPrecondition);}
  return {requestId:request.mutation.requestId,job:create(ResourceSchema,{kind:EntityKind.JOB,id:job,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({input:{version:4,action_id:request.mutation.requestId,revert:{message_id:message,native_turn_id:turn,context_revision:0}}})})};
 });
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.CODEX_SESSION_REVERT_V1]})});
  router.service(ResourceService,{getResource:request=>({resource:request.kind===EntityKind.MACHINE?create(ResourceSchema,{kind:EntityKind.MACHINE,id:machine,revision:1n,schemaVersion:1,documentJson:encode({worker_capabilities:["codex-session-revert-v1"]})}):request.id===other?second:original})});
  router.service(SessionService,{revertSession:revert});
 });
 function Harness({session}:{session:Resource}){
  const composer=useRef<HTMLTextAreaElement>(null);
  const state=useSessionRevert({session,active:true,draft:"current draft",draftGeneration:0,composer,blocked:false,changed:()=>{},restore});
  const send=()=>{if(!state.pending && !state.uncertain)enqueue(session.id);};
  return <><textarea aria-label="Draft" ref={composer} onKeyDown={event=>{if(event.key==="Enter" && event.ctrlKey)send();}}/><button disabled={state.pending || state.uncertain} onClick={send}>Send</button>{state.action(target)}{state.content}</>;
 }
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(session:Resource)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Harness key={session.id} session={session}/></MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered=render(view(original));
 await waitFor(()=>expect(screen.getByRole("button",{name:"Revert and edit"})).toHaveProperty("disabled",false));
 fireEvent.click(screen.getByRole("button",{name:"Revert and edit"}));fireEvent.click(within(screen.getByRole("dialog")).getByRole("button",{name:"Revert and edit"}));
 await waitFor(()=>expect(revert).toHaveBeenCalledTimes(1));
 rendered.rerender(view(second));expect(screen.getByRole("button",{name:"Send"})).toHaveProperty("disabled",false);
 rendered.rerender(view(original));expect(screen.getByRole("button",{name:"Send"})).toHaveProperty("disabled",true);
 fireEvent.keyDown(screen.getByRole("textbox",{name:"Draft"}),{key:"Enter",ctrlKey:true});expect(enqueue).not.toHaveBeenCalled();
 settle();
 if(outcome==="rejected"){
  await waitFor(()=>expect(screen.getByRole("button",{name:"Send"})).toHaveProperty("disabled",false));expect(restore).not.toHaveBeenCalled();return;
 }
 if(outcome==="uncertain"){
  fireEvent.click(await screen.findByRole("button",{name:"Retry the same Revert request"}));
  await waitFor(()=>expect(revert).toHaveBeenCalledTimes(2));expect(requests[1]).toEqual(requests[0]);
 }
 expect(screen.getByRole("button",{name:"Send"})).toHaveProperty("disabled",true);
 const completed=create(ResourceSchema,{...original,revision:4n,documentJson:encode({...body,context_revision:1,revert:{action_id:requests[0].mutation.requestId,job_id:job,result:{context_revision:1,retained_turn_ids:[],target:{message_id:message,native_turn_id:turn,context_revision:0,prompt:{prompt:"earlier prompt",mode:"execute"}}}}})});
 rendered.rerender(view(completed));
 await waitFor(()=>expect(restore).toHaveBeenCalledExactlyOnceWith("earlier prompt"));
 await waitFor(()=>expect(screen.getByRole("button",{name:"Send"})).toHaveProperty("disabled",false));
 expect(revert).toHaveBeenCalledTimes(outcome==="uncertain"?2:1);expect(enqueue).not.toHaveBeenCalled();
});
