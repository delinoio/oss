// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { InboxService, MarkSessionInboxReadRequestSchema, MarkSessionInboxReadResponseSchema, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents, useRetainedMutationIntents } from "./mutation";
import { InboxBadgePresentation } from "./inbox-badge";
import { acknowledgeSessionInboxRead, SessionInboxActivation, useSessionInboxRead } from "./session-inbox-read";
vi.mock("@tauri-apps/api/core",()=>({isTauri:()=>true,invoke:async()=>undefined}));
vi.mock("@tauri-apps/api/event",()=>({listen:async()=>()=>{}}));
beforeEach(()=>{vi.spyOn(document,"visibilityState","get").mockReturnValue("visible");vi.spyOn(document,"hasFocus").mockReturnValue(true);});
function Viewer({id,active=true,loaded=true,selection=0}:{id:string;active?:boolean;loaded?:boolean;selection?:number}){useSessionInboxRead(id,active,loaded,selection);return null;}
it("admits once per selection or actual foreground activation, never reads/reconnects",()=>{
 const state=new SessionInboxActivation();
 expect(state.inspect(true,true,0,false)).toBe(false);
 expect(state.inspect(true,true,0,true)).toBe(true);
 for(const ready of [true,false,true,true])expect(state.inspect(true,true,0,ready)).toBe(false);
 expect(state.inspect(true,false,0,true)).toBe(false);expect(state.inspect(true,true,0,true)).toBe(true);
 expect(state.inspect(true,true,1,true)).toBe(true);expect(state.inspect(false,true,1,true)).toBe(false);
 expect(state.inspect(true,true,1,true)).toBe(true);
});
it("checks original identities and immutable uint64/time acknowledgment",()=>{
 const request=create(MarkSessionInboxReadRequestSchema,{requestId:newRequestId(),sessionId:newRequestId()});
 const response=create(MarkSessionInboxReadResponseSchema,{...request,markedCount:201n,observedAt:"2026-10-10T00:00:00Z"});
 expect(acknowledgeSessionInboxRead(response,request)).toBe(true);
 for(const changed of [{sessionId:newRequestId()},{requestId:newRequestId()},{markedCount:-1n},{observedAt:"not-a-time"}])expect(acknowledgeSessionInboxRead({...response,...changed},request)).toBe(false);
});
it("waits for successful loading/focus and ignores polling/new-alert rerenders",async()=>{
 const id=newRequestId();const requests:string[]=[];
 const transport=createRouterTransport(r=>r.service(InboxService,{markSessionInboxRead:async request=>{requests.push(request.requestId);return {...request,markedCount:2n,observedAt:"2026-10-10T00:00:00Z"};}}));
 const client=new QueryClient();const view=(loaded:boolean,active=true,selection=0)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Viewer id={id} loaded={loaded} active={active} selection={selection}/></MutationIntents></QueryClientProvider></TransportProvider>;
 vi.mocked(document.hasFocus).mockReturnValue(false);const mounted=render(<StrictMode>{view(false)}</StrictMode>);
 mounted.rerender(<StrictMode>{view(true)}</StrictMode>);await act(async()=>{});expect(requests).toHaveLength(0);
 vi.mocked(document.hasFocus).mockReturnValue(true);fireEvent.focus(window);await waitFor(()=>expect(requests).toHaveLength(1));
 mounted.rerender(<StrictMode>{view(false)}</StrictMode>);mounted.rerender(<StrictMode>{view(true)}</StrictMode>);fireEvent.focus(window);await act(async()=>{});expect(requests).toHaveLength(1);
 vi.mocked(document.hasFocus).mockReturnValue(false);fireEvent.blur(window);vi.mocked(document.hasFocus).mockReturnValue(true);fireEvent.focus(window);await waitFor(()=>expect(requests).toHaveLength(2));
 expect(requests[0]).not.toBe(requests[1]);
});
it("retains only the uncertain original request across navigation and retries on a later activation",async()=>{
 const id=newRequestId();const requests:string[]=[];
 const transport=createRouterTransport(r=>r.service(InboxService,{markSessionInboxRead:async request=>{requests.push(request.requestId);if(requests.length===1)throw new ConnectError("fixture unavailable",Code.Unavailable);return {...request,markedCount:1n,observedAt:"2026-10-10T00:00:00Z"};}}));
 function Pending(){const values=useRetainedMutationIntents("inbox-session-read:");return <p>{values.some(v=>v.uncertain)?"uncertain":"settled"}</p>;}
 const client=new QueryClient();const view=(active:boolean,selection=0)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Pending/><Viewer id={id} active={active} selection={selection}/></MutationIntents></QueryClientProvider></TransportProvider>;
 const mounted=render(view(true));await waitFor(()=>expect(mounted.getByText("uncertain")).toBeDefined());
 mounted.rerender(view(true));await act(async()=>{});expect(requests).toHaveLength(1);
 mounted.rerender(view(false));mounted.rerender(view(true,1));await waitFor(()=>expect(requests).toHaveLength(2));expect(requests[0]).toBe(requests[1]);await waitFor(()=>expect(mounted.getByText("settled")).toBeDefined());
});
it("invalidates original Inbox/count queries after sender disposal, never a replacement connection",async()=>{
 const id=newRequestId();let finish!:()=>void;
 const transport=createRouterTransport(r=>r.service(InboxService,{markSessionInboxRead:async request=>{await new Promise<void>(resolve=>{finish=resolve;});return {...request,markedCount:2n,observedAt:"2026-10-10T00:00:00Z"};}}));
 const client=new QueryClient();const invalidate=vi.spyOn(client,"invalidateQueries");
 const view=(sender:boolean)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><InboxBadgePresentation ready supported/>{sender?<Viewer id={id}/>:null}</MutationIntents></QueryClientProvider></TransportProvider>;
 const mounted=render(view(true));await waitFor(()=>expect(finish).toBeDefined());mounted.rerender(view(false));await act(async()=>finish());await waitFor(()=>expect(invalidate).toHaveBeenCalledTimes(3));
 const methodNames=invalidate.mock.calls.map(([options])=>JSON.stringify(options?.queryKey));expect(methodNames.join(" ")).toContain("GetUnreadInboxCount");expect(methodNames.join(" ")).toContain("ListInbox");expect(methodNames.join(" ")).toContain("GetInboxEntry");
});
it("does not deliver an old connection acknowledgment into a successor registry",async()=>{
 const id=newRequestId();let finish!:()=>void;
 const original=createRouterTransport(r=>r.service(InboxService,{markSessionInboxRead:async request=>{await new Promise<void>(resolve=>{finish=resolve;});return {...request,markedCount:1n,observedAt:"2026-10-10T00:00:00Z"};}}));
 const originalClient=new QueryClient();const oldRefresh=vi.spyOn(originalClient,"invalidateQueries");
 const first=render(<TransportProvider transport={original}><QueryClientProvider client={originalClient}><MutationIntents><InboxBadgePresentation ready supported/><Viewer id={id}/></MutationIntents></QueryClientProvider></TransportProvider>);await waitFor(()=>expect(finish).toBeDefined());first.unmount();
 const successor=createRouterTransport(r=>r.service(InboxService,{}));const nextClient=new QueryClient();const nextRefresh=vi.spyOn(nextClient,"invalidateQueries");
 render(<TransportProvider transport={successor}><QueryClientProvider client={nextClient}><MutationIntents><InboxBadgePresentation ready supported/></MutationIntents></QueryClientProvider></TransportProvider>);
 await act(async()=>finish());expect(oldRefresh).not.toHaveBeenCalled();expect(nextRefresh).not.toHaveBeenCalled();
});
