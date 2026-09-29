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
  [Decision.Accept]: "Allow once", [Decision.Session]: "Allow for this session", [Decision.Decline]: "Decline", [Decision.Cancel]: "Cancel request",
  [Decision.Execpolicy]: "Allow with the offered command rule", [Decision.Network]: "Apply the offered network rule",
};
const responseLimit = 256 << 10;
function own<T>(record: Record<string, T>, key: string): T | undefined { return Object.hasOwn(record, key) ? record[key] : undefined; }

export function Interaction({ resource, refresh, draft, saveDraft, clearDraft, submissionAllowed = true, receiptRetryAllowed = true }: { resource: Resource; refresh: () => void; draft?: InboxInteractionDraft; saveDraft?: (value: InteractionDraftState) => void; clearDraft?: () => void; submissionAllowed?: boolean; receiptRetryAllowed?: boolean }) {
  const [accepted, setAccepted] = useState<Resource>();
  const current = accepted && accepted.id === resource.id && accepted.revision > resource.revision ? accepted : resource;
  const data = document(current);
  const changed = (result?: Resource) => { if (result) { setAccepted(result); clearDraft?.(); } refresh(); };
  return <article className="interaction"><header><h3>{text(data.type) === InteractionType.Question ? "Agent question" : "Native approval"}</h3><small>{text(data.closure)}</small></header>
    <p>Response: {text(object(data.response ?? data.approval_response).state) || "Not submitted"}</p>
    {data.claude != null ? <NativeClaudeInteraction data={data} resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : data.opencode != null ? <NativeInteraction data={data} resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : text(data.type) === InteractionType.Question ? <Questions resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : text(data.type) === InteractionType.Approval ? <Approval resource={current} accepted={changed} draft={draft?.editable} saveDraft={saveDraft} submissionAllowed={submissionAllowed} receiptRetryAllowed={receiptRetryAllowed} /> : <p>This native request type is not supported by this client.</p>}
  </article>;
}

