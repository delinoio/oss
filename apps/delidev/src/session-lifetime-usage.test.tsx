// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient,QueryClientProvider } from "@tanstack/react-query";
import { act,fireEvent,render,screen,waitFor } from "@testing-library/react";
import { expect,it,vi } from "vitest";
import { ResourceSchema,UsageService,UsageCoverage,GetUsageSummaryResponseSchema,newRequestId,type Resource,type GetUsageSummaryRequest } from "@delinoio/delidev-api-client";
import { SessionLifetimeUsage } from "./session-lifetime-usage";
import { SessionActivityProvider } from "./session-activity";
const summary=(from=1000n,until=3000n,value="9007199254740993")=>create(GetUsageSummaryResponseSchema,{fromUnixMs:from,untilUnixMs:until,coverage:UsageCoverage.OBSERVED_ROOT_RESPONSES,totals:{responses:1,input:{knownTotal:value,measuredResponses:1},output:{knownTotal:"0",measuredResponses:1}}});
function fixture(){
 const session=create(ResourceSchema,{id:newRequestId(),createdAt:"1970-01-01T00:00:01Z"});const read=vi.fn(async(_request:GetUsageSummaryRequest)=>summary());const transport=createRouterTransport(router=>router.service(UsageService,{getUsageSummary:read}));const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 function Body({resource,active}:{resource:Resource;active:boolean}){const[target,setTarget]=useState<HTMLElement|null>(null);return <SessionActivityProvider active={active}><div aria-label="Diagnostics" ref={setTarget}/><SessionLifetimeUsage session={resource} diagnosticsTarget={target}/></SessionActivityProvider>;}
 return{session,read,client,view:(resource=session,active=true)=><TransportProvider transport={transport}><QueryClientProvider client={client}><Body resource={resource} active={active}/></QueryClientProvider></TransportProvider>};
}
it("keeps only labels boxed and values exact, measured zero distinct",async()=>{
 const f=fixture(),view=render(f.view());await screen.findByText("9,007,199,254,740,993");const row=screen.getByLabelText("Known lifetime response tokens");expect([...row.querySelectorAll('.session-token-label')].map(node=>node.textContent)).toEqual(["In","Out"]);expect(row.children[0].children[1].textContent).toBe("9,007,199,254,740,993");expect(row.children[1].children[1].textContent).toBe("0");expect(f.read.mock.calls[0][0]).toMatchObject({sessionId:f.session.id,fromUnixMs:0n,untilUnixMs:0n,accountingProfile:0});view.unmount();f.client.clear();
});
it("retains completed totals stale after failed refresh and offers read Retry",async()=>{
 const f=fixture(),view=render(f.view());await screen.findByText("9,007,199,254,740,993");f.read.mockRejectedValueOnce(new Error("Read failed"));await act(()=>f.client.invalidateQueries({refetchType:"active"}));expect(await screen.findByText(/Previously completed usage is stale/)).toBeTruthy();expect(screen.getByText("9,007,199,254,740,993")).toBeTruthy();fireEvent.click(screen.getByRole("button",{name:"Retry read"}));await waitFor(()=>expect(screen.queryByText(/Previously completed usage is stale/)).toBeNull());view.unmount();f.client.clear();
});
it("never publishes the first subtotal when an older read fails",async()=>{
 const f=fixture();f.read.mockResolvedValueOnce(summary(2000n,3000n)).mockRejectedValueOnce(new Error("Older read failed"));const view=render(f.view());await screen.findByRole("button",{name:"Retry read"});expect(screen.queryByText("9,007,199,254,740,993")).toBeNull();expect(screen.queryByLabelText("Session usage range evidence")).toBeNull();expect(f.read.mock.calls[1][0]).toMatchObject({fromUnixMs:1000n,untilUnixMs:2000n});view.unmount();f.client.clear();
});
it("suspends inactive reads and fences replaced session results",async()=>{
 const f=fixture();let finish:(value:ReturnType<typeof summary>)=>void=()=>{};f.read.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));const view=render(f.view(f.session,false));await Promise.resolve();expect(f.read).not.toHaveBeenCalled();view.rerender(f.view());await waitFor(()=>expect(f.read).toHaveBeenCalledOnce());const next={...f.session,id:newRequestId()};f.read.mockResolvedValueOnce(summary(1000n,3000n,"7"));view.rerender(f.view(next));await screen.findByText("7");await act(async()=>{finish(summary());await Promise.resolve();});expect(screen.queryByText("9,007,199,254,740,993")).toBeNull();view.unmount();f.client.clear();
});
