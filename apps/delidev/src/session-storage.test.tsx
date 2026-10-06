// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionDeletionJobSchema, SessionDeletionState, SessionService, SystemCapability, SystemService, WorkspaceStorageAction, WorkspaceStorageService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionStorageAction, SessionStorageProvider } from "./session-storage";

function fixture(options: { local?: boolean; sidechat?: boolean; supported?: boolean; recoveryState?: "failed" | "canceled" } = {}) {
 const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:8n,documentJson:encode({name:"Original session",workspace:options.local?"local":"general-chat",...(options.sidechat?{fork:{sidechat_parent_snapshot:{}}}:{})})});
 let current=session;
 const job=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.JOB,schemaVersion:1,sessionId:session.id,revision:3n,documentJson:encode({type:"workspace-storage",state:"succeeded",input:{action:"preview"},output:{action:"preview",cleanup_verified:true,preview_digest:"a".repeat(64),source_bytes:"9007199254740993",retained_snapshot_bytes:"16",removed_source_bytes:"0",workspace_state:"present"}})});
 const cleanup={...job,id:newRequestId(),revision:2n,documentJson:encode({type:"workspace-storage",state:"claimed",input:{action:"cleanup"}})};
 if(options.recoveryState) {
  job.documentJson=encode({type:"workspace-storage",state:"uncertain",input:{action:"cleanup"},problem:{code:"recovery_required"}});
  current={...session,documentJson:encode({name:"Original session",workspace:"general-chat",storage:{state:"uncertain",job_id:job.id}})};
  cleanup.documentJson=encode({type:"workspace-storage",state:options.recoveryState,input:{action:"recover"}});
 }
 const snapshot=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SNAPSHOT,schemaVersion:1,sessionId:session.id,revision:1n,documentJson:encode({size_bytes:"16",created_at:"2026-10-04T00:00:00Z"})});
 const deletion=create(SessionDeletionJobSchema,{id:newRequestId(),sessionId:session.id,revision:1n,state:SessionDeletionState.PENDING,workersPending:1});
 const requests:unknown[]=[];const deletes:unknown[]=[];
 const request=vi.fn(async(value:{action:WorkspaceStorageAction})=>{requests.push(value);if(requests.length===1)throw new ConnectError("lost response",Code.Unavailable);current={...current,revision:9n};return {job:value.action===WorkspaceStorageAction.CLEANUP || value.action===WorkspaceStorageAction.RECOVER?cleanup:job};});
 const cancel=vi.fn(async(_value:unknown)=>({job:{...cleanup,revision:3n}}));
 const remove=vi.fn(async(value:unknown)=>{deletes.push(value);if(deletes.length===1)throw new ConnectError("lost deletion response",Code.Unavailable);return {job:deletion};});
 const getDeletion=vi.fn(async()=>({job:deletion}));
 const transport=createRouterTransport((router)=>{
  router.service(SystemService,{getStatus:()=>({capabilities:options.supported===false?[]:[SystemCapability.WORKSPACE_STORAGE_V1,SystemCapability.PERMANENT_SESSION_DELETION_V1]})});
  router.service(ResourceService,{getResource:()=>({resource:current}),listResources:(value)=>({resources:value.filter?.kind===EntityKind.SNAPSHOT?[snapshot]:[]})});
  router.service(WorkspaceStorageService,{requestWorkspaceStorage:request,getWorkspaceStorageOperation:(value)=>({job:value.id===job.id?job:cleanup}),cancelWorkspaceStorageOperation:cancel});
  router.service(SessionService,{deleteSession:remove,getSessionDeletion:getDeletion});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(active:boolean)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionStorageProvider>{active?<SessionStorageAction source={session}/>:<p>Other session</p>}</SessionStorageProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 return {view,session,job,cleanup,deletion,request,requests,cancel,remove,deletes,getDeletion};
}
async function open(){fireEvent.click(screen.getByRole("button",{name:"Workspace storage and permanent deletion"}));await screen.findByRole("button",{name:"Preview workspace usage"});}

it("retains preview retries across navigation and binds cleanup/cancellation to the exact original jobs",async()=>{
 const f=fixture();const rendered=render(f.view(true));await open();
 await waitFor(()=>expect((screen.getByRole("button",{name:"Preview workspace usage"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Preview workspace usage"}));fireEvent.click(screen.getByRole("button",{name:"Confirm selected storage action"}));
 await screen.findByRole("button",{name:"Retry the same storage request"});
 fireEvent.click(screen.getByRole("button",{name:"Close Workspace storage and permanent deletion"}));rendered.rerender(f.view(false));
 fireEvent.click(screen.getByRole("button",{name:"Return to retained storage or deletion operation"}));
 fireEvent.click(screen.getByRole("button",{name:"Retry the same storage request"}));
 await screen.findByText(/9,007,199,254,740,993 bytes/);
 expect(f.requests[1]).toEqual(f.requests[0]);
 await waitFor(()=>expect((screen.getByRole("button",{name:"Store and clean workspace"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Store and clean workspace"}));
 expect(screen.getByText(/Original independent cleanup must complete first/)).not.toBeNull();
 fireEvent.click(screen.getByRole("button",{name:"Confirm selected storage action"}));
 await waitFor(()=>expect(f.request).toHaveBeenCalledTimes(3));
 expect(f.requests[2]).toMatchObject({action:WorkspaceStorageAction.CLEANUP,previewJobId:f.job.id,mutation:{id:f.session.id,expectedRevision:9n}});
 fireEvent.click(await screen.findByRole("button",{name:"Cancel original storage operation"}));
 await waitFor(()=>expect(f.cancel).toHaveBeenCalledTimes(1));
 expect(f.cancel.mock.calls[0]?.[0]).toMatchObject({mutation:{id:f.cleanup.id,expectedRevision:2n}});
});

it("observes permanent deletion independently of the removed resource and retains its original request",async()=>{
 const f=fixture();const rendered=render(f.view(true));await open();
 await waitFor(()=>expect((screen.getByRole("button",{name:"Permanently delete session"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Permanently delete session"}));
 expect(screen.getByText(/This cannot be undone or canceled/)).not.toBeNull();
 fireEvent.click(screen.getByRole("button",{name:"Confirm permanent session deletion"}));
 await screen.findByRole("button",{name:"Retry the same permanent deletion request"});
 fireEvent.click(screen.getByRole("button",{name:"Close Workspace storage and permanent deletion"}));rendered.rerender(f.view(false));
 fireEvent.click(screen.getByRole("button",{name:"Return to retained storage or deletion operation"}));
 fireEvent.click(screen.getByRole("button",{name:"Retry the same permanent deletion request"}));
 await screen.findByText(/Permanent deletion is pending original Worker/);
 expect(f.deletes[1]).toEqual(f.deletes[0]);expect(f.deletes[0]).toMatchObject({mutation:{id:f.session.id,expectedRevision:8n}});
 expect(f.getDeletion).toHaveBeenCalled();
 f.deletion.state=SessionDeletionState.SUCCEEDED;f.deletion.databaseRemoved=true;f.deletion.backupsRemoved=true;
 fireEvent.click(screen.getByRole("button",{name:"Refresh permanent deletion"}));
 await screen.findByText("Permanent deletion completed.");
 expect(f.remove).toHaveBeenCalledTimes(2);
});

it.each([{local:true},{sidechat:true},{supported:false}])("preserves Local/Sidechat authority and explicit older-server guidance (%j)",async(options)=>{
 const f=fixture(options);render(f.view(true));fireEvent.click(screen.getByRole("button",{name:"Workspace storage and permanent deletion"}));
 await waitFor(()=>expect(screen.getByRole("button",{name:"Finish storage view"})).not.toBeNull());
 expect(screen.queryByRole("button",{name:"Preview workspace usage"})).toBeNull();
 if(options.supported===false)expect(await screen.findByText("Update the connected server to permanently delete managed sessions.")).not.toBeNull();
 expect(f.request).not.toHaveBeenCalled();expect(f.remove).not.toHaveBeenCalled();
});

it.each(["failed", "canceled"] as const)("retains the restored predecessor after %s recovery without reopening", async(recoveryState)=>{
 const f=fixture({recoveryState});render(f.view(true));await open();
 await screen.findByText("Storage operation: uncertain. Acceptance does not establish native cleanup.");
 await waitFor(()=>expect((screen.getByRole("button",{name:"Reconcile original storage operation"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Reconcile original storage operation"}));
 fireEvent.click(screen.getByRole("button",{name:"Confirm selected storage action"}));
 fireEvent.click(await screen.findByRole("button",{name:"Retry the same storage request"}));
 await screen.findByText(`Storage operation: ${recoveryState}. Acceptance does not establish native cleanup.`);
 await waitFor(()=>expect((screen.getByRole("button",{name:"Reconcile original storage operation"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Reconcile original storage operation"}));
 fireEvent.click(screen.getByRole("button",{name:"Confirm selected storage action"}));
 await waitFor(()=>expect(f.requests).toHaveLength(3));
 expect(f.requests[2]).toMatchObject({action:WorkspaceStorageAction.RECOVER,recoveryJobId:f.job.id,mutation:{id:f.session.id,expectedRevision:9n}});
 expect(f.requests[1]).toEqual(f.requests[0]);
});

it.each([Code.NotFound, Code.Unavailable])("makes an externally removed session resettable only after an authenticated not-found result (%s)", async code => {
 const source=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:8n,documentJson:encode({name:"Original removed session",workspace:"general-chat",storage:{state:"pending",job_id:newRequestId()}})});
 const next=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({name:"Next session",workspace:"general-chat"})});
 const getDeletion=vi.fn(()=>{throw new ConnectError("no retained deletion status",Code.NotFound);});
 const mutate=vi.fn();
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.WORKSPACE_STORAGE_V1,SystemCapability.PERMANENT_SESSION_DELETION_V1]})});
  router.service(ResourceService,{getResource:request=>{if(request.id===source.id)throw new ConnectError("original session unavailable",code);return {resource:next};},listResources:()=>({resources:[]})});
  router.service(WorkspaceStorageService,{getWorkspaceStorageOperation:()=>{throw new ConnectError("original job gone",Code.NotFound);},requestWorkspaceStorage:mutate});
  router.service(SessionService,{getSessionDeletion:getDeletion});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionStorageProvider><SessionStorageAction source={source}/><SessionStorageAction source={next}/></SessionStorageProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getAllByRole("button",{name:"Workspace storage and permanent deletion"})[0]!);
 await waitFor(()=>expect(client.isFetching()).toBe(0));
 if(code===Code.NotFound){
  expect(await screen.findByText("This session is no longer available. Its deletion cleanup status is unavailable.")).not.toBeNull();
  expect(getDeletion).toHaveBeenCalled();
  expect(screen.queryByText("Permanent deletion completed.")).toBeNull();
  fireEvent.click(await screen.findByRole("button",{name:"Finish storage view"}));
  fireEvent.click(screen.getAllByRole("button",{name:"Workspace storage and permanent deletion"})[1]!);
  expect(await screen.findByText(new RegExp(`Next session.*${next.id}`))).not.toBeNull();
 }else{
  expect(screen.queryByRole("button",{name:"Finish storage view"})).toBeNull();
  expect(getDeletion).not.toHaveBeenCalled();
 }
 expect(mutate).not.toHaveBeenCalled();
});
