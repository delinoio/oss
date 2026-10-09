// SPDX-License-Identifier: Apache-2.0
import {create} from "@bufbuild/protobuf";
import {createRouterTransport} from "@connectrpc/connect";
import {TransportProvider} from "@connectrpc/connect-query";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {act,fireEvent,render,screen,waitFor} from "@testing-library/react";
import {beforeEach,expect,it,vi} from "vitest";
import {EntityKind,ResourceSchema,ResourceService,WorkerService,newRequestId,type Resource} from "@delinoio/delidev-api-client";
import {encode} from "./documents";
import {readBranchInventory,StartingBranches,validStartingBranch} from "./starting-branches";

beforeEach(()=>{vi.stubGlobal("ResizeObserver",class{observe(){}disconnect(){}unobserve(){}});HTMLElement.prototype.scrollIntoView=()=>{};});
const resource=(kind:EntityKind,value:unknown)=>create(ResourceSchema,{kind,id:newRequestId(),schemaVersion:1,revision:1n,documentJson:encode(value)});
function fixture(){
 const repositories=[resource(EntityKind.REPOSITORY,{name:"primary",preferred_remote:"upstream",remote_url:"https://github.com/fixture/one.git"}),resource(EntityKind.REPOSITORY,{name:"secondary",preferred_remote:"upstream",remote_url:"https://github.com/fixture/two.git"})];
 const project=resource(EntityKind.PROJECT,{repositories:repositories.map(row=>row.id),primary_repository:repositories[0].id});
 const machine=resource(EntityKind.MACHINE,{worker_capabilities:["repository-branch-discovery-v1"]});
 const job=(repository:Resource,branches=["feature/topic","main"])=>resource(EntityKind.JOB,{type:"discover-repository-branches",state:"succeeded",input:{project_id:project.id,project_revision:1,repository_id:repository.id,repository_revision:1,machine_id:machine.id,machine_revision:1,remote:"upstream"},output:{project_id:project.id,project_revision:1,repository_id:repository.id,repository_revision:1,machine_id:machine.id,machine_revision:1,remote:"upstream",branches,observed_at:"2026-10-08T00:00:00Z"}});
 const discover=vi.fn(async (request:{repositoryId:string})=>({job:job(repositories.find(row=>row.id===request.repositoryId)!)}));
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:request=>({resource:[...repositories,machine].find(row=>row.id===request.id)})});router.service(WorkerService,{discoverRepositoryBranches:discover});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=(starting:unknown[]=[],change=vi.fn(),supported=true,selectedProject=project,validity?: (valid:boolean)=>void)=><TransportProvider transport={transport}><QueryClientProvider client={client}><StartingBranches validity={validity} project={selectedProject} machineId={machine.id} starting={starting} change={change} active supported={supported}/></QueryClientProvider></TransportProvider>;
 return {project,machine,repositories,job,discover,view};
}
async function openFirst(){const control=screen.getByRole("combobox",{name:"Starting branch"});control.focus();fireEvent.focus(control);await screen.findByRole("option",{name:"feature/topic"});}
function openAdditional(){const details=document.querySelector('.starting-branches > details')! as HTMLDetailsElement;details.open=true;fireEvent(details,new Event("toggle"));}

