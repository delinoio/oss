import { LocalizedText, copy, useLocale } from "./localization";
import { items, object, text, type Document } from "./documents";
import { bounded, positive, sha } from "./github-query-model";

const digest = (value: unknown) => typeof value === "string" && /^[a-f0-9]{64}$/.test(value);
const sourceKinds = new Map([["Repository", "repository"], ["Organization", "organization"], ["Enterprise", "enterprise"]]);

export function validWorkflowReference(raw: unknown): boolean {
  const value = object(raw);
  return positive(value.repository_id) && bounded(value.path, 1024) && value.path.startsWith(".github/workflows/") && !/[\\\r\n]/.test(value.path) && !value.path.split("/").some((part: string) => !part || part === "." || part === "..") && (value.sha == null || sha(value.sha)) && (value.ref == null || bounded(value.ref, 1024));
}

export function validPRRules(raw: unknown, item: Document): boolean {
  const value = object(raw);
  if (raw == null || value.base_ref !== item.base_ref || value.base_sha !== item.base_sha || value.head_sha !== item.head_sha || !digest(value.digest) || !Array.isArray(value.rules) || value.rules.length > 500) return false;
  const seen = new Set<string>();
  const sources = new Map<string, string>();
  for (const raw of value.rules) {
    const rule = object(raw), key = `${text(rule.ruleset_id)}:${text(rule.type)}`;
    if (!positive(rule.ruleset_id) || !bounded(rule.type, 100) || !bounded(rule.source, 512) || !bounded(rule.native_source_kind, 64) || /[\r\n]/.test(rule.type + rule.source + rule.native_source_kind) || rule.source_kind !== (sourceKinds.get(rule.native_source_kind) ?? "unknown") || !digest(rule.digest) || seen.has(key)) return false;
    seen.add(key);
    const source = JSON.stringify([rule.native_source_kind, rule.source]);
    if (sources.has(rule.ruleset_id) && sources.get(rule.ruleset_id) !== source) return false;
    sources.set(rule.ruleset_id, source);
    if (rule.required_workflows != null) {
      const workflows = object(rule.required_workflows);
      if (rule.type !== "workflows" || rule.required_checks != null || !Array.isArray(workflows.workflows) || workflows.workflows.length > 100 || (workflows.unknown_parameters != null && typeof workflows.unknown_parameters !== "boolean") || (workflows.do_not_enforce_on_create != null && typeof workflows.do_not_enforce_on_create !== "boolean")) return false;
      const seen = new Set<string>();
      for (const raw of workflows.workflows) {
        const ref = object(raw), key = `${ref.repository_id}:${ref.path}`;
        if (!validWorkflowReference(raw) || seen.has(key)) return false;
        seen.add(key);
      }
    }
    if (rule.type !== "required_status_checks") {
      if (rule.required_checks != null) return false;
      continue;
    }
    const required = object(rule.required_checks);
    if (typeof required.strict !== "boolean" || (required.unknown_parameters != null && typeof required.unknown_parameters !== "boolean") || (required.do_not_enforce_on_create != null && typeof required.do_not_enforce_on_create !== "boolean") || !Array.isArray(required.checks) || required.checks.length > 100) return false;
    const checks = new Set<string>();
    for (const raw of required.checks) {
      const check = object(raw);
      if (!bounded(check.context, 1024) || /[\r\n]/.test(check.context) || (check.integration_id != null && check.integration_id !== "0" && !positive(check.integration_id))) return false;
      const key = `${check.context}\0${check.integration_id ?? ""}`;
      if (checks.has(key)) return false;
      checks.add(key);
    }
  }
  return true;
}

export function PRRules({ value }: { value: Document }) {
  useLocale();
  const rules = items(value.rules);
  return <section aria-label={copy("github-rules.activePrBaseRules_707652")}>
    <p><LocalizedText id="github-rules.activeRulesForAtBase_116160" components={{ s0: <code>{text(value.base_ref)}</code>, s1: <code>{text(value.base_sha)}</code> }} /></p>
    <p>{copy("github-rules.theseRulesDescribeRequiredChecksThey_d1ba1d")}</p>
    {rules.length === 0 ? <p>{copy("github-rules.noActiveRulesWereReturnedFor_72967c")}</p> : rules.map((raw) => {
      const rule = object(raw), required = object(rule.required_checks);
      return <article key={`${text(rule.ruleset_id)}:${text(rule.type)}`}>
        <h5>{text(rule.type)}</h5>
        <p><LocalizedText id="github-rules.ruleset_d1d0bf" components={{ s0: <>{text(rule.native_source_kind)}</>, s1: <>{text(rule.source)}</>, s2: <>{text(rule.ruleset_id)}</>, s3: <>{rule.source_kind === "unknown" ? copy("github-rules.unknownSourceType_5f3c59") : ""}</> }} /></p>
        {rule.type === "required_status_checks" ? <>
          {required.unknown_parameters ? <p>{copy("github-rules.thisRuleIncludesUnrecognizedParametersCi_e5756c")}</p> : null}
          <p><LocalizedText id="github-rules.requireAnUpToDateBranch_cade59" components={{ s0: <>{required.strict ? copy("github-rules.yes_85a39a") : copy("github-rules.no_1ea442")}</> }} /></p>
          {items(required.checks).length ? <table><caption><LocalizedText id="github-rules.requiredStatusChecksRuleset_f7be98" components={{ s0: <>{text(rule.ruleset_id)}</> }} /></caption>
            <thead><tr><th scope={"col"}>{copy("github-rules.contextOrCheckName_177862")}</th><th scope={"col"}>{copy("github-rules.requiredApp_3802d1")}</th></tr></thead>
            <tbody>{items(required.checks).map((raw) => { const check = object(raw); return <tr key={`${text(check.context)}:${text(check.integration_id)}`}><th scope={"row"}>{text(check.context)}</th><td>{check.integration_id == null ? copy("github-rules.noAppRestrictionReported_39534b") : check.integration_id === "0" ? copy("github-rules.providerReported0Unresolved_3b4b8d") : copy("github-rules.app_915609", { v0: text(check.integration_id) })}</td></tr>; })}</tbody>
          </table> : <p>{copy("github-rules.thisRuleListsNoRequiredStatus_16ad10")}</p>}
        </> : null}
        {rule.required_workflows ? <ul aria-label={copy("github-rules.requiredWorkflows_49dd4c")}>{items(object(rule.required_workflows).workflows).map((raw) => { const ref = object(raw); return <li key={`${ref.repository_id}:${ref.path}`}><LocalizedText id="github-rules.repository_edbad0" components={{ s0: <>{text(ref.repository_id)}</>, s1: <code>{text(ref.path)}</code>, s2: <>{ref.sha ? <code>{text(ref.sha)}</code> : copy("github-rules.noImmutableSourceShaEvaluationRemains_48550b")}</> }} /></li>; })}</ul> : null}
      </article>;
    })}
  </section>;
}
