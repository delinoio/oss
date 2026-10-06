import { LocalizedText, copy, useLocale } from "./localization";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { InteractionDraftKind, useEditableInteractionDraft, type InteractionDraftState } from "./inbox-drafts";
import { nativeResponseByteLength, nativeResponseLimit, nativeResponseOverflow } from "./native-response-bounds";

export type ClaudeQuestion = { question: string; header: string; multiSelect: boolean; options: { label: string; description: string }[] };
enum Behavior { Allow = "allow", Deny = "deny" }
type Reply = { behavior: Behavior; answers?: Record<string, string>; message?: string; interrupt?: boolean };
type ClaudeDraft = Extract<InteractionDraftState, { kind: InteractionDraftKind.Claude }>;
const validText = (v: string, limit: number) => !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= limit;

// Native question replies use text keys and comma-separated multiple choices.
// Custom answers remain exact strings; skipped questions are explicitly omitted.
export function NativeClaudeResponse({ resource, questions, closed, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; questions?: ClaudeQuestion[]; closed: boolean; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  useLocale();
  const replyFor = (value: ClaudeDraft): Reply => value.denial
    ? { behavior: Behavior.Deny, message: value.reason, ...(value.interrupt ? { interrupt: true } : {}) }
    : { behavior: Behavior.Allow, ...(questions ? { answers: Object.fromEntries(questions.flatMap((q, i) => value.skipped[i] ? [] : [[q.question, value.customEnabled[i] ? value.custom[i] ?? "" : (value.selected[i] ?? []).join(", ")]])) } : {}) };
  const [editable, setEditable, editProblem] = useEditableInteractionDraft<ClaudeDraft>(InteractionDraftKind.Claude, () => ({ kind: InteractionDraftKind.Claude, selected: questions?.map(() => []) ?? [], custom: {}, customEnabled: {}, skipped: {}, denial: false, reason: "", interrupt: false }), draft, saveDraft, (value) => nativeResponseOverflow([
    { values: [value.reason], limit: 4096, guidance: "Keep the Claude denial reason within 4 KiB." },
    { values: Object.values(value.custom), limit: nativeResponseLimit, guidance: "Keep each Claude answer within 256 KiB." },
  ], () => ({ claude: replyFor(value) })));
  const { selected, custom, customEnabled, skipped, denial, reason, interrupt } = editable;
  const selectedRows = questions?.map((_, index) => selected[index] ?? []) ?? [];
  const questionMutation = useRetainedMutation(`claude-answer:${resource.id}`, InteractionQuery.respondQuestion, (r) => accepted(r.interaction));
  const approvalMutation = useRetainedMutation(`claude-approve:${resource.id}`, InteractionQuery.respondApproval, (r) => accepted(r.interaction));
  const mutation = questions ? questionMutation : approvalMutation;
  const reply = replyFor(editable);
  const { answers } = reply;
  const missing = !denial && questions?.some((_, i) => !skipped[i] && !customEnabled[i] && !selected[i]?.length);
  const invalid = denial ? !reason.trim() || !validText(reason, 4096) : Object.values(answers ?? {}).some((v) => !validText(v, 256 * 1024));
  const oversized = nativeResponseByteLength({ claude: reply }) > nativeResponseLimit;
  const blocked = closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  return <form aria-label={copy("native-claude-response.respondToOriginalClaudeRequest_566f4d")} onSubmit={(event) => {
    event.preventDefault();
    if (blocked || missing || invalid || oversized) return;
    void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ claude: reply }) });
  }}>
    <fieldset disabled={blocked}>
      <label className="checkbox"><input type="checkbox" checked={denial} onChange={(event) => setEditable({ ...editable, denial: event.target.checked })} />{copy("native-claude-response.denyThisRequest_b31c70")}</label>
      {denial ? <>
        <label>{copy("native-claude-response.reasonForDenial_2060f4")}<textarea value={reason} onChange={(event) => setEditable({ ...editable, reason: event.target.value })} /></label>
        <label className="checkbox"><input type="checkbox" checked={interrupt} onChange={(event) => setEditable({ ...editable, interrupt: event.target.checked })} />{copy("native-claude-response.alsoInterruptThisClaudeRun_01c924")}</label>
        {interrupt ? <p>{copy("native-claude-response.claudeWillStopAfterThisDenial_ed455d")}</p> : null}
      </> : questions ? questions.map((q, i) => <fieldset key={q.question}><legend>{q.header}</legend><p>{q.question}</p>
        {q.options.map((o) => <label className="checkbox" key={o.label}><input type={q.multiSelect ? "checkbox" : "radio"} name={`claude-${resource.id}-${i}`} checked={!customEnabled[i] && !skipped[i] && (selected[i] ?? []).includes(o.label)} onChange={(event) => {
          setEditable({ ...editable, skipped: { ...skipped, [i]: false }, customEnabled: { ...customEnabled, [i]: false }, selected: selectedRows.map((row, index) => index !== i ? row : event.target.checked ? q.multiSelect ? [...row, o.label] : [o.label] : row.filter((v) => v !== o.label)) });
        }} /><span>{o.label}<small>{o.description}</small></span></label>)}
        <label className="checkbox"><input type="checkbox" checked={customEnabled[i] ?? false} onChange={(event) => setEditable({ ...editable, customEnabled: { ...customEnabled, [i]: event.target.checked }, skipped: { ...skipped, [i]: false } })} /><LocalizedText id="native-claude-response.useAnExactCustomAnswerFor_e60759" components={{ s0: <>{i + 1}</> }} /></label>
        {customEnabled[i] ? <label><LocalizedText id="native-claude-response.customAnswerForQuestion_76fa79" components={{ s0: <>{i + 1}</> }} /><textarea value={custom[i] ?? ""} onChange={(event) => setEditable({ ...editable, custom: { ...custom, [i]: event.target.value } })} /></label> : null}
        <label className="checkbox"><input type="checkbox" checked={skipped[i] ?? false} onChange={(event) => setEditable({ ...editable, skipped: { ...skipped, [i]: event.target.checked }, ...(event.target.checked ? { customEnabled: { ...customEnabled, [i]: false }, selected: selectedRows.map((row, index) => index === i ? [] : row) } : {}) })} /><LocalizedText id="native-claude-response.leaveQuestionUnanswered_c5d878" components={{ s0: <>{i + 1}</> }} /></label>
      </fieldset>) : <p>{copy("native-claude-response.allowThisOriginalRequestWithIts_2a5592")}</p>}
      <button className="primary" disabled={Boolean(missing) || invalid || oversized}>{denial ? copy("native-claude-response.sendDenialToClaude_8203ab") : questions ? copy("native-claude-response.sendAnswersToClaude_e80585") : copy("native-claude-response.allowThisClaudeRequest_940805")}</button>
    </fieldset>
    {editProblem ? <p role="alert">{editProblem}</p> : null}
    {invalid || oversized ? <p role="alert">{copy("native-claude-response.keepTheCompleteResponseWithin256_7a7390")}</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>{copy("native-claude-response.retryTheSameResponseRequest_ebd80d")}</button> : null}
  </form>;
}
