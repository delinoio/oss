import { items, object, text, type Document } from "./documents";
import { bounded, positive, sha } from "./github-query-model";
import { validPRRules } from "./github-rules";
import { evaluableCIQueue, queueResultProvenance, selectedCIRollup, validCIQueue } from "./github-ci-queue";

const states = new Set(["unknown", "missing", "pending", "non-failing", "terminal-failure", "not-required"]);
const reasons = new Set(["observed", "no-matching-result", "app-unverified", "unknown-native-result", "unsupported-rule", "commit-unverified", "closed-pr", "workflow-unverified"]);
function ciTime(value: unknown): value is string {
  return typeof value === "string" && value.length <= 40 && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?(?:Z|[+-]\d\d:\d\d)$/.test(value) && Number.isFinite(Date.parse(value)) && Date.parse(value) >= 0;
}
function validEvidence(row: Document): boolean {
  const value = object(row.evidence), workflow = object(value.workflow);
  if (row.evidence == null) return false;
  for (const field of ["started_at", "completed_at", "created_at", "updated_at"]) if (value[field] != null && !ciTime(value[field])) return false;
  for (const field of ["title", "summary", "text", "description"]) if (value[field] != null && !bounded(value[field], 64 << 10, false)) return false;
  if (row.kind === "check-run") {
    if (!bounded(value.suite_node_id, 256) || value.created_at != null || value.updated_at != null || value.description != null || (value.workflow == null) !== (row.workflow_event == null)) return false;
    if (ciTime(value.started_at) && ciTime(value.completed_at) && Date.parse(value.completed_at) < Date.parse(value.started_at)) return false;
    if (value.workflow != null && (!bounded(workflow.node_id, 256) || !positive(workflow.run_number) || BigInt(text(workflow.run_number)) > 2147483647n || !positive(workflow.observed_attempt) || BigInt(text(workflow.observed_attempt)) > 2147483647n || !ciTime(workflow.created_at) || !ciTime(workflow.updated_at) || Date.parse(workflow.updated_at) < Date.parse(workflow.created_at))) return false;
    if ((workflow.suite_node_id == null) !== (workflow.commit_sha == null) || (workflow.suite_node_id != null && (workflow.suite_node_id !== value.suite_node_id || workflow.commit_sha !== row.commit_sha))) return false;
    return true;
  }
  return row.kind === "commit-status" && value.suite_node_id == null && value.started_at == null && value.completed_at == null && value.workflow == null && value.title == null && value.summary == null && value.text == null && ciTime(value.created_at) && ciTime(value.updated_at) && Date.parse(value.updated_at) >= Date.parse(value.created_at);
}
function validRollup(raw: unknown, expected: unknown, seen: Set<string>): boolean {
  const value = object(raw);
  if (raw == null || !sha(expected) || value.commit_sha !== expected || !Array.isArray(value.contexts) || value.contexts.length > 500 || value.total_count !== String(value.contexts.length)) return false;
  for (const raw of value.contexts) {
    const row = object(raw);
    if (!validEvidence(row) || !bounded(row.node_id, 256) || seen.has(row.node_id) || row.commit_sha !== expected || !bounded(row.name, 1024) || typeof row.required !== "boolean" || !bounded(row.native_status, 64)) return false;
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
  if (new TextEncoder().encode(JSON.stringify(value)).byteLength > 512 << 10) return false;
  const seen = new Set<string>();
  if (raw == null || !validPRRules(value.rules, item) || !validRollup(value.head, item.head_sha, seen) || !["MERGEABLE", "CONFLICTING", "UNKNOWN"].includes(text(value.native_mergeability)) || typeof value.in_merge_queue !== "boolean") return false;
  const merge = object(value.test_merge);
  const queue = object(value.merge_queue);
  if (value.merge_queue != null && (value.in_merge_queue !== true || !validCIQueue(queue, item) || (queue.rollup != null && !validRollup(queue.rollup, object(queue.entry).head_sha, new Set())))) return false;
  if (value.test_merge != null && (merge.commit_sha === item.head_sha || value.native_mergeability === "CONFLICTING" || !validRollup(value.test_merge, merge.commit_sha, seen))) return false;
  if (!states.has(text(result.state)) || !reasons.has(text(result.reason)) || !Array.isArray(result.requirements)) return false;
  let selected: Document;
  if (result.source === "head") {
    selected = object(value.head);
    if (result.evaluated_sha !== item.head_sha || items(merge.contexts).length > 0) return false;
  } else if (result.source === "test-merge") {
    selected = merge;
    if (result.evaluated_sha !== merge.commit_sha || items(merge.contexts).length === 0) return false;
  } else if (result.source === "merge-queue") {
    selected = object(queue.rollup);
    if (value.in_merge_queue !== true || !evaluableCIQueue(queue, item) || result.evaluated_sha !== object(queue.entry).head_sha) return false;
  } else if (result.source === "unknown") {
    return result.evaluated_sha == null && result.state === "unknown" && result.requirements.length === 0;
  } else return false;
  if ((result.source !== "merge-queue" && (value.in_merge_queue || value.native_mergeability === "UNKNOWN" || (value.native_mergeability === "MERGEABLE" && value.test_merge == null))) || item.state !== "open" || item.merged) return false;
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
      if (result.source === "merge-queue" && row.state !== "unknown" && !queueResultProvenance(context)) return false;
      if (result.source === "merge-queue" && row.state !== "unknown" && row.integration_id != null && (row.integration_id === "0" || object(context.application).id !== row.integration_id)) return false;
      used.add(id);
    }
  }
  return true;
}

