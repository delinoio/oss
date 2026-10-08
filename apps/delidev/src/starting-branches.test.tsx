// SPDX-License-Identifier: Apache-2.0
import {create} from "@bufbuild/protobuf";
import {createRouterTransport} from "@connectrpc/connect";
import {TransportProvider} from "@connectrpc/connect-query";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {fireEvent,render,screen,waitFor} from "@testing-library/react";
import {expect,it,vi} from "vitest";
import {EntityKind,ResourceSchema,ResourceService,WorkerService,newRequestId,type Resource} from "@delinoio/delidev-api-client";
import {encode} from "./documents";
import {readBranchInventory,StartingBranches} from "./starting-branches";

const resource=(kind:EntityKind,value:unknown)=>create(ResourceSchema,{kind,id:newRequestId(),schemaVersion:1,revision:1n,documentJson:encode(value)});
function fixture(){
 const repositories=[resource(EntityKind.REPOSITORY,{name:"primary",remote_url:"https://github.com/fixture/one.git"}),resource(EntityKind.REPOSITORY,{name:"secondary",remote_url:"https://github.com/fixture/two.git"})];
 const project=resource(EntityKind.PROJECT,{repositories:repositories.map(row=>row.id),primary_repository:repositories[0].id});
 const machine=resource(EntityKind.MACHINE,{worker_capabilities:["repository-branch-discovery-v1"]});
 const job=(repository:Resource,branches=["feature/topic","main"])=>resource(EntityKind.JOB,{type:"discover-repository-branches",state:"succeeded",input:{project_id:project.id,project_revision:1,repository_id:repository.id,repository_revision:1,machine_id:machine.id,machine_revision:1,remote:"upstream"},output:{project_id:project.id,project_revision:1,repository_id:repository.id,repository_revision:1,machine_id:machine.id,machine_revision:1,remote:"upstream",branches,observed_at:"2026-10-08T00:00:00Z"}});
 const discover=vi.fn(async (request:{repositoryId:string})=>({job:job(repositories.find(row=>row.id===request.repositoryId)!)}));
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:request=>({resource:[...repositories,machine].find(row=>row.id===request.id)})});router.service(WorkerService,{discoverRepositoryBranches:discover});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=(starting:unknown[]=[],change=vi.fn(),supported=true,selectedProject=project)=><TransportProvider transport={transport}><QueryClientProvider client={client}><StartingBranches project={selectedProject} machineId={machine.id} starting={starting} change={change} active supported={supported}/></QueryClientProvider></TransportProvider>;
 return {project,machine,repositories,job,discover,view};
}
async function openFirst(){const summary=screen.getAllByText(/Starting branch:/)[0];const details=summary.closest("details")!;details.open=true;fireEvent(details,new Event("toggle"));await screen.findByRole("option",{name:"feature/topic"});}
it("uses the selected Worker and serializes a remote override without changing saved configuration",async()=>{
 const f=fixture(),change=vi.fn();render(f.view([],change));await openFirst();
 fireEvent.change(screen.getAllByLabelText("Search branches")[0],{target:{value:"feature"}});
 fireEvent.change(screen.getAllByLabelText("Starting branch")[0],{target:{value:"feature/topic"}});
 expect(change).toHaveBeenCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/topic"}}]);
 expect(f.discover.mock.calls[0][0]).toMatchObject({repositoryId:f.repositories[0].id,machineId:f.machine.id});
});
it("refresh preserves a disappeared explicit branch and independent secondary override",async()=>{
 const f=fixture(),change=vi.fn(),starting=[{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/topic"}},{repository_id:f.repositories[1].id,reference:{type:"commit",name:"original"}}];
 render(f.view(starting,change));await openFirst();f.discover.mockImplementationOnce(async()=>({job:f.job(f.repositories[0],["main"])}));
 fireEvent.click(screen.getAllByRole("button",{name:"Refresh"})[0]);await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(2));await screen.findByRole("option",{name:/feature\/topic.*unavailable/});
 expect(change).not.toHaveBeenCalled();
});
it("retains saved/manual references when peers do not negotiate discovery",async()=>{
 const f=fixture();render(f.view([],vi.fn(),false));
 const details=screen.getAllByText(/Starting branch:/)[0].closest("details")!;details.open=true;fireEvent(details,new Event("toggle"));
 expect((await screen.findAllByText(/Branch discovery is unavailable/)).length).toBeGreaterThan(0);expect(f.discover).not.toHaveBeenCalled();
});
it("rejects stale, malformed and incomplete inventories without a generic document-limit increase",()=>{
 const f=fixture(),identity={project:f.project,repository:f.repositories[0],machine:f.machine};
 expect(readBranchInventory(f.job(f.repositories[0]),identity).branches).toEqual(["feature/topic","main"]);
 expect(readBranchInventory(f.job(f.repositories[0],["\uE000","😀"]),identity).branches).toEqual(["\uE000","😀"]);
 expect(()=>readBranchInventory(f.job(f.repositories[1]),identity)).toThrow(/Stale/);
 expect(()=>readBranchInventory(f.job(f.repositories[0],["main","feature/topic"]),identity)).toThrow(/Malformed/);
 expect(()=>readBranchInventory(f.job(f.repositories[0],["bad..branch"]),identity)).toThrow(/Malformed/);
});

it("discards a held lookup after project authority changes",async()=>{
 const f=fixture();let release!: (value:{job:Resource})=>void;
 f.discover.mockImplementationOnce(()=>new Promise(resolve=>{release=resolve;}));
 const rendered=render(f.view());const details=screen.getAllByText(/Starting branch:/)[0].closest("details")!;
 details.open=true;fireEvent(details,new Event("toggle"));await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(1));
 const changed=create(ResourceSchema,{...f.project,revision:2n});rendered.rerender(f.view([],vi.fn(),true,changed));
 release({job:f.job(f.repositories[0])});await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(2));
 expect(screen.queryByRole("option",{name:"feature/topic"})).toBeNull();
});
it("keeps repository selections independent and ordered",async()=>{
 const f=fixture(),change=vi.fn();const primary={repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/topic"}};
 render(f.view([primary],change));const details=screen.getAllByText(/Starting branch:/)[1].closest("details")!;details.open=true;fireEvent(details,new Event("toggle"));
 await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(1));await screen.findByRole("option",{name:"feature/topic"});
 fireEvent.change(screen.getAllByLabelText("Starting branch")[1],{target:{value:"main"}});
 expect(change).toHaveBeenCalledWith([primary,{repository_id:f.repositories[1].id,reference:{type:"remote-branch",remote:"upstream",name:"main"}}]);
});
