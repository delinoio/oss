// SPDX-License-Identifier: Apache-2.0
import { Timestamp } from "./timestamp-display";
import { LocalizedText, copy, useLocale } from "./localization";
import { items, object, text, type Document } from "./documents";
import { bounded, positive, sha } from "./github-query-model";
import { validPRRules, validWorkflowReference } from "./github-rules";
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

function validWorkflowRuns(raw: unknown, merge: Document): boolean {
  if (raw == null) return true;
  if (!Array.isArray(raw) || raw.length > 500 || !sha(merge.commit_sha)) return false;
  const seen = new Set<string>();
  for (const rawRun of raw) {
    const run = object(rawRun);
    if (!positive(run.run_id) || !positive(run.attempt) || BigInt(text(run.attempt)) > 2147483647n || !bounded(run.node_id, 256) || !bounded(run.suite_node_id, 256) || seen.has(run.node_id) || seen.has(run.suite_node_id) || run.commit_sha !== merge.commit_sha || !bounded(run.event, 100) || !bounded(run.native_status, 64) || (run.native_conclusion != null && !bounded(run.native_conclusion, 64)) || !Array.isArray(run.jobs) || run.jobs.length > 500) return false;
    seen.add(run.node_id); seen.add(run.suite_node_id);
    if (run.source != null && (!validWorkflowReference(run.source) || !sha(object(run.source).sha) || object(run.source).ref != null)) return false;
    if (run.source != null ? !bounded(run.source_file_node_id, 256) || !sha(run.source_blob_sha) : run.source_file_node_id != null || run.source_blob_sha != null) return false;
    const jobs = new Set<string>();
    for (const rawJob of run.jobs) {
      const job = object(rawJob);
      if (!bounded(job.node_id, 256) || jobs.has(job.node_id) || !bounded(job.native_status, 64) || (job.native_conclusion != null && !bounded(job.native_conclusion, 64))) return false;
      jobs.add(job.node_id);
    }
  }
  return true;
}

