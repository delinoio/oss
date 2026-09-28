import { items, object, text, type Document } from "./documents";
import { bounded, positive } from "./github-query-model";

const digest = (value: unknown) => typeof value === "string" && /^[a-f0-9]{64}$/.test(value);
const sourceKinds = new Map([["Repository", "repository"], ["Organization", "organization"], ["Enterprise", "enterprise"]]);

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
    if (rule.type !== "required_status_checks") {
      if (rule.required_checks != null) return false;
      continue;
    }
    const required = object(rule.required_checks);
    if (typeof required.strict !== "boolean" || (required.do_not_enforce_on_create != null && typeof required.do_not_enforce_on_create !== "boolean") || !Array.isArray(required.checks) || required.checks.length > 100) return false;
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
  const rules = items(value.rules);
  return <section aria-label="Active PR base rules">
    <p>Active rules for <code>{text(value.base_ref)}</code> at base <code>{text(value.base_sha)}</code>.</p>
    <p>These rules describe required checks. They do not establish which commit GitHub evaluates or whether CI passes.</p>
    {rules.length === 0 ? <p>No active rules were returned for this PR base. Classic branch protection is not included.</p> : rules.map((raw) => {
      const rule = object(raw), required = object(rule.required_checks);
      return <article key={`${text(rule.ruleset_id)}:${text(rule.type)}`}>
        <h5>{text(rule.type)}</h5>
        <p>{text(rule.native_source_kind)}: {text(rule.source)} · Ruleset {text(rule.ruleset_id)}{rule.source_kind === "unknown" ? " · Unknown source type" : ""}</p>
        {rule.type === "required_status_checks" ? <>
          <p>Require an up-to-date branch: {required.strict ? "Yes" : "No"}</p>
          {items(required.checks).length ? <table><caption>Required status checks · ruleset {text(rule.ruleset_id)}</caption>
            <thead><tr><th scope="col">Context or check name</th><th scope="col">Required App</th></tr></thead>
            <tbody>{items(required.checks).map((raw) => { const check = object(raw); return <tr key={`${text(check.context)}:${text(check.integration_id)}`}><th scope="row">{text(check.context)}</th><td>{check.integration_id == null ? "No App restriction reported" : check.integration_id === "0" ? "Provider reported 0 · unresolved" : `App ${text(check.integration_id)}`}</td></tr>; })}</tbody>
          </table> : <p>This rule lists no required status checks.</p>}
        </> : null}
      </article>;
    })}
  </section>;
}
