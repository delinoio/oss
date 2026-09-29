import { useState } from "react";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode, items, object, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { InteractionDraftKind, useEditableInteractionDraft, type InteractionDraftState } from "./inbox-drafts";

export enum GrokRequestKind { File = "file-permission", Question = "question", Plan = "plan-approval" }
export enum GrokDecision { Once = "allow-once", Session = "allow-edits-session", Reject = "reject-once", Approve = "approved", Revise = "cancelled", Abandon = "abandoned" }
export enum GrokQuestionOutcome { Accepted = "accepted", Cancelled = "cancelled", Skipped = "skip_interview" }
const uuid = (v: unknown) => typeof v === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v);
const bounded = (v: unknown, max: number): v is string => typeof v === "string" && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const nonempty = (v: unknown, max: number) => bounded(v, max) && v.trim().length > 0;
const digest = (v: unknown) => typeof v === "string" && /^[0-9a-f]{64}$/.test(v);
function validRequest(data: Document, id?: string): Document | undefined {
  const r = object(data.grok), request = object(data.native_request_id);
  if (r.version !== "1.0.41" || !uuid(data.execution_id) || !uuid(data.native_thread_id) || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(text(data.native_turn_id)) || !uuid(r.arrival_id) || id !== undefined && id !== r.arrival_id || request.kind !== "text" || request.text !== r.arrival_id || request.number != null || !digest(r.request_digest) || !digest(r.proposal_digest) || !["default", "plan"].includes(text(r.mode)) || !nonempty(data.native_item_id, 256) || data.questions != null || data.approval != null || data.opencode != null || data.claude != null || !Object.keys(r).every((k) => ["version", "kind", "arrival_id", "request_digest", "proposal_digest", "mode", "tool_name", "path", "content", "questions", "plan"].includes(k)) || encode(r).byteLength > 512 * 1024) return undefined;
  if (r.kind === GrokRequestKind.File) {
    if (data.type !== "native-approval" || r.mode !== "default" || r.tool_name !== "write" || !nonempty(r.path, 8192) || r.content !== undefined && !bounded(r.content, 256 * 1024) || r.plan != null || r.questions != null || data.response != null) return undefined;
  } else if (r.kind === GrokRequestKind.Plan) {
    const p = object(r.plan);
    if (data.type !== "native-approval" || r.mode !== "plan" || r.tool_name !== "exit_plan_mode" || r.path !== undefined || r.content !== undefined || r.questions != null || data.response != null || !nonempty(p.entry_tool_id, 256) || !nonempty(p.write_tool_id, 256) || p.entry_tool_id === p.write_tool_id || !text(p.entry_event_id).startsWith(`${text(data.native_thread_id)}-`) || !Number.isSafeInteger(p.revision) || Number(p.revision) < 1 || Number(p.revision) > 128 || !nonempty(p.content, 256 * 1024) || !digest(p.content_digest)) return undefined;
  } else if (r.kind === GrokRequestKind.Question) {
    if (data.type !== "user-question" || r.tool_name !== "ask_user_question" || r.path !== undefined || r.content !== undefined || r.plan != null || data.approval_response != null || !Array.isArray(r.questions) || r.questions.length === 0 || r.questions.length > 32) return undefined;
    const keys = new Set<string>();
    for (const value of r.questions) {
      const q = object(value);
      if (!nonempty(q.question, 16 * 1024) || keys.has(text(q.question)) || q.multiSelect !== null && typeof q.multiSelect !== "boolean" || !Array.isArray(q.options) || q.options.length === 0 || q.options.length > 64) return undefined;
      keys.add(text(q.question));
      const labels = new Set<string>();
      for (const option of q.options) { const o = object(option); if (!nonempty(o.label, 4096) || !bounded(o.description, 16 * 1024) || labels.has(text(o.label))) return undefined; labels.add(text(o.label)); }
    }
  } else return undefined;
  return r;
}

