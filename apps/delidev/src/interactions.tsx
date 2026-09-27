import { useState } from "react";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { NativeInteraction } from "./native-interaction";
import { NativeClaudeInteraction } from "./native-claude-interaction";

export enum InteractionType { Question = "user-question", Approval = "native-approval" }
enum GrantScope { Turn = "turn", Session = "session" }
enum Access { Omit = "omit", Read = "read", Write = "write", Deny = "deny" }
enum Decision { Accept = "accept", Session = "acceptForSession", Decline = "decline", Cancel = "cancel", Execpolicy = "acceptWithExecpolicyAmendment", Network = "applyNetworkPolicyAmendment" }
const decisionNames: Record<Decision, string> = {
  [Decision.Accept]: "Allow once", [Decision.Session]: "Allow for this session", [Decision.Decline]: "Decline", [Decision.Cancel]: "Cancel request",
  [Decision.Execpolicy]: "Allow with the offered command rule", [Decision.Network]: "Apply the offered network rule",
};
const responseLimit = 256 << 10;
function own<T>(record: Record<string, T>, key: string): T | undefined { return Object.hasOwn(record, key) ? record[key] : undefined; }

export function Interaction({ resource, refresh }: { resource: Resource; refresh: () => void }) {
  const [accepted, setAccepted] = useState<Resource>();
  const current = accepted && accepted.id === resource.id && accepted.revision > resource.revision ? accepted : resource;
  const data = document(current);
  const changed = (result?: Resource) => { if (result) setAccepted(result); refresh(); };
  return <article className="interaction"><header><h3>{text(data.type) === InteractionType.Question ? "Agent question" : "Native approval"}</h3><small>{text(data.closure)}</small></header>
    <p>Response: {text(object(data.response ?? data.approval_response).state) || "Not submitted"}</p>
    {data.claude != null ? <NativeClaudeInteraction data={data} resource={current} accepted={changed} /> : data.opencode != null ? <NativeInteraction data={data} resource={current} accepted={changed} /> : text(data.type) === InteractionType.Question ? <Questions resource={current} accepted={changed} /> : text(data.type) === InteractionType.Approval ? <Approval resource={current} accepted={changed} /> : <p>This native request type is not supported by this client.</p>}
  </article>;
}

function Questions({ resource, accepted }: { resource: Resource; accepted: (value?: Resource) => void }) {
  const data = document(resource);
  const questions = items(object(data.questions).questions).map(object);
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [free, setFree] = useState<Record<string, string>>({});
  const [unanswered, setUnanswered] = useState<Record<string, boolean>>({});
  const [problem, setProblem] = useState("");
  const mutation = useRetainedMutation(`answer:${resource.id}`, InteractionQuery.respondQuestion, (result) => accepted(result.interaction));
  const protectedAnswer = questions.some((q) => q.secret === true);
  const closed = text(data.closure) !== "open" || Boolean(data.response);
  const blocked = closed || protectedAnswer || mutation.busy || mutation.uncertain;
  const answers = (choices = selected, extra = free) => Object.fromEntries(questions.map((question) => {
    const id = text(question.id);
    return [id, [...new Set([...(own(choices, id) ?? []), ...(own(extra, id) ? [own(extra, id)] : [])])]];
  }));
  const update = (choices: Record<string, string[]>, extra: Record<string, string>) => {
    if (encode({ answers: answers(choices, extra) }).byteLength > responseLimit) { setProblem("The complete answer is too large. Shorten it before adding more text."); return; }
    setSelected(choices); setFree(extra); setProblem("");
  };
  const missing = questions.some((q) => !answers()[text(q.id)].length && !own(unanswered, text(q.id)));
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || missing) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ answers: answers() }) }); }}>
    <fieldset disabled={blocked}>
      {questions.map((q, index) => { const id = text(q.id); return <fieldset key={id}><legend>{text(q.header) || `Question ${index + 1}`}</legend><p>{text(q.text)}</p>
        {q.secret === true ? <p>This question requires protected answer delivery, which is not available yet. Do not enter the secret in a message.</p> : <>
          {items(q.options).map((option, optionIndex) => { const value = object(option), label = text(value.label); return <label className="checkbox" key={optionIndex}><input type="checkbox" checked={(own(selected, id) ?? []).includes(label)} onChange={(event) => { setUnanswered({ ...unanswered, [id]: false }); update({ ...selected, [id]: event.target.checked ? [...(own(selected, id) ?? []), label] : (own(selected, id) ?? []).filter((item) => item !== label) }, free); }} /><span>{label}{text(value.description) ? <small>{text(value.description)}</small> : null}</span></label>; })}
          {q.other === true || items(q.options).length === 0 ? <label>{items(q.options).length ? "Another answer" : "Your answer"}<textarea autoFocus={index === 0 && items(q.options).length === 0} rows={2} value={own(free, id) ?? ""} onChange={(event) => { setUnanswered({ ...unanswered, [id]: false }); update(selected, { ...free, [id]: event.target.value }); }} /></label> : null}
          <label className="checkbox"><input type="checkbox" checked={own(unanswered, id) ?? false} onChange={(event) => { setUnanswered({ ...unanswered, [id]: event.target.checked }); if (event.target.checked) update({ ...selected, [id]: [] }, { ...free, [id]: "" }); }} />Leave this question unanswered</label>
        </>}
      </fieldset>; })}
      <button className="primary" disabled={missing || questions.length === 0}>Send answers</button>
    </fieldset>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same answers</button> : null}
  </form>;
}

