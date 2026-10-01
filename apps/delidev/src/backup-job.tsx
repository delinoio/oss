import { useEffect, useRef } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { BackupCreationState, BackupDeletionState, SystemQuery, type BackupCreationJob, type BackupDeletionJob } from "@delinoio/delidev-api-client";
import { Problem } from "./ui";

export enum BackupJobKind { Creation = "creation", Deletion = "deletion" }
type TrackedJob = { kind: BackupJobKind.Creation; accepted: BackupCreationJob } | { kind: BackupJobKind.Deletion; accepted: BackupDeletionJob };

export function BackupJob({ kind, accepted, active, completed, dismiss }: TrackedJob & { active: boolean; completed: () => void; dismiss: () => void }) {
  const creation = useQuery(SystemQuery.getBackupCreation, { id: accepted.id }, {
    enabled: active && kind === BackupJobKind.Creation, retry: false,
    refetchInterval: query => active && kind === BackupJobKind.Creation && (query.state.error || (query.state.data?.job?.state !== BackupCreationState.SUCCEEDED && query.state.data?.job?.state !== BackupCreationState.FAILED)) ? 2000 : false,
  });
  const deletion = useQuery(SystemQuery.getBackupDeletion, { id: accepted.id }, {
    enabled: active && kind === BackupJobKind.Deletion, retry: false,
    refetchInterval: query => active && kind === BackupJobKind.Deletion && (query.state.error || query.state.data?.job?.state !== BackupDeletionState.SUCCEEDED) ? 2000 : false,
  });
  const query = kind === BackupJobKind.Creation ? creation : deletion;
  const candidate = query.data?.job;
  const job = !query.error && candidate?.id === accepted.id && candidate.backupId === accepted.backupId && candidate.revision >= accepted.revision ? candidate : undefined;
  const succeeded = job && (kind === BackupJobKind.Creation ? job.state === BackupCreationState.SUCCEEDED : job.state === BackupDeletionState.SUCCEEDED);
  const failed = kind === BackupJobKind.Creation && job?.state === BackupCreationState.FAILED;
  const pending = job && (kind === BackupJobKind.Creation ? job.state === BackupCreationState.PENDING : job.state === BackupDeletionState.PENDING);
  const reported = useRef<bigint | undefined>(undefined);
  useEffect(() => {
    if (active && succeeded && job && reported.current !== job.revision) {
      reported.current = job.revision;
      completed();
    }
  }, [active, succeeded, job, completed]);
  return <article className="result" aria-label={`Tracked ${kind} ${accepted.id}`}>
    <h4>Backup {accepted.backupId}</h4><p>Job {accepted.id} · revision {(job?.revision ?? accepted.revision).toString()}</p>
    <p role="status">Accepted {kind} {succeeded ? "completed" : failed ? "failed" : pending ? "pending" : "status unavailable"}</p>
    <Problem error={query.error} />
    {job?.problemCode ? <p>Operation needs attention: {job.problemCode}</p> : null}
    {query.error && query.data ? <p>The last observation is stale; current job status is unavailable.</p> : null}
    <button aria-label={`Refresh tracked ${kind} ${accepted.id}`} disabled={!active || query.isFetching} onClick={() => void query.refetch()}>Refresh</button>
    {succeeded || failed ? <button aria-label={`Dismiss tracking for completed ${kind} ${accepted.id}`} disabled={!active} onClick={dismiss}>Dismiss tracking</button> : null}
  </article>;
}
