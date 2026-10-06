import { LocalizedText, copy, useLocale } from "./localization";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { InteractionDraftKind, useEditableInteractionDraft, type InteractionDraftState } from "./inbox-drafts";

type NativeQuestion = { text: string; header: string; options: { label: string; description: string }[]; multiple?: boolean; custom?: boolean };
enum NativePermissionDecision { Once = "once", Always = "always", Reject = "reject" }
const encoder = new TextEncoder();
const validText = (v: string) => !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && encoder.encode(v).length <= 64 * 1024;

export function NativeQuestionResponse({ resource, questions, closed, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; questions: NativeQuestion[]; closed: boolean; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  useLocale();
  const [editable, setEditable] = useEditableInteractionDraft<Extract<InteractionDraftState, { kind: InteractionDraftKind.OpenCodeQuestion }>>(InteractionDraftKind.OpenCodeQuestion, () => ({ kind: InteractionDraftKind.OpenCodeQuestion, selected: questions.map(() => []), custom: {}, customEnabled: {}, unanswered: {} }), draft, saveDraft);
  const { selected, custom, customEnabled, unanswered } = editable;
  const selectedRows = questions.map((_, index) => selected[index] ?? []);
  const mutation = useRetainedMutation(`opencode-answer:${resource.id}`, InteractionQuery.respondQuestion, (r) => accepted(r.interaction));
  const answers = questions.map((_, i) => unanswered[i] ? [] : [...(selected[i] ?? []), ...(customEnabled[i] ? [custom[i] ?? ""] : [])]);
  const response = { opencode: { answers } };
  const missing = answers.some((row, i) => row.length === 0 && !unanswered[i]);
  const invalid = answers.some((row, i) => (!questions[i]?.multiple && row.length > 1) || new Set(row).size !== row.length || row.some((v) => !validText(v) || questions[i]!.options.filter((o) => o.label === v).length > 1));
  const oversized = encode(response).byteLength > 256 * 1024;
  const blocked = closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  return <form aria-label={copy("native-interaction-response.answerOriginalOpencodeQuestions_872727")} onSubmit={(event) => { event.preventDefault(); if (blocked || missing || invalid || oversized) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(response) }); }}>
    <fieldset disabled={blocked}>
      {questions.map((q, i) => <fieldset key={i}><legend>{q.header || `Question ${i + 1}`}</legend>
        {q.options.map((option, j) => <label className="checkbox" key={j}><input type={q.multiple ? "checkbox" : "radio"} name={`native-question-${resource.id}-${i}`} checked={(selected[i] ?? []).includes(option.label)} disabled={q.options.filter((o) => o.label === option.label).length > 1} onChange={(event) => {
          setEditable({ ...editable, unanswered: { ...unanswered, [i]: false }, customEnabled: !q.multiple ? { ...customEnabled, [i]: false } : customEnabled, selected: selectedRows.map((row, index) => index !== i ? row : event.target.checked ? q.multiple ? [...row, option.label] : [option.label] : row.filter((v) => v !== option.label)) });
        }} /><span>{option.label || "(Empty option)"}</span></label>)}
        {q.options.some((option, j) => q.options.findIndex((o) => o.label === option.label) !== j) ? <p>{copy("native-interaction-response.duplicateNativeOptionLabelsCannotBe_a1c083")}</p> : null}
        {q.custom !== false ? <><label className="checkbox"><input type="checkbox" checked={customEnabled[i] ?? false} onChange={(event) => setEditable({ ...editable, customEnabled: { ...customEnabled, [i]: event.target.checked }, unanswered: { ...unanswered, [i]: false }, selected: !q.multiple ? selectedRows.map((row, index) => index === i ? [] : row) : selectedRows })} />{copy("native-interaction-response.useACustomAnswer_862089")}</label>{customEnabled[i] ? <label><LocalizedText id="native-interaction-response.customAnswerForQuestion_76fa79" components={{ s0: <>{i + 1}</> }} /><textarea value={custom[i] ?? ""} onChange={(event) => setEditable({ ...editable, custom: { ...custom, [i]: event.target.value } })} /></label> : null}</> : null}
        <label className="checkbox"><input type="checkbox" checked={unanswered[i] ?? false} onChange={(event) => setEditable({ ...editable, unanswered: { ...unanswered, [i]: event.target.checked }, ...(event.target.checked ? { selected: selectedRows.map((row, index) => index === i ? [] : row), customEnabled: { ...customEnabled, [i]: false } } : {}) })} /><LocalizedText id="native-interaction-response.leaveQuestionUnanswered_c5d878" components={{ s0: <>{i + 1}</> }} /></label>
      </fieldset>)}
      <button className="primary" disabled={missing || invalid || oversized}>{copy("native-interaction-response.sendAnswers_8a7856")}</button>
      <button type="button" onClick={() => { if (blocked) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ opencode: { reject: true } }) }); }}>{copy("native-interaction-response.rejectQuestionRequest_d50673")}</button>
    </fieldset>
    {invalid ? <p role="alert">{copy("native-interaction-response.useValidDistinctAnswersThatMatch_1fc0d6")}</p> : null}
    {oversized ? <p role="alert">{copy("native-interaction-response.theCompleteResponseIsTooLarge_fbbffb")}</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>{copy("native-interaction-response.retryTheSameResponseRequest_ebd80d")}</button> : null}
  </form>;
}

