import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, PullRequestFixProfile, PullRequestFixService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { PRFixAction } from "./pr-fix";
import { defaultRemediationPolicy } from "./remediation-policy";

vi.mock("./configuration-fields", () => ({ ResourceChoice: ({ label, value, change, disabled }: { label: string; value: string; change: (value: string) => void; disabled: boolean }) => <label>{label}<input value={value} disabled={disabled} onChange={event => change(event.target.value)} /></label> }));

function fixture() {
 const project=newRequestId(), at="2026-09-28T00:00:00Z";
 const selection={repositoryId:newRequestId(),remoteRepositoryId:"37",pullRequestId:"53",number:"17"};
 const target={version:1,provider:"github.com",repository_id:selection.repositoryId,remote_repository_id:"37",repository_node_id:"R_37",owner:"fixture-owner",name:"repo",pull_request_id:"53",pull_request_node_id:"PR_53",number:"17",title:"Original",observed_at:at};
 const set=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.PROBLEM,revision:9007199254740993n,schemaVersion:1,documentJson:encode({version:1,type:"pull-request-set",target,feedback:{base_sha:"a".repeat(40),head_sha:"b".repeat(40),observed_at:at}})});
 const value={content_version:"c".repeat(64)};
 const row=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.PROBLEM,revision:9007199254740995n,schemaVersion:1,documentJson:encode(value)});
 const send=vi.fn(async(request:{requestId:string;documentJson:Uint8Array})=>{
  const original=JSON.parse(new TextDecoder().decode(request.documentJson));
  const id=newRequestId(),sessionId=newRequestId(),chainId=newRequestId();
  const session=create(ResourceSchema,{id:sessionId,sessionId,kind:EntityKind.SESSION,revision:1n,schemaVersion:1,projectId:project,documentJson:encode({project_id:project})});
  const problemSet=create(ResourceSchema,{...set,revision:set.revision+1n,documentJson:encode({...document(set),remediation:{id:chainId,sequence:1,automatic_attempts:0,resume_baseline:0,active_attempt_id:id}})});
  const attempt=create(ResourceSchema,{id,kind:EntityKind.PROBLEM,revision:3n,schemaVersion:1,documentJson:encode({version:1,type:"pull-request-remediation-attempt",set_id:set.id,chain_id:chainId,sequence:1,mode:"manual",state:"bound",policy:defaultRemediationPolicy(),problems:original.problems.map((p:{id:string;content_version:string})=>({id:p.id,content_version:p.content_version})),reserved:{actor_type:"owner",request_id:request.requestId,at},session_id:sessionId,input_id:newRequestId(),input_digest:"d".repeat(64),project_id:project,git_target:{version:1,target,head_repository:{provider:"github.com",id:"79",node_id:"R_fork",owner:"fixture-author",name:"fork",private:true},base_ref:"main",head_ref:"feature",base_sha:"a".repeat(40),head_sha:"b".repeat(40)}})});
  return {attempt,session,problemSet,requestId:request.requestId};
 });
 const capabilities=vi.fn(async()=>({profiles:[PullRequestFixProfile.CODEX_GIT_V1]}));
 const transport=createRouterTransport(router=>router.service(PullRequestFixService,{getPullRequestFixCapabilities:capabilities,requestPullRequestFix:send}));
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}}), refreshed=vi.fn();
 const view=(active=true)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{active?<PRFixAction row={row} set={set} value={value} selection={selection} disabled={false} refreshed={refreshed}/>:null}</MutationIntents></QueryClientProvider></TransportProvider>;
 const start=async()=>{fireEvent.click(screen.getByRole("button",{name:"Fix now"}));fireEvent.change(screen.getByLabelText("Fix project"),{target:{value:project}});await waitFor(()=>expect((screen.getByRole("button",{name:"Start fix"}) as HTMLButtonElement).disabled).toBe(false));fireEvent.click(screen.getByRole("button",{name:"Start fix"}));};
 return {project,selection,set,row,send,capabilities,view,start,refreshed};
}
it("binds exact decimal original revisions and an explicit project",async()=>{
 const f=fixture();render(f.view());expect(f.capabilities).not.toHaveBeenCalled();await f.start();await screen.findByText(/Fix accepted in session/);
 const wire=JSON.parse(new TextDecoder().decode(f.send.mock.calls[0][0].documentJson));
 expect(wire).toEqual({set_id:f.set.id,set_revision:"9007199254740993",project_id:f.project,repository_id:f.selection.repositoryId,problems:[{id:f.row.id,revision:"9007199254740995",content_version:"c".repeat(64)}]});expect(f.refreshed).toHaveBeenCalledOnce();
});
it("retains an uncertain original fix across navigation",async()=>{
 const f=fixture();f.send.mockRejectedValueOnce(new ConnectError("Lost original acknowledgment",Code.Unavailable));const v=render(f.view());await f.start();await screen.findByRole("button",{name:"Retry original fix request"});v.rerender(f.view(false));v.rerender(f.view());
 fireEvent.click(await screen.findByRole("button",{name:"Retry original fix request"}));await screen.findByText(/Fix accepted in session/);expect(f.send.mock.calls[1][0]).toEqual(f.send.mock.calls[0][0]);
});
it("retains original request after malformed acknowledgment even after unmount",async()=>{
 const f=fixture();let complete!:(v:any)=>void;f.send.mockImplementationOnce(()=>new Promise(resolve=>{complete=resolve}));const v=render(f.view());await f.start();await waitFor(()=>expect(f.send).toHaveBeenCalledOnce());v.rerender(f.view(false));complete({requestId:f.send.mock.calls[0][0].requestId});
 await new Promise(resolve=>setTimeout(resolve,0));v.rerender(f.view());fireEvent.click(await screen.findByRole("button",{name:"Retry original fix request"}));await screen.findByText(/Fix accepted in session/);expect(f.send.mock.calls[1][0]).toEqual(f.send.mock.calls[0][0]);
});
it("keeps work unavailable without the typed supported capability",async()=>{
 const f=fixture();f.capabilities.mockResolvedValueOnce({profiles:[]});render(f.view());fireEvent.click(screen.getByRole("button",{name:"Fix now"}));fireEvent.change(screen.getByLabelText("Fix project"),{target:{value:f.project}});await screen.findByText(/no supported manual PR fix profile/);expect((screen.getByRole("button",{name:"Start fix"}) as HTMLButtonElement).disabled).toBe(true);expect(f.send).not.toHaveBeenCalled();
});
