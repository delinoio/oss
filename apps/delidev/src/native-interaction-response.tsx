import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { InteractionDraftKind, useEditableInteractionDraft, type InteractionDraftState } from "./inbox-drafts";
import { nativeResponseByteLength, nativeResponseLimit, nativeResponseOverflow } from "./native-response-bounds";

type NativeQuestion = { text: string; header: string; options: { label: string; description: string }[]; multiple?: boolean; custom?: boolean };
enum NativePermissionDecision { Once = "once", Always = "always", Reject = "reject" }
const encoder = new TextEncoder();
const validText = (v: string) => !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && encoder.encode(v).length <= 64 * 1024;
type QuestionDraft = Extract<InteractionDraftState, { kind: InteractionDraftKind.OpenCodeQuestion }>;
type PermissionDraft = Extract<InteractionDraftState, { kind: InteractionDraftKind.OpenCodePermission }>;

export function NativeQuestionResponse({ resource, questions, closed, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; questions: NativeQuestion[]; closed: boolean; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  const responseFor = (value: QuestionDraft) => ({ opencode: { answers: questions.map((_, i) => value.unanswered[i] ? [] : [...(value.selected[i] ?? []), ...(value.customEnabled[i] ? [value.custom[i] ?? ""] : [])]) } });
  const [editable, setEditable, editProblem] = useEditableInteractionDraft<QuestionDraft>(InteractionDraftKind.OpenCodeQuestion, () => ({ kind: InteractionDraftKind.OpenCodeQuestion, selected: questions.map(() => []), custom: {}, customEnabled: {}, unanswered: {} }), draft, saveDraft, (value) => nativeResponseOverflow([
    { values: [...Object.values(value.custom), ...value.selected.flat()], limit: 64 * 1024, guidance: "Keep each OpenCode answer within 64 KiB." },
  ], () => responseFor(value)));
  const { selected, custom, customEnabled, unanswered } = editable;
  const selectedRows = questions.map((_, index) => selected[index] ?? []);
  const mutation = useRetainedMutation(`opencode-answer:${resource.id}`, InteractionQuery.respondQuestion, (r) => accepted(r.interaction));
  const response = responseFor(editable);
  const { answers } = response.opencode;
  const missing = answers.some((row, i) => row.length === 0 && !unanswered[i]);
  const invalid = answers.some((row, i) => (!questions[i]?.multiple && row.length > 1) || new Set(row).size !== row.length || row.some((v) => !validText(v) || questions[i]!.options.filter((o) => o.label === v).length > 1));
  const oversized = nativeResponseByteLength(response) > nativeResponseLimit;
  const blocked = closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  return <form aria-label="Answer original OpenCode questions" onSubmit={(event) => { event.preventDefault(); if (blocked || missing || invalid || oversized) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(response) }); }}>
    <fieldset disabled={blocked}>
      {questions.map((q, i) => <fieldset key={i}><legend>{q.header || `Question ${i + 1}`}</legend>
        {q.options.map((option, j) => <label className="checkbox" key={j}><input type={q.multiple ? "checkbox" : "radio"} name={`native-question-${resource.id}-${i}`} checked={(selected[i] ?? []).includes(option.label)} disabled={q.options.filter((o) => o.label === option.label).length > 1} onChange={(event) => {
          setEditable({ ...editable, unanswered: { ...unanswered, [i]: false }, customEnabled: !q.multiple ? { ...customEnabled, [i]: false } : customEnabled, selected: selectedRows.map((row, index) => index !== i ? row : event.target.checked ? q.multiple ? [...row, option.label] : [option.label] : row.filter((v) => v !== option.label)) });
        }} /><span>{option.label || "(Empty option)"}</span></label>)}
        {q.options.some((option, j) => q.options.findIndex((o) => o.label === option.label) !== j) ? <p>Duplicate native option labels cannot be selected unambiguously.</p> : null}
        {q.custom !== false ? <><label className="checkbox"><input type="checkbox" checked={customEnabled[i] ?? false} onChange={(event) => setEditable({ ...editable, customEnabled: { ...customEnabled, [i]: event.target.checked }, unanswered: { ...unanswered, [i]: false }, selected: !q.multiple ? selectedRows.map((row, index) => index === i ? [] : row) : selectedRows })} />Use a custom answer</label>{customEnabled[i] ? <label>Custom answer for question {i + 1}<textarea value={custom[i] ?? ""} onChange={(event) => setEditable({ ...editable, custom: { ...custom, [i]: event.target.value } })} /></label> : null}</> : null}
        <label className="checkbox"><input type="checkbox" checked={unanswered[i] ?? false} onChange={(event) => setEditable({ ...editable, unanswered: { ...unanswered, [i]: event.target.checked }, ...(event.target.checked ? { selected: selectedRows.map((row, index) => index === i ? [] : row), customEnabled: { ...customEnabled, [i]: false } } : {}) })} />Leave question {i + 1} unanswered</label>
      </fieldset>)}
      <button className="primary" disabled={missing || invalid || oversized}>Send answers</button>
      <button type="button" onClick={() => { if (blocked) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ opencode: { reject: true } }) }); }}>Reject question request</button>
    </fieldset>
    {editProblem ? <p role="alert">{editProblem}</p> : null}
    {invalid ? <p role="alert">Use valid, distinct answers that match each original question.</p> : null}
    {oversized ? <p role="alert">The complete response is too large. Shorten it without omitting question rows.</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>Retry the same response request</button> : null}
  </form>;
}

