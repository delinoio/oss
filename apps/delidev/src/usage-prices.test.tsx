// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { UsageService, ProviderService, GetUsageSummaryResponseSchema, SubscriptionServiceIdentity, TokenPricingMode, newRequestId, type GetTokenPricingRequest } from "@delinoio/delidev-api-client";
import { useState } from "react";
import { UsageModelPrices, retainedPrices, validSourceModel, type PriceSelection } from "./usage-prices";
import { MutationIntents } from "./mutation";
import { chooseScrollOption } from "./test-scroll-picker";
import { priceIdentity } from "./pricing";
it("selects exact independently sourced identities without a Model registry",async()=>{
 const providerId=newRequestId();const models=[{providerId,nativeId:"shared-exact",subscriptionService:SubscriptionServiceIdentity.UNSPECIFIED},{providerId:"",nativeId:"shared-exact",subscriptionService:SubscriptionServiceIdentity.CHATGPT}];
 const list=vi.fn(()=>({models}));const read=vi.fn((request:GetTokenPricingRequest)=>({providerRevision:request.model?.providerId?1n:0n,policy:{mode:TokenPricingMode.AUTOMATIC},reference:{state:"current"}}));
 const transport=createRouterTransport(router=>{router.service(UsageService,{listTokenPricing:list,getTokenPricing:read});router.service(ProviderService,{listProviderInventory:()=>({entries:[{providerId,displayName:"Original API"}]})});});const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 function View(){const [selection,select]=useState<PriceSelection>();return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><UsageModelPrices active selection={selection} select={select}/></MutationIntents></QueryClientProvider></TransportProvider>;}
 render(<View/>);const picker=screen.getByRole("combobox",{name:"Pricing model"});await chooseScrollOption(picker,priceIdentity(models[0]));await waitFor(()=>expect(read.mock.calls.at(-1)?.[0].model).toMatchObject(models[0]));await chooseScrollOption(picker,priceIdentity(models[1]));await waitFor(()=>expect(read.mock.calls.at(-1)?.[0].model).toMatchObject(models[1]));expect(list).toHaveBeenCalledTimes(1);
});
it("keeps same native IDs under different original sources independent in retained history",()=>{
 const providerId=newRequestId();const data=create(GetUsageSummaryResponseSchema,{pricing:[{pricing:{id:newRequestId(),model:{providerId,nativeId:"exact"}}},{pricing:{id:newRequestId(),model:{subscriptionService:SubscriptionServiceIdentity.CHATGPT,nativeId:"exact"}}}]});expect(retainedPrices(data,{providerId,nativeId:"exact"})).toHaveLength(1);expect(retainedPrices(data,{subscriptionService:SubscriptionServiceIdentity.CHATGPT,nativeId:"exact"})).toHaveLength(1);
});
it("rejects ambiguous, unbound and oversized source identities",()=>{expect(validSourceModel({providerId:newRequestId(),subscriptionService:SubscriptionServiceIdentity.CHATGPT,nativeId:"exact"})).toBeFalsy();expect(validSourceModel({providerId:"",subscriptionService:SubscriptionServiceIdentity.UNSPECIFIED,nativeId:"exact"})).toBeFalsy();expect(validSourceModel({providerId:"",subscriptionService:SubscriptionServiceIdentity.GROK,nativeId:"x".repeat(257)})).toBeFalsy();});
