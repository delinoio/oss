// SPDX-License-Identifier: Apache-2.0
import { type SyntheticEvent } from "react";
import { EntityKind, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { Routing, ServerPreferenceSection } from "./configuration-fields";
import { document, object } from "./documents";
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

export function ServerPreferencesUnavailable({ rows, section = ServerPreferenceSection.All }: { rows: Resource[]; section?: ServerPreferenceSection }) {
  const label = serverPreferenceLabel(section);
  return <section className="server-preferences-unavailable" aria-label={`${label} unavailable`}>
    <p role="status">{rows.length === 1 && rows[0].schemaVersion !== 1
      ? `Unsupported ${label.toLowerCase()} schema. Policy values are unavailable.`
      : rows.length === 1 && !readableServerPreferences(rows[0])
      ? `${label} contains unreadable or unsupported policy values. Policy values are unavailable.`
      : `The ${label.toLowerCase()} read is incomplete or does not identify one settings document. Refresh settings before editing.`}</p>
    {rows.map(row => <small className="server-preferences-id" key={row.id}>{row.id}</small>)}
  </section>;
}

export function serverPreferenceLabel(section: ServerPreferenceSection): string {
  return section === ServerPreferenceSection.GitWorkflow ? "Git workflow" : "Server preferences";
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