export function NativePermissionResponse({ resource, closed, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; closed: boolean; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  const rejectionFor = (value: PermissionDraft) => ({ opencode: { decision: NativePermissionDecision.Reject, ...(value.feedbackEnabled ? { feedback: value.feedback } : {}) } });
  const [editable, setEditable, editProblem] = useEditableInteractionDraft<PermissionDraft>(InteractionDraftKind.OpenCodePermission, () => ({ kind: InteractionDraftKind.OpenCodePermission, feedbackEnabled: false, feedback: "" }), draft, saveDraft, (value) => nativeResponseOverflow([
    { values: [value.feedback], limit: 64 * 1024, guidance: "Keep OpenCode correction feedback within 64 KiB." },
  ], () => rejectionFor(value)));
  const { feedbackEnabled, feedback } = editable;
  const mutation = useRetainedMutation(`opencode-approve:${resource.id}`, InteractionQuery.respondApproval, (r) => accepted(r.interaction));
  const blocked = closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  const rejection = rejectionFor(editable);
  const invalidFeedback = feedbackEnabled && (!validText(feedback) || nativeResponseByteLength(rejection) > nativeResponseLimit);
  const send = (decision: NativePermissionDecision) => {
    if (blocked || decision === NativePermissionDecision.Reject && invalidFeedback) return;
    void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(decision === NativePermissionDecision.Reject ? rejection : { opencode: { decision } }) });
  };
  return <form aria-label="Respond to original OpenCode permission" onSubmit={(event) => event.preventDefault()}>
    <p>Session allowance uses the native patterns listed above and applies only to this native session.</p>
    <fieldset disabled={blocked}>
      <button type="button" className="primary" onClick={() => send(NativePermissionDecision.Once)}>Allow once</button>
      <button type="button" onClick={() => send(NativePermissionDecision.Always)}>Allow for this native session</button>
      <label className="checkbox"><input type="checkbox" checked={feedbackEnabled} onChange={(event) => setEditable({ ...editable, feedbackEnabled: event.target.checked })} />Include correction feedback with rejection</label>
      {feedbackEnabled ? <label>Correction feedback<textarea value={feedback} onChange={(event) => setEditable({ ...editable, feedback: event.target.value })} /><small>Nonempty feedback lets this tool rejection continue. Other pending requests may also be rejected and stop the native run.</small></label> : null}
      <button type="button" disabled={invalidFeedback} onClick={() => send(NativePermissionDecision.Reject)}>Reject permission request</button>
    </fieldset>
    {editProblem ? <p role="alert">{editProblem}</p> : null}
    {invalidFeedback ? <p role="alert">Keep valid correction feedback within 64 KiB and the complete response within 256 KiB.</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>Retry the same response request</button> : null}
  </form>;
}