enum CIState {
  Unknown = "unknown",
  Missing = "missing",
  NotRequired = "not-required",
  Pending = "pending",
  NonFailing = "non-failing",
  TerminalFailure = "terminal-failure",
}
function workflowState(row: Document): CIState {
  if (row.native_status !== "COMPLETED") {
    return ["QUEUED", "IN_PROGRESS", "WAITING", "PENDING", "REQUESTED"].includes(text(row.native_status)) && row.native_conclusion == null ? CIState.Pending : CIState.Unknown;
  }
  if (["SUCCESS", "NEUTRAL", "SKIPPED"].includes(text(row.native_conclusion))) return CIState.NonFailing;
  return ["FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STALE", "STARTUP_FAILURE"].includes(text(row.native_conclusion)) ? CIState.TerminalFailure : CIState.Unknown;
}
const ciPriority = new Map([[CIState.Unknown, 5], [CIState.TerminalFailure, 4], [CIState.Missing, 3], [CIState.Pending, 2], [CIState.NonFailing, 1], [CIState.NotRequired, 0]]);
enum QueueCIReason {
  Observed = "observed",
  NoMatchingResult = "no-matching-result",
  AppUnverified = "app-unverified",
  WorkflowUnverified = "workflow-unverified",
  UnknownNativeResult = "unknown-native-result",
}
function queuedStatusAssessment(ref: Document, contexts: Map<string, Document>): { state: CIState; reason: QueueCIReason; ids: string[] } {
  const result = { state: CIState.Missing, reason: QueueCIReason.NoMatchingResult, ids: [] as string[] };
  if (ref.integration_id === "0") return { state: CIState.Unknown, reason: QueueCIReason.AppUnverified, ids: [] };
  const checkSources = new Map<string, number>();
  // Match the domain's complete ordered assessment, including unknown App or
  // workflow provenance and duplicate same-App runs. Referenced IDs alone cannot
  // prove a headline or hide another matching required result.
  for (const context of contexts.values()) {
    if (context.name !== ref.context || context.required !== true) continue;
    let state = context.kind === "commit-status"
      ? context.native_status === "SUCCESS" ? CIState.NonFailing : ["FAILURE", "ERROR"].includes(text(context.native_status)) ? CIState.TerminalFailure : ["PENDING", "EXPECTED"].includes(text(context.native_status)) ? CIState.Pending : CIState.Unknown
      : workflowState(context);
    let reason = QueueCIReason.Observed;
    const app = object(context.application);
    if (context.kind === "check-run") {
      if (context.application == null) { state = CIState.Unknown; reason = QueueCIReason.AppUnverified; }
      else if (app.id === "15368" && !queueResultProvenance(context)) { state = CIState.Unknown; reason = QueueCIReason.WorkflowUnverified; }
    }
    if (ref.integration_id != null) {
      if (context.application == null) { state = CIState.Unknown; reason = QueueCIReason.AppUnverified; }
      else if (app.id !== ref.integration_id) continue;
    }
    if (state === CIState.Unknown && reason === QueueCIReason.Observed) reason = QueueCIReason.UnknownNativeResult;
    if (context.kind === "check-run" && context.application != null) {
      const count = (checkSources.get(text(app.id)) ?? 0) + 1;
      checkSources.set(text(app.id), count);
      if (count > 1) { state = CIState.Unknown; reason = QueueCIReason.UnknownNativeResult; }
    }
    result.ids.push(text(context.node_id));
    if (result.state === CIState.Missing || ciPriority.get(state)! > ciPriority.get(result.state)!) { result.state = state; result.reason = reason; }
  }
  if (result.state === CIState.Missing) result.state = CIState.Unknown;
  return result;
}
function workflowAssessment(ref: Document, runs: Document[], contexts: Map<string, Document>, source: unknown, unsupported: boolean): { state: CIState; reason: string; ids: string[] } {
  const unknown = { state: CIState.Unknown, reason: "workflow-unverified", ids: [] as string[] };
  if (!sha(ref.sha) || unsupported || source !== "test-merge" || runs.some((run) => run.source == null)) return unknown;
  const matching = runs.filter((run) => object(run.source).repository_id === ref.repository_id && object(run.source).path === ref.path);
  const run = matching[0];
  if (matching.length !== 1 || object(run.source).sha !== ref.sha || run.event !== "pull_request" || items(run.jobs).length === 0) return unknown;
  let state = CIState.NonFailing, pending = false;
  const ids: string[] = [];
  for (const raw of items(run.jobs)) {
    const job = object(raw), context = contexts.get(text(job.node_id));
    const evidence = object(context?.evidence), workflow = object(evidence.workflow);
    if (!context || !context.required || context.kind !== "check-run" || object(context.application).id !== "15368" || evidence.suite_node_id !== run.suite_node_id || workflow.node_id !== run.node_id || workflow.observed_attempt !== run.attempt || context.workflow_event !== run.event || context.native_status !== job.native_status || context.native_conclusion !== job.native_conclusion) return unknown;
    const current = workflowState(job);
    pending ||= current === CIState.Pending;
    if (ciPriority.get(current)! > ciPriority.get(state)!) state = current;
    ids.push(text(job.node_id));
  }
  // A running aggregate cannot promote retained failures to a current failure;
  // terminal aggregates must agree with every attributed current-attempt job.
  const aggregate = workflowState(run);
  if (aggregate === CIState.Pending && state !== CIState.Unknown) return { state: CIState.Pending, reason: "observed", ids };
  if (aggregate !== state || pending) return unknown;
  return { state, reason: state === CIState.Unknown ? "unknown-native-result" : "observed", ids };
}

const nonCIRules = new Set(["creation", "update", "deletion", "required_linear_history", "required_deployments", "required_signatures", "pull_request", "non_fast_forward", "commit_message_pattern", "commit_author_email_pattern", "committer_email_pattern", "branch_name_pattern", "tag_name_pattern", "file_path_restriction", "max_file_path_length", "file_extension_restriction", "max_file_size"]);
function validAggregate(rules: unknown[], result: Document): boolean {
  let state = CIState.NotRequired, reason = "observed", index = 0;
  const unsupported = () => { state = CIState.Unknown; reason = "unsupported-rule"; };
  // Preserve Go's rule order, unknown-policy precedence and equal-priority
  // reason selection. A stale headline cannot override validated workflow rows.
  for (const raw of rules) {
    const rule = object(raw);
    if (rule.source_kind === "unknown") unsupported();
    if (rule.type === "merge_queue") {
      if (result.source !== "merge-queue") unsupported();
      continue;
    }
    if (rule.type !== "required_status_checks" && rule.type !== "workflows") {
      if (!nonCIRules.has(text(rule.type))) unsupported();
      continue;
    }
    const required = rule.type === "workflows" ? rule.required_workflows : rule.required_checks;
    if (required == null) { unsupported(); continue; }
    if (object(required).unknown_parameters === true) unsupported();
    const count = items(rule.type === "workflows" ? object(required).workflows : object(required).checks).length;
    for (let offset = 0; offset < count; offset++) {
      const row = object(items(result.requirements)[index++]);
      const current = text(row.state) as CIState;
      if (ciPriority.get(current)! > ciPriority.get(state)!) { state = current; reason = text(row.reason); }
    }
  }
  return result.state === state && result.reason === reason;
}

