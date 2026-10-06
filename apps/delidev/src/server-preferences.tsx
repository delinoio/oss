// SPDX-License-Identifier: Apache-2.0
import { type SyntheticEvent } from "react";
import { EntityKind, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { Routing, ServerPreferenceSection } from "./configuration-fields";
import { document, object, text } from "./documents";
import { ConflictStrategy, RemediationSessionStrategy } from "./remediation-policy";
import "./server-preferences.css";

export function readableServerPreferences(row: Resource): boolean {
  const data = document(row), policy = object(data.remediation);
  // Editable values must be present in the saved document. Never turn a parser
  // fallback, future schema or unknown policy into plausible default settings.
  return row.kind === EntityKind.SETTINGS && isEntityId(row.id) && row.revision > 0n && row.documentJson.byteLength <= 1 << 20
    && row.schemaVersion === 1 && Object.values(Routing).includes(data.default_routing as Routing)
    && typeof data.automatic_fetch === "boolean" && typeof data.notifications === "boolean"
    && [policy.ci_failure, policy.review_feedback, policy.merge_conflict].every(flag => typeof flag === "boolean")
    && Object.values(ConflictStrategy).includes(policy.conflict_strategy as ConflictStrategy)
    && Object.values(RemediationSessionStrategy).includes(policy.session_strategy as RemediationSessionStrategy)
    && typeof policy.attempt_limit === "number" && Number.isInteger(policy.attempt_limit) && policy.attempt_limit >= 1 && policy.attempt_limit <= 100;
}

export interface ServerPreferencesObservation {
  complete: boolean;
  resource?: Resource;
  fetching: boolean;
  error?: unknown;
}

export function latestServerPreferences(baseline?: Resource, ...observations: (Resource | undefined)[]): Resource | undefined {
  const replacement = baseline && observations.find(row => row && row.id !== baseline.id);
  return replacement || observations.reduce((latest, row) => row && (!latest || row.revision > latest.revision) ? row : latest, baseline);
}

export function ServerPreferencesUnavailable({ rows }: { rows: Resource[] }) {
  return <section className="server-preferences-unavailable" aria-label="Server preferences unavailable">
    <p role="status">{rows.length === 1 && rows[0].schemaVersion !== 1
      ? "Unsupported server preferences schema. Policy values are unavailable."
      : rows.length === 1 && !readableServerPreferences(rows[0])
      ? "Server preferences are unreadable or contain unsupported policy values. Policy values are unavailable."
      : "The server preferences read is incomplete or does not identify one settings document. Refresh settings before editing."}</p>
    {rows.map(row => <small className="server-preferences-id" key={row.id}>{row.id}</small>)}
  </section>;
}

export function serverPreferenceLabel(section: ServerPreferenceSection): string {
  return section === ServerPreferenceSection.GitWorkflow ? "Git workflow" : "Server preferences";
}

export function ServerPreferencesEmpty({ section = ServerPreferenceSection.All }: { section?: ServerPreferenceSection }) {
  const label = serverPreferenceLabel(section);
  return <section className="server-preferences-panel" aria-label={`No saved ${label.toLowerCase()}`}>
    <h2>No saved {label.toLowerCase()}</h2>
    <p>Review the defaults, then save one preference set for this server.</p>
    {section !== ServerPreferenceSection.GitWorkflow ? <section className="server-preference-section"><h3>Account routing</h3><p>Choose the default policy for Agent Workers that inherit server routing.</p></section> : null}
    {section !== ServerPreferenceSection.AccountRouting ? <>
    <section className="server-preference-section"><h3>Worktree fetch</h3><p>Allow fetching before Worktree preparation. Repository preferences also apply.</p></section>
    <section className="server-preference-section"><h3>Pull request remediation</h3><p>Configure bounded automatic fixes for linked pull requests. All automatic policies default off.</p></section>
    </> : null}
    <p className="server-preferences-footnote">Choose New {label} to review and save.</p>
  </section>;
}

export function ServerPreferencesSummary({ row, section = ServerPreferenceSection.All }: { row: Resource; section?: ServerPreferenceSection }) {
  const data = document(row), policy = object(data.remediation);
  const label = serverPreferenceLabel(section).toLowerCase();
  return <article className="server-preferences-panel" aria-label={`Saved ${label}`}>
    <h2>Saved {label}</h2><small className="server-preferences-id">{row.id}</small>
    {readableServerPreferences(row) ? <>
      {section !== ServerPreferenceSection.GitWorkflow ? <section className="server-preference-section"><h3>Account routing</h3><dl><dt>Default account routing</dt><dd>{text(data.default_routing)}</dd></dl></section> : null}
      {section !== ServerPreferenceSection.AccountRouting ? <>
      <section className="server-preference-section"><h3>Worktree fetch</h3><dl><dt>Automatic fetch before Worktree preparation</dt><dd>{data.automatic_fetch ? "Allowed" : "Disabled"}</dd></dl><p>Repository preferences also apply.</p></section>
      <section className="server-preference-section"><h3>Pull request remediation</h3>
        <p>Enabled policies run bounded fixes for linked pull requests when the Agent, Runner Device, and current evidence are eligible.</p>
        <dl>{[["ci_failure", "Automatically fix required CI failures"], ["review_feedback", "Automatically handle matching published feedback"], ["merge_conflict", "Automatically resolve verified merge conflicts"]].map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{policy[key] ? "On" : "Off"}</dd></div>)}</dl>
      </section>
      </> : null}
    </> : <p role="status">{row.schemaVersion !== 1 ? "Unsupported server preferences schema. Policy values are unavailable." : "Server preferences are unreadable or contain unsupported policy values. Policy values are unavailable."}</p>}
  </article>;
}

export function revealServerPreferenceInvalidControl(event: SyntheticEvent<HTMLFormElement>) {
  event.preventDefault();
  const first = Array.from(event.currentTarget.querySelectorAll<HTMLInputElement | HTMLSelectElement>("input, select"))
    .find(control => control.willValidate && !control.validity.valid);
  const details = first?.closest<HTMLDetailsElement>(".server-remediation-details");
  if (details) details.open = true;
  // Constraint validation precedes submit, including for mounted closed details.
  // Reveal before layout/focus without changing the retained draft or queries.
  window.requestAnimationFrame(() => { if (first?.isConnected && first.willValidate) first.focus(); });
}
