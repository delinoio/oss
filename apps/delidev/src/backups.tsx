import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery, BackupCreationState, BackupDeletionState, newRequestId } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function Backups({ active }: { active: boolean }) {
  const [page, setPage] = useState("");
  const [selected, setSelected] = useState("");
  const [created, setCreated] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [creationPage, setCreationPage] = useState("");
  const creations = useQuery(SystemQuery.listBackupCreations, { pageSize: 20, pageToken: creationPage }, { enabled: active, retry: false, refetchInterval: active ? 2000 : false });
  const [deletionPage, setDeletionPage] = useState("");
  const deletions = useQuery(SystemQuery.listBackupDeletions, { pageSize: 20, pageToken: deletionPage }, { enabled: active, retry: false, refetchInterval: active ? 2000 : false });
  const inventory = useQuery(SystemQuery.listBackups, { pageSize: 20, pageToken: page }, { enabled: active, retry: false });
  const inspection = useQuery(SystemQuery.inspectBackup, { id: selected }, { enabled: active && Boolean(selected), retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const refresh = () => { if (page) setPage(""); else void inventory.refetch(); };
  const create = useRetainedMutation("backup-create", SystemQuery.requestBackup, (result) => { setCreated(result.job?.id ?? ""); void creations.refetch(); });
  const remove = useRetainedMutation("backup-delete", SystemQuery.deleteBackup, () => { setSelected(""); setConfirm(false); refresh(); void deletions.refetch(); });
  const completed = creations.data?.jobs.filter(job => job.state === BackupCreationState.SUCCEEDED).map(job => job.id).join(",") ?? "";
  useEffect(() => { if (active && completed) void inventory.refetch(); }, [active, completed, inventory.refetch]);
  const checked = inspection.data?.backup?.id === selected && !inspection.error && !inspection.isFetching ? inspection.data : undefined;
  return <section aria-label="Managed database backups">
    <h2>Database backups</h2>
    <p>Backups contain private server data and committed database changes. Credentials, browser profiles, and Worker files are separate.</p>
    <div className="actions"><button disabled={!active || create.busy || create.uncertain} onClick={() => { setCreated(""); void create.send({ requestId: newRequestId() }); }}>Create database backup</button><button disabled={!active || inventory.isFetching} onClick={refresh}>Refresh backups</button></div>
    <Problem error={create.error} />
    {create.uncertain ? <button disabled={!active || create.busy} onClick={create.retry}>Retry the same backup creation</button> : null}
    {created ? <p role="status">Backup creation accepted: {created}</p> : null}
    <Problem error={inventory.error} />
    {inventory.error && inventory.data ? <p>The previous backup list is shown. Refresh before relying on it.</p> : null}
    {inventory.isPending ? <p>Reading managed backups…</p> : inventory.data?.backups.length === 0 ? <p>No managed backups.</p> : null}
    {inventory.data?.backups.map((item) => <article className="result" key={item.id}><h3>{item.id}</h3><p>{item.sizeBytes.toString()} bytes · {item.modifiedAt}</p><p>Integrity not established by this listing.</p><button disabled={!active || Boolean(inventory.error) || inventory.isFetching || inspection.isFetching} onClick={() => { if (selected === item.id) void inspection.refetch(); else { setSelected(item.id); setConfirm(false); } }}>Inspect backup {item.id}</button></article>)}
    <nav aria-label="Backup pages"><button disabled={!active || !page || inventory.isFetching} onClick={() => setPage("")}>First backup page</button><button disabled={!active || !inventory.data?.nextPageToken || inventory.isFetching || Boolean(inventory.error)} onClick={() => setPage(inventory.data!.nextPageToken)}>Next backup page</button></nav>
    {selected ? <section aria-label="Backup integrity inspection"><h3>Inspection: {selected}</h3><Problem error={inspection.error} />{inspection.isFetching ? <p role="status">Checking the selected backup…</p> : null}{checked ? <><p role="status">Database integrity and original server identity verified.</p><p>Schema {checked.schemaVersion} · {checked.backup!.sizeBytes.toString()} bytes</p><p>SHA-256: <code>{checked.sha256}</code></p><p>This observation does not restore data or prove that Worker files and credentials are recoverable.</p><fieldset><legend>Permanent backup deletion</legend><p>The selected image will be deleted. Accepted deletion cannot be canceled or undone by restoring an older database.</p><label><input type="checkbox" checked={confirm} disabled={remove.busy || remove.uncertain} onChange={event => setConfirm(event.target.checked)} />I confirm permanent deletion of backup {selected}</label><button disabled={!active || !confirm || remove.busy || remove.uncertain} onClick={() => { void remove.send({ requestId: newRequestId(), backup: checked.backup!, sha256: checked.sha256 }); }}>Permanently delete selected backup</button></fieldset></> : null}<button disabled={!active || inspection.isFetching} onClick={() => { setConfirm(false); void inspection.refetch(); }}>Recheck selected backup</button><button onClick={() => setSelected("")}>Close backup inspection</button></section> : null}
    <Problem error={remove.error} />
    {remove.uncertain ? <button disabled={!active || remove.busy} onClick={remove.retry}>Retry the same backup deletion</button> : null}
    <section aria-label="Backup creation jobs"><h3>Creation jobs</h3><p>Creation continues after this client disconnects and resumes after a server restart. A pending job only reserves an image ID; completed jobs record publication at that time, not current image availability.</p><button disabled={!active || creations.isFetching} onClick={() => { void creations.refetch(); }}>Refresh creation jobs</button><Problem error={creations.error} />{creations.error && creations.data ? <p>Previous creation observations are shown; current state is unavailable.</p> : null}
      {creations.data?.jobs.map(job => <article className="result" key={job.id}><h4>Backup {job.backupId}</h4><p>Job {job.id} · revision {job.revision.toString()}</p><p>{job.state === BackupCreationState.SUCCEEDED ? "Backup creation completed" : job.state === BackupCreationState.PENDING ? "Backup creation pending" : job.state === BackupCreationState.FAILED ? "Backup creation failed" : "Creation state unavailable"}</p>{job.problemCode ? <p>Creation needs attention: {job.problemCode}</p> : null}</article>)}
      <nav aria-label="Creation job pages"><button disabled={!active || !creationPage || creations.isFetching} onClick={() => setCreationPage("")}>First creation page</button><button disabled={!active || !creations.data?.nextPageToken || creations.isFetching || Boolean(creations.error)} onClick={() => setCreationPage(creations.data!.nextPageToken)}>Next creation page</button></nav>
    </section>
    <section aria-label="Backup deletion jobs"><h3>Deletion jobs</h3><p>Accepted jobs survive server restart. Failed cleanup remains pending and retries automatically. Removed image bytes are logical file size, not measured free disk space.</p><Problem error={deletions.error} />{deletions.error && deletions.data ? <p>Previous deletion observations are shown; current state is unavailable.</p> : null}
      {deletions.data?.jobs.map(job => <article className="result" key={job.id}><h4>Backup {job.backupId}</h4><p>Job {job.id} · revision {job.revision.toString()}</p><p>{job.state === BackupDeletionState.SUCCEEDED ? "Deletion completed" : job.state === BackupDeletionState.PENDING ? "Deletion pending" : "Deletion state unavailable"}</p>{job.problemCode ? <p>Cleanup needs attention: {job.problemCode}</p> : null}{job.state === BackupDeletionState.SUCCEEDED ? <p>{job.removalObserved ? `${job.imageBytes.toString()} image bytes removed` : "Image absence confirmed; original unlink byte count unavailable"}</p> : null}</article>)}
      <nav aria-label="Deletion job pages"><button disabled={!active || !deletionPage || deletions.isFetching} onClick={() => setDeletionPage("")}>First deletion page</button><button disabled={!active || !deletions.data?.nextPageToken || deletions.isFetching || Boolean(deletions.error)} onClick={() => setDeletionPage(deletions.data!.nextPageToken)}>Next deletion page</button></nav>
    </section>
  </section>;
}
