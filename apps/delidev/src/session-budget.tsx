import { formatDecimal, productError, ProductError, ownedMessage, useProductMessage, LocalizedText, copy, displayLocale, useLocale  } from "./localization";
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { BudgetState, SessionQuery, UsageCoverage, newRequestId, type Resource, type SessionBudgetView } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export interface BudgetDraft { enabled: boolean; currency: string; threshold: string }
export const emptyBudget: BudgetDraft = { enabled: false, currency: "", threshold: "" };
export function budgetInput(draft: BudgetDraft) {
  if (!draft.enabled) return undefined;
  if (!/^[A-Z]{3}$/.test(draft.currency) || !/^(0|[1-9][0-9]{0,17})(\.[0-9]{1,15})?$/.test(draft.threshold)) throw new ProductError("validation.6dd2f4b85563");
  return { currency: draft.currency, threshold: draft.threshold };
}
export function BudgetFields({ draft, change }: { draft: BudgetDraft; change: (value: BudgetDraft) => void }) {
  useLocale();
  return <><label className="checkbox"><input type="checkbox" checked={draft.enabled} onChange={(event) => change({ ...draft, enabled: event.target.checked })} />{copy("session-budget.enableEstimatedCostBudget_ed90b8")}</label>{draft.enabled ? <><label>{copy("session-budget.budgetCurrency_e4cbc5")}<input value={draft.currency} maxLength={3} placeholder={copy("session-budget.usd_a26cdf")} onChange={(event) => change({ ...draft, currency: event.target.value })} /></label><label>{copy("session-budget.estimatedCostThreshold_a8ed40")}<input inputMode="decimal" value={draft.threshold} onChange={(event) => change({ ...draft, threshold: event.target.value })} /></label></> : null}<p>{copy("session-budget.aBudgetGatesNewTurnsAnd_29da6f")}</p></>;
}
function BudgetEditor({ initial, current, readError, saved, cancel }: { initial: SessionBudgetView; current?: SessionBudgetView; readError: unknown; saved: (value?: SessionBudgetView) => void; cancel: () => void }) {
  useLocale();
  const [revision, setRevision] = useState(initial.session!.revision);
  const [draft, setDraft] = useState<BudgetDraft>(() => initial.budget ? { enabled: true, ...initial.budget } : emptyBudget);
  const [problem, setProblem] = useProductMessage("");
  const mutation = useRetainedMutation(`budget:${initial.session!.id}`, SessionQuery.setSessionBudget, (value) => saved(value.view));
  const stale = Boolean(current?.session && current.session.revision !== revision);
  const blocked = mutation.busy || mutation.uncertain;
  return <form onSubmit={(event) => {
    event.preventDefault(); if (blocked || stale || readError || !current?.session) return;
    try { const budget = budgetInput(draft); setProblem(""); void mutation.send({ mutation: { id: initial.session!.id, expectedRevision: revision, requestId: newRequestId() }, change: budget ? { case: "budget", value: budget } : { case: "remove", value: true } }); }
    catch (error) { setProblem(productError(error, "session-budget.extra.96afe54d0f2e")); }
  }}><fieldset disabled={blocked}><BudgetFields draft={draft} change={setDraft} /></fieldset>
    <p>{copy("session-budget.raisingOrRemovingABudgetAllows_1d7a26")}</p>
    {stale ? <p role="alert">{copy("session-budget.theSessionChangedYourBudgetDraft_fa196c")}</p> : null}
    {stale && !blocked ? <button type="button" disabled={Boolean(readError)} onClick={() => { if (current?.session) setRevision(current.session.revision); }}>{copy("session-budget.useLatestRevisionWithThisDraft_4bbad4")}</button> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={readError || mutation.error} />
    <div className="actions"><button className="primary" disabled={blocked || stale || Boolean(readError) || !current?.session}>{copy("session-budget.saveSessionBudget_73ab19")}</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("session-budget.retryTheSameBudget_585ed3")}</button> : null}<button type="button" disabled={blocked} onClick={cancel}>{copy("session-budget.cancelBudgetEdit_6c8917")}</button></div>
  </form>;
}
export function SessionBudget({ resource, changed, blocked }: { resource: Resource; changed: (value: Resource) => void; blocked: (value: boolean) => void }) {
  useLocale();
  const result = useQuery(SessionQuery.getSessionBudget, { sessionId: resource.id }, { refetchInterval: 5000 });
  const [editing, setEditing] = useState<SessionBudgetView>();
  const [missing, setMissing] = useState(false);
  const view = result.data?.view;
  useEffect(() => { blocked(view?.state === BudgetState.THRESHOLD_REACHED); }, [view?.state, blocked]);
  useEffect(() => { if (view?.session && !result.error) setMissing(false); }, [result.dataUpdatedAt, view?.session, result.error]);
  const current = view?.selectedCurrency;
  return <section aria-label={copy("session-budget.sessionEstimatedCostBudget_3b3c9f")} className="session-budget"><header><h3>{copy("session-budget.estimatedCostBudget_b859b0")}</h3><button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("session-budget.refreshBudget_13bcdd")}</button></header>
    <Problem error={result.error} />{view && result.error ? <p className="notice">{copy("session-budget.theDisplayedBudgetEvidenceMayBe_7abe71")}</p> : null}
    {view?.budget ? <><p><LocalizedText id="session-budget.thresholdKnownLifetimeSubtotal_2c6c7e" components={{ s0: <>{view.budget.currency}</>, s1: <>{formatDecimal(view.budget.threshold)}</>, s2: <>{current?.knownAmount ? copy("session-budget.message_42d375", { v0: current.currency, v1: formatDecimal(current.knownAmount) }) : copy("session-budget.unavailable_ca1844")}</> }} /></p>
      {view.state === BudgetState.THRESHOLD_REACHED ? <p role="alert">{copy("session-budget.budgetThresholdReachedNewTurnsAnd_6236ce")}</p> : view.state === BudgetState.ALLOW_INCOMPLETE ? <p className="notice">{copy("session-budget.incompleteEvidenceNewExecutionMayProceed_6c86c6")}</p> : <p className="notice">{copy("session-budget.budgetEvaluationIsUnavailableTheServer_f17303")}</p>}
      <p><LocalizedText id="session-budget.responsesWithCompleteTokenPriceCategories_cc7aef" components={{ s0: <>{current?.completeResponses.toLocaleString(displayLocale()) ?? "0"}</>, s1: <>{current?.partialResponses.toLocaleString(displayLocale()) ?? "0"}</>, s2: <>{current?.unavailableResponses.toLocaleString(displayLocale()) ?? "0"}</>, s3: <>{view.unpricedResponses.toLocaleString(displayLocale())}</>, s4: <>{view.otherCurrencyResponses.toLocaleString(displayLocale())}</> }} /></p>
      {view.coverage === UsageCoverage.OBSERVED_ROOT_ACCOUNTING_UNITS ? <p><LocalizedText id="session-budget.nativeUnitsWithCompleteCategoriesPartial_8d9ff0" components={{ s0: <>{current?.completeNativeUnits.toLocaleString(displayLocale()) ?? "0"}</>, s1: <>{current?.partialNativeUnits.toLocaleString(displayLocale()) ?? "0"}</>, s2: <>{current?.unavailableNativeUnits.toLocaleString(displayLocale()) ?? "0"}</>, s3: <>{view.unpricedNativeUnits.toLocaleString(displayLocale())}</>, s4: <>{view.otherCurrencyNativeUnits.toLocaleString(displayLocale())}</> }} /></p> : <p>{copy("session-budget.nativeBudgetEvidenceIsUnavailableFrom_a6710d")}</p>}
    </> : view ? <p>{copy("session-budget.noEstimatedCostBudgetIsConfigured_e4cbfc")}</p> : <p role="status">{copy("session-budget.loadingBudget_ff9d54")}</p>}
    {missing ? <p role="alert">{copy("session-budget.theServerAcknowledgedTheBudgetWithout_6e9839")}</p> : null}
    {editing ? <BudgetEditor initial={editing} current={view} readError={result.error} saved={(value) => { setEditing(undefined); setMissing(!value?.session); if (value?.session) changed(value.session); void result.refetch(); }} cancel={() => setEditing(undefined)} /> : <button disabled={!view?.session || Boolean(result.error || result.isFetching || missing)} onClick={() => { if (view?.session) setEditing(view); }}>{copy("session-budget.editSessionBudget_ee0f8a")}</button>}
  </section>;
}