export function validPRCI(raw: unknown, item: Document): boolean {
  const value = object(raw), result = object(value.result);
  if (new TextEncoder().encode(JSON.stringify(value)).byteLength > 512 << 10) return false;
  const seen = new Set<string>();
  if (raw == null || !validPRRules(value.rules, item) || !validRollup(value.head, item.head_sha, seen) || !["MERGEABLE", "CONFLICTING", "UNKNOWN"].includes(text(value.native_mergeability)) || typeof value.in_merge_queue !== "boolean") return false;
  const merge = object(value.test_merge);
  const queue = object(value.merge_queue);
  if (value.merge_queue != null && (value.in_merge_queue !== true || !validCIQueue(queue, item) || (queue.rollup != null && !validRollup(queue.rollup, object(queue.entry).head_sha, new Set())))) return false;
  if (!validWorkflowRuns(value.workflow_runs, merge)) return false;
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
    const closed = item.state !== "open" || item.merged !== false;
    const unavailable = value.in_merge_queue ? !evaluableCIQueue(queue, item) : value.native_mergeability === "UNKNOWN" || (value.native_mergeability === "MERGEABLE" && value.test_merge == null);
    return (closed || unavailable) && result.evaluated_sha == null && result.state === "unknown" && result.reason === (closed ? "closed-pr" : "commit-unverified") && result.requirements.length === 0;
  } else return false;
  if ((result.source !== "merge-queue" && (value.in_merge_queue || value.native_mergeability === "UNKNOWN" || (value.native_mergeability === "MERGEABLE" && value.test_merge == null))) || item.state !== "open" || item.merged) return false;
  const expected: Document[] = items(object(value.rules).rules).flatMap((raw) => {
    const rule = object(raw);
    if (rule.type === "required_status_checks") return items(object(rule.required_checks).checks).map((raw) => ({ ...object(raw), ruleset_id: rule.ruleset_id }));
    return rule.type === "workflows" ? items(object(rule.required_workflows).workflows).map((raw) => ({ context: object(raw).path, workflow: raw, ruleset_id: rule.ruleset_id, unsupported: object(rule.required_workflows).unknown_parameters === true })) : [];
  });
  if (result.requirements.length !== expected.length) return false;
  const contexts = new Map(items(selected.contexts).map((raw) => { const row = object(raw); return [text(row.node_id), row] as const; }));
  const workflowRuns = items(value.workflow_runs).map(object);
  const runsByID = new Map(workflowRuns.map((run) => [text(run.node_id), run] as const));
  for (const [index, raw] of result.requirements.entries()) {
    const row = object(raw), requirement = expected[index];
    if (row.ruleset_id !== requirement.ruleset_id || row.context !== requirement.context || row.integration_id !== requirement.integration_id || !states.has(text(row.state)) || !reasons.has(text(row.reason)) || !Array.isArray(row.result_node_ids) || row.result_node_ids.length > 500) return false;
    if ((row.workflow == null) !== (requirement.workflow == null)) return false;
    if (row.workflow != null) {
      if (!validWorkflowReference(row.workflow)) return false;
      for (const key of ["repository_id", "path", "sha", "ref"]) if (object(row.workflow)[key] !== object(requirement.workflow)[key]) return false;
      const expected = workflowAssessment(object(row.workflow), workflowRuns, contexts, result.source, requirement.unsupported === true);
      if (row.state !== expected.state || row.reason !== expected.reason || row.result_node_ids.length !== expected.ids.length || row.result_node_ids.some((id, index) => id !== expected.ids[index])) return false;
    } else if (result.source === "merge-queue") {
      const expected = queuedStatusAssessment(requirement, contexts);
      if (row.state !== expected.state || row.reason !== expected.reason || row.result_node_ids.length !== expected.ids.length || row.result_node_ids.some((id, index) => id !== expected.ids[index])) return false;
    }
    const used = new Set<string>();
    for (const id of row.result_node_ids) {
      if (typeof id !== "string" || used.has(id)) return false;
      const context = contexts.get(id);
      if (!context || context.required !== true || (row.workflow == null && context.name !== row.context)) return false;
      if (result.source === "merge-queue" && row.state !== "unknown" && !queueResultProvenance(context)) return false;
      if (result.source === "merge-queue" && row.state !== "unknown" && row.integration_id != null && (row.integration_id === "0" || object(context.application).id !== row.integration_id)) return false;
      if (row.workflow != null) {
        const evidence = object(context.evidence), workflow = object(evidence.workflow);
        const run = runsByID.get(text(workflow.node_id));
        const source = object(run?.source), ref = object(row.workflow);
        const job = items(run?.jobs).map(object).find((job) => job.node_id === id);
        if (result.source !== "test-merge" || !run || run.event !== "pull_request" || source.repository_id !== ref.repository_id || source.path !== ref.path || source.sha !== ref.sha || evidence.suite_node_id !== run.suite_node_id || workflow.observed_attempt !== run.attempt || object(context.application).id !== "15368" || context.workflow_event !== run.event || !job || context.native_status !== job.native_status || context.native_conclusion !== job.native_conclusion) return false;
      }
      used.add(id);
    }
  }
  return validAggregate(items(object(value.rules).rules), result);
}