function Questions({ resource, accepted, draft, saveDraft, submissionAllowed, receiptRetryAllowed }: { resource: Resource; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed: boolean; receiptRetryAllowed: boolean }) {
  const data = document(resource);
  const questions = items(object(data.questions).questions).map(object);
  const [editable, setEditable] = useEditableInteractionDraft<Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexQuestion }>>(InteractionDraftKind.CodexQuestion, () => ({ kind: InteractionDraftKind.CodexQuestion, selected: {}, free: {}, unanswered: {} }), draft, saveDraft);
  const { selected, free, unanswered } = editable;
  const [problem, setProblem] = useState("");
  const mutation = useRetainedMutation(`answer:${resource.id}`, InteractionQuery.respondQuestion, (result) => accepted(result.interaction));
  const protectedAnswer = questions.some((q) => q.secret === true);
  const closed = text(data.closure) !== "open" || Boolean(data.response);
  const blocked = closed || protectedAnswer || mutation.busy || mutation.uncertain || !submissionAllowed;
  const answers = (choices = selected, extra = free) => Object.fromEntries(questions.map((question) => {
    const id = text(question.id);
    return [id, [...new Set([...(own(choices, id) ?? []), ...(own(extra, id) ? [own(extra, id)] : [])])]];
  }));
  const update = (choices: Record<string, string[]>, extra: Record<string, string>, unansweredState = unanswered) => {
    if (encode({ answers: answers(choices, extra) }).byteLength > responseLimit) { setProblem("The complete answer is too large. Shorten it before adding more text."); return; }
    setEditable({ ...editable, selected: choices, free: extra, unanswered: unansweredState }); setProblem("");
  };
  const missing = questions.some((q) => !answers()[text(q.id)].length && !own(unanswered, text(q.id)));
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || missing) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ answers: answers() }) }); }}>
    <fieldset disabled={blocked}>
      {questions.map((q, index) => { const id = text(q.id); return <fieldset key={id}><legend>{text(q.header) || `Question ${index + 1}`}</legend><p>{text(q.text)}</p>
        {q.secret === true ? <p>This question requires protected answer delivery, which is not available yet. Do not enter the secret in a message.</p> : <>
          {items(q.options).map((option, optionIndex) => { const value = object(option), label = text(value.label); return <label className="checkbox" key={optionIndex}><input type="checkbox" checked={(own(selected, id) ?? []).includes(label)} onChange={(event) => { const unansweredNext = { ...unanswered, [id]: false }; update({ ...selected, [id]: event.target.checked ? [...(own(selected, id) ?? []), label] : (own(selected, id) ?? []).filter((item) => item !== label) }, free, unansweredNext); }} /><span>{label}{text(value.description) ? <small>{text(value.description)}</small> : null}</span></label>; })}
          {q.other === true || items(q.options).length === 0 ? <label>{items(q.options).length ? "Another answer" : "Your answer"}<textarea autoFocus={index === 0 && items(q.options).length === 0} rows={2} value={own(free, id) ?? ""} onChange={(event) => { update(selected, { ...free, [id]: event.target.value }, { ...unanswered, [id]: false }); }} /></label> : null}
          <label className="checkbox"><input type="checkbox" checked={own(unanswered, id) ?? false} onChange={(event) => { const unansweredNext = { ...unanswered, [id]: event.target.checked }; if (event.target.checked) update({ ...selected, [id]: [] }, { ...free, [id]: "" }, unansweredNext); else setEditable({ ...editable, unanswered: unansweredNext }); }} />Leave this question unanswered</label>
        </>}
      </fieldset>; })}
      <button className="primary" disabled={missing || questions.length === 0}>Send answers</button>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>Retry the same answers</button> : null}
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
  if (value.type === "glob_pattern") return `Pattern: ${text(value.pattern)}`;
  const special = object(value.value);
  return `${text(special.kind)}${text(special.path) ? ` · ${text(special.path)}` : ""}${text(special.subpath) ? ` / ${text(special.subpath)}` : ""}`;
}
function PermissionSelection({ profile, editable, change }: { profile: Document; editable: Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexApproval }>; change: (value: Extract<InteractionDraftState, { kind: InteractionDraftKind.CodexApproval }>) => void }) {
  const entries = requestedEntries(profile);
  const update = (selection: Record<number, DraftAccess>, allowNetwork: boolean, lifetime: DraftGrantScope, review: boolean) => {
    change({ ...editable, access: selection, network: allowNetwork, scope: lifetime, strict: review });
  };
  return <div><p>Select the access to grant. Unselected access is not granted.</p>
    {object(profile.network).enabled === true ? <label className="checkbox"><input type="checkbox" checked={editable.network} onChange={(event) => update(editable.access, event.target.checked, editable.scope, editable.strict)} />Requested network access</label> : null}
    {entries.map((entry, index) => <label key={index}><code>{pathLabel(object(entry.path))}</code>{entry.access === DraftAccess.Deny ? <small>Native deny rule · retained with any file grant</small> : <select aria-label={`Access for ${pathLabel(object(entry.path))}`} value={editable.access[index] ?? DraftAccess.Omit} onChange={(event) => update({ ...editable.access, [index]: event.target.value as DraftAccess }, editable.network, editable.scope, editable.strict)}><option value={DraftAccess.Omit}>Do not grant</option><option value={DraftAccess.Read}>Read</option>{entry.access === DraftAccess.Write ? <option value={DraftAccess.Write}>Write</option> : null}</select>}</label>)}
    <label>Grant duration<select value={editable.scope} onChange={(event) => update(editable.access, editable.network, event.target.value as DraftGrantScope, false)}><option value={DraftGrantScope.Turn}>This turn</option><option value={DraftGrantScope.Session}>This native session</option></select></label>
    <label className="checkbox"><input type="checkbox" disabled={editable.scope !== DraftGrantScope.Turn} checked={editable.strict} onChange={(event) => update(editable.access, editable.network, editable.scope, event.target.checked)} />Require strict native automatic review for this turn</label>
  </div>;
}

function Approval({ resource, accepted, draft, saveDraft, submissionAllowed, receiptRetryAllowed }: { resource: Resource; accepted: (value?: Resource) => void; draft?: InteractionDraftState; saveDraft?: (value: InteractionDraftState) => void; submissionAllowed: boolean; receiptRetryAllowed: boolean }) {
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
    {text(request.cwd) ? <p>Directory: <code>{text(request.cwd)}</code></p> : null}
    {text(request.grant_root) ? <p>Requested root: <code>{text(request.grant_root)}</code></p> : null}
    <details><summary>Exact native request scope</summary><pre>{JSON.stringify(request, null, 2)}</pre></details>
    {!compatible ? <p>This native approval version is not supported. The original request is preserved.</p> : null}
    <fieldset disabled={blocked}>
      {native.kind === "permissions" ? <PermissionSelection profile={object(request.permissions)} editable={editable} change={setEditable} /> : <label>Decision<select required value={editable.choice || ""} onChange={(event) => setEditable({ ...editable, choice: Number(event.target.value) || 0 })}><option value="">Select a decision</option>{offered.map((value, index) => <option key={index} value={index + 1} disabled={!Object.hasOwn(decisionNames, text(value.kind))}>{decisionNames[text(value.kind) as Decision] ?? "Unsupported native decision"}</option>)}</select></label>}
      {decision && (decision.kind === Decision.Execpolicy || decision.kind === Decision.Network) ? <pre>{JSON.stringify(decision, null, 2)}</pre> : null}
      <button className="primary" disabled={native.kind !== "permissions" && !decision}>{native.kind === "permissions" ? "Send selected permissions" : "Send decision"}</button>
    </fieldset><Problem error={mutation.error} />{mutation.uncertain ? <button type="button" disabled={mutation.busy || !receiptRetryAllowed} onClick={mutation.retry}>Retry the same approval response</button> : null}
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