export function NativeGrokInteraction({ data, resource, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { data: Document; resource?: Resource; accepted?: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  const request = validRequest(data, resource?.id);
  if (!request) return <p role="status">The retained Grok request is unavailable or inconsistent.</p>;
  const question = request.kind === GrokRequestKind.Question;
  return <section aria-label="Original Grok request">
    <p>{text(request.tool_name)} · {text(request.mode)}</p>
    {request.kind === GrokRequestKind.File ? <><p>Requested path: <code>{text(request.path)}</code></p><pre>{text(request.content)}</pre></> : null}
    {request.kind === GrokRequestKind.Plan ? <><p>Plan revision {Number(object(request.plan).revision)}</p><pre>{text(object(request.plan).content)}</pre></> : null}
    {question ? <ol>{items(request.questions).map((value, index) => { const q = object(value); return <li key={index}><p>{text(q.question)}</p><p>{q.multiSelect === true ? "Multiple choices offered" : q.multiSelect === false ? "Single choice offered" : "Native selection default"}</p><ul>{items(q.options).map((v, i) => { const o = object(v); return <li key={i}>{text(o.label)} — {text(o.description)}</li>; })}</ul></li>; })}</ol> : null}
    <p>{object(data.response ?? data.approval_response).state === "accepted" ? "Grok processed the original response. Execution outcome and cleanup are separate." : data.response != null || data.approval_response != null ? "Response retained; transmission and native acceptance are separate observations." : "Waiting for your response."}</p>
    {resource && accepted ? <GrokResponse key={resource.id} resource={resource} request={request} accepted={accepted} draft={draft} saveDraft={saveDraft} closed={data.closure !== "open" || data.response != null || data.approval_response != null} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : null}
  </section>;
}

function GrokResponse({ resource, request, accepted, draft, saveDraft, closed, submissionAllowed, receiptRetryAllowed }: { resource: Resource; request: Document; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; closed: boolean; submissionAllowed: boolean; receiptRetryAllowed: boolean }) {
  const [editable, setEditable] = useEditableInteractionDraft<Extract<InteractionDraftState, { kind: InteractionDraftKind.Grok }>>(InteractionDraftKind.Grok, () => ({ kind: InteractionDraftKind.Grok, answers: {}, outcome: GrokQuestionOutcome.Accepted, decision: "" }), draft, saveDraft);
  const [problem, setProblem] = useState("");
  const question = request.kind === GrokRequestKind.Question;
  const questionMutation = useRetainedMutation(`grok-answer:${resource.id}`, InteractionQuery.respondQuestion, (r) => accepted(r.interaction));
  const approvalMutation = useRetainedMutation(`grok-approve:${resource.id}`, InteractionQuery.respondApproval, (r) => accepted(r.interaction));
  const mutation = question ? questionMutation : approvalMutation;
  const blocked = closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  const answers = Object.fromEntries(items(request.questions).flatMap((v) => { const key = text(object(v).question), answer = Object.hasOwn(editable.answers, key) ? editable.answers[key] : ""; return answer ? [[key, answer]] : []; }));
  const reply = question ? { grok: editable.outcome === GrokQuestionOutcome.Cancelled ? { outcome: editable.outcome } : editable.outcome === GrokQuestionOutcome.Skipped ? { outcome: editable.outcome, partial_answers: answers } : { outcome: editable.outcome, answers } } : { grok: { decision: editable.decision } };
  const decisions = request.kind === GrokRequestKind.Plan ? [[GrokDecision.Approve, "Approve this revision"], [GrokDecision.Revise, "Keep Plan mode and revise"], [GrokDecision.Abandon, "Abandon Plan mode"]] : [[GrokDecision.Once, "Allow once"], [GrokDecision.Session, "Allow edits for this native session"], [GrokDecision.Reject, "Reject once"]];
  const missing = question ? editable.outcome === GrokQuestionOutcome.Accepted && Object.keys(answers).length !== items(request.questions).length : !decisions.some(([v]) => v === editable.decision);
  const invalid = Object.values(answers).some((v) => !nonempty(v, 64 * 1024)) || encode(reply).byteLength > 256 * 1024;
  return <form aria-label="Respond to original Grok request" onSubmit={(event) => {
    event.preventDefault(); if (blocked || missing || invalid) return;
    void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(reply) });
  }}>
    <fieldset disabled={blocked}>
      {question ? <><label>Interview outcome<select value={editable.outcome} onChange={(e) => setEditable({ ...editable, outcome: e.target.value as GrokQuestionOutcome })}><option value={GrokQuestionOutcome.Accepted}>Send complete answers</option><option value={GrokQuestionOutcome.Skipped}>Skip interview with partial answers</option><option value={GrokQuestionOutcome.Cancelled}>Cancel interview</option></select></label>
        {editable.outcome !== GrokQuestionOutcome.Cancelled ? items(request.questions).map((value, index) => { const q = object(value), key = text(q.question); return <label key={key}>Exact answer for question {index + 1}<textarea value={Object.hasOwn(editable.answers, key) ? editable.answers[key] : ""} onChange={(e) => {
          const next = { ...editable, answers: { ...editable.answers, [key]: e.target.value } };
          if (encode(next).byteLength > 256 * 1024) { setProblem("The complete answer is too large."); return; }
          setEditable(next); setProblem("");
        }} /></label>; }) : null}</> : <label>Decision<select required value={editable.decision} onChange={(e) => setEditable({ ...editable, decision: e.target.value as GrokDecision })}><option value="">Select a decision</option>{decisions.map(([v, label]) => <option key={v} value={v}>{label}</option>)}</select></label>}
      <button className="primary" disabled={missing || invalid}>Send response to Grok</button>
    </fieldset>
    {invalid || problem ? <p role="alert">{problem || "Use valid text within the response limit."}</p> : null}<Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>Retry the same response request</button> : null}
  </form>;
}