it("uses the selected Worker and serializes a remote override without changing saved configuration",async()=>{
 const f=fixture(),change=vi.fn();render(f.view([],change));await openFirst();
 fireEvent.change(screen.getByRole("combobox",{name:"Starting branch"}),{target:{value:"feature"}});fireEvent.click(screen.getByRole("option",{name:"feature/topic"}));
 expect(change).toHaveBeenCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/topic"}}]);
 expect(f.discover.mock.calls[0][0]).toMatchObject({repositoryId:f.repositories[0].id,machineId:f.machine.id});
});
it("refresh preserves a disappeared explicit branch and independent secondary override",async()=>{
 const f=fixture(),change=vi.fn(),starting=[{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/topic"}},{repository_id:f.repositories[1].id,reference:{type:"commit",name:"original"}}];
 render(f.view(starting,change));await openFirst();f.discover.mockImplementationOnce(async()=>({job:f.job(f.repositories[0],["main"])}));
 fireEvent.click(screen.getAllByRole("button",{name:"Refresh"})[0]);await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(2));await screen.findByText(/Selected branch unavailable/);expect((screen.getByRole("combobox",{name:"Starting branch"}) as HTMLInputElement).value).toBe("feature/topic");
 expect(change).not.toHaveBeenCalled();
});
it("retains saved/manual references when peers do not negotiate discovery",async()=>{
 const f=fixture();render(f.view([],vi.fn(),false));
 fireEvent.focus(screen.getByRole("combobox",{name:"Starting branch"}));
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
 const rendered=render(f.view());fireEvent.focus(screen.getByRole("combobox",{name:"Starting branch"}));await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(1));
 const changed=create(ResourceSchema,{...f.project,revision:2n});rendered.rerender(f.view([],vi.fn(),true,changed));
 release({job:f.job(f.repositories[0])});await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(2));
 expect(screen.queryByRole("option",{name:"feature/topic"})).toBeNull();
});
it("keeps repository selections independent and ordered",async()=>{
 const f=fixture(),change=vi.fn();const primary={repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/topic"}};
 render(f.view([primary],change));openAdditional();fireEvent.focus(screen.getByRole("combobox",{name:/Starting branch ·/}));
 await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(1));await screen.findByRole("option",{name:"feature/topic"});
 fireEvent.change(screen.getByRole("combobox",{name:/Starting branch ·/}),{target:{value:"main"}});
 expect(change).toHaveBeenCalledWith([primary,{repository_id:f.repositories[1].id,reference:{type:"remote-branch",remote:"upstream",name:"main"}}]);
});
it("accepts direct branches without discovery and rejects invalid byte/syntax drafts",async()=>{
 const f=fixture(),change=vi.fn(),validity=vi.fn();render(f.view([],change,false,f.project,validity));const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.focus(input);await screen.findByText(/Branch discovery is unavailable/);fireEvent.change(input,{target:{value:"feature/new"}});await waitFor(()=>expect(change).toHaveBeenLastCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/new"}}]));const count=change.mock.calls.length;
 fireEvent.change(input,{target:{value:"bad..branch"}});expect(input.getAttribute("aria-invalid")).toBe("true");expect(validity).toHaveBeenLastCalledWith(false);expect(change).toHaveBeenCalledTimes(count);expect((input as HTMLInputElement).checkValidity()).toBe(false);fireEvent.change(input,{target:{value:"한".repeat(342)}});expect(validity).toHaveBeenLastCalledWith(false);fireEvent.change(input,{target:{value:""}});expect(change).toHaveBeenLastCalledWith([]);expect(validity).toHaveBeenLastCalledWith(true);expect(f.discover).not.toHaveBeenCalled();
});
it.each(["bad..branch","@","-leading","/absolute","trailing/","x.lock","x/.hidden","x@{y}","a b","a".repeat(1025)])("checks Git branch syntax %s",name=>expect(validStartingBranch(name)).toBe(false));
it("preserves manual commit/custom remote on focus Refresh and Enter; explicit typing replaces only its repository",async()=>{
 const f=fixture(),change=vi.fn(),secondary={repository_id:f.repositories[1].id,reference:{type:"commit",name:"original"}},manual={repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"custom",name:"main"}};render(f.view([manual,secondary],change));fireEvent.focus(screen.getByRole("combobox",{name:"Starting branch"}));await screen.findByRole("option",{name:"main"});fireEvent.click(screen.getByRole("button",{name:"Refresh"}));await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(2));const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.keyDown(input,{key:"Enter"});expect(change).not.toHaveBeenCalled();fireEvent.change(input,{target:{value:"new-branch"}});expect(change).toHaveBeenLastCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"new-branch"}},secondary]);
});
it("keeps typing local, contains IME/Enter, retains input focus, and ignores disabled ancestors",async()=>{
 const f=fixture(),change=vi.fn(),validity=vi.fn();const view=render(<form onSubmit={event=>{event.preventDefault();throw new Error("Unexpected submit");}}>{f.view([],change,true,f.project,validity)}</form>);await openFirst();const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.keyDown(input,{key:"ArrowDown"});expect(input.getAttribute("aria-activedescendant")).toBeTruthy();fireEvent.compositionStart(input);fireEvent.change(input,{target:{value:"typed-ime"}});expect(change).not.toHaveBeenCalled();expect(validity).toHaveBeenLastCalledWith(false);fireEvent.keyDown(input,{key:"Enter",keyCode:229});expect(change).not.toHaveBeenCalled();fireEvent.compositionEnd(input);expect(change).toHaveBeenCalledTimes(1);fireEvent.change(input,{target:{value:"typed-other"}});expect(f.discover).toHaveBeenCalledTimes(1);fireEvent.keyDown(input,{key:"Escape"});expect(input.getAttribute("aria-expanded")).toBe("false");expect(document.activeElement).toBe(input);fireEvent.focus(input);fireEvent.keyDown(input,{key:"Tab"});expect(input.getAttribute("aria-expanded")).toBe("false");view.unmount();change.mockClear();render(<fieldset disabled>{f.view([],change)}</fieldset>);const blocked=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.change(blocked,{target:{value:"blocked"}});fireEvent.focus(blocked);expect(change).not.toHaveBeenCalled();expect(blocked.getAttribute("aria-expanded")).toBe("false");
});
it("uses origin when unset and keeps valid direct input after failed or empty discovery",async()=>{
 const f=fixture(),change=vi.fn();f.repositories[0].documentJson=encode({name:"primary"});f.discover.mockRejectedValueOnce(new Error("lookup unavailable"));render(f.view([],change));const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.focus(input);await waitFor(()=>expect(f.discover).toHaveBeenCalledTimes(1));fireEvent.change(input,{target:{value:"not-discovered"}});await waitFor(()=>expect(change).toHaveBeenLastCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"origin",name:"not-discovered"}}]));f.discover.mockResolvedValueOnce({job:f.job(f.repositories[0],[])});fireEvent.click(screen.getByRole("button",{name:"Refresh"}));await screen.findByText("The remote has no branches.");expect((input as HTMLInputElement).value).toBe("not-discovered");fireEvent.change(input,{target:{value:"still-direct"}});expect(change).toHaveBeenLastCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"origin",name:"still-direct"}}]);
});


