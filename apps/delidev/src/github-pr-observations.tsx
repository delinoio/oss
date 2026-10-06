import { LocalizedText, copy, useLocale } from "./localization";
import { items, object, text, type Document } from "./documents";
import { QueryOperation, type GitHubQuery, positive, bounded, date, actorValid } from "./github-query-model";

import { PRReviewers, validPRReviewers } from "./github-reviewers";
import { PRFeedback, validPRFeedback } from "./github-feedback";
import { PRCI, validPRCI } from "./github-ci";
import { PRRules, validPRRules } from "./github-rules";

const count = (value: unknown): value is string => typeof value === "string" && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n;
const checkStatuses = new Map([["queued", "queued"], ["in_progress", "in-progress"], ["completed", "completed"]]);
const checkStatus = (value: string) => checkStatuses.get(value) ?? "unknown";
const conclusions = new Map([["success", "success"], ["failure", "failure"], ["neutral", "neutral"], ["cancelled", "cancelled"], ["skipped", "skipped"], ["timed_out", "timed-out"], ["action_required", "action-required"], ["stale", "stale"], ["startup_failure", "startup-failure"]]);
const conclusion = (value: string) => conclusions.get(value) ?? "unknown";
const statusState = (value: string) => ["pending", "success", "failure", "error"].includes(value) ? value : "unknown";
export const isObservation = (query: GitHubQuery) => [QueryOperation.Diff, QueryOperation.Checks, QueryOperation.Statuses, QueryOperation.Rules, QueryOperation.CI, QueryOperation.Feedback, QueryOperation.Reviewers].includes(query.operation);
export function validPRObservation(result: Document, query: GitHubQuery, item: Document) {
  if (query.operation === QueryOperation.Reviewers) return query.kind === "pull-request" && result.diff == null && result.checks == null && result.statuses == null && result.rules == null && result.ci == null && result.feedback == null && validPRReviewers(result.reviewers, item);
  if (result.reviewers != null) return false;
  if (query.operation === QueryOperation.Feedback) return query.kind === "pull-request" && result.diff == null && result.checks == null && result.statuses == null && result.rules == null && result.ci == null && validPRFeedback(result.feedback, item);
  if (result.feedback != null) return false;
  const diff = object(result.diff), checks = object(result.checks), statuses = object(result.statuses);
  if (query.operation === QueryOperation.CI) return query.kind === "pull-request" && result.diff == null && result.checks == null && result.statuses == null && result.rules == null && validPRCI(result.ci, { ...item, repository_node_id: object(result.repository).node_id });
  if (result.ci != null) return false;
  if (query.operation === QueryOperation.Rules) return query.kind === "pull-request" && result.diff == null && result.checks == null && result.statuses == null && validPRRules(result.rules, item);
  if (result.rules != null) return false;
  if (!isObservation(query)) return result.diff == null && result.checks == null && result.statuses == null;
  if (query.kind !== "pull-request") return false;
  if (query.operation === QueryOperation.Diff) return result.diff != null && result.checks == null && result.statuses == null && bounded(diff.patch, 512 << 10, false) && /^[a-f0-9]{64}$/.test(text(diff.digest)) && diff.head_sha === item.head_sha && diff.base_sha === item.base_sha;
  if (result.diff != null) return false;
  if (query.operation === QueryOperation.Checks) {
    if (result.checks == null || result.statuses != null || checks.head_sha !== item.head_sha || checks.filter !== "latest" || !count(checks.total_count) || !Array.isArray(checks.runs) || checks.runs.length > (query.page_size ?? 0) || BigInt(checks.total_count) < BigInt(checks.runs.length)) return false;
    const seen = new Set<string>();
    for (const raw of checks.runs) {
      const run = object(raw), app = object(run.application);
      if (!positive(run.id) || seen.has(run.id) || !bounded(run.node_id, 256) || !bounded(run.name, 1024) || run.head_sha !== item.head_sha || !bounded(run.native_status, 64) || run.status !== checkStatus(run.native_status)) return false;
      seen.add(run.id);
      if ((run.conclusion == null) !== (run.native_conclusion == null) || (run.native_conclusion != null && (!bounded(run.native_conclusion, 64) || run.conclusion !== conclusion(run.native_conclusion)))) return false;
      if ((run.started_at != null && !date(run.started_at)) || (run.completed_at != null && !date(run.completed_at)) || (run.started_at != null && run.completed_at != null && Date.parse(text(run.completed_at)) < Date.parse(text(run.started_at)))) return false;
      if (run.application != null && (!positive(app.id) || !bounded(app.node_id, 256) || !bounded(app.slug, 100) || /[\r\n]/.test(app.slug))) return false;
    }
    return true;
  }
  if (query.operation !== QueryOperation.Statuses || result.statuses == null || result.checks != null || statuses.head_sha !== item.head_sha || !bounded(statuses.native_state, 64) || statuses.state !== statusState(statuses.native_state) || !count(statuses.total_count) || !Array.isArray(statuses.contexts) || statuses.contexts.length > (query.page_size ?? 0) || BigInt(statuses.total_count) < BigInt(statuses.contexts.length)) return false;
  if (statuses.total_count === "0" && !["pending", "unknown"].includes(text(statuses.state))) return false;
  const seen = new Set<string>();
  for (const raw of statuses.contexts) {
    const status = object(raw);
    if (!positive(status.id) || seen.has(status.id) || !bounded(status.node_id, 256) || !bounded(status.context, 1024) || !bounded(status.native_state, 64) || status.state !== statusState(status.native_state) || !date(status.created_at) || !date(status.updated_at) || Date.parse(text(status.updated_at)) < Date.parse(text(status.created_at)) || (status.description != null && !bounded(status.description, 4096, false)) || (status.creator != null && !actorValid(status.creator))) return false;
    seen.add(status.id);
  }
  return true;
}
export function PRObservation({ value, query, item }: { value: Document; query: GitHubQuery; item: Document }) {
  useLocale();
  if (!isObservation(query)) return null;
  if (query.operation === QueryOperation.Reviewers) return <PRReviewers value={object(value.reviewers)} />;
  if (query.operation === QueryOperation.Feedback) return <PRFeedback value={object(value.feedback)} />;
  if (query.operation === QueryOperation.CI) return <PRCI value={object(value.ci)} />;
  if (query.operation === QueryOperation.Rules) return <PRRules value={object(value.rules)} />;
  const diff = object(value.diff), checks = object(value.checks), statuses = object(value.statuses);
  return <section aria-label={copy("github-pr-observations.prHeadObservation_fb3487")}><p><LocalizedText id="github-pr-observations.observedPrHead_875e1e" components={{ s0: <code>{text(item.head_sha)}</code> }} /></p><p>{copy("github-pr-observations.theseResultsDoNotEvaluateActive_0543cb")}</p>
    {query.operation === QueryOperation.Diff ? <><p><LocalizedText id="github-pr-observations.immutableBaseSha256_8e86c8" components={{ s0: <code>{text(item.base_sha)}</code>, s1: <code>{text(diff.digest)}</code> }} /></p><pre>{text(diff.patch) || copy("github-pr-observations.extra.b73bbd676d6a")}</pre></> : null}
    {query.operation === QueryOperation.Checks ? <><p><LocalizedText id="github-pr-observations.githubLatestCheckFilterReportedCheck_76a03d" components={{ s0: <>{text(checks.total_count)}</> }} /></p>{items(checks.runs).length ? <table><caption>{copy("github-pr-observations.checkRunsForTheObservedHead_7e63cd")}</caption><thead><tr><th scope={"col"}>{copy("github-pr-observations.check_9d6084")}</th><th scope={"col"}>{copy("github-pr-observations.status_920e41")}</th><th scope={"col"}>{copy("github-pr-observations.conclusion_89db2a")}</th><th scope={"col"}>{copy("github-pr-observations.application_e7ad52")}</th></tr></thead><tbody>{items(checks.runs).map((raw) => { const run = object(raw), app = object(run.application); return <tr key={text(run.id)}><th scope={"row"}>{text(run.name)}<small> · {text(run.id)}</small></th><td>{text(run.native_status)}{run.status === "unknown" ? copy("github-pr-observations.unknown_92d909") : ""}</td><td>{text(run.native_conclusion) || copy("github-pr-observations.extra.adadface0166")}{run.conclusion === "unknown" ? copy("github-pr-observations.unknown_92d909") : ""}</td><td>{text(app.slug) ? copy("github-pr-observations.app_9fefc2", { v0: text(app.slug), v1: text(app.id) }) : copy("github-pr-observations.appIdentityUnavailable_5d83a1")}</td></tr>; })}</tbody></table> : <p>{copy("github-pr-observations.noCheckRunsOnThisReturned_46ed3f")}</p>}</> : null}
    {query.operation === QueryOperation.Statuses ? <><p><LocalizedText id="github-pr-observations.githubCombinedCommitStatusReportedContexts_ffd610" components={{ s0: <>{text(statuses.native_state)}</>, s1: <>{statuses.state === "unknown" ? copy("github-pr-observations.unknown_92d909") : ""}</>, s2: <>{text(statuses.total_count)}</> }} /></p>{items(statuses.contexts).length ? <table><caption>{copy("github-pr-observations.commitStatusContextsForTheObserved_bc40a8")}</caption><thead><tr><th scope={"col"}>{copy("github-pr-observations.context_a6e600")}</th><th scope={"col"}>{copy("github-pr-observations.state_a3b50c")}</th><th scope={"col"}>{copy("github-pr-observations.description_526e00")}</th><th scope={"col"}>{copy("github-pr-observations.creator_88447b")}</th></tr></thead><tbody>{items(statuses.contexts).map((raw) => { const status = object(raw); return <tr key={text(status.id)}><th scope={"row"}>{text(status.context)}</th><td>{text(status.native_state)}{status.state === "unknown" ? copy("github-pr-observations.unknown_92d909") : ""}</td><td>{text(status.description) || copy("github-pr-observations.extra.adadface0166")}</td><td>{text(object(status.creator).login) || copy("github-pr-observations.extra.ca1844969742")}</td></tr>; })}</tbody></table> : <p>{copy("github-pr-observations.noCommitStatusContextsOnThis_b459e0")}</p>}</> : null}
  </section>;
}