export function CIOriginalEvidence({ row }: { row: Document }) {
  useLocale();
  const value = object(row.evidence), workflow = object(value.workflow);
  return <details><summary>{copy("github-ci.originalLifecycleAndOutput_25753b")}</summary>
    <p><LocalizedText id="github-ci.resultId_d45e9f" components={{ s0: <>{text(row.node_id)}</>, s1: <>{value.suite_node_id ? copy("github-ci.suite_e79a3a", { v0: text(value.suite_node_id) }) : ""}</> }} /></p>
    <dl>{[["started_at", copy("github-ci.extra.ecbc89cd37a0")], ["completed_at", copy("github-ci.extra.22a970d2e5b1")], ["created_at", copy("github-ci.extra.d70b9e24bca2")], ["updated_at", copy("github-ci.extra.3a5ecca188c0")]].map(([key, label]) => value[key] ? <div key={key}><dt>{label}</dt><dd><Timestamp value={text(value[key])} /></dd></div> : null)}</dl>
    {value.workflow ? <p><LocalizedText id="github-ci.workflowRunObservedWorkflowAttemptThis_dbe9db" components={{ s0: <>{text(workflow.node_id)}</>, s1: <>{text(workflow.run_number)}</>, s2: <>{text(workflow.observed_attempt)}</> }} /></p> : null}
    {[["title", copy("github-ci.extra.4c198f5da765")], ["summary", copy("github-ci.extra.8696494cf17f")], ["text", copy("github-ci.extra.89420fdb493e")], ["description", copy("github-ci.extra.00ffd18a5ad0")]].map(([key, label]) => value[key] != null ? <div key={key}><h5>{label}</h5><pre>{text(value[key])}</pre></div> : null)}
  </details>;
}

