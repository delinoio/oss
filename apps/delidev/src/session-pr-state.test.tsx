// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render,screen,waitFor } from "@testing-library/react";
import { expect,it,vi } from "vitest";
import { EntityKind,IntegrationService,ResourceService,ResourceSchema,newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionPRState,SessionPRReadQueue,SessionPRStatus,sessionPRObservation } from "./session-pr-state";
function fixture(){
 const profile=newRequestId(),repository=create(ResourceSchema,{id:newRequestId(),revision:1n,kind:EntityKind.REPOSITORY,schemaVersion:1,documentJson:encode({integration_id:profile,github_owner:"owner",github_name:"repo"})});
 const association={repository_id:repository.id,remote_repository_id:"37",repository_node_id:"R_37",owner:"owner",name:"repo",pull_request_id:"9007199254740993",pull_request_node_id:"PR_17",number:"17",observed_at:"2026-09-28T00:00:00Z"};
 const item={provider:"github.com",kind:"pull-request",identity_source:"pull-request-api",id:association.pull_request_id,node_id:"PR_17",number:"17",title:"Original",state:"open",created_at:"2026-09-01T00:00:00Z",updated_at:"2026-09-28T00:00:00Z",url:"https://github.com/owner/repo/pull/17",draft:false,body:"",merged:false,base_ref:"main",head_ref:"feature",base_sha:"a".repeat(40),head_sha:"b".repeat(40)};
 const observation={repository_id:repository.id,repository_revision:"1",profile_id:profile,generation_id:newRequestId(),observed_at:"2026-09-28T00:00:01Z",identity:{id:"17",node_id:"U_17",login:"reader"},repository:{provider:"github.com",id:"37",node_id:"R_37",owner:"owner",name:"repo",private:true},query:{kind:"pull-request",operation:"detail",number:"17"},items:[item]};
 return{repository,association,item,observation};
}
it.each(["open","merged","closed"])("renders validated %s state only through icon/dot and accessible text",async(kind)=>{
 const f=fixture();f.item.state=kind==="open"?"open":"closed";f.item.merged=kind==="merged";
 const read=vi.fn(async()=>({schemaVersion:1,documentJson:encode(f.observation)}));
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:()=>({resource:f.repository})});router.service(IntegrationService,{queryRepositoryIntegration:read});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}}),row=create(ResourceSchema,{id:newRequestId(),sessionId:newRequestId(),kind:EntityKind.PULL_REQUEST,revision:1n});
 const view=render(<TransportProvider transport={transport}><QueryClientProvider client={client}><SessionPRStatus row={row} association={f.association} active queue={new SessionPRReadQueue()}/></QueryClientProvider></TransportProvider>);
 const label=kind==="open"?"Open pull request":kind==="merged"?"Merged pull request":"Closed pull request without merge";
 const icon=await screen.findByRole("img",{name:label});expect(icon.getAttribute("data-pr-state")).toBe(kind);expect(icon.querySelector(".session-pr-status-dot")).toBeTruthy();expect(screen.queryByText(label)).toBeNull();expect(icon.getAttribute("title")).toBe(label);expect(read).toHaveBeenCalledOnce();view.unmount();client.clear();
});
it.each(["repository","pr","node","stale","contradictory"])("rejects %s observation without replacing original identity",kind=>{
 const f=fixture();if(kind==="repository")f.observation.repository.id="38";if(kind==="pr")f.item.id="99";if(kind==="node")f.item.node_id="PR_foreign";if(kind==="stale")f.observation.observed_at="2026-09-27T00:00:00Z";if(kind==="contradictory")f.item.merged=true;
 expect(sessionPRObservation(encode(f.observation),f.repository,f.association)).toBeUndefined();
});
it("limits in-flight reads to four and drops a canceled queued row",async()=>{
 const queue=new SessionPRReadQueue(),controllers=Array.from({length:7},()=>new AbortController());let active=0,max=0;const release:(()=>void)[]=[];
 const reads=controllers.map((controller,i)=>queue.run(async()=>{active++;max=Math.max(max,active);await new Promise<void>(resolve=>release.push(resolve));active--;return i;},controller.signal).catch(()=>-1));
 await waitFor(()=>expect(release).toHaveLength(4));controllers[5].abort();release.splice(0).forEach(done=>done());await waitFor(()=>expect(release).toHaveLength(2));release.splice(0).forEach(done=>done());expect(await Promise.all(reads)).toEqual([0,1,2,3,4,-1,6]);expect(max).toBe(4);
});
it("inactive row has a neutral icon and performs no read",async()=>{
 const f=fixture(),read=vi.fn();const transport=createRouterTransport(router=>router.service(IntegrationService,{queryRepositoryIntegration:read}));const client=new QueryClient();const row=create(ResourceSchema,{id:newRequestId(),sessionId:newRequestId(),revision:1n});const view=render(<TransportProvider transport={transport}><QueryClientProvider client={client}><SessionPRStatus row={row} association={f.association} active={false} queue={new SessionPRReadQueue()}/></QueryClientProvider></TransportProvider>);expect(screen.getByRole("img",{name:"Pull request state unavailable"}).getAttribute("data-pr-state")).toBe(SessionPRState.Unavailable);await Promise.resolve();expect(read).not.toHaveBeenCalled();view.unmount();client.clear();
});
