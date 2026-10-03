import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { BudgetState, SessionQuery, newRequestId, type Resource, type SessionBudgetView } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export interface BudgetDraft { enabled: boolean; currency: string; threshold: string }
export const emptyBudget: BudgetDraft = { enabled: false, currency: "", threshold: "" };
export function budgetInput(draft: BudgetDraft) {
  if (!draft.enabled) return undefined;
  if (!/^[A-Z]{3}$/.test(draft.currency) || !/^(0|[1-9][0-9]{0,17})(\.[0-9]{1,15})?$/.test(draft.threshold)) throw new Error("Provide an uppercase three-letter budget currency and a nonnegative decimal threshold with at most fifteen fractional digits.");
  return { currency: draft.currency, threshold: draft.threshold };
}
export function BudgetFields({ draft, change }: { draft: BudgetDraft; change: (value: BudgetDraft) => void }) {
  return <><label className="checkbox"><input type="checkbox" checked={draft.enabled} onChange={(event) => change({ ...draft, enabled: event.target.checked })} />Enable estimated-cost budget</label>{draft.enabled ? <><label>Budget currency<input value={draft.currency} maxLength={3} placeholder="USD" onChange={(event) => change({ ...draft, currency: event.target.value })} /></label><label>Estimated-cost threshold<input inputMode="decimal" value={draft.threshold} onChange={(event) => change({ ...draft, threshold: event.target.value })} /></label></> : null}<p>A budget gates new turns and Resume at the known subtotal. Pending input is retained and accepted work is not interrupted. This does not set a billing ceiling.</p></>;
}
function BudgetEditor({ initial, current, readError, saved, cancel }: { initial: SessionBudgetView; current?: SessionBudgetView; readError: unknown; saved: (value?: SessionBudgetView) => void; cancel: () => void }) {
  const [revision, setRevision] = useState(initial.session!.revision);
  const [draft, setDraft] = useState<BudgetDraft>(() => initial.budget ? { enabled: true, ...initial.budget } : emptyBudget);
  const [problem, setProblem] = useState("");
  const mutation = useRetainedMutation(`budget:${initial.session!.id}`, SessionQuery.setSessionBudget, (value) => saved(value.view));
  const stale = Boolean(current?.session && current.session.revision !== revision);
  const blocked = mutation.busy || mutation.uncertain;
  return <form onSubmit={(event) => {
    event.preventDefault(); if (blocked || stale || readError || !current?.session) return;
    try { const budget = budgetInput(draft); setProblem(""); void mutation.send({ mutation: { id: initial.session!.id, expectedRevision: revision, requestId: newRequestId() }, change: budget ? { case: "budget", value: budget } : { case: "remove", value: true } }); }
    catch (error) { setProblem(error instanceof Error ? error.message : "Review the budget fields."); }
  }}><fieldset disabled={blocked}><BudgetFields draft={draft} change={setDraft} /></fieldset>
    <p>Raising or removing a budget allows already eligible queued turns. Paused and archived sessions stay paused.</p>
    {stale ? <p role="alert">The session changed. Your budget draft is retained. Review the current status before using its latest revision.</p> : null}
    {stale && !blocked ? <button type="button" disabled={Boolean(readError)} onClick={() => { if (current?.session) setRevision(current.session.revision); }}>Use latest revision with this draft</button> : null}
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={readError || mutation.error} />
    <div className="actions"><button className="primary" disabled={blocked || stale || Boolean(readError) || !current?.session}>Save session budget</button>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same budget</button> : null}<button type="button" disabled={blocked} onClick={cancel}>Cancel budget edit</button></div>
  </form>;
}
export function SessionBudget({ resource, changed, blocked }: { resource: Resource; changed: (value: Resource) => void; blocked: (value: boolean) => void }) {
  const result = useQuery(SessionQuery.getSessionBudget, { sessionId: resource.id }, { refetchInterval: 5000 });
  const [editing, setEditing] = useState<SessionBudgetView>();
  const [missing, setMissing] = useState(false);
  const view = result.data?.view;
  useEffect(() => { blocked(view?.state === BudgetState.THRESHOLD_REACHED); }, [view?.state, blocked]);
  useEffect(() => { if (view?.session && !result.error) setMissing(false); }, [result.dataUpdatedAt, view?.session, result.error]);
  const current = view?.selectedCurrency;
  return <section aria-label="Session estimated-cost budget" className="session-budget"><header><h3>Estimated-cost budget</h3><button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh budget</button></header>
    <Problem error={result.error} />{view && result.error ? <p className="notice">The displayed budget evidence may be stale. New execution is checked by the server.</p> : null}
    {view?.budget ? <><p>Threshold: {view.budget.currency} {view.budget.threshold} · Known lifetime subtotal: {current?.knownAmount ? `${current.currency} ${current.knownAmount}` : "Unavailable"}</p>
      {view.state === BudgetState.THRESHOLD_REACHED ? <p role="alert">Budget threshold reached. New turns and Resume are blocked; pending input and accepted work are retained.</p> : view.state === BudgetState.ALLOW_INCOMPLETE ? <p className="notice">Incomplete evidence: new execution may proceed below the known threshold. This does not verify budget compliance or actual spend.</p> : <p className="notice">Budget evaluation is unavailable. The server must check new execution.</p>}
      <p>{current?.completeResponses.toLocaleString() ?? "0"} responses with complete token-price categories · {current?.partialResponses.toLocaleString() ?? "0"} partial · {current?.unavailableResponses.toLocaleString() ?? "0"} unavailable. {view.unpricedResponses.toLocaleString()} unpriced responses and {view.otherCurrencyResponses.toLocaleString()} responses in other currencies are outside this subtotal. Missing native telemetry is unavailable, not zero.</p>
      <p>{current?.completeNativeUnits.toLocaleString() ?? "0"} native units with complete categories · {current?.partialNativeUnits.toLocaleString() ?? "0"} partial · {current?.unavailableNativeUnits.toLocaleString() ?? "0"} unavailable. {view.unpricedNativeUnits.toLocaleString()} unpriced native units and {view.otherCurrencyNativeUnits.toLocaleString()} native units in other currencies are outside this subtotal.</p>
    </> : view ? <p>No estimated-cost budget is configured.</p> : <p role="status">Loading budget…</p>}
    {missing ? <p role="alert">The server acknowledged the budget without a readable result. Refresh the current session before making another change.</p> : null}
    {editing ? <BudgetEditor initial={editing} current={view} readError={result.error} saved={(value) => { setEditing(undefined); setMissing(!value?.session); if (value?.session) changed(value.session); void result.refetch(); }} cancel={() => setEditing(undefined)} /> : <button disabled={!view?.session || Boolean(result.error || result.isFetching || missing)} onClick={() => { if (view?.session) setEditing(view); }}>Edit session budget</button>}
  </section>;
}