const stateLabels = new Map([["unknown", "Unknown"], ["missing", "Required result missing"], ["pending", "Required checks pending"], ["non-failing", "Observed required results are non-failing"], ["terminal-failure", "Terminal required CI failure"], ["not-required", "No active ruleset CI requirements"]]);
const reasonLabels = new Map([
  ["app-unverified", "The required App could not be verified."],
  ["workflow-unverified", "The original workflow source, event or current attempt could not be verified."],
  ["unknown-native-result", "GitHub reported an unknown or incomplete result."],
  ["unsupported-rule", "A rule needs additional evaluation before automatic CI handling."],
  ["commit-unverified", "GitHub's evaluated commit could not be verified, including merge-queue evaluation."],
  ["closed-pr", "The PR is closed; these results cannot start automatic handling."],
  ["no-matching-result", "No matching required result was observed."],
]);
export function PRCI({ value, historical = false }: { value: Document; historical?: boolean }) {
  useLocale();
  const result = object(value.result), selected = selectedCIRollup(value), queue = object(value.merge_queue), entry = object(queue.entry);
  return <section aria-label={copy("github-ci.requiredCiEvaluation_73f44c")}>
    <p role="status">{stateLabels.get(text(result.state))}</p>
    {result.evaluated_sha ? <p><LocalizedText id="github-ci.evaluatedCommit_47f166" components={{ s0: <>{result.source === "merge-queue" ? copy("github-ci.mergeQueueEntry_9183ca") : result.source === "test-merge" ? copy("github-ci.testMerge_7e7b58") : copy("github-ci.head_9f2e6d")}</>, s1: <code>{text(result.evaluated_sha)}</code> }} /></p> : null}
    {value.merge_queue ? <p><LocalizedText id="github-ci.queueEntryPositionStrategyBaseEntry_69a534" components={{ s0: <>{text(queue.node_id)}</>, s1: <>{text(entry.node_id)}</>, s2: <>{text(entry.position)}</>, s3: <>{text(queue.strategy)}</>, s4: <code>{text(entry.base_sha) || copy("github-ci.extra.ca1844969742")}</code>, s5: <>{text(entry.state)}</> }} /></p> : null}
    {reasonLabels.has(text(result.reason)) ? <p>{reasonLabels.get(text(result.reason))}</p> : null}
    <p><LocalizedText id="github-ci.missingPendingAndUnknownResultsDo_e03994" components={{ s0: <>{historical ? copy("github-ci.thisIsTheOriginalRetainedEvaluation_39bd1d") : copy("github-ci.thisIsACurrentObservationOf_00d15a")}</> }} /></p>
    {items(value.workflow_runs).length ? <details><summary>{copy("github-ci.originalRequiredWorkflowEvidence_e011ee")}</summary><ul>{items(value.workflow_runs).map((raw) => { const run = object(raw), source = object(run.source); return <li key={text(run.node_id)}><LocalizedText id="github-ci.runAttempt_e1cfdf" components={{ s0: <>{text(run.run_id)}</>, s1: <>{text(run.attempt)}</>, s2: <>{text(run.event)}</>, s3: <>{text(run.native_status)}</>, s4: <>{source.repository_id ? <><LocalizedText id="github-ci.sourceRepository_e6b05f" components={{ s0: <>{text(source.repository_id)}</>, s1: <code>{text(source.path)}</code>, s2: <code>{text(source.sha)}</code> }} /></> : copy("github-ci.sourceUnavailable_5a3a76")}</> }} /></li>; })}</ul></details> : null}
    {items(result.requirements).length ? <table><caption>{copy("github-ci.activeRulesetCiRequirements_6af302")}</caption><thead><tr><th scope={"col"}>{copy("github-ci.requirement_f5f569")}</th><th scope={"col"}>{copy("github-ci.sourceOrApp_5d45b9")}</th><th scope={"col"}>{copy("github-ci.result_6e7d50")}</th></tr></thead><tbody>{items(result.requirements).map((raw, index) => { const row = object(raw); return <tr key={index}><th scope={"row"}>{text(row.context)}<small><LocalizedText id="github-ci.ruleset_ec6fd7" components={{ s0: <>{text(row.ruleset_id)}</> }} /></small></th><td>{row.workflow ? copy("github-ci.repository_371a60", { v0: text(object(row.workflow).repository_id) }) : text(row.integration_id) || copy("github-ci.extra.46167b516d99")}</td><td>{stateLabels.get(text(row.state))}{reasonLabels.has(text(row.reason)) ? <small> · {reasonLabels.get(text(row.reason))}</small> : null}</td></tr>; })}</tbody></table> : null}
    {result.source !== "unknown" ? <details><summary><LocalizedText id="github-ci.inspectedCheckAndStatusResults_719a67" components={{ s0: <>{text(selected.total_count)}</> }} /></summary><ul>{items(selected.contexts).map((raw) => { const row = object(raw); return <li key={text(row.node_id)}>{text(row.name)} · {text(row.kind)} · {text(row.native_status)}{row.native_conclusion ? copy("github-ci.message_af9903", { v0: text(row.native_conclusion) }) : ""} · {row.required ? copy("github-ci.githubRequired_303be0") : copy("github-ci.githubOptional_5148ec")}{row.application ? copy("github-ci.app_979677", { v0: text(object(row.application).id) }) : ""}{row.workflow_event ? copy("github-ci.message_2fa20b", { v0: text(row.workflow_event) }) : ""}<CIOriginalEvidence row={row} /></li>; })}</ul></details> : null}
  </section>;
}

export function validCIContext(raw: unknown): boolean {
 const row = object(raw);
 return validRollup({ commit_sha: row.commit_sha, total_count: "1", contexts: [raw] }, row.commit_sha, new Set());
}
