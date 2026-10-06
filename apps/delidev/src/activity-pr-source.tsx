import { LocalizedText, copy, useLocale } from "./localization";
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
  useLocale();
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
  return <section aria-label={copy("activity-pr-source.originalPrActivitySource_39ade6")}>
    <Problem error={source.error || parent.error} />
    {source.isPending || parent.isPending ? <p role="status">{copy("activity-pr-source.readingRetainedPrSource_cfa2ca")}</p> : !valid ? <p role="alert">{copy("activity-pr-source.theOriginalPrSourceIsUnavailable_1c6ae0")}</p> : <>
      <p><LocalizedText id="activity-pr-source.recordedSourceRevisionCurrentSourceRevision_df8461" components={{ s0: <>{revision.toString()}</>, s1: <>{row!.revision.toString()}</> }} /></p>
      {verified ? <p><LocalizedText id="activity-pr-source.dedicatedVerificationRecordItsOriginalProblem_17b711" components={{ s0: <>{target.verificationId}</> }} /></p> : value?.type === "pull-request-remediation-attempt" ? <p><LocalizedText id="activity-pr-source.currentAttemptStateACompletedAttempt_6b3175" components={{ s0: <>{text(value.state)}</>, s1: <>{value.outcome ? copy("activity-pr-source.outcome_f04e83", { v0: text(value.outcome) }) : ""}</> }} /></p> : value ? <>
        <p><LocalizedText id="activity-pr-source.originalProblemKindContentVersionCurrent_0adac4" components={{ s0: <>{text(value.kind)}</>, s1: <code>{text(value.content_version)}</code>, s2: <>{text(value.state)}</> }} /></p>
        {value.feedback ? <pre>{text(object(value.feedback).body) || "Empty original feedback."}</pre> : <p>{copy("activity-pr-source.originalEvidenceRemainsAvailableInRetained_0e5bc5")}</p>}
      </> : null}
    </>}
  </section>;
}

export function ActivityPRSource({ target, revision }: { target: ActivityPRMetadata; revision: bigint }) {
  useLocale();
  const [open, setOpen] = useState(false);
  return <div><button aria-expanded={open} onClick={() => setOpen(!open)}>{open ? copy("activity-pr-source.closeOriginalPrRecord_963c00") : copy("activity-pr-source.inspectOriginalPrRecord_fa92e2")}</button>{open ? <OriginalPRSource target={target} revision={revision} /> : null}</div>;
}

export function ActivityPRDetails({ target, revision, active }: { target: ActivityPRMetadata; revision: bigint; active: boolean }) {
  useLocale();
  return <>
    <p>{target.owner}/{target.name} #{target.number}</p>
    {target.attemptState !== ActivityPRAttemptState.ACTIVITY_PR_ATTEMPT_STATE_UNSPECIFIED ? <p><LocalizedText id="activity-pr-source.attemptSuccessDoesNotEstablishVerified_f0764b" components={{ s0: <>{ActivityPRAttemptState[target.attemptState]?.replace("ACTIVITY_PR_ATTEMPT_STATE_", "").toLowerCase().replaceAll("_", " ")}</>, s1: <>{ActivityPRMode[target.mode]?.replace("ACTIVITY_PR_MODE_", "").toLowerCase()}</> }} /></p> : null}
    <p><LocalizedText id="activity-pr-source.actor_3b75fc" components={{ s0: <>{ActivityPRActorType[target.actorType]?.replace("ACTIVITY_PR_ACTOR_TYPE_", "").toLowerCase()}</>, s1: <>{target.deviceId ? copy("activity-pr-source.message_a4e9a4", { v0: target.deviceId }) : ""}</> }} /></p>
    <details><summary>{copy("activity-pr-source.originalActivityReferences_bae436")}</summary><p><LocalizedText id="activity-pr-source.sourceRevisionRequest_b783ad" components={{ s0: <>{target.sourceId}</>, s1: <>{revision.toString()}</>, s2: <>{target.requestId}</> }} /></p>{target.problems.map(ref => <p key={ref.id}><LocalizedText id="activity-pr-source.problemVersion_f62c8e" components={{ s0: <>{ref.id}</>, s1: <code>{ref.contentVersion}</code> }} /></p>)}</details>
    {active ? <ActivityPRSource target={target} revision={revision} /> : null}
  </>;
}
