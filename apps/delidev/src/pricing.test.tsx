// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { InputPricingMode, PricingVersionSchema, UsageService, newRequestId, TokenPricingMode, SubscriptionServiceIdentity, type PricingVersion, type SetTokenPricingModeRequest, type SetTokenPricingRequest } from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { ModelPricing } from "./pricing";
function fixture(initial = false, subscription = false) {
 const model={providerId:subscription?"":newRequestId(),nativeId:"exact-native-id",subscriptionService:subscription?SubscriptionServiceIdentity.CHATGPT:SubscriptionServiceIdentity.UNSPECIFIED};
 const version=(revision=1n)=>create(PricingVersionSchema,{id:newRequestId(),model,providerId:model.providerId,revision,basis:{currency:"USD",source:"Explicit fixture source",asOf:"2026-09-25",inputMode:InputPricingMode.UNIFORM,inputPerMillion:"2.5",exclusions:[]}});
 let pricing:PricingVersion|undefined=initial?version():undefined;let providerRevision=subscription?0n:1n;let policyRevision=0n;let mode=TokenPricingMode.AUTOMATIC;
 const read=vi.fn(()=>({pricing,providerRevision,policy:{model,mode,revision:policyRevision},reference:{state:"current",checkedAtUnixMs:0n,costsJson:new Uint8Array()}}));
 const write=vi.fn((request:SetTokenPricingRequest)=>{pricing=create(PricingVersionSchema,{...version((request.expectedRevision??0n)+1n),basis:request.basis});policyRevision++;mode=TokenPricingMode.MANUAL;return Promise.resolve({pricing,policy:{model,mode,revision:policyRevision},requestId:request.requestId});});
 const switchMode=vi.fn((request:SetTokenPricingModeRequest)=>{mode=request.mode;policyRevision++;return {requestId:request.requestId,current:read()};});
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}});
 const transport=createRouterTransport(router=>router.service(UsageService,{getTokenPricing:read,setTokenPricing:write,setTokenPricingMode:switchMode,refreshTokenPrices:()=>({})}));
 const view=(active=true)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ModelPricing model={model} active={active} close={()=>{}} /></MutationIntents></QueryClientProvider></TransportProvider>;
 return {model,write,read,switchMode,view,version,refresh:()=>client.invalidateQueries(),setPricing:(value:PricingVersion)=>{pricing=value;},setProviderRevision:(value:bigint)=>{providerRevision=value;}};
}
it("saves exact nullable decimals with source, provider, price and policy revisions",async()=>{
 const f=fixture();render(f.view());await screen.findByText("No exact reference rate is available.");await waitFor(()=>expect(screen.getByRole("button",{name:"Edit token pricing"})).toHaveProperty("disabled",false));fireEvent.click(screen.getByRole("button",{name:"Edit token pricing"}));
 for(const [name,value] of [["Currency","USD"],["Pricing source","Original source"],["As-of date","2026-09-25"],["Input rate per million","0.000000001"],["Output rate per million","0"]])fireEvent.change(screen.getByLabelText(name),{target:{value}});
 fireEvent.click(screen.getByRole("button",{name:"Save pricing version"}));await waitFor(()=>expect(f.write).toHaveBeenCalledTimes(1));await waitFor(()=>expect(screen.queryByText("New pricing version")).toBeNull());
 expect(f.write.mock.calls[0][0]).toMatchObject({model:f.model,expectedProviderRevision:1n,expectedPolicyRevision:0n,expectedRevision:0n,basis:{currency:"USD",inputPerMillion:"0.000000001",outputPerMillion:"0"}});expect(f.write.mock.calls[0][0].basis?.cachedInputPerMillion).toBeUndefined();
});
it("retains an uncertain exact request and stale draft across hidden visits",async()=>{
 const f=fixture(true);const view=render(f.view());await screen.findByRole("button",{name:"Edit token pricing"});await waitFor(()=>expect(screen.getByRole("button",{name:"Edit token pricing"})).toHaveProperty("disabled", false));fireEvent.click(screen.getByRole("button",{name:"Edit token pricing"}));
 f.write.mockRejectedValueOnce(new ConnectError("Lost acceptance",Code.Unavailable));fireEvent.change(screen.getByLabelText("Input rate per million"),{target:{value:"3"}});fireEvent.click(screen.getByRole("button",{name:"Save pricing version"}));await screen.findByRole("button",{name:"Retry the same price"});const original=f.write.mock.calls[0][0];
 f.setPricing(f.version(3n));f.setProviderRevision(2n);view.rerender(f.view(false));view.rerender(f.view());await f.refresh();await screen.findByText(/The model or price changed elsewhere/);expect(screen.getByLabelText("Input rate per million")).toHaveProperty("value", "3");expect(screen.getByRole("button",{name:"Save pricing version"})).toHaveProperty("disabled", true);fireEvent.click(screen.getByRole("button",{name:"Retry the same price"}));await waitFor(()=>expect(f.write).toHaveBeenCalledTimes(2));expect(f.write.mock.calls[1][0]).toEqual(original);
});
it("rejects imprecise decimal syntax before sending",async()=>{const f=fixture(true);render(f.view());await waitFor(()=>expect(screen.getByRole("button",{name:"Edit token pricing"})).toHaveProperty("disabled", false));fireEvent.click(screen.getByRole("button",{name:"Edit token pricing"}));fireEvent.change(screen.getByLabelText("Input rate per million"),{target:{value:"1e-9"}});fireEvent.click(screen.getByRole("button",{name:"Save pricing version"}));await screen.findByText(/Use nonnegative decimal rates/);expect(f.write).not.toHaveBeenCalled();});
it("allows an explicit Automatic switch without a matching reference",async()=>{const f=fixture(false,true);render(f.view());await waitFor(()=>expect(screen.getByRole("combobox",{name:"Pricing mode"})).toHaveProperty("disabled", false));fireEvent.change(screen.getByRole("combobox",{name:"Pricing mode"}),{target:{value:TokenPricingMode.MANUAL}});await waitFor(()=>expect(f.switchMode).toHaveBeenCalledTimes(1));expect(f.switchMode.mock.calls[0][0]).toMatchObject({model:f.model,expectedProviderRevision:0n,expectedPolicyRevision:0n,expectedRevision:0n});expect(screen.getByText(/Subscription models use API-equivalent/)).toBeTruthy();});
it("keeps deleted API sources read-only",async()=>{const f=fixture(true);f.setProviderRevision(0n);render(f.view());await screen.findByRole("combobox",{name:"Pricing mode"});expect(screen.getByRole("button",{name:"Edit token pricing"})).toHaveProperty("disabled", true);expect(screen.getByRole("combobox",{name:"Pricing mode"})).toHaveProperty("disabled", true);});
