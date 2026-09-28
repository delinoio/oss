import { items, object, text, type Document } from "./documents";
import { QueryOperation, type GitHubQuery, positive, bounded, date, actorValid } from "./github-query-model";

import { PRRules, validPRRules } from "./github-rules";

const count = (value: unknown): value is string => typeof value === "string" && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n;
const checkStatuses = new Map([["queued", "queued"], ["in_progress", "in-progress"], ["completed", "completed"]]);
const checkStatus = (value: string) => checkStatuses.get(value) ?? "unknown";
const conclusions = new Map([["success", "success"], ["failure", "failure"], ["neutral", "neutral"], ["cancelled", "cancelled"], ["skipped", "skipped"], ["timed_out", "timed-out"], ["action_required", "action-required"], ["stale", "stale"], ["startup_failure", "startup-failure"]]);
const conclusion = (value: string) => conclusions.get(value) ?? "unknown";
const statusState = (value: string) => ["pending", "success", "failure", "error"].includes(value) ? value : "unknown";
export const isObservation = (query: GitHubQuery) => [QueryOperation.Diff, QueryOperation.Checks, QueryOperation.Statuses, QueryOperation.Rules].includes(query.operation);
export function validPRObservation(result: Document, query: GitHubQuery, item: Document) {
  const diff = object(result.diff), checks = object(result.checks), statuses = object(result.statuses);
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
  if (!isObservation(query)) return null;
  if (query.operation === QueryOperation.Rules) return <PRRules value={object(value.rules)} />;
  const diff = object(value.diff), checks = object(value.checks), statuses = object(value.statuses);
  return <section aria-label="PR head observation"><p>Observed PR head: <code>{text(item.head_sha)}</code></p><p>These results do not evaluate active rulesets or the commit GitHub evaluates for required CI. Missing, pending or unknown results do not establish passing CI.</p>
    {query.operation === QueryOperation.Diff ? <><p>Immutable base: <code>{text(item.base_sha)}</code> · SHA-256: <code>{text(diff.digest)}</code></p><pre>{text(diff.patch) || "GitHub returned an empty comparison diff."}</pre></> : null}
    {query.operation === QueryOperation.Checks ? <><p>GitHub latest-check filter · {text(checks.total_count)} reported check runs</p>{items(checks.runs).length ? <table><caption>Check runs for the observed head</caption><thead><tr><th scope="col">Check</th><th scope="col">Status</th><th scope="col">Conclusion</th><th scope="col">Application</th></tr></thead><tbody>{items(checks.runs).map((raw) => { const run = object(raw), app = object(run.application); return <tr key={text(run.id)}><th scope="row">{text(run.name)}<small> · {text(run.id)}</small></th><td>{text(run.native_status)}{run.status === "unknown" ? " (unknown)" : ""}</td><td>{text(run.native_conclusion) || "Not reported"}{run.conclusion === "unknown" ? " (unknown)" : ""}</td><td>{text(app.slug) ? `${text(app.slug)} · App ${text(app.id)}` : "App identity unavailable"}</td></tr>; })}</tbody></table> : <p>No check runs on this returned page.</p>}</> : null}
    {query.operation === QueryOperation.Statuses ? <><p>GitHub combined commit status: {text(statuses.native_state)}{statuses.state === "unknown" ? " (unknown)" : ""} · {text(statuses.total_count)} reported contexts</p>{items(statuses.contexts).length ? <table><caption>Commit status contexts for the observed head</caption><thead><tr><th scope="col">Context</th><th scope="col">State</th><th scope="col">Description</th><th scope="col">Creator</th></tr></thead><tbody>{items(statuses.contexts).map((raw) => { const status = object(raw); return <tr key={text(status.id)}><th scope="row">{text(status.context)}</th><td>{text(status.native_state)}{status.state === "unknown" ? " (unknown)" : ""}</td><td>{text(status.description) || "Not reported"}</td><td>{text(object(status.creator).login) || "Unavailable"}</td></tr>; })}</tbody></table> : <p>No commit status contexts on this returned page.</p>}</> : null}
  </section>;
}
