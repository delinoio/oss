import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { NativeGrokInteraction } from "./native-grok-interactions";
import { useState } from "react";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { NativeInteraction } from "./native-interaction";
import { NativeClaudeInteraction } from "./native-claude-interaction";
import { DraftAccess, DraftGrantScope, InteractionDraftKind, useEditableInteractionDraft, type InboxInteractionDraft, type InteractionDraftState } from "./inbox-drafts";

export enum InteractionType { Question = "user-question", Approval = "native-approval" }
enum Decision { Accept = "accept", Session = "acceptForSession", Decline = "decline", Cancel = "cancel", Execpolicy = "acceptWithExecpolicyAmendment", Network = "applyNetworkPolicyAmendment" }
const decisionNames: Record<Decision, string> = {
  get [Decision.Accept]() { return copy("interactions.allowOnce_168511"); }, get [Decision.Session]() { return copy("interactions.allowForThisSession_42ab01"); }, get [Decision.Decline]() { return copy("interactions.decline_a2d285"); }, get [Decision.Cancel]() { return copy("interactions.cancelRequest_561966"); },
  get [Decision.Execpolicy]() { return copy("interactions.allowWithTheOfferedCommandRule_c081df"); }, get [Decision.Network]() { return copy("interactions.applyTheOfferedNetworkRule_a20a0f"); },
};
const responseLimit = 256 << 10;
function own<T>(record: Record<string, T>, key: string): T | undefined { return Object.hasOwn(record, key) ? record[key] : undefined; }

export function Interaction({ resource, refresh, draft, saveDraft, clearDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; refresh: () => void; draft?: InboxInteractionDraft; saveDraft?: (value: InteractionDraftState) => void; clearDraft?: () => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  useLocale();
  const [accepted, setAccepted] = useState<Resource>();
  const current = accepted && accepted.id === resource.id && accepted.revision > resource.revision ? accepted : resource;
  const data = document(current);
  const changed = (result?: Resource) => { if (result) { setAccepted(result); clearDraft?.(); } refresh(); };
  return <article className="interaction"><header><h3>{text(data.type) === InteractionType.Question ? copy("interactions.agentQuestion_1a6b3f") : copy("interactions.nativeApproval_c515b9")}</h3><small>{text(data.closure)}</small></header>
    <p><LocalizedText id="interactions.response_83879c" components={{ s0: <>{text(object(data.response ?? data.approval_response).state) || copy("interactions.extra.d3289e625281")}</> }} /></p>
    {Object.hasOwn(data,"grok") ? <NativeGrokInteraction data={data} resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : data.claude != null ? <NativeClaudeInteraction data={data} resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : data.opencode != null ? <NativeInteraction data={data} resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : text(data.type) === InteractionType.Question ? <Questions resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : text(data.type) === InteractionType.Approval ? <Approval resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : <p>{copy("interactions.thisNativeRequestTypeIsNot_6fd7af")}</p>}
  </article>;
}

function Questions({ resource, accepted, draft, saveDraft, submissionAllowed, receiptRetryAllowed }: { resource: Resource; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed: boolean; receiptRetryAllowed: boolean }) {
  useLocale();
  const data = document(resource);
  const questions = items(object(data.questions).questions).map(object);
  const [editable, setEditable] = useEditableInteractionDraft<Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexQuestion }>>(InteractionDraftKind.CodexQuestion, () => ({ kind: InteractionDraftKind.CodexQuestion, selected: {}, free: {}, unanswered: {} }), draft, saveDraft);
  const { selected, free, unanswered } = editable;
  const [problem, setProblem] = useProductMessage("");
  const mutation = useRetainedMutation(`answer:${resource.id}`, InteractionQuery.respondQuestion, (result) => accepted(result.interaction));
  const protectedAnswer = questions.some((q) => q.secret === true);
  const closed = text(data.closure) !== "open" || Boolean(data.response);
  const blocked = closed || protectedAnswer || mutation.busy || mutation.uncertain || !submissionAllowed;
  const answers = (choices = selected, extra = free) => Object.fromEntries(questions.map((question) => {
    const id = text(question.id);
    return [id, [...new Set([...(own(choices, id) ?? []), ...(own(extra, id) ? [own(extra, id)] : [])])]];
  }));
  const update = (choices: Record<string, string[]>, extra: Record<string, string>, unansweredState = unanswered) => {
    if (encode({ answers: answers(choices, extra) }).byteLength > responseLimit) { setProblem(ownedMessage("interactions.extra.a5276b87c12c")); return; }
    setEditable({ ...editable, selected: choices, free: extra, unanswered: unansweredState }); setProblem("");
  };
  const missing = questions.some((q) => !answers()[text(q.id)].length && !own(unanswered, text(q.id)));
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || missing) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ answers: answers() }) }); }}>
    <fieldset disabled={blocked}>
      {questions.map((q, index) => { const id = text(q.id); return <fieldset key={id}><legend>{text(q.header) || copy("interactions.sentence.49c0fedf3648", { v0: index + 1 })}</legend><p>{text(q.text)}</p>
        {q.secret === true ? <p>{copy("interactions.thisQuestionRequiresProtectedAnswerDelivery_e552da")}</p> : <>
          {items(q.options).map((option, optionIndex) => { const value = object(option), label = text(value.label); return <label className="checkbox" key={optionIndex}><input type="checkbox" checked={(own(selected, id) ?? []).includes(label)} onChange={(event) => { const unansweredNext = { ...unanswered, [id]: false }; update({ ...selected, [id]: event.target.checked ? [...(own(selected, id) ?? []), label] : (own(selected, id) ?? []).filter((item) => item !== label) }, free, unansweredNext); }} /><span>{label}{text(value.description) ? <small>{text(value.description)}</small> : null}</span></label>; })}
          {q.other === true || items(q.options).length === 0 ? <label>{items(q.options).length ? copy("interactions.anotherAnswer_b941a8") : copy("interactions.yourAnswer_d0e869")}<textarea autoFocus={index === 0 && items(q.options).length === 0} rows={2} value={own(free, id) ?? ""} onChange={(event) => { update(selected, { ...free, [id]: event.target.value }, { ...unanswered, [id]: false }); }} /></label> : null}
          <label className="checkbox"><input type="checkbox" checked={own(unanswered, id) ?? false} onChange={(event) => { const unansweredNext = { ...unanswered, [id]: event.target.checked }; if (event.target.checked) update({ ...selected, [id]: [] }, { ...free, [id]: "" }, unansweredNext); else setEditable({ ...editable, unanswered: unansweredNext }); }} />{copy("interactions.leaveThisQuestionUnanswered_820042")}</label>
        </>}
      </fieldset>; })}
      <button className="primary" disabled={missing || questions.length === 0}>{copy("interactions.sendAnswers_8a7856")}</button>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>{copy("interactions.retryTheSameAnswers_572a30")}</button> : null}
  </form>;
}