it.each(["@", "-leading"])("blocks direct creation for server-rejected branch %s", async name => {
 const f=fixture(),change=vi.fn(),validity=vi.fn();render(f.view([],change,false,f.project,validity));
 const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.focus(input);await screen.findByText(/Branch discovery is unavailable/);
 fireEvent.change(input,{target:{value:name}});
 expect(input.getAttribute("aria-invalid")).toBe("true");expect(validity).toHaveBeenLastCalledWith(false);expect(change).not.toHaveBeenCalled();
});


it("retains the typed invalid draft when Enter cannot commit a disabled candidate", async () => {
 const f=fixture(),change=vi.fn(),validity=vi.fn();let release!:()=>void;
 const pending=new Promise<void>(resolve=>{release=resolve;});
 const transport=createRouterTransport(router=>router.service(ResourceService,{getResource:async request=>{
  if(request.id===f.repositories[0].id)await pending;
  return {resource:[...f.repositories,f.machine].find(row=>row.id===request.id)};
 }}));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><StartingBranches project={f.project} machineId={f.machine.id} starting={[]} change={change} validity={validity} active supported={false}/></QueryClientProvider></TransportProvider>);
 const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.focus(input);fireEvent.change(input,{target:{value:"feature/new"}});
 const candidate=screen.getByRole("option",{name:'Use "feature/new"'});expect((candidate as HTMLButtonElement).disabled).toBe(true);
 fireEvent.keyDown(input,{key:"ArrowDown"});fireEvent.keyDown(input,{key:"ArrowDown"});fireEvent.keyDown(input,{key:"Enter"});
 expect(change).not.toHaveBeenCalled();expect((input as HTMLInputElement).value).toBe("feature/new");expect(input.getAttribute("aria-invalid")).toBe("true");expect(validity).toHaveBeenLastCalledWith(false);
 await act(async()=>{release();});
 await waitFor(()=>expect(change).toHaveBeenLastCalledWith([{repository_id:f.repositories[0].id,reference:{type:"remote-branch",remote:"upstream",name:"feature/new"}}]));
 expect(validity).toHaveBeenLastCalledWith(true);
});


