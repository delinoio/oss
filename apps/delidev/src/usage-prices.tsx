// SPDX-License-Identifier: Apache-2.0
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, supportsResourceSchema, subscriptionServiceFromWire, type GetUsageSummaryResponse, type PricingVersion, type SubscriptionServiceIdentity } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { document, text } from "./documents";
import { ModelPricing, PricingBasis } from "./pricing";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";

export interface PriceSelection { modelId: string; providerId?: string; subscriptionService?: SubscriptionServiceIdentity; history?: PricingVersion[] }
export function retainedPrices(data: GetUsageSummaryResponse | undefined, selection: PriceSelection): PricingVersion[] {
  const versions = [...data?.pricing.map(row => row.pricing) ?? [], ...data?.nativeAccounting.flatMap(summary => summary.pricing.map(row => row.pricing)) ?? []];
  const unique = new Map<string, PricingVersion>();
  for (const price of versions) if (price && price.modelId === selection.modelId && (!selection.providerId || price.providerId === selection.providerId) && (!selection.subscriptionService || price.subscriptionService === selection.subscriptionService)) unique.set(price.id, price);
  return [...unique.values()];
}
export function UsageModelPrices({ selection, select, active, data }: { selection?: PriceSelection; select: (value: PriceSelection | undefined) => void; active: boolean; data?: GetUsageSummaryResponse }) {
  useLocale();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: selection?.modelId ?? "" }, { enabled: active && Boolean(selection), refetchInterval: active && selection ? 5000 : false });
  const model = current.data?.resource;
  const value = document(model);
  // Historical provider/service identity cannot become a current editor after a configuration change.
  const matches = model && model.id === selection?.modelId && model.kind === EntityKind.MODEL && model.revision > 0n && supportsResourceSchema(model) && value.retired !== true && (!selection?.providerId || text(value.provider_id) === selection.providerId) && (!selection?.subscriptionService || text(value.subscription_service) === subscriptionServiceFromWire(selection.subscriptionService));
  const history = selection ? selection.history ?? retainedPrices(data, selection) : [];
  return <section className="usage-model-prices"><h2>{copy("usage.prices")}</h2><p>{copy("usage.referenceRates")}</p>
    <ResourceChoice label={copy("usage.priceModel")} emptyLabel={copy("usage.choosePriceModel")} kind={EntityKind.MODEL} value={selection?.modelId ?? ""} change={id => select(id ? { modelId: id } : undefined)} active={active} />
    <p>{copy("usage.manualPricing")}</p>
    {!selection ? <p>{copy("usage.choosePriceModel")}</p> : <><Problem error={current.error} summary={copy("usage.modelReadHelp")} />{current.isPending ? <p role="status">{copy("usage.loadingModelDetails")}</p> : null}
      {matches ? <ModelPricing key={model.id} model={model} active={active && !current.error} close={() => select(undefined)} compact /> : <p>{copy("usage.historicalPriceOnly")}</p>}
      <Disclosure><DisclosureSummary>{copy("usage.originalPriceIdentity")}</DisclosureSummary><dl className="usage-original-identities"><dt>{copy("usage.model_5e2c61")}</dt><dd>{selection.modelId}</dd><dt>{copy("usage.provider_472590")}</dt><dd>{selection.providerId || text(value.provider_id) || "—"}</dd><dt>{copy("usage.subscriptionService_0e16df")}</dt><dd>{selection.subscriptionService ? subscriptionServiceFromWire(selection.subscriptionService) : text(value.subscription_service) || "—"}</dd></dl></Disclosure>
      {history.length ? <Disclosure><DisclosureSummary>{copy("usage.priceDetails")}</DisclosureSummary>{history.map(price => <PricingBasis key={price.id} value={price} />)}</Disclosure> : !matches && !current.isPending ? <p>{copy("usage.noHistoricalPrice")}</p> : null}
    </>}
    <p>{copy("usage.futurePricesOnly")}</p>
  </section>;
}
