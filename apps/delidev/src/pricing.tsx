import { useRetainSettingsTask } from "./settings-task-context";
import { SettingsTaskActions } from "./settings-task";
import { useState , useId } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { subscriptionServiceLabel, InputPricingMode, UsageQuery, newRequestId, type PricingVersion, type Resource, type TokenPricing } from "@delinoio/delidev-api-client";
import { resourceName } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function PricingBasis({ value }: { value: PricingVersion }) {
  const p = value.basis;
  if (!p) return <p>Pricing basis unavailable.</p>;
  return <div className="pricing-basis"><dl><dt>Source</dt><dd>{p.source}</dd><dt>As of</dt><dd>{p.asOf}</dd><dt>Currency</dt><dd>{p.currency}</dd><dt>Input pricing</dt><dd>{p.inputMode === InputPricingMode.UNIFORM ? "Uniform input (cache included)" : p.inputMode === InputPricingMode.CACHED_DISCOUNT ? "Separate uncached input and cached reads" : "Unknown mode"}</dd><dt>Input / million</dt><dd>{p.inputPerMillion ?? "Unavailable"}</dd>{p.inputMode === InputPricingMode.CACHED_DISCOUNT ? <><dt>Cached input / million</dt><dd>{p.cachedInputPerMillion ?? "Unavailable"}</dd></> : null}<dt>Output / million</dt><dd>{p.outputPerMillion ?? "Unavailable"}</dd></dl>
    {p.exclusions.length ? <><h4>Declared exclusions</h4><ul>{p.exclusions.map((value, index) => <li key={index}>{value}</li>)}</ul></> : null}
    <p>Only observed token categories are covered. Provider fees, taxes, currency conversion and unobserved usage are excluded. This is not actual spend or a billing ceiling.</p>
    <small>Version {value.revision.toString()} · {value.id}</small><small>Original model {value.modelId} · {value.subscriptionService ? `subscription service ${subscriptionServiceLabel(value.subscriptionService)}` : `provider ${value.providerId}`}</small>
  </div>;
}
interface Draft { currency: string; source: string; asOf: string; inputMode: InputPricingMode; input: string; cached: string; output: string; exclusions: string }
function draftPrice(value?: TokenPricing): Draft {
  return { currency: value?.currency ?? "", source: value?.source ?? "", asOf: value?.asOf ?? "", inputMode: value?.inputMode ?? InputPricingMode.UNIFORM, input: value?.inputPerMillion ?? "", cached: value?.cachedInputPerMillion ?? "", output: value?.outputPerMillion ?? "", exclusions: value?.exclusions.join("\n") ?? "" };
}
function priceInput(draft: Draft) {
  const rate = (value: string) => {
    if (!value) return undefined;
    if (!/^(0|[1-9][0-9]{0,17})(\.[0-9]{1,9})?$/.test(value)) throw new Error("Use nonnegative decimal rates with at most nine fractional digits; leave missing rates blank.");
    return value;
  };
  const date = new Date(`${draft.asOf}T00:00:00Z`);
  if (!/^[A-Z]{3}$/.test(draft.currency) || !draft.source.trim() || !/^\d{4}-\d{2}-\d{2}$/.test(draft.asOf) || !Number.isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== draft.asOf || draft.asOf < "1970-01-01") throw new Error("Provide a three-letter uppercase currency, source and valid as-of date.");
  const basis = { currency: draft.currency, source: draft.source, asOf: draft.asOf, inputMode: draft.inputMode, inputPerMillion: rate(draft.input), cachedInputPerMillion: draft.inputMode === InputPricingMode.CACHED_DISCOUNT ? rate(draft.cached) : undefined, outputPerMillion: rate(draft.output), exclusions: draft.exclusions ? draft.exclusions.split("\n") : [] };
  if (basis.inputPerMillion === undefined && basis.cachedInputPerMillion === undefined && basis.outputPerMillion === undefined) throw new Error("Provide at least one explicit rate. A zero rate must be entered as 0.");
  const bytes = (value: string) => new TextEncoder().encode(value).length;
  if (bytes(basis.source) > 2048 || basis.exclusions.length > 16 || basis.exclusions.some((value) => !value.trim() || bytes(value) > 512)) throw new Error("Keep the source within 2,048 bytes and use at most 16 nonempty exclusion lines of 512 bytes each.");
  return basis;
}
function PricingEditor({ model, initial, modelRevision, current, readError, saved, cancel }: { model: Resource; initial?: PricingVersion; modelRevision: bigint; current?: { pricing?: PricingVersion; modelRevision: bigint }; readError?: unknown; saved: (value?: PricingVersion) => void; cancel: () => void }) {
  const taskFormId = useId();
  const [draft, setDraft] = useState(() => draftPrice(initial?.basis));
  const [problem, setProblem] = useState("");
  const mutation = useRetainedMutation(`pricing:${model.id}`, UsageQuery.setModelPricing, (value) => saved(value.pricing));
  const stale = current && (current.modelRevision !== modelRevision || current.pricing?.id !== initial?.id);
  const blocked = mutation.busy || mutation.uncertain;
  const change = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));
  return <form id={`${taskFormId}-1`} onSubmit={(event) => {
    event.preventDefault(); if (blocked || stale || readError || !current) return;
    try { const basis = priceInput(draft); setProblem(""); void mutation.send({ mutation: { id: model.id, expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, expectedModelRevision: modelRevision, basis }); }
    catch (error) { setProblem(error instanceof Error ? error.message : "Review the pricing fields."); }
  }}><h4>New pricing version</h4><fieldset disabled={blocked}>
    <label>Currency<input value={draft.currency} maxLength={3} placeholder="USD" onChange={(event) => change("currency", event.target.value)} /></label>
    <label>Pricing source<textarea value={draft.source} maxLength={2048} onChange={(event) => change("source", event.target.value)} /></label>
    <label>As-of date<input type="date" value={draft.asOf} onChange={(event) => change("asOf", event.target.value)} /></label>
    <label>Input pricing mode<select value={draft.inputMode} onChange={(event) => change("inputMode", Number(event.target.value) as InputPricingMode)}><option value={InputPricingMode.UNIFORM}>Uniform input (cache included)</option><option value={InputPricingMode.CACHED_DISCOUNT}>Separate cached-read rate</option></select></label>
    <label>Input rate per million<input inputMode="decimal" value={draft.input} onChange={(event) => change("input", event.target.value)} /></label>
    {draft.inputMode === InputPricingMode.CACHED_DISCOUNT ? <label>Cached-input rate per million<input inputMode="decimal" value={draft.cached} onChange={(event) => change("cached", event.target.value)} /></label> : null}
    <label>Output rate per million<input inputMode="decimal" value={draft.output} onChange={(event) => change("output", event.target.value)} /></label>
    <label>Exclusions (one per line)<textarea value={draft.exclusions} onChange={(event) => change("exclusions", event.target.value)} /></label>
  </fieldset><p>Leave unavailable rates blank. Enter 0 only for an explicitly zero rate. New prices apply to future recorded responses; previous estimates keep their original basis.</p>
    {draft.inputMode === InputPricingMode.CACHED_DISCOUNT ? <p>Input/cache estimates require consistent native cache-read counts and explicitly zero cache writes. Missing or unsupported breakdowns remain unavailable.</p> : null}
    {stale ? <p role="alert">The model or price changed elsewhere. Your draft is retained. Cancel this edit and reopen current pricing before saving.</p> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={readError || mutation.error} />
    <SettingsTaskActions form={`${taskFormId}-1`} className=""><button className="primary" disabled={blocked || Boolean(stale || readError) || !current}>Save pricing version</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same price</button> : null}<button type="button" disabled={blocked} onClick={cancel}>Cancel pricing edit</button></SettingsTaskActions>
  </form>;
}
export function ModelPricing({ model, active, close }: { model: Resource; active: boolean; close: () => void }) {
  const current = useQuery(UsageQuery.getModelPricing, { modelId: model.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [editing, setEditing] = useState<{ initial?: PricingVersion; modelRevision: bigint }>();
  const [accepted, setAccepted] = useState<PricingVersion>();
  const [missingResult, setMissingResult] = useState(false);
  useRetainSettingsTask(missingResult);
  const data = current.data;
  return <section><header><h3>Token pricing · {resourceName(model)}</h3><button disabled={current.isFetching} onClick={() => void current.refetch()}>Refresh pricing</button></header>
    <p>Enter a source-backed estimate basis for this model. Rates are not fetched or verified automatically.</p>
    {accepted ? <p role="status">Accepted pricing version {accepted.revision.toString()}. Current selection is shown after refresh.</p> : null}
    {missingResult ? <p role="alert">The server acknowledged the price without a readable version. Inspect current pricing before starting another save.</p> : null}
    {editing ? <PricingEditor model={model} initial={editing.initial} modelRevision={editing.modelRevision} current={data} readError={current.error} saved={(value) => { setEditing(undefined); setAccepted(value); setMissingResult(!value); void current.refetch(); if (value) close(); }} cancel={() => setEditing(undefined)} /> : <><Problem error={current.error} />{data && current.error ? <p>The last retrieved pricing may be stale.</p> : null}{data?.pricing ? <PricingBasis value={data.pricing} /> : data ? <p>No pricing basis has been configured. Earlier responses stay unpriced.</p> : <p role="status">Loading pricing…</p>}
    {data && data.modelRevision !== model.revision ? <p role="alert">The model configuration changed. Return to Models and refresh before editing its pricing.</p> : null}
    <SettingsTaskActions className=""><button disabled={!data || Boolean(current.error || current.isFetching || missingResult) || data.modelRevision !== model.revision} onClick={() => { if (data) setEditing({ initial: data.pricing, modelRevision: data.modelRevision }); }}>Edit token pricing</button><button onClick={close}>Back to Models</button></SettingsTaskActions></>}
  </section>;
}
