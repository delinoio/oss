import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ActivityPRAttemptState, ActivityPRActorType, ActivityPRMode, EntityKind, ResourceQuery, type ActivityPRMetadata } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { readPRProblem, readPRProblemSet } from "./pr-problems";
import { readRemediationAttempt } from "./pr-remediation-model";
import { Problem } from "./ui";
import { date, uuid } from "./github-query-model";

const options = { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false };

function OriginalPRSource({ target, revision }: { target: ActivityPRMetadata; revision: bigint }) {
  const source = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROBLEM, id: target.sourceId }, options);
  const parent = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROBLEM, id: target.problemSetId }, options);
  const row = source.data?.resource, set = parent.data?.resource;
  const selection = { repositoryId: "", remoteRepositoryId: target.remoteRepositoryId, pullRequestId: target.pullRequestId, number: target.number };
  const validSet = set && readPRProblemSet(set, selection);
  const value = row && set && validSet ? readPRProblem(row, set, selection) ?? readRemediationAttempt(row, set) : undefined;
  // Verification is already an immutable metadata-only source. Do not display
  // its private proof commitment or infer it from a different record's state.
  const verification = document(row);
  const actor = object(verification.actor);
  const verified = target.verificationId === target.sourceId && row?.id === target.verificationId && row.kind === EntityKind.PROBLEM && row.schemaVersion === 1 && row.revision === 1n && !row.sessionId && !row.projectId && verification.version === 1 && verification.type === "pull-request-handling-verification" && verification.set_id === target.problemSetId && /^[a-f0-9]{64}$/.test(text(verification.proof_digest)) && uuid(actor.request_id) && date(actor.at) && (actor.actor_type === "owner" && actor.device_id == null || actor.actor_type === "client" && uuid(actor.device_id)) && Boolean(validSet);
  const references = verified ? verification.problems : value?.type === "pull-request-remediation-attempt" ? value.problems : value ? [{ id: row?.id, content_version: value.content_version }] : undefined;
  const sameReferences = Array.isArray(references) && references.length === target.problems.length && references.every((ref, index) => object(ref).id === target.problems[index]?.id && object(ref).content_version === target.problems[index]?.contentVersion);
  const valid = (value || verified) && sameReferences && row?.id === target.sourceId && row.revision >= revision;
  return <section aria-label="Original PR activity source">
    <Problem error={source.error || parent.error} />
    {source.isPending || parent.isPending ? <p role="status">Reading retained PR source…</p> : !valid ? <p role="alert">The original PR source is unavailable or inconsistent. The activity grants no handling or execution authority.</p> : <>
      <p>Recorded source revision: {revision.toString()} · Current source revision: {row!.revision.toString()}</p>
      {verified ? <p>Dedicated verification record: {target.verificationId}. Its original problem versions remain separate from attempt outcomes.</p> : value?.type === "pull-request-remediation-attempt" ? <p>Current attempt state: {text(value.state)}{value.outcome ? ` · Outcome: ${text(value.outcome)}` : ""}. A completed attempt does not establish verified handling.</p> : value ? <>
        <p>Original problem kind: {text(value.kind)} · Content version: <code>{text(value.content_version)}</code> · Current local handling: {text(value.state)}</p>
        {value.feedback ? <pre>{text(object(value.feedback).body) || "Empty original feedback."}</pre> : <p>Original evidence remains available in retained PR history.</p>}
      </> : null}
    </>}
  </section>;
}

export function ActivityPRSource({ target, revision }: { target: ActivityPRMetadata; revision: bigint }) {
  const [open, setOpen] = useState(false);
  return <div><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? "Close original PR record" : "Inspect original PR record"}</button>{open ? <OriginalPRSource target={target} revision={revision} /> : null}</div>;
}

export function ActivityPRDetails({ target, revision, active }: { target: ActivityPRMetadata; revision: bigint; active: boolean }) {
  return <>
    <p>{target.owner}/{target.name} #{target.number}</p>
    {target.attemptState !== ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_UNSPECIFIED ? <p>Attempt: {ActivityPRAttemptState[target.attemptState]?.replace("ACTIVITY_PR_ATTEMPT_STATE_", "").toLowerCase().replaceAll("_", " ")} · {ActivityPRMode[target.mode]?.replace("ACTIVITY_PR_MODE_", "").toLowerCase()}. Success does not establish verified handling.</p> : null}
    <p>Actor: {ActivityPRActorType[target.actorType]?.replace("ACTIVITY_PR_ACTOR_TYPE_", "").toLowerCase()}{target.deviceId ? ` ${target.deviceId}` : ""}</p>
    <details><summary>Original activity references</summary><p>Source {target.sourceId} · revision {revision.toString()} · request {target.requestId}</p>{target.problems.map(ref => <p key={ref.id}>Problem {ref.id} · version <code>{ref.contentVersion}</code></p>)}</details>
    {active ? <ActivityPRSource target={target} revision={revision} /> : null}
  </>;
}