function requestedEntries(profile: Document): Document[] {
  const files = object(profile.file_system);
  // A present empty native entries collection overrides legacy read/write
  // mirrors. Preserve exact descriptors; the execution Worker resolves paths.
  if (Array.isArray(files.entries)) return files.entries.map(object);
  return [DraftAccess.Read, DraftAccess.Write].flatMap((access) => items(files[access]).map((path) => ({ access, path: { type: "path", path } })));
}
function pathLabel(value: Document): string {
  if (value.type === "path") return text(value.path);
  if (value.type === "glob_pattern") return copy("interactions.pattern", { pattern: text(value.pattern) });
  const special = object(value.value);
  return `${text(special.kind)}${text(special.path) ? ` · ${text(special.path)}` : ""}${text(special.subpath) ? ` / ${text(special.subpath)}` : ""}`;
}
function PermissionSelection({ profile, editable, change }: { profile: Document; editable: Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexApproval }>; change: (value: Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexApproval }>) => void }) {
  useLocale();
  const entries = requestedEntries(profile);
  const update = (selection: Record<number, DraftAccess>, allowNetwork: boolean, lifetime: DraftGrantScope, review: boolean) => {
    change({ ...editable, access: selection, network: allowNetwork, scope: lifetime, strict: review });
  };
  return <div><p>{copy("interactions.selectTheAccessToGrantUnselected_26a8f7")}</p>
    {object(profile.network).enabled === true ? <label className="checkbox"><input type="checkbox" checked={editable.network} onChange={(event) => update(editable.access, event.target.checked, editable.scope, editable.strict)} />{copy("interactions.requestedNetworkAccess_69911c")}</label> : null}
    {entries.map((entry, index) => <label key={index}><code>{pathLabel(object(entry.path))}</code>{entry.access === DraftAccess.Deny ? <small>{copy("interactions.nativeDenyRuleRetainedWithAny_876e8c")}</small> : <select aria-label={copy("interactions.accessFor_8ce4fa", { v0: pathLabel(object(entry.path)) })} value={editable.access[index] ?? DraftAccess.Omit} onChange={(event) => update({ ...editable.access, [index]: event.target.value as DraftAccess }, editable.network, editable.scope, editable.strict)}><option value={DraftAccess.Omit}>{copy("interactions.doNotGrant_241870")}</option><option value={DraftAccess.Read}>{copy("interactions.read_9b9a8d")}</option>{entry.access === DraftAccess.Write ? <option value={DraftAccess.Write}>{copy("interactions.write_3f0092")}</option> : null}</select>}</label>)}
    <label>{copy("interactions.grantDuration_43deb7")}<select value={editable.scope} onChange={(event) => update(editable.access, editable.network, event.target.value as DraftGrantScope, false)}><option value={DraftGrantScope.Turn}>{copy("interactions.thisTurn_2b7be2")}</option><option value={DraftGrantScope.Session}>{copy("interactions.thisNativeSession_614425")}</option></select></label>
    <label className="checkbox"><input type="checkbox" disabled={editable.scope !== DraftGrantScope.Turn} checked={editable.strict} onChange={(event) => update(editable.access, editable.network, editable.scope, event.target.checked)} />{copy("interactions.requireStrictNativeAutomaticReviewFor_d115fa")}</label>
  </div>;
}

