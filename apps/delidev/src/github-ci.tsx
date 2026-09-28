import { items, object, text, type Document } from "./documents";
import { bounded, positive, sha } from "./github-query-model";
import { validPRRules } from "./github-rules";

const states = new Set(["unknown", "missing", "pending", "non-failing", "terminal-failure", "not-required"]);
const reasons = new Set(["observed", "no-matching-result", "app-unverified", "unknown-native-result", "unsupported-rule", "commit-unverified", "closed-pr", "workflow-unverified"]);
function validRollup(raw: unknown, expected: unknown, seen: Set<string>): boolean {
  const value = object(raw);
  if (raw == null || !sha(expected) || value.commit_sha !== expected || !Array.isArray(value.contexts) || value.contexts.length > 500 || value.total_count !== String(value.contexts.length)) return false;
  for (const raw of value.contexts) {
    const row = object(raw);
    if (!bounded(row.node_id, 256) || seen.has(row.node_id) || row.commit_sha !== expected || !bounded(row.name, 1024) || typeof row.required !== "boolean" || !bounded(row.native_status, 64)) return false;
    seen.add(row.node_id);
    if (row.kind === "check-run") {
      if ((row.native_conclusion != null && !bounded(row.native_conclusion, 64)) || (row.workflow_event != null && !bounded(row.workflow_event, 100))) return false;
      const app = object(row.application);
      if (row.application != null && (!positive(app.id) || !bounded(app.node_id, 256) || !bounded(app.slug, 100))) return false;
    } else if (row.kind !== "commit-status" || row.application != null || row.native_conclusion != null || row.workflow_event != null) return false;
  }
  return true;
}
export function validPRCI(raw: unknown, item: Document): boolean {
  const value = object(raw), result = object(value.result);
  const seen = new Set<string>();
  if (raw == null || !validPRRules(value.rules, item) || !validRollup(value.head, item.head_sha, seen) || !["MERGEABLE", "CONFLICTING", "UNKNOWN"].includes(text(value.native_mergeability)) || typeof value.in_merge_queue !== "boolean") return false;
  const merge = object(value.test_merge);
  if (value.test_merge != null && (merge.commit_sha === item.head_sha || value.native_mergeability === "CONFLICTING" || !validRollup(value.test_merge, merge.commit_sha, seen))) return false;
  if (!states.has(text(result.state)) || !reasons.has(text(result.reason)) || !Array.isArray(result.requirements)) return false;
  let selected: Document;
  if (result.source === "head") {
    selected = object(value.head);
    if (result.evaluated_sha !== item.head_sha || items(merge.contexts).length > 0) return false;
  } else if (result.source === "test-merge") {
    selected = merge;
    if (result.evaluated_sha !== merge.commit_sha || items(merge.contexts).length === 0) return false;
  } else if (result.source === "unknown") {
    return result.evaluated_sha == null && result.state === "unknown" && result.requirements.length === 0;
  } else return false;
  if (value.in_merge_queue || value.native_mergeability === "UNKNOWN" || (value.native_mergeability === "MERGEABLE" && value.test_merge == null) || item.state !== "open" || item.merged) return false;
  const expected: Document[] = items(object(value.rules).rules).flatMap((raw) => {
    const rule = object(raw);
    return rule.type === "required_status_checks" ? items(object(rule.required_checks).checks).map((raw) => ({ ...object(raw), ruleset_id: rule.ruleset_id })) : [];
  });
  if (result.requirements.length !== expected.length) return false;
  const contexts = new Map(items(selected.contexts).map((raw) => { const row = object(raw); return [text(row.node_id), row] as const; }));
  for (const [index, raw] of result.requirements.entries()) {
    const row = object(raw), requirement = expected[index];
    if (row.ruleset_id !== requirement.ruleset_id || row.context !== requirement.context || row.integration_id !== requirement.integration_id || !states.has(text(row.state)) || !reasons.has(text(row.reason)) || !Array.isArray(row.result_node_ids) || row.result_node_ids.length > 500) return false;
    const used = new Set<string>();
    for (const id of row.result_node_ids) {
      if (typeof id !== "string" || used.has(id)) return false;
      const context = contexts.get(id);
      if (!context || context.name !== row.context || context.required !== true) return false;
      used.add(id);
    }
  }
  return true;
}

const stateLabels = new Map([["unknown", "Unknown"], ["missing", "Required result missing"], ["pending", "Required checks pending"], ["non-failing", "Observed required results are non-failing"], ["terminal-failure", "Terminal required CI failure"], ["not-required", "No active ruleset status checks required"]]);
const reasonLabels = new Map([
  ["app-unverified", "The required App could not be verified."],
  ["workflow-unverified", "The workflow event could not be verified."],
  ["unknown-native-result", "GitHub reported an unknown or incomplete result."],
  ["unsupported-rule", "A rule needs additional evaluation before automatic CI handling."],
  ["commit-unverified", "GitHub's evaluated commit could not be verified, including merge-queue evaluation."],
  ["closed-pr", "The PR is closed; these results cannot start automatic handling."],
  ["no-matching-result", "No matching required result was observed."],
]);
export function PRCI({ value }: { value: Document }) {
  const result = object(value.result), selected = result.source === "test-merge" ? object(value.test_merge) : object(value.head);
  return <section aria-label="Required CI evaluation">
    <p role="status">{stateLabels.get(text(result.state))}</p>
    {result.evaluated_sha ? <p>Evaluated {result.source === "test-merge" ? "test merge" : "head"} commit: <code>{text(result.evaluated_sha)}</code></p> : null}
    {reasonLabels.has(text(result.reason)) ? <p>{reasonLabels.get(text(result.reason))}</p> : null}
    <p>This is a current observation of active rulesets. Missing, pending and unknown results do not establish passing CI. A later action requires fresh evidence.</p>
    {items(result.requirements).length ? <table><caption>Active ruleset CI requirements</caption><thead><tr><th scope="col">Requirement</th><th scope="col">App</th><th scope="col">Result</th></tr></thead><tbody>{items(result.requirements).map((raw, index) => { const row = object(raw); return <tr key={index}><th scope="row">{text(row.context)}<small> · ruleset {text(row.ruleset_id)}</small></th><td>{text(row.integration_id) || "No restriction reported"}</td><td>{stateLabels.get(text(row.state))}{reasonLabels.has(text(row.reason)) ? <small> · {reasonLabels.get(text(row.reason))}</small> : null}</td></tr>; })}</tbody></table> : null}
    {result.source !== "unknown" ? <details><summary>Inspected check and status results ({text(selected.total_count)})</summary><ul>{items(selected.contexts).map((raw) => { const row = object(raw); return <li key={text(row.node_id)}>{text(row.name)} · {text(row.kind)} · {text(row.native_status)}{row.native_conclusion ? ` / ${text(row.native_conclusion)}` : ""} · {row.required ? "GitHub required" : "GitHub optional"}{row.application ? ` · App ${text(object(row.application).id)}` : ""}{row.workflow_event ? ` · ${text(row.workflow_event)}` : ""}</li>; })}</ul></details> : null}
  </section>;
}
