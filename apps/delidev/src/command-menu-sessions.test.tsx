// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { EntityKind, ResourceSchema, ResourceService, SessionService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { commandSession } from "./command-menu-sessions";
import { CommandGroup, CommandMenu } from "./command-menu";
import { ShortcutProvider } from "./shortcut-provider";
import { encode } from "./documents";
const row=(name:string,projectId="",extra={})=>create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,revision:1n,schemaVersion:1,projectId,documentJson:encode({name,workspace:"worktree",...extra})});
beforeEach(()=>{vi.stubGlobal("ResizeObserver",class{observe(){}disconnect(){}unobserve(){}});HTMLElement.prototype.scrollIntoView=()=>{};});
function mount(read:(token:string)=>Promise<{sessions:Resource[];nextPageToken:string}>|{sessions:Resource[];nextPageToken:string},project:(id:string)=>Promise<Resource>|Resource=()=>{throw new Error("unavailable");}) {
 const requests:string[]=[],opened=vi.fn(),client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const transport=createRouterTransport(router=>{router.service(SessionService,{listSessions:request=>{expect(request.projectId).toBe("");expect(request.includeArchived).toBe(false);expect(request.pageSize).toBe(50);requests.push(request.pageToken);return read(request.pageToken);}});router.service(ResourceService,{getResource:async request=>({resource:await project(request.id)})});});
 function Fixture(){const [visible,setVisible]=useState(true);return <><button onClick={()=>setVisible(true)}>Open</button>{visible?<CommandMenu close={()=>setVisible(false)} openSession={opened} commands={[{value:"create:one",group:CommandGroup.Create,label:"Static command",run:vi.fn()}]}/>:null}</>;}
 render(<QueryClientProvider client={client}><TransportProvider transport={transport}><ShortcutProvider><Fixture/></ShortcutProvider></TransportProvider></QueryClientProvider>);return {requests,opened,client};
}
it("projects only title/routing metadata and rejects invalid resources",()=>{const source=row("Title","",{secret:"private",title_state:"generating",transcript:"ignored"});expect(commandSession(source)).toEqual({id:source.id,revision:1n,title:"Title",projectId:"",conversationKind:"work-session"});expect(commandSession({...source,id:"bad"})).toBeUndefined();expect(commandSession({...source,revision:0n})).toBeUndefined();expect(commandSession(row("a".repeat(257)))).toBeUndefined();});
it("does not read on empty/whitespace, visits all 125 pages once and searches titles only",async()=>{
 const target=row("Needle","",{transcript:"private needle",title_state:"state-needle"});const {requests,client}=mount(token=>{const index=Number(token||0);return {sessions:[index===124?target:row(`Page ${index}`)],nextPageToken:index<124?String(index+1):""};});
 const input=screen.getByRole("combobox");expect(requests).toEqual([]);fireEvent.change(input,{target:{value:" "}});expect(requests).toEqual([]);
 // Accepted pages deliberately yield to React through a timer. Drive those
 // 125 serial turns explicitly: CI CPU contention can exceed findByRole's
 // one-second wall clock even while traversal continues correctly.
 vi.useFakeTimers({toFake:["setTimeout","clearTimeout"]});
 try {
  fireEvent.change(input,{target:{value:"Needle"}});
  for(let page=0;page<125&&requests.length<125;page++)await act(async()=>{await vi.advanceTimersByTimeAsync(1);});
  expect(requests).toHaveLength(125);expect(screen.getByRole("option",{name:"Needle"})).toBeTruthy();expect(new Set(requests).size).toBe(125);expect(client.getQueryCache().getAll()).toHaveLength(0);
  fireEvent.change(input,{target:{value:target.id}});expect(screen.getByText("No matching sessions.")).toBeTruthy();expect(requests).toHaveLength(125);fireEvent.change(input,{target:{value:"state-needle"}});expect(screen.queryByRole("option",{name:"Needle"})).toBeNull();fireEvent.change(input,{target:{value:" "}});expect(screen.queryByText("No matching sessions.")).toBeNull();expect(requests).toHaveLength(125);
 }finally{vi.useRealTimers();}
});
it("retains partial results, retries the exact failed token, and closes before opening once",async()=>{
 const first=row("Match first"),second=row("Match second");let fail=true;const {requests,opened}=mount(token=>{if(!token)return {sessions:[first],nextPageToken:"next"};if(fail)throw new ConnectError("temporary",Code.Unavailable);return {sessions:[second],nextPageToken:""};});
 fireEvent.change(screen.getByRole("combobox"),{target:{value:"Match"}});await screen.findByRole("button",{name:"Retry"});expect(screen.getByRole("option",{name:"Match first"})).toBeTruthy();expect(screen.queryByText("No matching sessions.")).toBeNull();fail=false;fireEvent.click(screen.getByRole("button",{name:"Retry"}));await screen.findByRole("option",{name:"Match second"});expect(requests).toEqual(["","next","next"]);fireEvent.click(screen.getByRole("option",{name:"Match second"}));expect(screen.queryByRole("dialog")).toBeNull();expect(opened).toHaveBeenCalledExactlyOnceWith(second.id,undefined,"Match second");
});
it("stalled cursors need explicit reload and dispose late responses on close",async()=>{
 let resolve!:(value:{sessions:Resource[];nextPageToken:string})=>void;let slow=false;const {requests}=mount(()=>slow?new Promise(done=>{resolve=done;}):{sessions:[row("Match")],nextPageToken:"same"});fireEvent.change(screen.getByRole("combobox"),{target:{value:"Match"}});await screen.findByRole("button",{name:"Reload"});expect(requests).toEqual(["","same"]);slow=true;fireEvent.click(screen.getByRole("button",{name:"Reload"}));await waitFor(()=>expect(requests).toEqual(["","same",""]));fireEvent.click(screen.getByRole("button",{name:"Close command menu"}));await act(async()=>resolve({sessions:[row("Late")],nextPageToken:"tail"}));expect(requests).toHaveLength(3);expect(screen.queryByRole("dialog")).toBeNull();
});
it("loads at most eight project labels concurrently without blocking duplicate titles or selection",async()=>{
 const ids=Array.from({length:10},()=>newRequestId()),sessions=ids.map(id=>row("Duplicate",id));const waits=new Map<string,(row:Resource)=>void>();const {opened}=mount(()=>({sessions,nextPageToken:""}),id=>new Promise(done=>waits.set(id,done)));
 fireEvent.change(screen.getByRole("combobox"),{target:{value:"Duplicate"}});await waitFor(()=>expect(screen.getAllByRole("option")).toHaveLength(10));await waitFor(()=>expect(waits.size).toBe(8));const selected=document.querySelector('[data-selected="true"]')?.getAttribute("data-value");
 await act(async()=>waits.get(ids[0])!(create(ResourceSchema,{id:ids[0],kind:EntityKind.PROJECT,revision:1n,schemaVersion:1,documentJson:encode({name:"Current project"})})));await screen.findByText("Current project");expect(document.querySelector('[data-selected="true"]')?.getAttribute("data-value")).toBe(selected);await waitFor(()=>expect(waits.size).toBe(9));fireEvent.click(screen.getAllByRole("option")[0]);expect(opened).toHaveBeenCalledTimes(1);
});

it.each([{reload:false,label:"Retry"},{reload:true,label:"Reload"}])("keeps focused $label Enter separate from session selection",async ({reload})=>{
 let fail=true;const selected=row("Match first");const {requests,opened}=mount(token=>{if(reload)return {sessions:[],nextPageToken:fail?"same":""};if(!token)return {sessions:[selected],nextPageToken:"next"};if(fail)throw new ConnectError("temporary",Code.Unavailable);return {sessions:[],nextPageToken:""};});
 fireEvent.change(screen.getByRole("combobox"),{target:{value:"Match"}});const action=await screen.findByRole("button",{name:reload?"Reload":"Retry"});action.focus();const before=[...requests];expect(fireEvent.keyDown(action,{key:"Enter"})).toBe(true);expect(opened).not.toHaveBeenCalled();expect(requests).toEqual(before);expect(screen.getByRole("dialog")).toBeTruthy();
 // Native button activation is a click; jsdom requires that second event.
 fail=false;fireEvent.click(action);await waitFor(()=>expect(requests).toHaveLength(before.length+1));expect(requests.at(-1)).toBe(reload?"":"next");expect(opened).not.toHaveBeenCalled();expect(screen.getByRole("dialog")).toBeTruthy();
});