function Approval({ resource, accepted, draft, saveDraft, submissionAllowed, receiptRetryAllowed }: { resource: Resource; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed: boolean; receiptRetryAllowed: boolean }) {
  useLocale();
  const data = document(resource), approval = object(data.approval), native = object(approval.codex), command = object(native.command);
  const [editable, setEditable] = useEditableInteractionDraft<Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexApproval }>>(InteractionDraftKind.CodexApproval, () => ({ kind: InteractionDraftKind.CodexApproval, choice: 0, access: {}, network: false, scope: DraftGrantScope.Turn, strict: false }), draft, saveDraft);
  const mutation = useRetainedMutation(`approve:${resource.id}`, InteractionQuery.respondApproval, (result) => accepted(result.interaction));
  const compatible = approval.harness === "codex" && approval.version === "0.151.0" && ["command", "file-change", "permissions"].includes(text(native.kind));
  const closed = text(data.closure) !== "open" || Boolean(data.approval_response);
  const blocked = !compatible || closed || mutation.busy || mutation.uncertain || !submissionAllowed;
  const offered = native.kind === "command" ? items(command.available_decisions).map(object) : native.kind === "file-change" ? [Decision.Accept, Decision.Session, Decision.Decline, Decision.Cancel].map((kind) => ({ kind, execpolicy: null, network_policy: null })) : [];
  const decision = editable.choice ? offered[editable.choice - 1] : undefined;
  const request = native.kind === "command" ? command : native.kind === "file-change" ? object(native.file) : object(native.permissions);
  const grant = { scope: editable.scope, permissions: entriesForGrant(request, editable), ...(editable.strict ? { strict_auto_review: true } : {}) };
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || (native.kind !== "permissions" && !decision)) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(native.kind === "permissions" ? { grant } : { decision }) }); }}>
    <p>{text(native.kind)} · {text(approval.harness)} {text(approval.version)}</p>
    {text(request.reason) ? <p>{text(request.reason)}</p> : null}
    {text(request.command) ? <pre>{text(request.command)}</pre> : null}
    {text(request.cwd) ? <p><LocalizedText id="interactions.directory_369f13" components={{ s0: <code>{text(request.cwd)}</code> }} /></p> : null}
    {text(request.grant_root) ? <p><LocalizedText id="interactions.requestedRoot_a5811f" components={{ s0: <code>{text(request.grant_root)}</code> }} /></p> : null}
    <details><summary>{copy("interactions.exactNativeRequestScope_984d63")}</summary><pre>{JSON.stringify(request, null, 2)}</pre></details>
    {!compatible ? <p>{copy("interactions.thisNativeApprovalVersionIsNot_3fac55")}</p> : null}
    <fieldset disabled={blocked}>
      {native.kind === "permissions" ? <PermissionSelection profile={object(request.permissions)} editable={editable} change={setEditable} /> : <label>{copy("interactions.decision_640ae4")}<select required value={editable.choice || ""} onChange={(event) => setEditable({ ...editable, choice: Number(event.target.value) || 0 })}><option value="">{copy("interactions.selectADecision_087ce7")}</option>{offered.map((value, index) => <option key={index} value={index + 1} disabled={!Object.hasOwn(decisionNames, text(value.kind))}>{decisionNames[text(value.kind) as Decision] ?? copy("interactions.unsupportedNativeDecision_72a15c")}</option>)}</select></label>}
      {decision && (decision.kind === Decision.Execpolicy || decision.kind === Decision.Network) ? <pre>{JSON.stringify(decision, null, 2)}</pre> : null}
      <button className="primary" disabled={native.kind !== "permissions" && !decision}>{native.kind === "permissions" ? copy("interactions.sendSelectedPermissions_b0cf2a") : copy("interactions.sendDecision_c45798")}</button>
    </fieldset><Problem error={mutation.error} />{mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>{copy("interactions.retryTheSameApprovalResponse_d37bf9")}</button> : null}
  </form>;
}

function entriesForGrant(request: Document, editable: Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexApproval }>): Document {
  const profile = object(request.permissions), entries = requestedEntries(profile);
  const selected = entries.flatMap((entry, index) => {
    if (entry.access === DraftAccess.Deny) return [entry];
    const access = editable.access[index];
    return access && access !== DraftAccess.Omit ? [{ ...entry, access }] : [];
  });
  const selectedFiles = selected.some((entry) => entry.access !== DraftAccess.Deny) ? selected : [];
  const permissions: Document = {};
  if (editable.network) permissions.network = { enabled: true };
  if (selectedFiles.length) permissions.file_system = { read: null, write: null, entries: selectedFiles, glob_scan_max_depth: object(profile.file_system).glob_scan_max_depth ?? null };
  return permissions;
}