function requestedEntries(profile: Document): Document[] {
  const files = object(profile.file_system);
  // A present empty native entries collection overrides legacy read/write
  // mirrors. Preserve exact descriptors; the execution Worker resolves paths.
  if (Array.isArray(files.entries)) return files.entries.map(object);
  return [Access.Read, Access.Write].flatMap((access) => items(files[access]).map((path) => ({ access, path: { type: "path", path } })));
}
function pathLabel(value: Document): string {
  if (value.type === "path") return text(value.path);
  if (value.type === "glob_pattern") return `Pattern: ${text(value.pattern)}`;
  const special = object(value.value);
  return `${text(special.kind)}${text(special.path) ? ` · ${text(special.path)}` : ""}${text(special.subpath) ? ` / ${text(special.subpath)}` : ""}`;
}
function PermissionSelection({ profile, change }: { profile: Document; change: (value: Document) => void }) {
  const entries = requestedEntries(profile);
  const [access, setAccess] = useState<Record<number, Access>>({});
  const [network, setNetwork] = useState(false);
  const [scope, setScope] = useState(GrantScope.Turn);
  const [strict, setStrict] = useState(false);
  const update = (selection: Record<number, Access>, allowNetwork: boolean, lifetime: GrantScope, review: boolean) => {
    setAccess(selection); setNetwork(allowNetwork); setScope(lifetime); setStrict(review);
    const grants = entries.flatMap((entry, index) => selection[index] && selection[index] !== Access.Omit && entry.access !== Access.Deny ? [{ ...entry, access: selection[index] }] : []);
    const selectedEntries = grants.length ? entries.flatMap((entry, index) => entry.access === Access.Deny ? [entry] : selection[index] && selection[index] !== Access.Omit ? [{ ...entry, access: selection[index] }] : []) : [];
    const permissions: Document = {};
    if (allowNetwork) permissions.network = { enabled: true };
    if (selectedEntries.length) permissions.file_system = { read: null, write: null, entries: selectedEntries, glob_scan_max_depth: object(profile.file_system).glob_scan_max_depth ?? null };
    change({ permissions, scope: lifetime, ...(review ? { strict_auto_review: true } : {}) });
  };
  return <div><p>Select the access to grant. Unselected access is not granted.</p>
    {object(profile.network).enabled === true ? <label className="checkbox"><input type="checkbox" checked={network} onChange={(event) => update(access, event.target.checked, scope, strict)} />Requested network access</label> : null}
    {entries.map((entry, index) => <label key={index}><code>{pathLabel(object(entry.path))}</code>{entry.access === Access.Deny ? <small>Native deny rule · retained with any file grant</small> : <select aria-label={`Access for ${pathLabel(object(entry.path))}`} value={access[index] ?? Access.Omit} onChange={(event) => update({ ...access, [index]: event.target.value as Access }, network, scope, strict)}><option value={Access.Omit}>Do not grant</option><option value={Access.Read}>Read</option>{entry.access === Access.Write ? <option value={Access.Write}>Write</option> : null}</select>}</label>)}
    <label>Grant duration<select value={scope} onChange={(event) => update(access, network, event.target.value as GrantScope, false)}><option value={GrantScope.Turn}>This turn</option><option value={GrantScope.Session}>This native session</option></select></label>
    <label className="checkbox"><input type="checkbox" disabled={scope !== GrantScope.Turn} checked={strict} onChange={(event) => update(access, network, scope, event.target.checked)} />Require strict native automatic review for this turn</label>
  </div>;
}

