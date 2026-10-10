// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { createQueryOptions, TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { EntityKind, InboxQuery, InboxService, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionInboxReadInvalidation, useSessionInboxRead } from "./session-inbox-read";
const native = vi.hoisted(() => ({ desktop: true }));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => native.desktop }));
let focused = true, visible = true;
beforeEach(() => { native.desktop = true; focused = true; visible = true; vi.spyOn(document,"hasFocus").mockImplementation(() => focused); Object.defineProperty(document,"visibilityState",{configurable:true,get:()=>visible?"visible":"hidden"}); });
function Sender({id,active=true,admitted=true,loaded=true}:{id:string;active?:boolean;admitted?:boolean;loaded?:boolean}){useSessionInboxRead(id,active,admitted,loaded);return null;}
function fixture() {
 const id=newRequestId(),client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const mark=vi.fn(async(request:{requestId:string;sessionId:string})=>({...request,markedCount:2n,observedAt:"2026-10-10T00:00:00Z",replayed:false}));
 const resource=create(ResourceSchema,{id,kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({source:"MANUAL",archive:"active",outcome:"idle"})});
 const read=vi.fn(async()=>({resource})),list=vi.fn(async()=>({resources:[] as (typeof resource)[],nextPageToken:""}));
 const transport=createRouterTransport(router=>{router.service(InboxService,{markSessionInboxRead:mark});router.service(ResourceService,{getResource:read,listResources:list});});
 const wrap=(children:React.ReactNode)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionInboxReadInvalidation/>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
 return {id,client,transport,mark,read,list,resource,wrap};
}
function background(){focused=false;fireEvent(window,new Event("blur"));}
function foreground(){focused=true;fireEvent(window,new Event("focus"));}
it("acknowledges once per successful foreground activation, not polls, rerenders or reconnects",async()=>{
 const f=fixture(),view=render(<StrictMode>{f.wrap(<Sender id={f.id}/>)}</StrictMode>);
 await waitFor(()=>expect(f.mark).toHaveBeenCalledTimes(1));
 view.rerender(<StrictMode>{f.wrap(<Sender id={f.id}/>)}</StrictMode>); await act(async()=>{await f.client.invalidateQueries();});
 view.rerender(<StrictMode>{f.wrap(<Sender id={f.id} admitted={false}/>)}</StrictMode>);view.rerender(<StrictMode>{f.wrap(<Sender id={f.id}/>)}</StrictMode>);
 await act(async()=>{});expect(f.mark).toHaveBeenCalledTimes(1);
 background();foreground();await waitFor(()=>expect(f.mark).toHaveBeenCalledTimes(2));
 expect(f.mark.mock.calls[1][0].requestId).not.toBe(f.mark.mock.calls[0][0].requestId);
});
it.each(["mobile","hidden","unfocused","inactive","unadmitted","incomplete"])("does not acknowledge %s conversations",async(reason)=>{
 const f=fixture();if(reason==="mobile")native.desktop=false;if(reason==="hidden")visible=false;if(reason==="unfocused")focused=false;
 render(f.wrap(<Sender id={f.id} active={reason!=="inactive"} admitted={reason!=="unadmitted"} loaded={reason!=="incomplete"}/>));await act(async()=>{});expect(f.mark).not.toHaveBeenCalled();expect(f.read).not.toHaveBeenCalled();
});
it.each(["failed","foreign","sidechat","wrong-transcript","stale","malformed","unsupported"])("rejects a %s fresh initial observation",async(reason)=>{
 const f=fixture();if(reason==="failed")f.read.mockRejectedValue(new ConnectError("Read failed",Code.Unavailable));
 if(reason==="stale")f.read.mockResolvedValue({resource:create(ResourceSchema,{...f.resource,revision:0n})});
 if(reason==="malformed")f.read.mockResolvedValue({resource:create(ResourceSchema,{...f.resource,documentJson:new Uint8Array([255])})});
 if(reason==="unsupported")f.read.mockResolvedValue({resource:create(ResourceSchema,{...f.resource,schemaVersion:999})});
 if(reason==="foreign")f.read.mockResolvedValue({resource:create(ResourceSchema,{...f.resource,id:newRequestId()})});
 if(reason==="sidechat")f.read.mockResolvedValue({resource:create(ResourceSchema,{...f.resource,documentJson:encode({source:"SIDECHAT",archive:"active",outcome:"idle"})})});
 if(reason==="wrong-transcript")f.list.mockResolvedValue({resources:[create(ResourceSchema,{id:newRequestId(),kind:EntityKind.MESSAGE,revision:1n,sessionId:newRequestId()})],nextPageToken:""});
 render(f.wrap(<Sender id={f.id}/>));await waitFor(()=>expect(f.read).toHaveBeenCalled());await act(async()=>{});expect(f.mark).not.toHaveBeenCalled();
});
it("retains an uncertain request across navigation and retries its exact receipt on a later activation",async()=>{
 const f=fixture();f.mark.mockRejectedValueOnce(new ConnectError("Reply lost",Code.Unavailable));
 const view=render(f.wrap(<Sender id={f.id}/>));await waitFor(()=>expect(f.mark).toHaveBeenCalledTimes(1));await act(async()=>{});
 view.rerender(f.wrap(null));view.rerender(f.wrap(<Sender id={f.id}/>));await waitFor(()=>expect(f.mark).toHaveBeenCalledTimes(2));
 expect(f.mark.mock.calls[1][0]).toEqual(f.mark.mock.calls[0][0]);
});
it("invalidates original Inbox and count caches when acceptance settles after chat disposal",async()=>{
 const f=fixture();let resolve!:(response:Awaited<ReturnType<typeof f.mark>>)=>void;f.mark.mockImplementationOnce(request=>new Promise(done=>{resolve=result=>done({...request,...result});}));
 const options=createQueryOptions(InboxQuery.getUnreadInboxCount,{}, {transport:f.transport});f.client.setQueryData(options.queryKey,{unreadCount:3n,observedAt:"2026-10-10T00:00:00Z"});
 const view=render(f.wrap(<Sender id={f.id}/>));await waitFor(()=>expect(f.mark).toHaveBeenCalledTimes(1));view.rerender(f.wrap(null));
 await act(async()=>{resolve({...f.mark.mock.calls[0][0],markedCount:2n,observedAt:"2026-10-10T00:00:00Z",replayed:false});});
 expect(f.client.getQueryState(options.queryKey)?.isInvalidated).toBe(true);expect(f.client.getQueryData(options.queryKey)).toMatchObject({unreadCount:3n});
});