export function NativePermissionResponse({ resource, closed, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; closed: boolean; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  useLocale();
  const [editable, setEditable] = useEditableInteractionDraft<Extract<InteractionDraftState, { kind: InteractionDraftKind.OpenCodePermission }>>(InteractionDraftKind.OpenCodePermission, () => ({ kind: InteractionDraftKind.OpenCodePermission, feedbackEnabled: false, feedback: "" }), draft, saveDraft);
  const { feedbackEnabled, feedback } = editable;
  const mutation = useRetainedMutation(`opencode-approve:${resource.id}`, InteractionQuery.respondApproval, (r) => accepted(r.interaction));
  const blocked = closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  const rejection = { opencode: { decision: NativePermissionDecision.Reject, ...(feedbackEnabled ? { feedback } : {}) } };
  const invalidFeedback = feedbackEnabled && (!validText(feedback) || encode(rejection).byteLength > 256 * 1024);
  const send = (decision: NativePermissionDecision) => {
    if (blocked || decision === NativePermissionDecision.Reject && invalidFeedback) return;
    void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(decision === NativePermissionDecision.Reject ? rejection : { opencode: { decision } }) });
  };
  return <form aria-label={copy("native-interaction-response.respondToOriginalOpencodePermission_78b0d2")} onSubmit={(event) => event.preventDefault()}>
    <p>{copy("native-interaction-response.sessionAllowanceUsesTheNativePatterns_2193c7")}</p>
    <fieldset disabled={blocked}>
      <button type="button" className="primary" onClick={() => send(NativePermissionDecision.Once)}>{copy("native-interaction-response.allowOnce_168511")}</button>
      <button type="button" onClick={() => send(NativePermissionDecision.Always)}>{copy("native-interaction-response.allowForThisNativeSession_76b040")}</button>
      <label className="checkbox"><input type="checkbox" checked={feedbackEnabled} onChange={(event) => setEditable({ ...editable, feedbackEnabled: event.target.checked })} />{copy("native-interaction-response.includeCorrectionFeedbackWithRejection_199c84")}</label>
      {feedbackEnabled ? <label>{copy("native-interaction-response.correctionFeedback_c92684")}<textarea value={feedback} onChange={(event) => setEditable({ ...editable, feedback: event.target.value })} /><small>{copy("native-interaction-response.nonemptyFeedbackLetsThisToolRejection_8284bd")}</small></label> : null}
      <button type="button" disabled={invalidFeedback} onClick={() => send(NativePermissionDecision.Reject)}>{copy("native-interaction-response.rejectPermissionRequest_3b124c")}</button>
    </fieldset>
    {invalidFeedback ? <p role="alert">{copy("native-interaction-response.keepValidCorrectionFeedbackWithin64_ca809b")}</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>{copy("native-interaction-response.retryTheSameResponseRequest_ebd80d")}</button> : null}
  </form>;
}
