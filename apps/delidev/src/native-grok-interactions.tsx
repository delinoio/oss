import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy, resolveMessage, useLocale } from "./localization";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { object, encode, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { InteractionDraftKind, GrokDraftOutcome as Outcome, GrokDraftFileDecision as FileDecision, GrokDraftPlanDecision as PlanDecision, interactionRequestIdentity, useEditableInteractionDraft, type GrokDraftDecision, type InteractionDraftState } from "./inbox-drafts";
import { nativeResponseByteLength, nativeResponseLimit, nativeResponseOverflow } from "./native-response-bounds";

enum Method { Update = "session/update", Notification = "_x.ai/session_notification", File = "session/request_permission", Question = "_x.ai/ask_user_question", Plan = "_x.ai/exit_plan_mode" }
const uuid = (v: unknown, version = 7): v is string => typeof v === "string" && new RegExp(`^[a-f0-9]{8}-[a-f0-9]{4}-${version}[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`).test(v);
const bounded = (v: unknown, max: number, nonempty = false): v is string => typeof v === "string" && (!nonempty || v.length > 0) && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max;
const count = (v: unknown): v is string => typeof v === "string" && /^(0|[1-9][0-9]{0,19})$/.test(v) && BigInt(v) <= 18446744073709551615n;
const digest = (v: unknown) => typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const exact = (v: Document, keys: string[]) => Object.keys(v).length === keys.length && keys.every((k) => Object.hasOwn(v, k));
const event = (v: unknown, thread: unknown) => typeof thread === "string" && typeof v === "string" && v.startsWith(`${thread}-`) && count(v.slice(thread.length + 1));
type Question = { question: string; options: {label: string; description: string}[]; multiSelect: boolean | null };
function questions(v: unknown): v is Question[] {
 if (!Array.isArray(v) || !v.length || v.length > 32 || encode(v).length > (256 << 10)) return false;
 const keys = new Set<string>();
 return v.every((value) => { const q = object(value); if (!exact(q, ["question", "options", "multiSelect"]) || !bounded(q.question, 16 << 10, true) || keys.has(q.question) || q.multiSelect !== null && typeof q.multiSelect !== "boolean" || !Array.isArray(q.options) || q.options.length < 1 || q.options.length > 64) return false; keys.add(q.question); const labels = new Set<string>(); return q.options.every((value) => {const o = object(value); if (!exact(o, ["label", "description"]) || !bounded(o.label, 4096, true) || labels.has(o.label) || !bounded(o.description, 16 << 10)) return false; labels.add(o.label); return true;}); });
}
// This bounded structural display check never authorizes a native operation.
// Original ordering/proposals and response decisions are validated in Go.
function observation(v: Document, thread: unknown) {
 const p = object(v.payload), meta = object(p._meta), u = object(p.update);
 // Original proposal bytes are inert display evidence. Go checks their exact
 // payload and digest; historical records may omit them, notifications cannot.
 if (v.proposal_json !== undefined && (typeof v.proposal_json !== "string" || v.proposal_json.length > (512 << 10) || !bounded(v.proposal_json, 512 << 10, true))) return false;
 if (!Object.values(Method).includes(v.method as Method) || !uuid(thread) || p.sessionId !== thread || encode(v).length > (512 << 10) || !Object.keys(v).every((k) => ["method", "payload", "request_id", "arrival_id", "inherited_permission", "plan_origin", "proposal_json"].includes(k))) return false;
 if (v.method === Method.Update || v.method === Method.Notification) return v.request_id === undefined && v.arrival_id === undefined && v.proposal_json === undefined && typeof u.sessionUpdate === "string" && (meta.eventId === undefined || event(meta.eventId, thread)) && (meta.agentTimestampMs === undefined || count(meta.agentTimestampMs)) && (meta.totalTokens === undefined || count(meta.totalTokens)) && (v.inherited_permission === undefined || uuid(v.inherited_permission));
 const id = object(v.request_id);
 return uuid(v.arrival_id) && (exact(id, ["kind", "text"]) && id.kind === "text" && bounded(id.text, 128, true) || exact(id, ["kind", "number"]) && id.kind === "number" && Number.isSafeInteger(id.number) || exact(id, ["kind", "decimal"]) && id.kind === "decimal" && typeof id.decimal === "string" && /^-?(0|[1-9][0-9]{0,18})$/.test(id.decimal));
}
function request(data: Document) {
 const r = object(data.grok), v = object(r.event), p = object(v.payload), native = object(data.native_request_id);
 if (!exact(r, ["version", "observation_id", "event", "request_digest", "proposal_digest", ...(r.plan === undefined ? [] : ["plan"])]) || r.version !== "1.0.41" || !uuid(r.observation_id) || !digest(r.request_digest) || !digest(r.proposal_digest) || !uuid(data.execution_id) || !uuid(data.native_thread_id) || !uuid(data.native_turn_id, 4) || !observation(v, data.native_thread_id) || JSON.stringify(native) !== JSON.stringify(v.request_id) || [data.claude, data.opencode, data.questions, data.approval].some((v) => v != null) || !["open", "native-closed", "turn-ended"].includes(text(data.closure))) return undefined;
 if (v.method === Method.Question) { if (data.type !== "user-question" || p.toolCallId !== data.native_item_id || !questions(p.questions) || r.plan !== undefined || data.approval_response !== undefined) return undefined; }
 else if (data.type !== "native-approval" || data.response !== undefined) return undefined;
 else if (v.method === Method.File) { const t = object(p.toolCall); if (t.toolCallId !== data.native_item_id || !Array.isArray(p.options) || p.options.length !== 3 || !p.options.every((value, i) => {
 const option = object(value);
 const offered = [
  {optionId: FileDecision.Session, name: "Yes, allow all edits during this session", kind: "allow_always"},
  {optionId: FileDecision.Once, name: "Yes", kind: "allow_once"},
  {optionId: FileDecision.Reject, name: "No, and tell Grok what to do differently", kind: "reject_once"},
 ][i]!;
 return exact(option, ["optionId", "name", "kind"]) && option.optionId === offered.optionId && option.name === offered.name && option.kind === offered.kind;
 }) || r.plan !== undefined) return undefined; }
 else if (v.method === Method.Plan) { const plan = object(r.plan), origin = object(plan.origin); if (p.toolCallId !== data.native_item_id || !bounded(p.planContent, 256 << 10, true) || !bounded(plan.write_tool_id, 256, true) || !digest(plan.content_digest) || !bounded(origin.entry_tool_id, 256, true) || !event(origin.entry_event_id, data.native_thread_id) || !Number.isInteger(origin.revision) || Number(origin.revision) < 1 || Number(origin.revision) > 128) return undefined; }
 else return undefined;
 const response = object(data.response ?? data.approval_response);
 if (data.response !== undefined || data.approval_response !== undefined) { if (!uuid(response.id) || !exact(object(response.input), ["grok"]) || !["queued", "claimed", "transmitted", "accepted", "uncertain", "canceled"].includes(text(response.state))) return undefined; const acceptance = object(response.acceptance), delivery = object(response.delivery); if (response.state === "accepted" && (data.closure !== "native-closed" || acceptance.evidence !== "native-grok-tool-result" || delivery.state !== "transmitted" || !Number.isSafeInteger(acceptance.sequence) || Number(acceptance.sequence) <= Number(delivery.sequence))) return undefined; }
 return {r,v,p};
}
export function NativeGrokInteraction({data, resource, accepted, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true}: {data: Document; resource?: Resource; accepted?: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (v: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean}) {
  useLocale();
 const original = request(data); if (!original) return <section aria-label={copy("native-grok-interactions.grokRequestUnavailable_ee9c93")}><p>{copy("native-grok-interactions.theRetainedGrokRequestIsUnavailable_362f5b")}</p></section>;
 const {r,v,p} = original, response = object(data.response ?? data.approval_response);
 return <section aria-label={copy("native-grok-interactions.originalGrokRequest_812673")}><p>{v.method === Method.Question ? copy("native-grok-interactions.nativeGrokQuestions_80f7c6") : v.method === Method.Plan ? copy("native-grok-interactions.nativeGrokPlanProposal_24d109") : copy("native-grok-interactions.nativeGrokWritePermission_021e58")}</p>
 {v.method === Method.Plan ? <><p><LocalizedText id="native-grok-interactions.planRevision_7444f0" components={{ s0: <>{Number(object(object(r.plan).origin).revision)}</> }} /></p><pre>{text(p.planContent)}</pre><p>{copy("native-grok-interactions.approveLeavesPlanCancelKeepsPlan_ae15b2")}</p></> : null}
 <Disclosure><DisclosureSummary>{copy("native-grok-interactions.originalNativeRequest_56b572")}</DisclosureSummary><pre>{JSON.stringify(v, null, 2)}</pre></Disclosure>
 <p><LocalizedText id="native-grok-interactions.toolResultExecutionOutcomeAndCleanup_3e5972" components={{ s0: <>{response.state === "accepted" ? copy("native-grok-interactions.grokResolvedThisResponseAndReported_46a060") : response.state === "transmitted" ? copy("native-grok-interactions.theResponseWasTransmittedNativeAcceptance_bafc2a") : response.state === "uncertain" ? copy("native-grok-interactions.responseDeliveryIsUncertainExecutionRemains_3e92f9") : data.closure !== "open" ? copy("native-grok-interactions.theOriginalRequestIsClosed_fda953") : copy("native-grok-interactions.theOriginalRequestIsWaitingFor_e922dd")}</> }} /></p>
 {response.input !== undefined ? <Disclosure><DisclosureSummary>{copy("native-grok-interactions.retainedResponse_690778")}</DisclosureSummary><pre>{JSON.stringify(response.input, null, 2)}</pre></Disclosure> : null}
  {resource && accepted ? <GrokResponse key={interactionRequestIdentity(resource)} resource={resource} original={original} accepted={accepted} draft={draft} saveDraft={saveDraft} closed={data.closure !== "open" || response.input !== undefined} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : null}
 </section>;
}
function GrokResponse({resource,original,accepted,draft,saveDraft,closed,submissionAllowed,receiptRetryAllowed}: {resource:Resource;original:NonNullable<ReturnType<typeof request>>;accepted:(r?:Resource)=>void;draft?:InteractionDraftState;saveDraft?:(v:InteractionDraftState)=>void;closed:boolean;submissionAllowed:boolean;receiptRetryAllowed:boolean}) {
  useLocale();
 const {v,p}=original, qs=v.method===Method.Question?p.questions as Question[]:undefined;
 type GrokDraft = Extract<InteractionDraftState, {kind: InteractionDraftKind.Grok}>;
 const responseFor = (value: GrokDraft) => {
  const answers = Object.fromEntries(qs?.flatMap((q,i)=>value.outcome===Outcome.Cancelled||value.outcome===Outcome.Skip&&!value.partial[i]?[]:[[q.question,value.answers[i]??""]])??[]);
  const annotations = Object.fromEntries(qs?.flatMap((q,i)=>value.notes[i]?[[q.question,{notes:value.notes[i]}]]:[])??[]);
  const reply = qs?value.outcome===Outcome.Accepted?{outcome:value.outcome,answers,...(Object.keys(annotations).length?{annotations}:{})}:value.outcome===Outcome.Skip?{outcome:value.outcome,partial_answers:answers}:{outcome:value.outcome}:v.method===Method.Plan?{outcome:value.decision}:{decision:value.decision};
  return {answers, annotations, reply};
 };
 const [state,set,editProblem]=useEditableInteractionDraft<GrokDraft>(InteractionDraftKind.Grok,()=>({kind:InteractionDraftKind.Grok,outcome:Outcome.Accepted,decision:"",answers:{},notes:{},partial:{}}),draft,saveDraft,(value)=>nativeResponseOverflow([
  {values:Object.values(value.answers),limit:64<<10,message:"native-response-bounds.keepGrokAnswerWithin64Kib"},
  {values:Object.values(value.notes),limit:64<<10,message:"native-response-bounds.keepGrokNoteWithin64Kib"},
 ],()=>({grok:responseFor(value).reply})));
 const question=useRetainedMutation(`grok-answer:${resource.id}`,InteractionQuery.respondQuestion,(r)=>accepted(r.interaction));
 const approval=useRetainedMutation(`grok-approve:${resource.id}`,InteractionQuery.respondApproval,(r)=>accepted(r.interaction));
 const mutation=qs?question:approval;
 const {answers,annotations,reply}=responseFor(state);
 const invalid=nativeResponseByteLength({grok:reply})>nativeResponseLimit||Object.values(answers).some((v)=>!bounded(v,64<<10,true))||Object.values(annotations).some((v)=>!bounded(v.notes,64<<10))||!qs&&!state.decision;
 const blocked=closed||!submissionAllowed||mutation.busy||mutation.uncertain;
 return <form aria-label={copy("native-grok-interactions.respondToOriginalGrokRequest_84c3fd")} onSubmit={(e)=>{e.preventDefault();if(blocked||invalid)return;void mutation.send({mutation:{id:resource.id,expectedRevision:resource.revision,requestId:newRequestId()},responseJson:encode({grok:reply})});}}><fieldset disabled={blocked}>
 {qs?<><label>{copy("native-grok-interactions.questionResponse_0fe79d")}<select value={state.outcome} onChange={(e)=>set({...state,outcome:e.target.value as Outcome})}><option value={Outcome.Accepted}>{copy("native-grok-interactions.answerAllQuestions_832b29")}</option><option value={Outcome.Cancelled}>{copy("native-grok-interactions.declineTheseQuestions_6e5639")}</option><option value={Outcome.Skip}>{copy("native-grok-interactions.skipInterviewWithSelectedPartialAnswers_173dab")}</option></select></label><p>{copy("native-grok-interactions.decliningQuestionsContinuesTheNativeInput_24b8fc")}</p>{state.outcome!==Outcome.Cancelled?qs.map((q,i)=><fieldset key={q.question}><legend>{q.question}</legend><p>{q.multiSelect===true?copy("native-grok-interactions.multipleSelectionsOffered_be4e07"):q.multiSelect===false?copy("native-grok-interactions.singleSelectionOffered_fb90f6"):copy("native-grok-interactions.nativeMultiSelectValueIsNull_381617")}</p><ul>{q.options.map((o)=><li key={o.label}>{o.label} — {o.description}</li>)}</ul><label><LocalizedText id="native-grok-interactions.exactAnswerForQuestion_4901e5" components={{ s0: <>{i+1}</> }} /><textarea value={state.answers[i]??""} onChange={(e)=>set({...state,answers:{...state.answers,[i]:e.target.value}})}/></label>{state.outcome===Outcome.Accepted?<label><LocalizedText id="native-grok-interactions.notesForQuestion_89155a" components={{ s0: <>{i+1}</> }} /><textarea value={state.notes[i]??""} onChange={(e)=>set({...state,notes:{...state.notes,[i]:e.target.value}})}/></label>:<label><input type="checkbox" checked={state.partial[i]??false} onChange={(e)=>set({...state,partial:{...state.partial,[i]:e.target.checked}})}/>{copy("native-grok-interactions.includeThisPartialAnswer_a02cd3")}</label>}</fieldset>):null}</>:<label>{copy("native-grok-interactions.decision_640ae4")}<select autoFocus value={state.decision} onChange={(e)=>set({...state,decision:e.target.value as GrokDraftDecision})}><option value="">{copy("native-grok-interactions.selectADecision_087ce7")}</option>{(v.method===Method.Plan?Object.values(PlanDecision):Object.values(FileDecision)).map((d)=><option key={d} value={d}>{d}</option>)}</select></label>}
 {state.decision===FileDecision.Session?<p>{copy("native-grok-interactions.theOriginalNativeSessionRemembersThis_ab3c65")}</p>:null}<button className="primary" disabled={invalid}>{copy("native-grok-interactions.sendResponseToGrok_91c83e")}</button></fieldset>{editProblem?<p role="alert">{resolveMessage(editProblem)}</p>:null}<Problem error={mutation.error}/>{mutation.uncertain?<button type="button" disabled={mutation.busy||!receiptRetryAllowed} onClick={mutation.retry}>{copy("native-grok-interactions.retryTheSameResponseRequest_ebd80d")}</button>:null}</form>;
}
export function NativeGrokTool({data}:{data:Document}) {
  useLocale();
 const v=object(data.grok_tool),p=object(v.payload),u=object(p.update),meta=object(p._meta);
 if(data.role!=="tool"||data.state!=="complete"||!uuid(data.execution_id)||!uuid(data.native_turn_id,4)||!observation(v,data.native_thread_id)||[data.grok_text,data.grok_user,data.claude,data.claude_tool,data.claude_progress,data.claude_interruption,data.tool,data.artifact,data.progress,data.phase,data.native_parent_id].some((v)=>v!==undefined))return <article aria-label={copy("native-grok-interactions.grokToolUnavailable_889de6")}>{copy("native-grok-interactions.theRetainedGrokToolObservationIs_1fa3dc")}</article>;
 return <article className="message" aria-label={copy("native-grok-interactions.originalGrokToolObservation_24d213")}><header><strong>{text(u.name)||text(u.title)||text(u.sessionUpdate)||text(v.method)}</strong><small>{text(u.status)}</small></header>{u.currentModeId!==undefined?<p><LocalizedText id="native-grok-interactions.nativeCurrentMode_87740c" components={{ s0: <>{text(u.currentModeId)}</> }} /></p>:null}{meta.totalTokens!==undefined?<p><LocalizedText id="native-grok-interactions.reportedNativeContextTokens_522b65" components={{ s0: <>{text(meta.totalTokens)}</> }} /></p>:null}{v.inherited_permission!==undefined?<p><LocalizedText id="native-grok-interactions.writeUsesTheOriginalCompletedSession_38ee16" components={{ s0: <>{text(v.inherited_permission)}</> }} /></p>:null}{v.plan_origin!==undefined?<p><LocalizedText id="native-grok-interactions.nativePlanFileRevision_bd5d35" components={{ s0: <>{Number(object(v.plan_origin).revision)}</> }} /></p>:null}<Disclosure><DisclosureSummary>{copy("native-grok-interactions.originalArgumentsPreviewAndAppliedResult_a46b46")}</DisclosureSummary><pre>{JSON.stringify(v,null,2)}</pre></Disclosure><p>{copy("native-grok-interactions.thisOrderedObservationDoesNotEstablish_2ec122")}</p></article>;
}
export function NativeGrokToolsTerminal({progress}:{progress:Document}) {
  useLocale();
 if(progress.grok_tools_terminal===undefined)return null;
 const v=object(progress.grok_tools_terminal),counts=object(v.counts),observed=object(progress.observed),content=object(progress.grok_content);
 const valid=exact(v,["kind","native_event_id","timestamp_ms","elapsed_ms","model","reason","counts","total_tokens","model_calls","api_duration_ms","turns",...(v.rejection===undefined?[]:["rejection"])])&&v.kind==="closed-first-tools"&&uuid(progress.execution_id)&&uuid(progress.native_thread_id)&&uuid(progress.native_turn_id,4)&&event(v.native_event_id,progress.native_thread_id)&&v.model===observed.model&&count(v.timestamp_ms)&&BigInt(v.timestamp_ms)<=253402300799999n&&[v.elapsed_ms,v.total_tokens,v.model_calls,v.api_duration_ms,v.turns].every(count)&&Number.isInteger(content.responses)&&Number(content.responses)>=1&&Number(content.responses)<=128&&v.model_calls===String(content.responses)&&v.turns===v.model_calls&&exact(counts,["input_tokens","output_tokens","cache_read_input_tokens","cache_creation_input_tokens","reasoning_tokens"])&&Object.values(counts).every(count)&&exact(object(progress.grok_response_totals),Object.keys(counts))&&Object.entries(counts).every(([key,value])=>object(progress.grok_response_totals)[key]===value)&&[progress.grok_terminal,progress.grok_stop,progress.claude_terminal,progress.claude_stop,progress.claude_denial,progress.opencode_stop].every((v)=>v===undefined)&&(v.reason==="end_turn"&&progress.outcome==="succeeded"&&v.rejection===undefined||v.reason==="PermissionRejected"&&progress.outcome==="stopped"&&exact(object(v.rejection),["tool","reason"])&&object(v.rejection).tool==="write"&&bounded(object(v.rejection).reason,4096,true));
 if(!valid)return <p>{copy("native-grok-interactions.theRetainedGrokToolsTerminalIs_2cca79")}</p>;
 return <section aria-label={copy("native-grok-interactions.originalGrokToolsCompletion_7b84d5")}><p><LocalizedText id="native-grok-interactions.nativeOutcome_6d39b1" components={{ s0: <>{text(v.reason)}</> }} /></p><dl><dt>{copy("native-grok-interactions.reportedInputTokens_696907")}</dt><dd>{text(counts.input_tokens)}</dd><dt>{copy("native-grok-interactions.reportedOutputTokens_4eabd2")}</dt><dd>{text(counts.output_tokens)}</dd><dt>{copy("native-grok-interactions.reportedNativeTotalTokens_357240")}</dt><dd>{text(v.total_tokens)}</dd><dt>{copy("native-grok-interactions.modelResponses_9fe614")}</dt><dd>{text(v.model_calls)}</dd><dt>{copy("native-grok-interactions.nativeElapsedTimeMs_3a0a11")}</dt><dd>{text(v.elapsed_ms)}</dd></dl><p><LocalizedText id="native-grok-interactions.reportedInputTotalsOverlapIndividualResponse_8c1282" components={{ s0: <>{progress.cleanup_verified===true?copy("native-grok-interactions.confirmed_959991"):copy("native-grok-interactions.unconfirmed_c78a68")}</> }} /></p></section>;
}
