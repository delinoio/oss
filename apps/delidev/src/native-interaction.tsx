import { LocalizedText, copy, useLocale } from "./localization";
import type { Resource } from "@delinoio/delidev-api-client";
import { NativePermissionResponse, NativeQuestionResponse } from "./native-interaction-response";
import { object } from "./documents";
import type { InteractionDraftState } from "./inbox-drafts";

const encoder = new TextEncoder();
function bounded(value: unknown, max: number): value is string { return typeof value === "string" && value.length <= max && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && encoder.encode(value).length <= max; }
function shape(value: unknown, keys: string[]): boolean { return value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).every((key) => keys.includes(key)); }
function native(value: unknown, prefix: string): boolean { return typeof value === "string" && new RegExp(`^${prefix}_[0-9a-f]{12}[a-zA-Z0-9]{14}$`).test(value); }
function strings(value: unknown): value is string[] { return Array.isArray(value) && value.length <= 1024 && value.every((v) => bounded(v, 32768)); }
type Question = { text: string; header: string; options: { label: string; description: string }[]; multiple?: boolean; custom?: boolean };
function questions(value: unknown): value is Question[] {
  return Array.isArray(value) && value.length <= 128 && value.every((entry) => {
    const q = object(entry);
    return shape(entry, ["text", "header", "options", "multiple", "custom"]) && bounded(q.text, 64 * 1024) && bounded(q.header, 4096) && (q.multiple === undefined || typeof q.multiple === "boolean") && (q.custom === undefined || typeof q.custom === "boolean") && Array.isArray(q.options) && q.options.length <= 256 && q.options.every((entry) => { const v = object(entry); return shape(entry, ["label", "description"]) && bounded(v.label, 4096) && bounded(v.description, 64 * 1024); });
  });
}
function objectJSON(value: unknown): value is string { if (!bounded(value, 256 * 1024)) return false; try { const v: unknown = JSON.parse(value); return v !== null && typeof v === "object" && !Array.isArray(v); } catch { return false; } }

// Questions keep original matrix order and optional flags. This component has
// separate response forms: displaying native scopes cannot authorize a response.
function nativeResponse(value: unknown, question: boolean): boolean {
  if (value == null) return true;
  const r = object(value), input = object(r.input), native = object(input.opencode);
  return ["queued", "claimed", "transmitted", "uncertain", "canceled", "accepted"].includes(String(r.state)) && shape(r.input, ["opencode"]) && (question ? (shape(input.opencode, ["reject"]) && native.reject === true || shape(input.opencode, ["answers"]) && Array.isArray(native.answers) && native.answers.every((row) => Array.isArray(row) && row.every((v) => bounded(v, 64 * 1024)))) : shape(input.opencode, ["decision", "feedback"]) && ["once", "always", "reject"].includes(String(native.decision)) && (native.feedback === undefined || native.decision === "reject" && bounded(native.feedback, 64 * 1024)));
}

function nativePolicyClosure(data: Record<string, unknown>): boolean {
  if (data.opencode_closure == null) return true;
  const p = object(data.opencode_closure), original = object(data.opencode), request = object(data.native_request_id), response = object(data.approval_response);
  if (data.type !== "native-approval" || data.closure !== "native-closed" || data.response != null || !shape(data.opencode_closure, ["native_event_id", "proposal_event_id", "decision", "sources"]) || !native(p.native_event_id, "evt") || p.native_event_id === p.proposal_event_id || p.proposal_event_id !== original.native_event_id || !["always", "reject"].includes(String(p.decision)) || !Array.isArray(p.sources) || p.sources.length === 0 || p.sources.length > 1024 || data.approval_response != null && (response.state !== "canceled" || response.claim != null || response.delivery != null || response.acceptance != null)) return false;
  const ids = new Set<string>(), requests = new Set<string>();
  return p.sources.every((value) => {
    const source = object(value), id = source.interaction_id, nativeID = source.native_request_id;
    if (!shape(value, ["interaction_id", "native_request_id"]) || typeof id !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id) || typeof nativeID !== "string" || !native(nativeID, "per") || nativeID === request.text || ids.has(id) || requests.has(nativeID)) return false;
    ids.add(id); requests.add(nativeID); return true;
  });
}