export function CIOriginalEvidence({ row }: { row: Document }) {
  const value = object(row.evidence), workflow = object(value.workflow);
  return <details><summary>Original lifecycle and output</summary>
    <p>Result ID: {text(row.node_id)}{value.suite_node_id ? ` · Suite: ${text(value.suite_node_id)}` : ""}</p>
    <dl>{[["started_at", "Started"], ["completed_at", "Completed"], ["created_at", "Created"], ["updated_at", "Updated"]].map(([key, label]) => value[key] ? <div key={key}><dt>{label}</dt><dd><time dateTime={text(value[key])}>{text(value[key])}</time></dd></div> : null)}</dl>
    {value.workflow ? <p>Workflow {text(workflow.node_id)} · run {text(workflow.run_number)} · observed workflow attempt {text(workflow.observed_attempt)}. This aggregate attempt does not prove that each retained check ran again.</p> : null}
    {[["title", "Original title"], ["summary", "Original summary"], ["text", "Original text"], ["description", "Original description"]].map(([key, label]) => value[key] != null ? <div key={key}><h5>{label}</h5><pre>{text(value[key])}</pre></div> : null)}
  </details>;
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
export function PRCI({ value, historical = false }: { value: Document; historical?: boolean }) {
  const result = object(value.result), selected = selectedCIRollup(value), queue = object(value.merge_queue), entry = object(queue.entry);
  return <section aria-label="Required CI evaluation">
    <p role="status">{stateLabels.get(text(result.state))}</p>
    {result.evaluated_sha ? <p>Evaluated {result.source === "merge-queue" ? "merge queue entry" : result.source === "test-merge" ? "test merge" : "head"} commit: <code>{text(result.evaluated_sha)}</code></p> : null}
    {value.merge_queue ? <p>Queue {text(queue.node_id)} · entry {text(entry.node_id)} · position {text(entry.position)} · strategy {text(queue.strategy)} · base <code>{text(entry.base_sha) || "Unavailable"}</code>. Entry state {text(entry.state)} does not establish a failed check.</p> : null}
    {reasonLabels.has(text(result.reason)) ? <p>{reasonLabels.get(text(result.reason))}</p> : null}
    <p>{historical ? "This is the original retained evaluation of active rulesets." : "This is a current observation of active rulesets."} Missing, pending and unknown results do not establish passing CI. A later action requires fresh evidence.</p>
    {items(result.requirements).length ? <table><caption>Active ruleset CI requirements</caption><thead><tr><th scope="col">Requirement</th><th scope="col">App</th><th scope="col">Result</th></tr></thead><tbody>{items(result.requirements).map((raw, index) => { const row = object(raw); return <tr key={index}><th scope="row">{text(row.context)}<small> · ruleset {text(row.ruleset_id)}</small></th><td>{text(row.integration_id) || "No restriction reported"}</td><td>{stateLabels.get(text(row.state))}{reasonLabels.has(text(row.reason)) ? <small> · {reasonLabels.get(text(row.reason))}</small> : null}</td></tr>; })}</tbody></table> : null}
    {result.source !== "unknown" ? <details><summary>Inspected check and status results ({text(selected.total_count)})</summary><ul>{items(selected.contexts).map((raw) => { const row = object(raw); return <li key={text(row.node_id)}>{text(row.name)} · {text(row.kind)} · {text(row.native_status)}{row.native_conclusion ? ` / ${text(row.native_conclusion)}` : ""} · {row.required ? "GitHub required" : "GitHub optional"}{row.application ? ` · App ${text(object(row.application).id)}` : ""}{row.workflow_event ? ` · ${text(row.workflow_event)}` : ""}<CIOriginalEvidence row={row} /></li>; })}</ul></details> : null}
  </section>;
}

export function validCIContext(raw: unknown): boolean {
 const row = object(raw);
 return validRollup({ commit_sha: row.commit_sha, total_count: "1", contexts: [raw] }, row.commit_sha, new Set());
}
