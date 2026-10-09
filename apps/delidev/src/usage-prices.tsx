// SPDX-License-Identifier: Apache-2.0
import { useCallback } from "react";
import { UsageQuery, SubscriptionServiceIdentity, isEntityId, subscriptionServiceLabel, type GetUsageSummaryResponse, type ListTokenPricingResponse, type PricingVersion } from "@delinoio/delidev-api-client";
import { Code, ConnectError } from "@connectrpc/connect";
import { ModelPricing, PricingBasis, priceIdentity, type SourceModel } from "./pricing";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { ScrollPicker } from "./scroll-picker";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { useProviderPages } from "./model-pagination";

export interface PriceSelection { nativeId: string; providerId?: string; subscriptionService?: SubscriptionServiceIdentity; history?: PricingVersion[] }
export function selectionModel(selection:PriceSelection):SourceModel{return {nativeId:selection.nativeId,providerId:selection.providerId??"",subscriptionService:selection.subscriptionService??SubscriptionServiceIdentity.UNSPECIFIED};}
export function retainedPrices(data:GetUsageSummaryResponse|undefined,selection:PriceSelection):PricingVersion[]{
 const versions=[...data?.pricing.map(row=>row.pricing)??[],...data?.nativeAccounting.flatMap(summary=>summary.pricing.map(row=>row.pricing))??[]];const key=priceIdentity(selectionModel(selection));const unique=new Map<string,PricingVersion>();
 for(const price of versions)if(price && priceIdentity(price.model)===key)unique.set(price.id,price);
 return [...unique.values()];
}
export function validSourceModel(model:SourceModel){return model.nativeId && new TextEncoder().encode(model.nativeId).length<=256 && (!model.providerId && [SubscriptionServiceIdentity.CHATGPT,SubscriptionServiceIdentity.CLAUDE,SubscriptionServiceIdentity.GROK,SubscriptionServiceIdentity.OPENCODE_GO].includes(model.subscriptionService) || isEntityId(model.providerId) && model.subscriptionService===SubscriptionServiceIdentity.UNSPECIFIED);}
function usePriceModels(active:boolean){
 const request=useCallback((pageToken:string)=>({pageToken,pageSize:50}),[]);
 const project=useCallback((response:ListTokenPricingResponse)=>{if(response.models.length>50 || response.models.some(model=>!validSourceModel(model)) || new Set(response.models.map(priceIdentity)).size!==response.models.length)throw new ConnectError("Invalid source price page.",Code.DataLoss);return {rows:response.models.map(model=>({id:priceIdentity(model),revision:1n,model})),payload:response.models,nextPageToken:response.nextPageToken};},[]);
 const reader=useConnectPaginationReader(UsageQuery.listTokenPricing,request,project);return usePaginationChain("usage-source-prices",active,reader,true);
}
export function UsageModelPrices({selection,select,active,data}:{selection?:PriceSelection;select:(value:PriceSelection|undefined)=>void;active:boolean;data?:GetUsageSummaryResponse}){
 useLocale();const models=usePriceModels(active);const providers=useProviderPages("",active,false);
 const identity=selection?selectionModel(selection):undefined;
 const label=(model:SourceModel)=>`${model.providerId?providers.rows.find(row=>row.providerId===model.providerId)?.displayName || data?.groups.find(row=>row.model?.providerId===model.providerId)?.providerName || model.providerId:subscriptionServiceLabel(model.subscriptionService)} · ${model.nativeId}`;
 const history=selection?selection.history??retainedPrices(data,selection):[];
 return <section className="usage-model-prices"><h2>{copy("usage.prices")}</h2><p>{copy("usage.referenceRates")}</p>
 <ScrollPicker label={copy("usage.priceModel")} value={priceIdentity(identity)} selectedLabel={identity?label(identity):""} placeholder={copy("usage.choosePriceModel")} query={models} active={active} options={models.rows.map(row=>({id:row.id,label:label(row.model)}))} change={id=>{const row=models.rows.find(row=>row.id===id);if(row)select(row.model);}} />
 {identity && validSourceModel(identity)?<><ModelPricing key={priceIdentity(identity)} model={identity} active={active} close={()=>select(undefined)} compact/>{history.length?<Disclosure><DisclosureSummary>{copy("usage.historicalVersions")}</DisclosureSummary>{history.map(price=><PricingBasis key={price.id} value={price}/>)}</Disclosure>:null}</>:<p>{copy("usage.choosePriceModel")}</p>}
 </section>;
}
