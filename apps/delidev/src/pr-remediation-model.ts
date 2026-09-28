import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { bounded, positive, uuid } from "./github-query-model";
import { utcTimestamp } from "./timestamp";

export enum AttemptState { Reserved = "reserved", Bound = "bound", Running = "running", Uncertain = "uncertain", Finished = "finished", Canceled = "canceled" }
const count = (value: unknown, max = 10000): value is number => typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= max;
const digest = (value: unknown) => typeof value === "string" && /^[a-f0-9]{64}$/.test(value);
const absent = (value: unknown) => value === undefined;
const audit = (value: Document) => uuid(value.request_id) && Boolean(utcTimestamp(value.at)) && (value.actor_type === "owner" ? absent(value.device_id) : value.actor_type === "client" && uuid(value.device_id));
export const activeAttempt = (value: unknown) => [AttemptState.Reserved, AttemptState.Bound, AttemptState.Running, AttemptState.Uncertain].includes(value as AttemptState);

export function readRemediationChain(value: unknown): Document | undefined {
  const chain = object(value), limit = object(chain.limit);
  if (!uuid(chain.id) || !count(chain.sequence) || !count(chain.automatic_attempts, chain.sequence) || !count(chain.resume_baseline, chain.automatic_attempts) || !absent(chain.active_attempt_id) && !uuid(chain.active_attempt_id)) return;
  if (chain.resume_baseline > 0 && absent(chain.last_resume) || !absent(chain.last_resume) && !audit(object(chain.last_resume))) return;
  if (!absent(chain.limit) && (!count(limit.limit, 100) || limit.limit < 1 || !count(limit.attempts) || limit.attempts < limit.limit || limit.attempts !== chain.automatic_attempts - chain.resume_baseline || !digest(limit.policy_digest) || !utcTimestamp(limit.at))) return;
  return chain;
}

function policyValid(value: Document): boolean {
  if (!["ci_failure", "review_feedback", "merge_conflict"].every(key => typeof value[key] === "boolean") || !["merge", "rebase"].includes(text(value.conflict_strategy)) || !["reuse", "dedicated"].includes(text(value.session_strategy)) || !count(value.attempt_limit, 100) || value.attempt_limit < 1 || ["agent_id", "machine_id"].some(key => !absent(value[key]) && !uuid(value[key]))) return false;
  if (absent(value.reviewer_selectors)) return true;
  if (!Array.isArray(value.reviewer_selectors) || value.reviewer_selectors.length > 100) return false;
  const seen = new Set<string>();
  for (const raw of value.reviewer_selectors) {
    const selector = object(raw), kind = text(selector.kind);
    if (kind === "minimum-permission" ? !["READ", "TRIAGE", "WRITE", "MAINTAIN", "ADMIN"].includes(text(selector.permission)) || !absent(selector.id) || !absent(selector.node_id) : !["user", "bot", "app"].includes(kind) || !positive(selector.id) || !bounded(selector.node_id, 256) || !absent(selector.permission)) return false;
    const key = `${kind}:${text(selector.id) || text(selector.permission)}`;
    if (seen.has(key)) return false;
    seen.add(key);
  }
  return true;
}

export function readRemediationAttempt(row: Resource, set: Resource): Document | undefined {
  const v = document(row), chain = readRemediationChain(document(set).remediation), reserved = object(v.reserved);
  if (!chain || row.kind !== EntityKind.PROBLEM || row.schemaVersion !== 1 || !uuid(row.id) || row.revision <= 0n || row.revision >= 1n << 63n || row.sessionId || row.projectId || row.documentJson.byteLength > 1 << 20 || v.version !== 1 || v.type !== "pull-request-remediation-attempt" || v.set_id !== set.id || v.chain_id !== chain.id || !count(v.sequence, Number(chain.sequence)) || v.sequence === 0 || !["manual", "automatic"].includes(text(v.mode)) || !Object.values(AttemptState).includes(v.state as AttemptState) || activeAttempt(v.state) !== (chain.active_attempt_id === row.id) || !audit(reserved) || !policyValid(object(v.policy))) return;
  if (!Array.isArray(v.problems) || v.problems.length < 1 || v.problems.length > 1001) return;
  const seen = new Set<string>();
  for (const raw of v.problems) { const ref = object(raw); if (!uuid(ref.id) || !digest(ref.content_version) || seen.has(text(ref.id))) return; seen.add(text(ref.id)); }
  const bound = !absent(v.session_id) && !absent(v.input_id), started = !absent(v.execution_id) && !absent(v.started_at);
  if (absent(v.session_id) !== absent(v.input_id) || bound && (!uuid(v.session_id) || !uuid(v.input_id) || !digest(v.input_digest)) || !bound && !absent(v.input_digest) || absent(v.execution_id) !== absent(v.started_at)) return;
  const reservedAt = utcTimestamp(reserved.at)!, startedAt = utcTimestamp(v.started_at), finishedAt = utcTimestamp(v.finished_at);
  if (started && (!bound || !uuid(v.execution_id) || !startedAt || startedAt < reservedAt) || !absent(v.finished_at) && (!finishedAt || finishedAt < reservedAt || startedAt && finishedAt < startedAt)) return;
  const rejected = v.outcome === "not-started" && uuid(v.startup_rejection_job_id);
  if (!absent(v.startup_rejection_job_id) && (v.state !== AttemptState.Finished || !rejected)) return;
  switch (v.state) {
    case AttemptState.Reserved: if (bound || started || !absent(v.finished_at) || !absent(v.outcome)) return; break;
    case AttemptState.Bound: if (!bound || started || !absent(v.finished_at) || !absent(v.outcome)) return; break;
    case AttemptState.Running: case AttemptState.Uncertain: if (!started || !absent(v.finished_at) || !absent(v.outcome)) return; break;
    case AttemptState.Finished: if (!started || !finishedAt || !rejected && !["succeeded", "failed", "stopped"].includes(text(v.outcome))) return; break;
    case AttemptState.Canceled: if (started || !finishedAt || !absent(v.outcome)) return; break;
  }
  return v;
}