it.each(["\u00a0", "\u0085", "\u1680", "\u2000\u200a", "\u2028\u2029", "\u202f", "\u205f", "\u3000"])("rejects server-invalid whitespace-only branch %j without committing", async name => {
 const f=fixture(),change=vi.fn(),validity=vi.fn();render(f.view([],change,false,f.project,validity));
 const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.focus(input);await screen.findByText(/Branch discovery is unavailable/);
 fireEvent.change(input,{target:{value:name}});
 expect(validStartingBranch(name)).toBe(false);expect(change).not.toHaveBeenCalled();expect(input.getAttribute("aria-invalid")).toBe("true");expect(validity).toHaveBeenLastCalledWith(false);
});
it("preserves nonempty branch spelling instead of trimming Unicode whitespace",()=>{expect(validStartingBranch("\u00a0topic\u00a0")).toBe(true);});


it.each(["Escape", "Tab", "outside"])("keeps invalid-draft guidance visible after %s dismissal", async dismissal => {
 const f=fixture(),change=vi.fn(),validity=vi.fn();render(f.view([],change,false,f.project,validity));
 const input=screen.getByRole("combobox",{name:"Starting branch"});fireEvent.focus(input);await screen.findByText(/Branch discovery is unavailable/);
 fireEvent.change(input,{target:{value:"bad..branch"}});
 if(dismissal==="outside")fireEvent.pointerDown(document.body);else fireEvent.keyDown(input,{key:dismissal});
 expect(input.getAttribute("aria-expanded")).toBe("false");expect(input.getAttribute("aria-invalid")).toBe("true");expect((input as HTMLInputElement).value).toBe("bad..branch");
 expect(screen.getByRole("alert").hidden).toBe(false);expect(screen.getByRole("alert").id).toBe(input.getAttribute("aria-describedby"));
 expect(change).not.toHaveBeenCalled();expect(validity).toHaveBeenLastCalledWith(false);
 fireEvent.change(input,{target:{value:""}});fireEvent.keyDown(input,{key:"Escape"});expect(screen.queryByRole("alert")).toBeNull();
});


it("clamps the existing popup at both viewport edges after anchor scrolling", async()=>{
 const f=fixture(),change=vi.fn();render(f.view([],change));await openFirst();
 const input=screen.getByRole("combobox",{name:"Starting branch"}),panel=document.querySelector<HTMLElement>(".starting-branch-popup")!;
 Object.defineProperty(panel,"offsetHeight",{configurable:true,value:180});Object.defineProperty(panel,"offsetWidth",{configurable:true,value:240});Object.defineProperty(panel,"scrollHeight",{configurable:true,value:180});
 const bounds=vi.spyOn(input,"getBoundingClientRect");
 bounds.mockReturnValue({top:-140,bottom:-100,left:20,right:260,width:240,height:40,x:20,y:-140,toJSON:()=>({})});
 fireEvent.scroll(window);expect(Number.parseFloat(panel.style.top)).toBe(8);
 bounds.mockReturnValue({top:window.innerHeight+100,bottom:window.innerHeight+140,left:20,right:260,width:240,height:40,x:20,y:window.innerHeight+100,toJSON:()=>({})});
 fireEvent.scroll(window);expect(Number.parseFloat(panel.style.top)+panel.offsetHeight).toBeLessThanOrEqual(window.innerHeight-8);
 expect(panel).toBe(document.querySelector(".starting-branch-popup"));expect(screen.getByRole("button",{name:"Refresh"})).toBeTruthy();expect(change).not.toHaveBeenCalled();expect(f.discover).toHaveBeenCalledOnce();
 bounds.mockRestore();
});
