import { useId, type ReactNode } from "react";
import { items, object, text, type Document } from "./documents";

export enum ConflictStrategy { Merge = "merge", Rebase = "rebase" }
export enum RemediationSessionStrategy { Reuse = "reuse", Dedicated = "dedicated" }
export enum ReviewerSelectorKind { User = "user", Bot = "bot", App = "app", Permission = "minimum-permission" }
export enum ReviewerPermission { Read = "READ", Triage = "TRIAGE", Write = "WRITE", Maintain = "MAINTAIN", Admin = "ADMIN" }
export enum RemediationDetailPresentation { Expanded, Collapsible }

export function defaultRemediationPolicy(): Document {
  return { ci_failure: false, review_feedback: false, merge_conflict: false, conflict_strategy: ConflictStrategy.Merge, session_strategy: RemediationSessionStrategy.Reuse, attempt_limit: 3 };
}

function PolicyChoice({ label, value, values, change }: { label: string; value: unknown; values: string[]; change: (value: string) => void }) {
  const selected = text(value);
  return <label>{label}<select required value={selected} onChange={event => change(event.target.value)}>
    {!values.includes(selected) ? <option value={selected}>{selected ? `Unsupported selection · ${selected}` : "Select a value"}</option> : null}
    {values.map(option => <option key={option} value={option}>{option}</option>)}
  </select></label>;
}

export function RemediationPolicyFields({ value, change, children, presentation = RemediationDetailPresentation.Expanded }: { value: Document; change: (value: Document) => void; children: ReactNode; presentation?: RemediationDetailPresentation }) {
  const helperId = useId();
  const field = (key: string, next: unknown) => change({ ...value, [key]: next });
  const selectors = items(value.reviewer_selectors).map(object);
  const update = (index: number, next: Document) => field("reviewer_selectors", selectors.map((selector, i) => i === index ? next : selector));
  const details = <>
    <PolicyChoice label="Remediation session strategy" value={value.session_strategy} values={Object.values(RemediationSessionStrategy)} change={next => field("session_strategy", next)} />
    <p>Reuse selects an eligible linked session first. Archived or explicitly paused sessions cannot be resumed automatically. A new session requires the Agent Worker and Runner Device selected below.</p>
    {children}
    <PolicyChoice label="Conflict resolution strategy" value={value.conflict_strategy} values={Object.values(ConflictStrategy)} change={next => field("conflict_strategy", next)} />
    {value.conflict_strategy === ConflictStrategy.Rebase ? <p>Rebase requires force-with-lease against the expected remote head. A changed head requires reconciliation.</p> : null}
    <label>Consecutive automatic attempt limit<input type="number" min={1} max={100} step={1} required value={typeof value.attempt_limit === "number" ? value.attempt_limit : ""} onChange={event => field("attempt_limit", event.target.value === "" ? null : Number(event.target.value))} /></label>
    <fieldset><legend>Automatic feedback reviewers</legend>
      <p>Any one selector may match. Identities and required current permissions must be verified before execution. Names and bot suffixes do not identify an account or App.</p>
      {selectors.length === 0 ? <p>No reviewer selectors: published feedback cannot trigger automatic handling. It remains available for manual action.</p> : null}
      <ol>{selectors.map((selector, index) => <li key={index}><fieldset><legend>Reviewer selector {index + 1}</legend>
        <PolicyChoice label={`Selector ${index + 1} type`} value={selector.kind} values={Object.values(ReviewerSelectorKind)} change={kind => update(index, kind === ReviewerSelectorKind.Permission ? { kind, permission: ReviewerPermission.Read } : { kind, id: "", node_id: "" })} />
        {selector.kind === ReviewerSelectorKind.Permission ? <PolicyChoice label={`Selector ${index + 1} minimum permission`} value={selector.permission} values={Object.values(ReviewerPermission)} change={permission => update(index, { ...selector, permission })} /> : <>
          <label>Selector {index + 1} GitHub numeric ID<input required inputMode="numeric" pattern="[1-9][0-9]*" maxLength={20} value={text(selector.id)} onChange={event => update(index, { ...selector, id: event.target.value })} /></label>
          <label>Selector {index + 1} GitHub node ID<input required maxLength={256} value={text(selector.node_id)} onChange={event => update(index, { ...selector, node_id: event.target.value })} /></label>
          <p>Use the exact IDs from verified feedback author or App details. User, bot and App identities are separate.</p>
        </>}
        <button type="button" onClick={() => field("reviewer_selectors", selectors.filter((_, i) => i !== index))}>Remove reviewer selector {index + 1}</button>
      </fieldset></li>)}</ol>
      <button type="button" disabled={selectors.length >= 100} onClick={() => field("reviewer_selectors", [...selectors, { kind: ReviewerSelectorKind.User, id: "", node_id: "" }])}>Add reviewer selector</button>
    </fieldset>
  </>;
  return <fieldset><legend>Pull request remediation policy</legend>
    <p>Enabled policies run bounded fixes for linked pull requests when the Agent, Runner Device, and current evidence are eligible.</p>
    {[["ci_failure", "Automatically fix required CI failures"], ["review_feedback", "Automatically handle matching published feedback"], ["merge_conflict", "Automatically resolve verified merge conflicts"]].map(([key, label]) => <label className="checkbox" key={key}><input type="checkbox" checked={value[key] === true} onChange={event => field(key, event.target.checked)} />{label}</label>)}
    {presentation === RemediationDetailPresentation.Collapsible ? <details className="server-remediation-details">
      <summary aria-label="Remediation details" aria-describedby={helperId}>Remediation details<span id={helperId} className="server-remediation-helper">Session strategy, execution targets, conflicts, attempt limit, and reviewers.</span></summary>
      <div className="server-remediation-fields">{details}</div>
    </details> : details}
  </fieldset>;
}
