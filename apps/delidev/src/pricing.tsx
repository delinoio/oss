import { Disclosure, DisclosureSummary } from "./disclosure";
import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { formatDecimal, productError, ProductError, ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useCloseSettingsTask } from "./settings-task-context";
import { SettingsTaskActions } from "./settings-task";
import { useState, useId } from "react";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { subscriptionServiceLabel, InputPricingMode, UsageQuery, newRequestId, type PricingVersion, type GetTokenPricingResponse, TokenPricingMode, SubscriptionServiceIdentity, type TokenPricing } from "@delinoio/delidev-api-client";
import { Timestamp } from "./timestamp-display";
export interface SourceModel {providerId:string;subscriptionService:SubscriptionServiceIdentity;nativeId:string}
export function priceIdentity(model?:SourceModel){return model?JSON.stringify([model.providerId,model.subscriptionService,model.nativeId]):"";}
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function PricingBasis({ value }: { value: PricingVersion }) {
  useLocale();
  const p = value.basis;
  if (!p) return <p>{copy("pricing.pricingBasisUnavailable_1b8fe3")}</p>;
  return <div className="pricing-basis"><dl><dt>{copy("pricing.source_0e570c")}</dt><dd>{p.source}</dd><dt>{copy("pricing.asOf_431575")}</dt><dd>{p.asOf}</dd><dt>{copy("pricing.currency_3ac1a9")}</dt><dd>{p.currency}</dd><dt>{copy("pricing.inputPricing_a2d912")}</dt><dd>{p.inputMode === InputPricingMode.UNIFORM ? copy("pricing.uniformInputCacheIncluded_420f49") : p.inputMode === InputPricingMode.CACHED_DISCOUNT ? copy("pricing.separateUncachedInputAndCachedReads_c91ad9") : copy("pricing.unknownMode_892fdb")}</dd><dt>{copy("pricing.inputMillion_fb54c1")}</dt><dd>{formatDecimal(p.inputPerMillion ?? "") || copy("pricing.unavailable_ca1844")}</dd>{p.inputMode === InputPricingMode.CACHED_DISCOUNT ? <><dt>{copy("pricing.cachedInputMillion_187c72")}</dt><dd>{formatDecimal(p.cachedInputPerMillion ?? "") || copy("pricing.unavailable_ca1844")}</dd></> : null}<dt>{copy("pricing.outputMillion_bd8cb4")}</dt><dd>{formatDecimal(p.outputPerMillion ?? "") || copy("pricing.unavailable_ca1844")}</dd></dl>
    {p.exclusions.length ? <><h4>{copy("pricing.declaredExclusions_dc075b")}</h4><ul>{p.exclusions.map((value, index) => <li key={index}>{value}</li>)}</ul></> : null}
    <p>{copy("pricing.onlyObservedTokenCategoriesAreCovered_783253")}</p>
    <small><LocalizedText id="pricing.version_4fda4a" components={{ s0: <>{value.revision.toString()}</>, s1: <>{value.id}</> }} /></small><small><LocalizedText id="pricing.originalModel_62c471" components={{ s0: <>{value.model?.nativeId ?? ""}</>, s1: <>{value.subscriptionService ? copy("pricing.subscriptionService_596422", { v0: subscriptionServiceLabel(value.subscriptionService) }) : copy("pricing.provider_28af03", { v0: value.providerId })}</> }} /></small>
  </div>;
}
interface Draft { currency: string; source: string; asOf: string; inputMode: InputPricingMode; input: string; cached: string; output: string; exclusions: string }
function draftPrice(value?: TokenPricing): Draft {
  return { currency: value?.currency ?? "", source: value?.source ?? "", asOf: value?.asOf ?? "", inputMode: value?.inputMode ?? InputPricingMode.UNIFORM, input: value?.inputPerMillion ?? "", cached: value?.cachedInputPerMillion ?? "", output: value?.outputPerMillion ?? "", exclusions: value?.exclusions.join("\n") ?? "" };
}
function priceInput(draft: Draft) {
  const rate = (value: string) => {
    if (!value) return undefined;
    if (!/^(0|[1-9][0-9]{0,17})(\.[0-9]{1,9})?$/.test(value)) throw new ProductError("validation.a1d598578ee9");
    return value;
  };
  const date = new Date(`${draft.asOf}T00:00:00Z`);
  if (!/^[A-Z]{3}$/.test(draft.currency) || !draft.source.trim() || !/^\d{4}-\d{2}-\d{2}$/.test(draft.asOf) || !Number.isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== draft.asOf || draft.asOf < "1970-01-01") throw new ProductError("validation.02134c57a4a6");
  const basis = { currency: draft.currency, source: draft.source, asOf: draft.asOf, inputMode: draft.inputMode, inputPerMillion: rate(draft.input), cachedInputPerMillion: draft.inputMode === InputPricingMode.CACHED_DISCOUNT ? rate(draft.cached) : undefined, outputPerMillion: rate(draft.output), exclusions: draft.exclusions ? draft.exclusions.split("\n") : [] };
  if (basis.inputPerMillion === undefined && basis.cachedInputPerMillion === undefined && basis.outputPerMillion === undefined) throw new ProductError("validation.e5d23860896b");
  const bytes = (value: string) => new TextEncoder().encode(value).length;
  if (bytes(basis.source) > 2048 || basis.exclusions.length > 16 || basis.exclusions.some((value) => !value.trim() || bytes(value) > 512)) throw new ProductError("validation.3574c19968e0");
  return basis;
}
function PricingEditor({ model, initial, original, current, readError, retryRead, saved, cancel }: { model: SourceModel; initial?: PricingVersion; original:GetTokenPricingResponse; current?: GetTokenPricingResponse; readError?: unknown; retryRead: () => void; saved: (value?: PricingVersion) => void; cancel: () => void }) {
  useLocale();
  const taskFormId = useId();
  const [draft, setDraft] = useState(() => draftPrice(initial?.basis ?? original.reference?.basis));
  const [problem, setProblem] = useProductMessage("");
  const mutation = useRetainedMutation(`pricing:${priceIdentity(model)}`, UsageQuery.setTokenPricing, (value) => saved(value.pricing), (result,request)=>result.requestId===request.requestId && priceIdentity(result.pricing?.model)===priceIdentity(model) && result.policy?.mode===TokenPricingMode.MANUAL && result.policy.revision===(request.expectedPolicyRevision??0n)+1n);
  const stale = current && (current.providerRevision!==original.providerRevision || current.policy?.revision!==original.policy?.revision || current.pricing?.id !== initial?.id);
  const blocked = mutation.busy || mutation.uncertain;
  const change = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));
  return <form id={`${taskFormId}-1`} onSubmit={(event) => {
    event.preventDefault(); if (blocked || stale || readError || !current) return;
    try { const basis = priceInput(draft); setProblem(""); void mutation.send({ model, expectedRevision:initial?.revision??0n,requestId:newRequestId(),expectedProviderRevision:original.providerRevision,expectedPolicyRevision:original.policy?.revision??0n,basis }); }
    catch (error) { setProblem(productError(error, "pricing.extra.6d92cc676144")); }
  }}><h4>{copy("pricing.newPricingVersion_cd6ddf")}</h4><fieldset disabled={blocked}>
    <label>{copy("pricing.currency_3ac1a9")}<input value={draft.currency} maxLength={3} placeholder={copy("pricing.usd_a26cdf")} onChange={(event) => change("currency", event.target.value)} /></label>
    <label>{copy("pricing.pricingSource_c4d80f")}<textarea value={draft.source} maxLength={2048} onChange={(event) => change("source", event.target.value)} /></label>
    <label>{copy("pricing.asOfDate_6983bc")}<input type="date" value={draft.asOf} onChange={(event) => change("asOf", event.target.value)} /></label>
    <label>{copy("pricing.inputPricingMode_d6c996")}<select value={draft.inputMode} onChange={(event) => change("inputMode", Number(event.target.value) as InputPricingMode)}><option value={InputPricingMode.UNIFORM}>{copy("pricing.uniformInputCacheIncluded_420f49")}</option><option value={InputPricingMode.CACHED_DISCOUNT}>{copy("pricing.separateCachedReadRate_75f7c6")}</option></select></label>
    <label>{copy("pricing.inputRatePerMillion_5e6658")}<input inputMode="decimal" value={draft.input} onChange={(event) => change("input", event.target.value)} /></label>
    {draft.inputMode === InputPricingMode.CACHED_DISCOUNT ? <label>{copy("pricing.cachedInputRatePerMillion_2be047")}<input inputMode="decimal" value={draft.cached} onChange={(event) => change("cached", event.target.value)} /></label> : null}
    <label>{copy("pricing.outputRatePerMillion_d1dea9")}<input inputMode="decimal" value={draft.output} onChange={(event) => change("output", event.target.value)} /></label>
    <label>{copy("pricing.exclusionsOnePerLine_7cdb99")}<textarea value={draft.exclusions} onChange={(event) => change("exclusions", event.target.value)} /></label>
  </fieldset><p>{copy("pricing.leaveUnavailableRatesBlankEnter0_1a6a0a")}</p>
    {draft.inputMode === InputPricingMode.CACHED_DISCOUNT ? <p>{copy("pricing.inputCacheEstimatesRequireConsistentNative_33a2eb")}</p> : null}
    {stale ? <p role="alert">{copy("pricing.theModelOrPriceChangedElsewhere_c5c5d8")}</p> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={readError || mutation.error} actions={readError ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={blocked} onClick={retryRead}>{copy("ui.retryCurrentRead")}</SettingsActionButton> : undefined} />
    <SettingsTaskActions form={`${taskFormId}-1`} className=""><SettingsActionButton icon={SettingsActionIcon.Save} className="primary" disabled={blocked || Boolean(stale || readError) || !current}>{copy("pricing.savePricingVersion_5baab6")}</SettingsActionButton>{mutation.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("pricing.retryTheSamePrice_de1e7a")}</SettingsActionButton> : null}<SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" data-settings-task-cancel disabled={blocked} onClick={cancel}>{copy("pricing.cancelPricingEdit_dc5004")}</SettingsActionButton></SettingsTaskActions>
  </form>;
}
export function ModelPricing({ model, active, close, compact = false }: { model: SourceModel; active: boolean; close: () => void; compact?: boolean }) {
 useLocale(); const key=priceIdentity(model);
 const current=useQuery(UsageQuery.getTokenPricing,{model},{enabled:active,refetchInterval:active?5000:false});
 const [editing,setEditing]=useState<GetTokenPricingResponse>();
 const mode=useRetainedMutation(`pricing-mode:${key}`,UsageQuery.setTokenPricingMode,()=>void current.refetch(),(result,request)=>result.requestId===request.requestId && result.current?.policy?.mode===request.mode && result.current.policy.revision===(request.expectedPolicyRevision??0n)+1n);
 const refresh=useMutation(UsageQuery.refreshTokenPrices,{onSuccess:()=>void current.refetch()});
 const data=current.data;
 const valid=Boolean(data?.policy && [TokenPricingMode.AUTOMATIC,TokenPricingMode.MANUAL].includes(data.policy.mode) && (!data.pricing || priceIdentity(data.pricing.model)===key) && data.reference && ["current","stale","unavailable"].includes(data.reference.state));
 const busy=mode.busy || mode.uncertain || refresh.isPending;
 const basis=data?.pricing?.basis ?? (data?.policy?.mode===TokenPricingMode.AUTOMATIC && !data.reference?.unsupportedEstimate?data.reference?.basis:undefined);
 return <section className="source-pricing">
 {!compact?<h3>{model.nativeId}</h3>:null}
 <div className="usage-price-controls"><label>{copy("pricing.mode")}<select aria-label={copy("pricing.mode")} value={data?.policy?.mode??TokenPricingMode.AUTOMATIC} disabled={!active || !valid || busy || Boolean(current.error || editing)} onChange={event=>{if(data?.policy)void mode.send({model,mode:Number(event.target.value),expectedPolicyRevision:data.policy.revision,expectedProviderRevision:data.providerRevision,requestId:newRequestId()});}}><option value={TokenPricingMode.AUTOMATIC}>{copy("pricing.automatic")}</option><option value={TokenPricingMode.MANUAL}>{copy("pricing.manual")}</option></select></label>
 <SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} type="button" disabled={!active || busy || Boolean(editing)} onClick={()=>refresh.mutate({})}>{copy("pricing.refreshPrices")}</SettingsActionButton>
 <SettingsActionButton icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} type="button" disabled={!active || !valid || busy || Boolean(current.error || editing)} onClick={()=>{if(data)setEditing(data);}}>{copy("pricing.editTokenPricing_44ae61")}</SettingsActionButton></div>
 {editing?<PricingEditor model={model} initial={editing.pricing} original={editing} current={data} readError={current.error} retryRead={()=>void current.refetch()} saved={()=>{setEditing(undefined);void current.refetch();}} cancel={()=>setEditing(undefined)}/>:<>
 <Problem error={current.error || mode.error || refresh.error}/>
 {mode.uncertain?<SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={mode.busy} onClick={mode.retry}>{copy("pricing.retryMode")}</SettingsActionButton>:null}
 {!valid && data?<p role="alert">{copy("pricing.invalidSourcePrice")}</p>:null}
 <dl className="usage-price-rates">{[[copy("pricing.inputMillion_fb54c1"),basis?.inputPerMillion],[copy("pricing.cachedInputMillion_187c72"),basis?.cachedInputPerMillion],[copy("pricing.outputMillion_bd8cb4"),basis?.outputPerMillion]].map(([label,rate])=><div key={label}><dt>{label}</dt><dd>{rate===undefined || rate===""?copy("pricing.unavailable_ca1844"):`${basis?.currency} ${formatDecimal(rate)} / 1M`}</dd></div>)}</dl>
 <p className="usage-price-metadata">{basis?.source || "models.dev"} · {basis?.currency || "USD"} · {data?.reference?.checkedAtUnixMs? <><span>{copy("pricing.checked")}</span> <Timestamp value={new Date(Number(data.reference.checkedAtUnixMs)).toISOString()}/></>:copy("pricing.unavailable_ca1844")} · {copy("pricing.checksDaily")}</p>
 {data?.reference?.state==="stale"?<p role="status">{copy("pricing.staleReference")}</p>:data?.reference?.state==="unavailable"?<p role="status">{copy("pricing.referenceUnavailable")}</p>:!data?.reference?.basis?<p>{copy("pricing.noExactReference")}</p>:null}
 {data?.reference?.unsupportedEstimate?<p>{copy("pricing.unsupportedReference")}</p>:null}
 <p>{copy("usage.futurePricesOnly")}</p>
 <Disclosure><DisclosureSummary>{copy("usage.priceDetails")}</DisclosureSummary>
 {data?.pricing?<PricingBasis value={data.pricing}/>:null}
 <dl><dt>{copy("usage.model_5e2c61")}</dt><dd>{model.nativeId}</dd><dt>{copy("usage.provider_472590")}</dt><dd>{model.providerId || subscriptionServiceLabel(model.subscriptionService)}</dd>
 {data?.pricing?.provenance?<><dt>{copy("pricing.upstreamIdentity")}</dt><dd>{data.pricing.provenance.providerKey} · {data.pricing.provenance.modelKey}</dd><dt>{copy("pricing.snapshot")}</dt><dd>{data.pricing.provenance.snapshotSha256}</dd></>:null}</dl>
 {data?.reference?.costsJson.length?<pre>{new TextDecoder().decode(data.reference.costsJson)}</pre>:null}</Disclosure>
 <p>{copy("pricing.subscriptionEquivalent")}</p>
 </>}
 {!compact?<SettingsActionButton icon={SettingsActionIcon.Back} type="button" onClick={close}>{copy("pricing.backToModels_a28c02")}</SettingsActionButton>:null}
 </section>;
}