function Approval({ resource, accepted }: { resource: Resource; accepted: (value?: Resource) => void }) {
  const data = document(resource), approval = object(data.approval), native = object(approval.codex), command = object(native.command);
  const [choice, setChoice] = useState("");
  const [grant, setGrant] = useState<Document>({ permissions: {}, scope: GrantScope.Turn });
  const mutation = useRetainedMutation(`approve:${resource.id}`, InteractionQuery.respondApproval, (result) => accepted(result.interaction));
  const compatible = approval.harness === "codex" && approval.version === "0.151.0" && ["command", "file-change", "permissions"].includes(text(native.kind));
  const closed = text(data.closure) !== "open" || Boolean(data.approval_response);
  const blocked = !compatible || closed || mutation.busy || mutation.uncertain;
  const offered = native.kind === "command" ? items(command.available_decisions).map(object) : native.kind === "file-change" ? [Decision.Accept, Decision.Session, Decision.Decline, Decision.Cancel].map((kind) => ({ kind, execpolicy: null, network_policy: null })) : [];
  const decision = choice ? offered[Number(choice) - 1] : undefined;
  const request = native.kind === "command" ? command : native.kind === "file-change" ? object(native.file) : object(native.permissions);
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || (native.kind !== "permissions" && !decision)) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(native.kind === "permissions" ? { grant } : { decision }) }); }}>
    <p>{text(native.kind)} · {text(approval.harness)} {text(approval.version)}</p>
    {text(request.reason) ? <p>{text(request.reason)}</p> : null}
    {text(request.command) ? <pre>{text(request.command)}</pre> : null}
    {text(request.cwd) ? <p>Directory: <code>{text(request.cwd)}</code></p> : null}
    {text(request.grant_root) ? <p>Requested root: <code>{text(request.grant_root)}</code></p> : null}
    <details><summary>Exact native request scope</summary><pre>{JSON.stringify(request, null, 2)}</pre></details>
    {!compatible ? <p>This native approval version is not supported. The original request is preserved.</p> : null}
    <fieldset disabled={blocked}>
      {native.kind === "permissions" ? <PermissionSelection profile={object(request.permissions)} change={setGrant} /> : <label>Decision<select required value={choice} onChange={(event) => setChoice(event.target.value)}><option value="">Select a decision</option>{offered.map((value, index) => <option key={index} value={index + 1} disabled={!Object.hasOwn(decisionNames, text(value.kind))}>{decisionNames[text(value.kind) as Decision] ?? "Unsupported native decision"}</option>)}</select></label>}
      {decision && (decision.kind === Decision.Execpolicy || decision.kind === Decision.Network) ? <pre>{JSON.stringify(decision, null, 2)}</pre> : null}
      <button className="primary" disabled={native.kind !== "permissions" && !decision}>{native.kind === "permissions" ? "Send selected permissions" : "Send decision"}</button>
    </fieldset><Problem error={mutation.error} />{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same approval response</button> : null}
  </form>;
}
