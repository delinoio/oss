import { copy, useLocale } from "./localization";
import { type SyntheticEvent } from "react";
import { type Resource } from "@delinoio/delidev-api-client";
import { Routing } from "./configuration-fields";
import { document, object, text } from "./documents";
import { ConflictStrategy, RemediationSessionStrategy } from "./remediation-policy";
import "./server-preferences.css";

export function readableServerPreferences(row: Resource): boolean {
  const data = document(row), policy = object(data.remediation);
  // Summary values must be present in the saved document. Never turn a parser
  // fallback, future schema or unknown policy into plausible default settings.
  return row.schemaVersion === 1 && Object.values(Routing).includes(data.default_routing as Routing)
    && typeof data.automatic_fetch === "boolean" && typeof data.notifications === "boolean"
    && [policy.ci_failure, policy.review_feedback, policy.merge_conflict].every(flag => typeof flag === "boolean")
    && Object.values(ConflictStrategy).includes(policy.conflict_strategy as ConflictStrategy)
    && Object.values(RemediationSessionStrategy).includes(policy.session_strategy as RemediationSessionStrategy)
    && typeof policy.attempt_limit === "number" && Number.isInteger(policy.attempt_limit) && policy.attempt_limit >= 1 && policy.attempt_limit <= 100;
}

export function ServerPreferencesEmpty() {
  useLocale();
  return <section className="server-preferences-panel" aria-label={copy("server-preferences.noSavedServerPreferences_6b56a2")}>
    <h2>{copy("server-preferences.noSavedServerPreferences_6b56a2")}</h2>
    <p>{copy("server-preferences.reviewTheDefaultsThenSaveOne_2fb225")}</p>
    <section className="server-preference-section"><h3>{copy("server-preferences.accountRouting_0c3707")}</h3><p>{copy("server-preferences.chooseTheDefaultPolicyForAgent_fbb9a2")}</p></section>
    <section className="server-preference-section"><h3>{copy("server-preferences.worktreeFetch_0d4c18")}</h3><p>{copy("server-preferences.allowFetchingBeforeWorktreePreparationRepository_e71fc1")}</p></section>
    <section className="server-preference-section"><h3>{copy("server-preferences.pullRequestRemediation_4cded4")}</h3><p>{copy("server-preferences.configureBoundedAutomaticFixesForLinked_c08378")}</p></section>
    <p className="server-preferences-footnote">{copy("server-preferences.chooseNewServerPreferencesToReview_3a11af")}</p>
  </section>;
}

export function ServerPreferencesSummary({ row }: { row: Resource }) {
  useLocale();
  const data = document(row), policy = object(data.remediation);
  return <article className="server-preferences-panel" aria-label={copy("server-preferences.savedServerPreferences_8341c8")}>
    <h2>{copy("server-preferences.savedServerPreferences_8341c8")}</h2><small className="server-preferences-id">{row.id}</small>
    {readableServerPreferences(row) ? <>
      <section className="server-preference-section"><h3>{copy("server-preferences.accountRouting_0c3707")}</h3><dl><dt>{copy("server-preferences.defaultAccountRouting_bb44ea")}</dt><dd>{text(data.default_routing)}</dd></dl></section>
      <section className="server-preference-section"><h3>{copy("server-preferences.worktreeFetch_0d4c18")}</h3><dl><dt>{copy("server-preferences.automaticFetchBeforeWorktreePreparation_510043")}</dt><dd>{data.automatic_fetch ? copy("server-preferences.allowed_1bb201") : copy("server-preferences.disabled_75081b")}</dd></dl></section>
      <section className="server-preference-section"><h3>{copy("server-preferences.pullRequestRemediation_4cded4")}</h3>
        <p>{copy("server-preferences.enabledPoliciesRunBoundedFixesFor_2758c5")}</p>
        <dl>{[["ci_failure", "Automatically fix required CI failures"], ["review_feedback", "Automatically handle matching published feedback"], ["merge_conflict", "Automatically resolve verified merge conflicts"]].map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{policy[key] ? copy("server-preferences.on_130011") : copy("server-preferences.off_ca7981")}</dd></div>)}</dl>
      </section>
    </> : <p role="status">{row.schemaVersion !== 1 ? copy("server-preferences.unsupportedServerPreferencesSchemaPolicyValues_86046f") : copy("server-preferences.serverPreferencesAreUnreadableOrContain_17b4fc")}</p>}
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
