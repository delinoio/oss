import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery, BackupCreationState, BackupDeletionState, newRequestId, type BackupCreationJob, type BackupDeletionJob, type InspectBackupResponse } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { BackupJob, BackupJobKind } from "./backup-job";

import "./backups.css";

const maxTrackedJobs = 20;
enum HistoryTab { Creation = "creation", Deletion = "deletion" }
const historyTabs = [HistoryTab.Creation, HistoryTab.Deletion];
const bytes = new Intl.NumberFormat("en-US");
const modifiedDate = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", month: "short", day: "numeric", year: "numeric" });
const modifiedTime = new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });

function modificationLabel(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : `${modifiedDate.format(date)} · ${modifiedTime.format(date)} UTC`;
}

function hidePager(page: string, count: number | undefined, next: string | undefined, error: unknown) {
  return !page && count === 0 && !next && !error;
}

function EmptyHistory({ children }: { children: string }) {
  return <div className="backups-empty-history"><svg aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg><p>{children}</p><p>Accepted jobs will appear here.</p></div>;
}

export function Backups({ active }: { active: boolean }) {
  const panelId = useId();
  const [historyTab, setHistoryTab] = useState(HistoryTab.Creation);
  const [focusedTab, setFocusedTab] = useState(HistoryTab.Creation);
  const tabButtons = useRef(new Map<HistoryTab, HTMLButtonElement>());
  const inspectButtons = useRef(new Map<string, HTMLButtonElement>());
  const inspectionOrigin = useRef("");
  const returnInspectionFocus = useRef(false);
  const inspectionHeading = useRef<HTMLHeadingElement>(null);
  const listHeading = useRef<HTMLHeadingElement>(null);
  const [inspectionActivation, setInspectionActivation] = useState(0);
  // Only an explicit Inspect activation requests focus. Read completion,
  // reconnect and category reactivation must never repeat this handoff.
  useLayoutEffect(() => {
    if (inspectionActivation) inspectionHeading.current?.focus();
  }, [inspectionActivation]);
  const moveTabFocus = (event: KeyboardEvent<HTMLButtonElement>, tab: HistoryTab) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setHistoryTab(tab);
      return;
    }
    const index = historyTabs.indexOf(tab);
    const next = event.key === "Home" ? 0 : event.key === "End" ? historyTabs.length - 1 : event.key === "ArrowRight" ? (index + 1) % historyTabs.length : event.key === "ArrowLeft" ? (index + historyTabs.length - 1) % historyTabs.length : undefined;
    if (next === undefined) return;
    event.preventDefault();
    const target = historyTabs[next]!;
    setFocusedTab(target);
    tabButtons.current.get(target)?.focus();
  };
  const [trackedCreations, setTrackedCreations] = useState<BackupCreationJob[]>([]);
  const [trackedDeletions, setTrackedDeletions] = useState<BackupDeletionJob[]>([]);
  const [page, setPage] = useState("");
  const [selected, setSelected] = useState("");
  useLayoutEffect(() => {
    if (!selected && returnInspectionFocus.current) {
      returnInspectionFocus.current = false;
      // Wait for the close commit so an in-flight inspection no longer disables
      // the originating row button before returning keyboard focus.
      const origin = inspectButtons.current.get(inspectionOrigin.current);
      (origin && !origin.disabled ? origin : listHeading.current)?.focus();
    }
  }, [selected]);
  const [created, setCreated] = useState("");
  const [confirmation, setConfirmation] = useState<InspectBackupResponse>();
  const [creationPage, setCreationPage] = useState("");
  const creations = useQuery(SystemQuery.listBackupCreations, { pageSize: 20, pageToken: creationPage }, { enabled: active, retry: false, refetchInterval: active ? 2000 : false });
  const [deletionPage, setDeletionPage] = useState("");
  const deletions = useQuery(SystemQuery.listBackupDeletions, { pageSize: 20, pageToken: deletionPage }, { enabled: active, retry: false, refetchInterval: active ? 2000 : false });
  const inventory = useQuery(SystemQuery.listBackups, { pageSize: 20, pageToken: page }, { enabled: active, retry: false });
  const inspection = useQuery(SystemQuery.inspectBackup, { id: selected }, { enabled: active && Boolean(selected), retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const refresh = useCallback(() => { if (page) setPage(""); else void inventory.refetch(); }, [page, inventory.refetch]);
  const create = useRetainedMutation("backup-create", SystemQuery.requestBackup, (result) => { setCreated(result.job?.id ?? ""); if (result.job) { const job = result.job; setTrackedCreations(current => current.some(item => item.id === job.id) ? current : [...current, job]); } void creations.refetch(); });
  const remove = useRetainedMutation("backup-delete", SystemQuery.deleteBackup, (result) => { if (result.job) { const job = result.job; setTrackedDeletions(current => current.some(item => item.id === job.id) ? current : [...current, job]); } setSelected(""); setConfirmation(undefined); refresh(); void deletions.refetch(); });
  const completed = creations.data?.jobs.filter(job => job.state === BackupCreationState.SUCCEEDED).map(job => job.id).join(",") ?? "";
  useEffect(() => { if (active && completed) void inventory.refetch(); }, [active, completed, inventory.refetch]);
  const checked = inspection.data?.backup?.id === selected && !inspection.error && !inspection.isFetching ? inspection.data : undefined;
  const confirm = active && checked !== undefined && confirmation === checked;
  useEffect(() => {
    if (!active || inspection.isFetching || (confirmation !== undefined && confirmation !== inspection.data)) setConfirmation(undefined);
  }, [active, inspection.data, inspection.isFetching, confirmation]);
  return <section className="backups-settings" aria-label="Managed database backups">
    <header className="backups-header">
      <div><h1 aria-live="polite" aria-atomic="true">Backups</h1><p>Inspect managed database images and follow durable creation and deletion jobs on the selected server.</p></div>
      <div className="backups-actions">
        <button disabled={!active || inventory.isFetching} onClick={refresh}>Refresh backups</button>
        <button className="backups-primary" disabled={!active || create.busy || create.uncertain || trackedCreations.length >= maxTrackedJobs} onClick={() => { setCreated(""); void create.send({ requestId: newRequestId() }); }}>Create database backup</button>
      </div>
    </header>
    <p className="backups-scope">Backups contain private server data and committed database changes. Credentials, browser profiles, and Worker files are separate.</p>
    <Problem error={create.error} />
    {create.uncertain ? <button disabled={!active || create.busy} onClick={create.retry}>Retry the same backup creation</button> : null}
    {created ? <p role="status">Backup creation accepted: {created}</p> : null}
    <section className="backups-panel backups-inventory" aria-labelledby={`${panelId}-inventory`}>
      <header className="backups-panel-header"><h2 id={`${panelId}-inventory`} ref={listHeading} tabIndex={-1}>Database backups</h2>{inventory.data ? <span>{inventory.data.backups.length} on this page</span> : null}</header>
      <div className="backups-read-state">
        <Problem error={inventory.error} />
        {inventory.error && inventory.data ? <p>The previous backup list is shown. Refresh before relying on it.</p> : null}
        {!inventory.data && inventory.isPending ? <p role="status">Reading managed backups…</p> : null}
        {inventory.data && inventory.isFetching ? <p role="status">Updating managed backups…</p> : null}
        {inventory.data?.backups.length === 0 ? <p>{page ? "No backups on this page." : "No managed backups."}</p> : null}
      </div>
      <table role="table" aria-labelledby={`${panelId}-inventory`}>
        <thead><tr role="row"><th role="columnheader" id={`${panelId}-modified`} scope="col">Modified (UTC) / Backup ID</th><th role="columnheader" id={`${panelId}-size`} scope="col">Size</th><th role="columnheader" id={`${panelId}-integrity`} scope="col">Integrity</th><th role="columnheader" id={`${panelId}-action`} scope="col"><span className="backups-sr-only">Action</span></th></tr></thead>
        <tbody>{inventory.data?.backups.map(item => <tr role="row" key={item.id} className={selected === item.id ? "backups-selected" : undefined}>
          <td role="cell" headers={`${panelId}-modified`}><time dateTime={item.modifiedAt}>{modificationLabel(item.modifiedAt)}</time><span className="backups-sr-only">Original modification timestamp: {item.modifiedAt}</span><code className="backups-id">{item.id}</code></td>
          <td role="cell" headers={`${panelId}-size`}><span className="backups-cell-label" aria-hidden="true">Size</span>{bytes.format(item.sizeBytes)} bytes</td>
          <td role="cell" headers={`${panelId}-integrity`}><span className="backups-cell-label" aria-hidden="true">Integrity</span><span className="backups-integrity">{checked?.backup?.id === item.id ? "Verified inspection" : "Not checked"}</span></td>
          <td role="cell" headers={`${panelId}-action`}><button ref={node => { if (node) inspectButtons.current.set(item.id, node); else inspectButtons.current.delete(item.id); }} aria-label={`Inspect backup ${item.id}`} disabled={!active || Boolean(inventory.error) || inventory.isFetching || inspection.isFetching} onClick={() => {
            inspectionOrigin.current = item.id;
            setInspectionActivation(value => value + 1);
            setConfirmation(undefined);
            if (selected === item.id) void inspection.refetch(); else setSelected(item.id);
          }}>Inspect</button></td>
        </tr>)}</tbody>
      </table>
      <footer className="backups-inventory-footer"><p>Integrity not established by this listing.</p>
        {!hidePager(page, inventory.data?.backups.length, inventory.data?.nextPageToken, inventory.error) ? <nav className="backups-actions" aria-label="Backup pages"><button disabled={!active || !page || inventory.isFetching} onClick={() => setPage("")}>First backup page</button><button disabled={!active || !inventory.data?.nextPageToken || inventory.isFetching || Boolean(inventory.error)} onClick={() => setPage(inventory.data!.nextPageToken)}>Next backup page</button></nav> : null}
      </footer>
    </section>
    {selected ? <section className="backups-panel backups-inspection" aria-label="Backup integrity inspection">
      <h2 ref={inspectionHeading} tabIndex={-1}>Inspection: {selected}</h2>
      <Problem error={inspection.error} />
      {inspection.isFetching ? <p role="status">Checking the selected backup…</p> : null}
      {checked ? <>
        <p role="status">Database integrity and original server identity verified.</p>
        <dl><div><dt>Backup ID</dt><dd><code>{checked.backup!.id}</code></dd></div><div><dt>Modified (UTC)</dt><dd><time dateTime={checked.backup!.modifiedAt}>{checked.backup!.modifiedAt}</time></dd></div><div><dt>Size</dt><dd>{bytes.format(checked.backup!.sizeBytes)} bytes</dd></div><div><dt>Schema</dt><dd>{checked.schemaVersion}</dd></div><div><dt>SHA-256</dt><dd><code>{checked.sha256}</code></dd></div></dl>
        <p>This observation does not restore data or prove that Worker files and credentials are recoverable.</p>
        <fieldset className="backups-deletion"><legend>Permanent backup deletion</legend><p>The selected image will be deleted. Accepted deletion cannot be canceled or undone by restoring an older database.</p><label><input type="checkbox" checked={confirm} disabled={remove.busy || remove.uncertain} onChange={event => setConfirmation(event.target.checked ? checked : undefined)} />I confirm permanent deletion of backup {selected}</label><button className="backups-destructive" disabled={!active || !confirm || remove.busy || remove.uncertain || trackedDeletions.length >= maxTrackedJobs} onClick={() => { void remove.send({ requestId: newRequestId(), backup: checked.backup!, sha256: checked.sha256 }); }}>Permanently delete selected backup</button></fieldset>
      </> : null}
      <div className="backups-actions"><button disabled={!active || inspection.isFetching} onClick={() => { setConfirmation(undefined); void inspection.refetch(); }}>Recheck selected backup</button><button onClick={() => { returnInspectionFocus.current = true; setSelected(""); setConfirmation(undefined); }}>Close backup inspection</button></div>
    </section> : null}
    <Problem error={remove.error} />
    {remove.uncertain ? <button disabled={!active || remove.busy} onClick={remove.retry}>Retry the same backup deletion</button> : null}
    {trackedCreations.length || trackedDeletions.length ? <section className="backups-panel backups-accepted" aria-label="Accepted backup operations"><h2>Accepted operations</h2><p>These jobs are observed directly, independently of the history pages below.</p>
      {trackedCreations.map(job => <BackupJob key={job.id} kind={BackupJobKind.Creation} accepted={job} active={active} completed={refresh} dismiss={() => setTrackedCreations(current => current.filter(item => item.id !== job.id))} />)}
      {trackedDeletions.map(job => <BackupJob key={job.id} kind={BackupJobKind.Deletion} accepted={job} active={active} completed={refresh} dismiss={() => setTrackedDeletions(current => current.filter(item => item.id !== job.id))} />)}
      {trackedCreations.length >= maxTrackedJobs || trackedDeletions.length >= maxTrackedJobs ? <p>Dismiss completed tracking entries to accept more operations here. Their server history remains available.</p> : null}
    </section> : null}
    <section className="backups-panel backups-history" aria-label="Operation history">
      <header className="backups-history-heading"><h2>Operation history</h2><button hidden={historyTab !== HistoryTab.Creation} disabled={!active || creations.isFetching} onClick={() => { void creations.refetch(); }}>Refresh creation jobs</button></header>
      <div className="backups-tabs" role="tablist" aria-label="Backup operation history">{historyTabs.map(tab => <button key={tab} ref={node => { if (node) tabButtons.current.set(tab, node); else tabButtons.current.delete(tab); }} role="tab" id={`${panelId}-tab-${tab}`} aria-controls={`${panelId}-panel-${tab}`} aria-selected={historyTab === tab} tabIndex={focusedTab === tab ? 0 : -1} onFocus={() => setFocusedTab(tab)} onKeyDown={event => moveTabFocus(event, tab)} onClick={() => { setFocusedTab(tab); setHistoryTab(tab); }}>{tab === HistoryTab.Creation ? "Creation jobs" : "Deletion jobs"}</button>)}</div>
      <div role="tabpanel" id={`${panelId}-panel-creation`} aria-labelledby={`${panelId}-tab-creation`} hidden={historyTab !== HistoryTab.Creation}>
        <p>Creation continues after this client disconnects and resumes after a server restart. A pending job only reserves an image ID; completed jobs record publication at that time, not current image availability.</p>
        <Problem error={creations.error} />{creations.error && creations.data ? <p>Previous creation observations are shown; current state is unavailable.</p> : null}
        {!creations.data && creations.isPending ? <p role="status">Reading creation jobs…</p> : null}{creations.data && creations.isFetching ? <p role="status">Updating creation jobs…</p> : null}
        {creations.data?.jobs.length === 0 ? <EmptyHistory>No creation jobs on this page.</EmptyHistory> : null}
        {creations.data?.jobs.map(job => <article className="backups-job" key={job.id}><h3>Backup {job.backupId}</h3><p>Job {job.id} · revision {job.revision.toString()}</p><p>{job.state === BackupCreationState.SUCCEEDED ? "Backup creation completed" : job.state === BackupCreationState.PENDING ? "Backup creation pending" : job.state === BackupCreationState.FAILED ? "Backup creation failed" : "Creation state unavailable"}</p>{job.problemCode ? <p>Creation needs attention: {job.problemCode}</p> : null}</article>)}
        {!hidePager(creationPage, creations.data?.jobs.length, creations.data?.nextPageToken, creations.error) ? <nav className="backups-actions" aria-label="Creation job pages"><button disabled={!active || !creationPage || creations.isFetching} onClick={() => setCreationPage("")}>First creation page</button><button disabled={!active || !creations.data?.nextPageToken || creations.isFetching || Boolean(creations.error)} onClick={() => setCreationPage(creations.data!.nextPageToken)}>Next creation page</button></nav> : null}
      </div>
      <div role="tabpanel" id={`${panelId}-panel-deletion`} aria-labelledby={`${panelId}-tab-deletion`} hidden={historyTab !== HistoryTab.Deletion}>
        <p>Accepted jobs survive server restart. Failed cleanup remains pending and retries automatically. Removed image bytes are logical file size, not measured free disk space.</p>
        <Problem error={deletions.error} />{deletions.error && deletions.data ? <p>Previous deletion observations are shown; current state is unavailable.</p> : null}
        {!deletions.data && deletions.isPending ? <p role="status">Reading deletion jobs…</p> : null}{deletions.data && deletions.isFetching ? <p role="status">Updating deletion jobs…</p> : null}
        {deletions.data?.jobs.length === 0 ? <EmptyHistory>No deletion jobs on this page.</EmptyHistory> : null}
        {deletions.data?.jobs.map(job => <article className="backups-job" key={job.id}><h3>Backup {job.backupId}</h3><p>Job {job.id} · revision {job.revision.toString()}</p><p>{job.state === BackupDeletionState.SUCCEEDED ? "Deletion completed" : job.state === BackupDeletionState.PENDING ? "Deletion pending" : "Deletion state unavailable"}</p>{job.problemCode ? <p>Cleanup needs attention: {job.problemCode}</p> : null}{job.state === BackupDeletionState.SUCCEEDED ? <p>{job.removalObserved ? `${bytes.format(job.imageBytes)} image bytes removed` : "Image absence confirmed; original unlink byte count unavailable"}</p> : null}</article>)}
        {!hidePager(deletionPage, deletions.data?.jobs.length, deletions.data?.nextPageToken, deletions.error) ? <nav className="backups-actions" aria-label="Deletion job pages"><button disabled={!active || !deletionPage || deletions.isFetching} onClick={() => setDeletionPage("")}>First deletion page</button><button disabled={!active || !deletions.data?.nextPageToken || deletions.isFetching || Boolean(deletions.error)} onClick={() => setDeletionPage(deletions.data!.nextPageToken)}>Next deletion page</button></nav> : null}
      </div>
    </section>
  </section>;
}
