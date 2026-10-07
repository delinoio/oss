import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useRef } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { BackupCreationState, BackupDeletionState, SystemQuery, type BackupCreationJob, type BackupDeletionJob } from "@delinoio/delidev-api-client";
import { Problem } from "./ui";

export enum BackupJobKind { Creation = "creation", Deletion = "deletion" }
type TrackedJob = { kind: BackupJobKind.Creation; accepted: BackupCreationJob } | { kind: BackupJobKind.Deletion; accepted: BackupDeletionJob };

export function BackupJob({ kind, accepted, active, completed, dismiss }: TrackedJob & { active: boolean; completed: () => void; dismiss: () => void }) {
  useLocale();
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
  return <article className="result" aria-label={copy("backup-job.backupTracking", { v0: accepted.backupId })}>
    <h4><LocalizedText id="backup-job.backup_181f9b" components={{ s0: <>{accepted.backupId}</> }} /></h4>
    {!succeeded ? <p role="status">{failed ? copy("backup-job.failed_5d28a9") : pending ? copy("backup-job.pending_62a2fe") : copy("backup-job.statusUnavailable_180119")}</p> : null}
    <Problem error={query.error} />
    {job?.problemCode ? <p><LocalizedText id="backup-job.operationNeedsAttention_bfbf09" components={{ s0: <>{job.problemCode}</> }} /></p> : null}
    {query.error && query.data ? <p>{copy("backup-job.theLastObservationIsStaleCurrent_4263e8")}</p> : null}
    {query.error ? <button disabled={!active || query.isFetching} onClick={() => void query.refetch()}>{copy("jobs.retryStatusRead")}</button> : null}
    {succeeded || failed ? <button aria-label={copy("backup-job.dismissBackup", { v0: accepted.backupId })} disabled={!active} onClick={dismiss}>{copy("backup-job.dismissTracking_12e6bb")}</button> : null}
  </article>;
}
