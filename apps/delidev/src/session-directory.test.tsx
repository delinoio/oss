// SPDX-License-Identifier: Apache-2.0
import { Code,ConnectError,createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient,QueryClientProvider } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { fireEvent,render,screen,waitFor,within } from "@testing-library/react";
import { expect,it } from "vitest";
import { EntityKind,ResourceSchema,ResourceService,SessionService,SystemService,SessionDirectoryService,SystemCapability,newRequestId,SessionQuery,type Resource,type ChangeSessionDirectoryRequest } from "@delinoio/delidev-api-client";
import { DirectoryConnectionBarrier,MutationIntents,useSessionDirectoryPending,useRetainedMutation } from "./mutation";
import { useSessionDirectory } from "./session-directory";
import { encode } from "./documents";
function fixture(lost:boolean){
 const id=newRequestId(),machine=newRequestId(),execution=newRequestId(),sourceJob=newRequestId(),repo=newRequestId(),job=newRequestId();let original:ChangeSessionDirectoryRequest|undefined,changes=0,reads=0,other=0,wrong=false,complete=false;
 const data={machine_id:machine,initial_execution:{configuration:{harness:"codex"}},preparation:{state:"ready"},archive:"active",recovery:"none",dispatch:"ready",outcome:"succeeded",execution:{execution_id:execution,job_id:sourceJob,cleanup_verified:true}};
 const session=create(ResourceSchema,{id,kind:EntityKind.SESSION,schemaVersion:1,revision:7n,documentJson:encode(data)});
 const operation=()=>({sessionId:id,requestId:original!.mutation!.requestId,job:{id:job,kind:EntityKind.JOB,sessionId:id,schemaVersion:1,revision:1n,documentJson:encode({type:"change-session-directory",state:complete?"succeeded":"queued",parent_id:sourceJob})},generation:complete?{generationId:newRequestId(),jobId:job,requestId:original!.mutation!.requestId,sourceExecutionId:wrong?newRequestId():execution,repositoryId:repo,relativePath:"src",previousGenerationId:""}:undefined});
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SESSION_DIRECTORY_V1]})});
  router.service(ResourceService,{getResource:()=>({resource:{id:machine,kind:EntityKind.MACHINE,schemaVersion:1,revision:1n,documentJson:encode({worker_capabilities:["session-directory-v1"]})}})});
  router.service(SessionService,{editQueuedInput:()=>{other++;return {};},readSessionWorkspace:()=>({documentJson:encode({roots:[{repository_id:repo,name:"Example",primary:true}],size:"0",binary:false,truncated:false})})});
  router.service(SessionDirectoryService,{changeSessionDirectory:request=>{changes++;original=request;if(lost)throw new ConnectError("fixture response lost",Code.Unavailable);return {operation:operation()};},getSessionDirectoryOperation:request=>{reads++;expect(request.sessionId).toBe(id);expect(request.requestId).toBe(original!.mutation!.requestId);return {operation:operation()};}});
 });
 function Controller({value}:{value:Resource}){const owner=useSessionDirectory(value,true,false);return <>{owner.content}<p>{owner.pending?"Directory fenced":"Directory free"}</p></>;}
 function Other(){const edit=useRetainedMutation("edit-input:fixture",SessionQuery.editQueuedInput,undefined,undefined,false,undefined,id);return <button onClick={()=>void edit.send({mutation:{id:newRequestId(),expectedRevision:1n,requestId:newRequestId()}})}>Edit original input fixture</button>;}
 function Fence(){return <p>{useSessionDirectoryPending(id)?"Original connection fenced":"Original connection free"}</p>;}
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=(visible=true,value=session)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Fence/><Other/>{visible?<Controller value={value}/>:null}</MutationIntents></QueryClientProvider></TransportProvider>;
 return {session,view,counts:()=>({changes,reads,other}),finish:(invalid=false)=>{complete=true;wrong=invalid;}};
}
async function submit(){const button=await screen.findByRole("button",{name:"Change directory"});await waitFor(()=>expect((button as HTMLButtonElement).disabled).toBe(false));fireEvent.click(button);const input=await screen.findByLabelText("Relative directory");expect(document.activeElement).toBe(input);fireEvent.change(input,{target:{value:"src"}});fireEvent.click(within(screen.getByRole("dialog")).getByRole("button",{name:"Change directory"}));await screen.findByText("Original connection fenced");}
it("keeps the original uncertain request through unmount and only observes its receipt",async()=>{
 const f=fixture(true),mounted=render(f.view());await submit();fireEvent.click(screen.getByRole("button",{name:"Edit original input fixture"}));expect(f.counts()).toEqual({changes:1,reads:0,other:0});mounted.rerender(f.view(false));expect(screen.getByText("Original connection fenced")).toBeDefined();
 const later=create(ResourceSchema,{...f.session,revision:9n});mounted.rerender(f.view(true,later));expect(await screen.findByText("Original directory change is pending: Example · src")).toBeDefined();f.finish(true);fireEvent.click(await screen.findByRole("button",{name:"Check original directory change"}));await screen.findByRole("alert");expect(screen.getByText("Original connection fenced")).toBeDefined();
 f.finish();fireEvent.click(screen.getByRole("button",{name:"Check original directory change"}));await screen.findByText("The original Worker verified the new directory.");expect(screen.getByText("Original connection free")).toBeDefined();expect(f.counts()).toEqual({changes:1,reads:2,other:0});expect(screen.queryByText(f.session.id)).toBeNull();
});
it("retains queued admission without optimistic completion or automatic polling",async()=>{
 const f=fixture(false);render(f.view());await submit();await waitFor(()=>expect(screen.getByText("Directory fenced")).toBeDefined());expect(screen.queryByText("The original Worker verified the new directory.")).toBeNull();expect(f.counts()).toEqual({changes:1,reads:0,other:0});
 fireEvent.click(screen.getByRole("button",{name:"Check original directory change"}));await waitFor(()=>expect(f.counts().reads).toBe(1));expect(screen.getByText("Original connection fenced")).toBeDefined();f.finish();fireEvent.click(screen.getByRole("button",{name:"Check original directory change"}));await screen.findByText("The original Worker verified the new directory.");expect(f.counts().changes).toBe(1);
});

it("Escape closes only the dialog before admission",async()=>{
 const f=fixture(false);render(f.view());const button=await screen.findByRole("button",{name:"Change directory"});await waitFor(()=>expect((button as HTMLButtonElement).disabled).toBe(false));fireEvent.click(button);await screen.findByLabelText("Relative directory");fireEvent(screen.getByRole("dialog"),new Event("cancel",{bubbles:true,cancelable:true}));expect(screen.queryByRole("dialog")).toBeNull();expect(f.counts()).toEqual({changes:0,reads:0,other:0});
});

it("preserves original connection and actor while a directory receipt is retained",()=>{
 const original={server:"original",actor:"paired-original"},replacement={server:"replacement",actor:"paired-new"},barrier=new DirectoryConnectionBarrier<typeof original>();
 expect(barrier.select("original",original)).toBe(original);barrier.observe(true);
 expect(barrier.select("replacement",replacement)).toBe(original);
 const refreshed={...original};expect(barrier.select("original",refreshed)).toBe(refreshed);
 barrier.observe(false);expect(barrier.select("replacement",replacement)).toBe(replacement);
});
