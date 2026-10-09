import { StrictMode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { InboxService, InboxQuery, InboxReadState, newRequestId } from "@delinoio/delidev-api-client";
import { InboxBadgePresentation, unreadBadgeCount } from "./inbox-badge";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { fireEvent, screen } from "@testing-library/react";
const native=vi.hoisted(()=>({invoke:vi.fn(),listener:()=>{},selection:{scope:"original",generation:1} as {scope:string;generation:number}|null}));
vi.mock("@tauri-apps/api/core",()=>({isTauri:()=>true,invoke:native.invoke}));
vi.mock("@tauri-apps/api/event",()=>({listen:async (_:string,listener:()=>void)=>{native.listener=listener;return ()=>{};}}));
beforeEach(()=>{native.selection={scope:"original",generation:1};native.invoke.mockReset();native.invoke.mockImplementation(async(command:string)=>command==="begin_inbox_badge"?"original":command==="read_inbox_badge_selection"?native.selection:undefined);});
function mount(read:()=>Promise<{unreadCount:bigint;observedAt:string}>,supported=true,strict=false){const transport=createRouterTransport(router=>router.service(InboxService,{getUnreadInboxCount:read}));const client=new QueryClient({defaultOptions:{queries:{retry:false}}});const body=<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><InboxBadgePresentation ready supported={supported}/></MutationIntents></QueryClientProvider></TransportProvider>;return render(strict?<StrictMode>{body}</StrictMode>:body);}
it("validates exact uint64 count and observation metadata",()=>{expect(unreadBadgeCount(0n,"2026-10-09T00:00:00Z")).toBe("0");expect(unreadBadgeCount(18446744073709551615n,"2026-10-09T00:00:00Z")).toBe("18446744073709551615");expect(unreadBadgeCount(-1n,"2026-10-09T00:00:00Z")).toBeUndefined();expect(unreadBadgeCount(1n,"missing")).toBeUndefined();expect(unreadBadgeCount(1,"2026-10-09T00:00:00Z")).toBeUndefined();});
it("publishes exact count independently of notification permission or Inbox mount",async()=>{const read=vi.fn(async()=>({unreadCount:260n,observedAt:"2026-10-09T00:00:00Z"}));mount(read);await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({scope:"original",generation:1,count:"260"})));expect(native.invoke.mock.calls.some(([name])=>name.includes("notification"))).toBe(false);});
it("does not substitute candidate/list pagination on unsupported servers",async()=>{const read=vi.fn(async()=>({unreadCount:5n,observedAt:"2026-10-09T00:00:00Z"}));mount(read,false);await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({count:null})));expect(read).not.toHaveBeenCalled();});
it("drops an old selected read and fetches fresh data when the same window is selected again",async()=>{let finish!:(value:{unreadCount:bigint;observedAt:string})=>void;const read=vi.fn(()=>new Promise<{unreadCount:bigint;observedAt:string}>(resolve=>{finish=resolve;}));mount(read);await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));native.selection=null;await act(async()=>native.listener());await act(async()=>finish({unreadCount:71n,observedAt:"2026-10-09T00:00:00Z"}));expect(native.invoke.mock.calls.some(([name,value])=>name==="publish_inbox_badge"&&value.count==="71")).toBe(false);native.selection={scope:"original",generation:3};await act(async()=>native.listener());await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));await act(async()=>finish({unreadCount:2n,observedAt:"2026-10-09T00:00:00Z"}));await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({generation:3,count:"2"})));});
it("clears an unavailable aggregate instead of retaining its previous count",async()=>{mount(async()=>{throw new Error("Unavailable");});await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({count:null})));});

it("does not renew native freshness from unchanged selection events and cached data",async()=>{mount(async()=>({unreadCount:7n,observedAt:"2026-10-09T00:00:00Z"}));await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({count:"7"})));const before=native.invoke.mock.calls.filter(([name,value])=>name==="publish_inbox_badge"&&value.count==="7").length;await act(async()=>native.listener());expect(native.invoke.mock.calls.filter(([name,value])=>name==="publish_inbox_badge"&&value.count==="7")).toHaveLength(before);});
it("serializes StrictMode scope creation and clears the original scope on disconnect/unmount",async()=>{const view=mount(async()=>({unreadCount:9n,observedAt:"2026-10-09T00:00:00Z"}),true,true);await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({count:"9"})));expect(native.invoke.mock.calls.filter(([name])=>name==="begin_inbox_badge")).toHaveLength(1);const previous=native.invoke.mock.calls.filter(([name])=>name==="publish_inbox_badge").at(-1)![1];view.unmount();await waitFor(()=>expect(native.invoke).toHaveBeenCalledWith("publish_inbox_badge",expect.objectContaining({scope:"original",generation:1,count:null,revision:previous.revision+1})));});

it("refreshes the selected badge after an accepted retained read mutation outlives its detail",async()=>{
 let finish!:()=>void;
 const read=vi.fn(async()=>({unreadCount:7n,observedAt:"2026-10-10T00:00:00Z"}));
 const transport=createRouterTransport(router=>router.service(InboxService,{getUnreadInboxCount:read,setInboxReadState:()=>new Promise(resolve=>{finish=()=>resolve({});})}));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const id=newRequestId();
 function Detail(){const mutation=useRetainedMutation(`inbox-read:${id}`,InboxQuery.setInboxReadState);return <button onClick={()=>void mutation.send({mutation:{id,requestId:newRequestId(),expectedRevision:1n},readState:InboxReadState.READ})}>Mark fixture read</button>;}
 function Shell({detail}:{detail:boolean}){return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><InboxBadgePresentation ready supported/>{detail?<Detail/>:null}</MutationIntents></QueryClientProvider></TransportProvider>;}
 const view=render(<Shell detail/>);
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));
 fireEvent.click(screen.getByRole("button",{name:"Mark fixture read"}));
 await waitFor(()=>expect(finish).toBeTypeOf("function"));
 view.rerender(<Shell detail={false}/>);
 await act(async()=>finish());
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));
});