function nativeStopClosure(data: Record<string, unknown>): boolean {
  if (data.opencode_stop == null) return true;
  const p = object(data.opencode_stop), stop = object(p.stop), original = object(data.opencode);
  const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
  if (data.closure !== "turn-ended" || data.opencode_closure != null || !shape(data.opencode_stop, ["stop", "proposal_event_id", "tool_interrupted"]) || p.tool_interrupted !== true || p.proposal_event_id !== original.native_event_id || !shape(p.stop, ["request_id", "input_request_id", "input_part_id", "assistant_id", "history_digest", "http_accepted", "interrupted_observed", "terminal_observed", "idle_observed", "pending_cleared", "cleanup_verified", "retry_observations", "retry_canceled_observed"]) || !uuid(stop.request_id) || !uuid(stop.input_request_id) || stop.request_id === stop.input_request_id || !native(stop.input_part_id, "prt") || !native(stop.assistant_id, "msg") || stop.assistant_id === data.native_turn_id || typeof stop.history_digest !== "string" || !/^[0-9a-f]{64}$/.test(stop.history_digest) || typeof stop.http_accepted !== "boolean" || typeof stop.interrupted_observed !== "boolean" || !stop.http_accepted && !stop.interrupted_observed || stop.terminal_observed !== true || stop.idle_observed !== true || stop.pending_cleared !== true || stop.cleanup_verified !== true) return false;
  if (stop.retry_observations !== undefined) {
    if (!Array.isArray(stop.retry_observations) || stop.retry_observations.length > 1024) return false;
    const events = new Set<string>();
    for (const value of stop.retry_observations) {
      const retry = object(value);
      if (!shape(value, ["native_event_id", "attempt", "next"]) || typeof retry.native_event_id !== "string" || !native(retry.native_event_id, "evt") || events.has(retry.native_event_id) || typeof retry.attempt !== "number" || !Number.isSafeInteger(retry.attempt) || retry.attempt < 0 || typeof retry.next !== "number" || !Number.isSafeInteger(retry.next) || retry.next < 0) return false;
      events.add(retry.native_event_id);
    }
  }
  if (stop.retry_canceled_observed !== undefined && (stop.retry_canceled_observed !== true || stop.http_accepted !== true || stop.interrupted_observed !== false || !Array.isArray(stop.retry_observations) || stop.retry_observations.length === 0)) return false;
  return [data.response, data.approval_response].every((value) => { const r = object(value); return value == null || r.state === "canceled" && r.claim == null && r.delivery == null && r.acceptance == null; });
}

