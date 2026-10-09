import { validBranchPrefix } from "./session-defaults";
import { copy, useLocale } from "./localization";
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
    && [1, 2, 3].includes(row.schemaVersion) && (row.schemaVersion !== 3 || typeof data.plan_mode_default === "boolean" && typeof data.branch_prefix === "string" && validBranchPrefix(data.branch_prefix)) && (row.schemaVersion === 1 || typeof data.automatic_plan_approval === "boolean") && Object.values(Routing).includes(data.default_routing as Routing)
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
  useLocale();
  const label = serverPreferenceLabel(section);
  return <section className="server-preferences-unavailable" aria-label={`${label} unavailable`}>
    <p role="status">{rows.length === 1 && ![1, 2, 3].includes(rows[0].schemaVersion)
      ? `Unsupported ${label.toLowerCase()} schema. Policy values are unavailable.`
      : rows.length === 1 && !readableServerPreferences(rows[0])
      ? `${label} contains unreadable or unsupported policy values. Policy values are unavailable.`
      : `The ${label.toLowerCase()} read is incomplete or does not identify one settings document. Refresh settings before editing.`}</p>
    {rows.map(row => <small className="server-preferences-id" key={row.id}>{row.id}</small>)}
  </section>;
}

export function serverPreferenceLabel(section: ServerPreferenceSection): string {
  return copy("configuration-fields.projectDefaults");
}

export function ServerPreferencesEmpty({ section = ServerPreferenceSection.All }: { section?: ServerPreferenceSection }) {
  useLocale();
  const label = serverPreferenceLabel(section);
  const shortLabel = label.toLowerCase();
  return <section className="server-preferences-panel" aria-label={copy("server-preferences.noSavedPreferences", { v0: shortLabel })}>
    <h2>{copy("server-preferences.noSavedPreferences", { v0: shortLabel })}</h2>
    <p>{copy("server-preferences.reviewTheDefaultsThenSaveOne_2fb225")}</p>
    {section !== ServerPreferenceSection.GitWorkflow ? <section className="server-preference-section"><h3>{copy("server-preferences.defaultAccountRouting_bb44ea")}.</h3><p>{copy("server-preferences.chooseTheDefaultPolicyForAgent_fbb9a2")}</p></section> : null}
    {section !== ServerPreferenceSection.AccountRouting ? <>
    <section className="server-preference-section"><h3>{copy("server-preferences.worktreeFetch_0d4c18")}</h3><p>{copy("server-preferences.allowFetchingBeforeWorktreePreparationRepository_e71fc1")}</p></section>
    <section className="server-preference-section"><h3>{copy("server-preferences.pullRequestRemediation_4cded4")}</h3><p>{copy("server-preferences.configureBoundedAutomaticFixesForLinked_c08378")}</p></section>
    </> : null}
    <p className="server-preferences-footnote">{copy("server-preferences.chooseNewPreferencesToReview", { v0: label })}</p>
  </section>;
}

export function ServerPreferencesSummary({ row, section = ServerPreferenceSection.All }: { row: Resource; section?: ServerPreferenceSection }) {
  useLocale();
  const data = document(row), policy = object(data.remediation);
  const shortLabel = serverPreferenceLabel(section).toLowerCase();
  return <article className="server-preferences-panel" aria-label={copy("server-preferences.savedPreferences", { v0: shortLabel })}>
    <h2>{copy("server-preferences.savedPreferences", { v0: shortLabel })}</h2><small className="server-preferences-id">{row.id}</small>
    {readableServerPreferences(row) ? <>
 {section === ServerPreferenceSection.ProjectDefaults && row.schemaVersion === 3 ? <dl><dt>{copy("configuration-fields.planModeDefault")}</dt><dd>{copy(data.plan_mode_default ? "configuration-fields.enabled" : "configuration-fields.disabled")}</dd></dl> : null}
 {section === ServerPreferenceSection.GitWorkflow && row.schemaVersion === 3 ? <section><h3>{copy("configuration-fields.branchPrefix")}</h3><p>{text(data.branch_prefix) || copy("configuration-fields.disabled")}</p><p>{copy("configuration-fields.branchPrefixHelp")}</p></section> : null}
      {section !== ServerPreferenceSection.GitWorkflow ? <section className="server-preference-section"><h3>{copy("server-preferences.accountRouting_0c3707")}</h3><dl><dt>{copy("server-preferences.defaultAccountRouting_bb44ea")}</dt><dd>{text(data.default_routing)}</dd></dl></section> : null}
      {section !== ServerPreferenceSection.AccountRouting ? <>
      <section className="server-preference-section"><h3>{copy("server-preferences.worktreeFetch_0d4c18")}</h3><dl><dt>{copy("server-preferences.automaticFetchBeforeWorktreePreparation_510043")}</dt><dd>{data.automatic_fetch ? copy("server-preferences.allowed_1bb201") : copy("server-preferences.disabled_75081b")}</dd></dl><p>{copy("server-preferences.repositoryPreferencesAlsoApply")}</p></section>
      <section className="server-preference-section"><h3>{copy("server-preferences.pullRequestRemediation_4cded4")}</h3>
        <p>{copy("server-preferences.enabledPoliciesRunBoundedFixesFor_2758c5")}</p>
        <dl>{[["ci_failure", copy("server-preferences.extra.5ecef0b36ba3")], ["review_feedback", copy("server-preferences.extra.feab8b0b6d7b")], ["merge_conflict", copy("server-preferences.extra.7f64e75c8c6e")]].map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{policy[key] ? copy("server-preferences.on_130011") : copy("server-preferences.off_ca7981")}</dd></div>)}</dl>
      </section>
      </> : null}
    </> : <p role="status">{![1, 2, 3].includes(row.schemaVersion) ? copy("server-preferences.unsupportedServerPreferencesSchemaPolicyValues_86046f") : copy("server-preferences.serverPreferencesAreUnreadableOrContain_17b4fc")}</p>}
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
