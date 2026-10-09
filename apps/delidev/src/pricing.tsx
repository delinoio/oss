import { Disclosure, DisclosureSummary } from "./disclosure";
import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { formatDecimal, productError, ProductError, ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useCloseSettingsTask } from "./settings-task-context";
import { SettingsTaskActions } from "./settings-task";
import { useState, useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { subscriptionServiceLabel, InputPricingMode, UsageQuery, newRequestId, type PricingVersion, type Resource, type TokenPricing } from "@delinoio/delidev-api-client";
import { resourceName } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function PricingBasis({ value }: { value: PricingVersion }) {
  useLocale();
  const p = value.basis;
  if (!p) return <p>{copy("pricing.pricingBasisUnavailable_1b8fe3")}</p>;
  return <div className="pricing-basis"><dl><dt>{copy("pricing.source_0e570c")}</dt><dd>{p.source}</dd><dt>{copy("pricing.asOf_431575")}</dt><dd>{p.asOf}</dd><dt>{copy("pricing.currency_3ac1a9")}</dt><dd>{p.currency}</dd><dt>{copy("pricing.inputPricing_a2d912")}</dt><dd>{p.inputMode === InputPricingMode.UNIFORM ? copy("pricing.uniformInputCacheIncluded_420f49") : p.inputMode === InputPricingMode.CACHED_DISCOUNT ? copy("pricing.separateUncachedInputAndCachedReads_c91ad9") : copy("pricing.unknownMode_892fdb")}</dd><dt>{copy("pricing.inputMillion_fb54c1")}</dt><dd>{formatDecimal(p.inputPerMillion ?? "") || copy("pricing.unavailable_ca1844")}</dd>{p.inputMode === InputPricingMode.CACHED_DISCOUNT ? <><dt>{copy("pricing.cachedInputMillion_187c72")}</dt><dd>{formatDecimal(p.cachedInputPerMillion ?? "") || copy("pricing.unavailable_ca1844")}</dd></> : null}<dt>{copy("pricing.outputMillion_bd8cb4")}</dt><dd>{formatDecimal(p.outputPerMillion ?? "") || copy("pricing.unavailable_ca1844")}</dd></dl>
    {p.exclusions.length ? <><h4>{copy("pricing.declaredExclusions_dc075b")}</h4><ul>{p.exclusions.map((value, index) => <li key={index}>{value}</li>)}</ul></> : null}
    <p>{copy("pricing.onlyObservedTokenCategoriesAreCovered_783253")}</p>
    <small><LocalizedText id="pricing.version_4fda4a" components={{ s0: <>{value.revision.toString()}</>, s1: <>{value.id}</> }} /></small><small><LocalizedText id="pricing.originalModel_62c471" components={{ s0: <>{value.modelId}</>, s1: <>{value.subscriptionService ? copy("pricing.subscriptionService_596422", { v0: subscriptionServiceLabel(value.subscriptionService) }) : copy("pricing.provider_28af03", { v0: value.providerId })}</> }} /></small>
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
function PricingEditor({ model, initial, modelRevision, current, readError, retryRead, saved, cancel }: { model: Resource; initial?: PricingVersion; modelRevision: bigint; current?: { pricing?: PricingVersion; modelRevision: bigint }; readError?: unknown; retryRead: () => void; saved: (value?: PricingVersion) => void; cancel: () => void }) {
  useLocale();
  const taskFormId = useId();
  const [draft, setDraft] = useState(() => draftPrice(initial?.basis));
  const [problem, setProblem] = useProductMessage("");
  const mutation = useRetainedMutation(`pricing:${model.id}`, UsageQuery.setModelPricing, (value) => saved(value.pricing));
  const stale = current && (current.modelRevision !== modelRevision || current.pricing?.id !== initial?.id);
  const blocked = mutation.busy || mutation.uncertain;
  const change = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));
  return <form id={`${taskFormId}-1`} onSubmit={(event) => {
    event.preventDefault(); if (blocked || stale || readError || !current) return;
    try { const basis = priceInput(draft); setProblem(""); void mutation.send({ mutation: { id: model.id, expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, expectedModelRevision: modelRevision, basis }); }
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
export function ModelPricing({ model, active, close, compact = false }: { model: Resource; active: boolean; close: () => void; compact?: boolean }) {
  const closeTask = useCloseSettingsTask(close);
  const current = useQuery(UsageQuery.getModelPricing, { modelId: model.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [editing, setEditing] = useState<{ initial?: PricingVersion; modelRevision: bigint }>();
  const [accepted, setAccepted] = useState<PricingVersion>();
  const [missingResult, setMissingResult] = useState(false);
  const data = current.data;
  return <section><header><h3><LocalizedText id="pricing.tokenPricing_537ed0" components={{ s0: <>{resourceName(model)}</> }} /></h3><SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} disabled={current.isFetching} onClick={() => void current.refetch()}>{copy("pricing.refreshPricing_880e13")}</SettingsActionButton></header>
    <p>{copy("pricing.enterASourceBackedEstimateBasis_fa3472")}</p>
    {accepted ? <p role="status"><LocalizedText id="pricing.acceptedPricingVersionCurrentSelectionIs_5b49f3" components={{ s0: <>{accepted.revision.toString()}</> }} /></p> : null}
    {missingResult ? <p role="alert">{copy("pricing.theServerAcknowledgedThePriceWithout_617a41")}</p> : null}
    {editing ? <PricingEditor model={model} initial={editing.initial} modelRevision={editing.modelRevision} current={data} readError={current.error} retryRead={() => { if (active && !current.isFetching) void current.refetch(); }} saved={(value) => { setEditing(undefined); setAccepted(value); setMissingResult(!value); void current.refetch(); }} cancel={() => setEditing(undefined)} /> : <><Problem error={current.error} actions={current.error ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</SettingsActionButton> : undefined} />{data && current.error ? <p>{copy("pricing.theLastRetrievedPricingMayBe_b7ff30")}</p> : null}{data?.pricing ? compact ? <><dl className="usage-price-rates">{[[copy("pricing.inputMillion_fb54c1"), data.pricing.basis?.inputPerMillion], [copy("pricing.cachedInputMillion_187c72"), data.pricing.basis?.cachedInputPerMillion], [copy("pricing.outputMillion_bd8cb4"), data.pricing.basis?.outputPerMillion]].map(([label, rate]) => <div key={label}><dt>{label}</dt><dd>{rate === undefined || rate === "" ? copy("pricing.unavailable_ca1844") : `${data.pricing?.basis?.currency} ${formatDecimal(rate)}`}</dd></div>)}</dl><p>{data.pricing.basis?.source} · {data.pricing.basis?.currency} · {data.pricing.basis?.asOf}</p><Disclosure><DisclosureSummary>{copy("usage.priceDetails")}</DisclosureSummary><PricingBasis value={data.pricing} /></Disclosure></> : <PricingBasis value={data.pricing} /> : data ? <p>{copy("pricing.noPricingBasisHasBeenConfigured_e602ce")}</p> : <p role="status">{copy("pricing.loadingPricing_856f08")}</p>}
    {data && data.modelRevision !== model.revision ? <p role="alert">{copy("pricing.theModelConfigurationChangedReturnTo_063c62")}</p> : null}
    <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} disabled={!data || Boolean(current.error || current.isFetching || missingResult) || data.modelRevision !== model.revision} onClick={() => { if (data) setEditing({ initial: data.pricing, modelRevision: data.modelRevision }); }}>{copy("pricing.editTokenPricing_44ae61")}</SettingsActionButton>{!compact ? <SettingsActionButton icon={SettingsActionIcon.Back} onClick={close}>{copy("pricing.backToModels_a28c02")}</SettingsActionButton> : null}</div></>}
  </section>;
}