export function NativeInteraction({ data, resource, accepted = () => {}, draft, saveDraft, submissionAllowed = true, receiptRetryAllowed = true }: { data: Record<string, unknown>; resource?: Resource; accepted?: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  useLocale();
  const r = object(data.opencode), id = object(data.native_request_id), permission = object(r.permission);
  const question = data.type === "user-question", approval = data.type === "native-approval";
  const valid = (question || approval) && r.version === "1.18.32" && shape(data.opencode, ["version", "native_event_id", "native_message_id", "call_id", "permission", "questions"]) && data.questions == null && data.approval == null && (question ? data.approval_response == null && nativeResponse(data.response, true) : data.response == null && nativeResponse(data.approval_response, false)) && native(r.native_event_id, "evt") && native(r.native_message_id, "msg") && native(data.native_item_id, "prt") && native(data.native_thread_id, "ses") && native(data.native_turn_id, "msg") && r.native_message_id !== data.native_turn_id && bounded(r.call_id, 1024) && r.call_id.trim() && id.kind === "text" && id.number == null && native(id.text, question ? "que" : "per") && ["open", "native-closed", "turn-ended"].includes(String(data.closure)) &&
    (question ? r.permission == null && questions(r.questions) : r.questions == null && shape(r.permission, ["name", "patterns", "always", "metadata_json"]) && bounded(permission.name, 256) && permission.name.trim() && strings(permission.patterns) && strings(permission.always) && objectJSON(permission.metadata_json));
  if (!valid || !nativePolicyClosure(data) || !nativeStopClosure(data)) return <p>{copy("native-interaction.theRetainedOpencodeRequestIsUnavailable_0b6341")}</p>;
  return <section aria-label={copy("native-interaction.originalOpencodeRequest_3b6e23")}>
    <p><LocalizedText id="native-interaction.opencode_fc9056" components={{ s0: <>{String(r.version)}</> }} /></p>
    {question ? <ol>{(r.questions as Question[]).map((q, i) => <li key={i}><h4>{q.header || `Question ${i + 1}`}</h4><pre>{q.text}</pre><p><LocalizedText id="native-interaction.multipleChoicesCustomAnswers_845859" components={{ s0: <>{q.multiple === undefined ? copy("native-interaction.nativeDefault_2bab94") : q.multiple ? copy("native-interaction.allowed_1bb201") : copy("native-interaction.notAllowed_f0406a")}</>, s1: <>{q.custom === undefined ? copy("native-interaction.nativeDefault_2bab94") : q.custom ? copy("native-interaction.allowed_1bb201") : copy("native-interaction.notAllowed_f0406a")}</> }} /></p><ul>{q.options.map((o, j) => <li key={j}><strong>{o.label}</strong><pre>{o.description}</pre></li>)}</ul></li>)}</ol> : <><p><LocalizedText id="native-interaction.requestedPermission_994993" components={{ s0: <>{String(permission.name)}</> }} /></p><details><summary>{copy("native-interaction.requestedPatterns_9611b0")}</summary><ol>{(permission.patterns as string[]).map((v, i) => <li key={i}><pre>{v}</pre></li>)}</ol></details><details><summary>{copy("native-interaction.nativeAlwaysPatterns_6ac4e0")}</summary><ol>{(permission.always as string[]).map((v, i) => <li key={i}><pre>{v}</pre></li>)}</ol></details><details><summary>{copy("native-interaction.originalNativeMetadata_d7e8f9")}</summary><pre>{String(permission.metadata_json)}</pre></details></>}
    {question && (r.questions as Question[]).length === 0 ? <p>{copy("native-interaction.theOriginalQuestionListIsEmpty_e8fe41")}</p> : null}
    {data.opencode_closure != null ? <p><LocalizedText id="native-interaction.noNativeResponseWasSentFor_a39de4" components={{ s0: <>{object(data.opencode_closure).decision === "always" ? copy("native-interaction.opencodeAutomaticallyAllowedThisPendingRequest_d45a67") : copy("native-interaction.opencodeAutomaticallyRejectedThisPendingRequest_f95bb9")}</> }} /></p> : null}
    {data.opencode_stop != null ? <p>{copy("native-interaction.thisUnansweredRequestWasCanceledAfter_66419c")}</p> : null}
    {resource ? question ? <NativeQuestionResponse key={resource.id} resource={resource} questions={r.questions as Question[]} closed={data.closure !== "open" || data.response != null} accepted={accepted} draft={draft} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : <NativePermissionResponse key={resource.id} resource={resource} closed={data.closure !== "open" || data.approval_response != null} accepted={accepted} draft={draft} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : null}
    <p>{resource ? data.closure === "open" ? copy("native-interaction.theOriginalRequestRemainsPendingUntil_89aa8f") : copy("native-interaction.thisRetainedRequestIsClosed_655dbe") : data.closure === "open" ? copy("native-interaction.thisRequestIsPendingOpenIts_6a1db0") : copy("native-interaction.thisRetainedRequestIsClosed_655dbe")}</p>
  </section>;
}
