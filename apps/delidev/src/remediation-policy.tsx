import { LocalizedText, copy, useLocale } from "./localization";
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
  useLocale();
  const selected = text(value);
  return <label>{label}<select required value={selected} onChange={event => change(event.target.value)}>
    {!values.includes(selected) ? <option value={selected}>{selected ? copy("remediation-policy.unsupportedSelection_f0a19a", { v0: selected }) : copy("remediation-policy.selectAValue_019c6e")}</option> : null}
    {values.map(option => <option key={option} value={option}>{option}</option>)}
  </select></label>;
}

export function RemediationPolicyFields({ value, change, children, presentation = RemediationDetailPresentation.Expanded }: { value: Document; change: (value: Document) => void; children: ReactNode; presentation?: RemediationDetailPresentation }) {
  useLocale();
  const helperId = useId();
  const field = (key: string, next: unknown) => change({ ...value, [key]: next });
  const selectors = items(value.reviewer_selectors).map(object);
  const update = (index: number, next: Document) => field("reviewer_selectors", selectors.map((selector, i) => i === index ? next : selector));
  const details = <>
    <PolicyChoice label={copy("remediation-policy.remediationSessionStrategy_225752")} value={value.session_strategy} values={Object.values(RemediationSessionStrategy)} change={next => field("session_strategy", next)} />
    <p>{copy("remediation-policy.reuseSelectsAnEligibleLinkedSession_c48674")}</p>
    {children}
    <PolicyChoice label={copy("remediation-policy.conflictResolutionStrategy_9753fe")} value={value.conflict_strategy} values={Object.values(ConflictStrategy)} change={next => field("conflict_strategy", next)} />
    {value.conflict_strategy === ConflictStrategy.Rebase ? <p>{copy("remediation-policy.rebaseRequiresForceWithLeaseAgainst_5ffb1a")}</p> : null}
    <label>{copy("remediation-policy.consecutiveAutomaticAttemptLimit_844605")}<input type="number" min={1} max={100} step={1} required value={typeof value.attempt_limit === "number" ? value.attempt_limit : ""} onChange={event => field("attempt_limit", event.target.value === "" ? null : Number(event.target.value))} /></label>
    <fieldset><legend>{copy("remediation-policy.automaticFeedbackReviewers_940eb7")}</legend>
      <p>{copy("remediation-policy.anyOneSelectorMayMatchIdentities_db62c3")}</p>
      {selectors.length === 0 ? <p>{copy("remediation-policy.noReviewerSelectorsPublishedFeedbackCannot_033112")}</p> : null}
      <ol>{selectors.map((selector, index) => <li key={index}><fieldset><legend><LocalizedText id="remediation-policy.reviewerSelector_6a8da1" components={{ s0: <>{index + 1}</> }} /></legend>
        <PolicyChoice label={copy("remediation-policy.selectorType_d6da3a", { v0: index + 1 })} value={selector.kind} values={Object.values(ReviewerSelectorKind)} change={kind => update(index, kind === ReviewerSelectorKind.Permission ? { kind, permission: ReviewerPermission.Read } : { kind, id: "", node_id: "" })} />
        {selector.kind === ReviewerSelectorKind.Permission ? <PolicyChoice label={copy("remediation-policy.selectorMinimumPermission_6cd00b", { v0: index + 1 })} value={selector.permission} values={Object.values(ReviewerPermission)} change={permission => update(index, { ...selector, permission })} /> : <>
          <label><LocalizedText id="remediation-policy.selectorGithubNumericId_ab986c" components={{ s0: <>{index + 1}</> }} /><input required inputMode="numeric" pattern="[1-9][0-9]*" maxLength={20} value={text(selector.id)} onChange={event => update(index, { ...selector, id: event.target.value })} /></label>
          <label><LocalizedText id="remediation-policy.selectorGithubNodeId_0ffe19" components={{ s0: <>{index + 1}</> }} /><input required maxLength={256} value={text(selector.node_id)} onChange={event => update(index, { ...selector, node_id: event.target.value })} /></label>
          <p>{copy("remediation-policy.useTheExactIdsFromVerified_267b6d")}</p>
        </>}
        <button type="button" onClick={() => field("reviewer_selectors", selectors.filter((_, i) => i !== index))}><LocalizedText id="remediation-policy.removeReviewerSelector_b485a1" components={{ s0: <>{index + 1}</> }} /></button>
      </fieldset></li>)}</ol>
      <button type="button" disabled={selectors.length >= 100} onClick={() => field("reviewer_selectors", [...selectors, { kind: ReviewerSelectorKind.User, id: "", node_id: "" }])}>{copy("remediation-policy.addReviewerSelector_c0a36d")}</button>
    </fieldset>
  </>;
  return <fieldset><legend>{copy("remediation-policy.pullRequestRemediationPolicy_98e643")}</legend>
    <p>{copy("remediation-policy.enabledPoliciesRunBoundedFixesFor_2758c5")}</p>
    {[["ci_failure", "Automatically fix required CI failures"], ["review_feedback", "Automatically handle matching published feedback"], ["merge_conflict", "Automatically resolve verified merge conflicts"]].map(([key, label]) => <label className="checkbox" key={key}><input type="checkbox" checked={value[key] === true} onChange={event => field(key, event.target.checked)} />{label}</label>)}
    {presentation === RemediationDetailPresentation.Collapsible ? <details className="server-remediation-details">
      <summary aria-label={copy("remediation-policy.remediationDetails_15b723")} aria-describedby={helperId}><LocalizedText id="remediation-policy.remediationDetails_8af20a" components={{ s0: <span id={helperId} className="server-remediation-helper">{copy("remediation-policy.sessionStrategyExecutionTargetsConflictsAttempt_ef59b9")}</span> }} /></summary>
      <div className="server-remediation-fields">{details}</div>
    </details> : details}
  </fieldset>;
}
