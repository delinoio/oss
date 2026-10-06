import { LocalizedText, copy, useLocale } from "./localization";
import { SettingsHeading } from "./settings-presentation";
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
  useLocale();
  return <div className="backups-empty-history"><svg aria-hidden="true" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg><p>{children}</p><p>{copy("backups.acceptedJobsWillAppearHere_840140")}</p></div>;
}

export function Backups({ active }: { active: boolean }) {
  useLocale();
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
  return <section className="backups-settings" aria-label={copy("backups.managedDatabaseBackups_322e74")}>
    <SettingsHeading title={copy("backups.backups_3334fe")} description={copy("backups.manageDatabaseBackupsAndFollowBackup_8b4b4f")} actions={<>
        <button disabled={!active || inventory.isFetching} onClick={refresh}>{copy("backups.refreshBackups_47e8b1")}</button>
        <button className="backups-primary" disabled={!active || create.busy || create.uncertain || trackedCreations.length >= maxTrackedJobs} onClick={() => { setCreated(""); void create.send({ requestId: newRequestId() }); }}>{copy("backups.createDatabaseBackup_9e717f")}</button>
      </>} />
    <p className="backups-scope">{copy("backups.backupsContainPrivateServerDataAnd_e910aa")}</p>
    <Problem error={create.error} />
    {create.uncertain ? <button disabled={!active || create.busy} onClick={create.retry}>{copy("backups.retryTheSameBackupCreation_52ba47")}</button> : null}
    {created ? <p role="status"><LocalizedText id="backups.backupCreationAccepted_77b91d" components={{ s0: <>{created}</> }} /></p> : null}
    <section className="backups-panel backups-inventory" aria-labelledby={`${panelId}-inventory`}>
      <header className="backups-panel-header"><h2 id={`${panelId}-inventory`} ref={listHeading} tabIndex={-1}>{copy("backups.databaseBackups_e6ded7")}</h2>{inventory.data ? <span><LocalizedText id="backups.onThisPage_3ff888" components={{ s0: <>{inventory.data.backups.length}</> }} /></span> : null}</header>
      <div className="backups-read-state">
        <Problem error={inventory.error} />
        {inventory.error && inventory.data ? <p>{copy("backups.thePreviousBackupListIsShown_756d12")}</p> : null}
        {!inventory.data && inventory.isPending ? <p role="status">{copy("backups.readingManagedBackups_60b272")}</p> : null}
        {inventory.data && inventory.isFetching ? <p role="status">{copy("backups.updatingManagedBackups_4d6219")}</p> : null}
        {inventory.data?.backups.length === 0 ? <p>{page ? copy("backups.noBackupsOnThisPage_056811") : copy("backups.noManagedBackups_c08d9c")}</p> : null}
      </div>
      <table role="table" aria-labelledby={`${panelId}-inventory`}>
        <thead><tr role="row"><th role="columnheader" id={`${panelId}-modified`} scope="col">{copy("backups.modifiedUtcBackupId_bacb2a")}</th><th role="columnheader" id={`${panelId}-size`} scope="col">{copy("backups.size_1af851")}</th><th role="columnheader" id={`${panelId}-integrity`} scope="col">{copy("backups.integrity_ad5ea6")}</th><th role="columnheader" id={`${panelId}-action`} scope="col"><span className="backups-sr-only">{copy("backups.action_64cff1")}</span></th></tr></thead>
        <tbody>{inventory.data?.backups.map(item => <tr role="row" key={item.id} className={selected === item.id ? "backups-selected" : undefined}>
          <td role="cell" headers={`${panelId}-modified`}><time dateTime={item.modifiedAt}>{modificationLabel(item.modifiedAt)}</time><span className="backups-sr-only"><LocalizedText id="backups.originalModificationTimestamp_06e09f" components={{ s0: <>{item.modifiedAt}</> }} /></span><code className="backups-id">{item.id}</code></td>
          <td role="cell" headers={`${panelId}-size`}><LocalizedText id="backups.bytes_825916" components={{ s0: <span className="backups-cell-label" aria-hidden="true">{copy("backups.size_1af851")}</span>, s1: <>{bytes.format(item.sizeBytes)}</> }} /></td>
          <td role="cell" headers={`${panelId}-integrity`}><span className="backups-cell-label" aria-hidden="true">{copy("backups.integrity_ad5ea6")}</span><span className="backups-integrity">{checked?.backup?.id === item.id ? copy("backups.verifiedInspection_14f284") : copy("backups.notChecked_d16948")}</span></td>
          <td role="cell" headers={`${panelId}-action`}><button ref={node => { if (node) inspectButtons.current.set(item.id, node); else inspectButtons.current.delete(item.id); }} aria-label={copy("backups.inspectBackup_78fbba", { v0: item.id })} disabled={!active || Boolean(inventory.error) || inventory.isFetching || inspection.isFetching} onClick={() => {
            inspectionOrigin.current = item.id;
            setInspectionActivation(value => value + 1);
            setConfirmation(undefined);
            if (selected === item.id) void inspection.refetch(); else setSelected(item.id);
          }}>{copy("backups.inspect_e0723a")}</button></td>
        </tr>)}</tbody>
      </table>
      <footer className="backups-inventory-footer"><p>{copy("backups.integrityNotEstablishedByThisListing_299564")}</p>
        {!hidePager(page, inventory.data?.backups.length, inventory.data?.nextPageToken, inventory.error) ? <nav className="backups-actions" aria-label={copy("backups.backupPages_ca0a4a")}><button disabled={!active || !page || inventory.isFetching} onClick={() => setPage("")}>{copy("backups.firstBackupPage_b83ba3")}</button><button disabled={!active || !inventory.data?.nextPageToken || inventory.isFetching || Boolean(inventory.error)} onClick={() => setPage(inventory.data!.nextPageToken)}>{copy("backups.nextBackupPage_7b7dfe")}</button></nav> : null}
      </footer>
    </section>
    {selected ? <section className="backups-panel backups-inspection" aria-label={copy("backups.backupIntegrityInspection_6b0728")}>
      <h2 ref={inspectionHeading} tabIndex={-1}><LocalizedText id="backups.inspection_39c73e" components={{ s0: <>{selected}</> }} /></h2>
      <Problem error={inspection.error} />
      {inspection.isFetching ? <p role="status">{copy("backups.checkingTheSelectedBackup_ca89e6")}</p> : null}
      {checked ? <>
        <p role="status">{copy("backups.databaseIntegrityAndOriginalServerIdentity_26b0d4")}</p>
        <dl><div><dt>{copy("backups.backupId_8c6f39")}</dt><dd><code>{checked.backup!.id}</code></dd></div><div><dt>{copy("backups.modifiedUtc_d81af6")}</dt><dd><time dateTime={checked.backup!.modifiedAt}>{checked.backup!.modifiedAt}</time></dd></div><div><dt>{copy("backups.size_1af851")}</dt><dd><LocalizedText id="backups.bytes_e17732" components={{ s0: <>{bytes.format(checked.backup!.sizeBytes)}</> }} /></dd></div><div><dt>{copy("backups.schema_07b091")}</dt><dd>{checked.schemaVersion}</dd></div><div><dt>{copy("backups.sha256_bbd07c")}</dt><dd><code>{checked.sha256}</code></dd></div></dl>
        <p>{copy("backups.thisObservationDoesNotRestoreData_2a506c")}</p>
        <fieldset className="backups-deletion"><legend>{copy("backups.permanentBackupDeletion_7640fe")}</legend><p>{copy("backups.theSelectedImageWillBeDeleted_214572")}</p><label><input type="checkbox" checked={confirm} disabled={remove.busy || remove.uncertain} onChange={event => setConfirmation(event.target.checked ? checked : undefined)} /><LocalizedText id="backups.iConfirmPermanentDeletionOfBackup_6eefd3" components={{ s0: <>{selected}</> }} /></label><button className="backups-destructive" disabled={!active || !confirm || remove.busy || remove.uncertain || trackedDeletions.length >= maxTrackedJobs} onClick={() => { void remove.send({ requestId: newRequestId(), backup: checked.backup!, sha256: checked.sha256 }); }}>{copy("backups.permanentlyDeleteSelectedBackup_6bc00c")}</button></fieldset>
      </> : null}
      <div className="backups-actions"><button disabled={!active || inspection.isFetching} onClick={() => { setConfirmation(undefined); void inspection.refetch(); }}>{copy("backups.recheckSelectedBackup_2db613")}</button><button onClick={() => { returnInspectionFocus.current = true; setSelected(""); setConfirmation(undefined); }}>{copy("backups.closeBackupInspection_acadd7")}</button></div>
    </section> : null}
    <Problem error={remove.error} />
    {remove.uncertain ? <button disabled={!active || remove.busy} onClick={remove.retry}>{copy("backups.retryTheSameBackupDeletion_0f7462")}</button> : null}
    {trackedCreations.length || trackedDeletions.length ? <section className="backups-panel backups-accepted" aria-label={copy("backups.acceptedBackupOperations_aab6f8")}><h2>{copy("backups.acceptedOperations_bc91ab")}</h2><p>{copy("backups.theseJobsAreObservedDirectlyIndependently_2dfe12")}</p>
      {trackedCreations.map(job => <BackupJob key={job.id} kind={BackupJobKind.Creation} accepted={job} active={active} completed={refresh} dismiss={() => setTrackedCreations(current => current.filter(item => item.id !== job.id))} />)}
      {trackedDeletions.map(job => <BackupJob key={job.id} kind={BackupJobKind.Deletion} accepted={job} active={active} completed={refresh} dismiss={() => setTrackedDeletions(current => current.filter(item => item.id !== job.id))} />)}
      {trackedCreations.length >= maxTrackedJobs || trackedDeletions.length >= maxTrackedJobs ? <p>{copy("backups.dismissCompletedTrackingEntriesToAccept_4bacdc")}</p> : null}
    </section> : null}
    <section className="backups-panel backups-history" aria-label={copy("backups.operationHistory_93bf53")}>
      <header className="backups-history-heading"><h2>{copy("backups.operationHistory_93bf53")}</h2><button hidden={historyTab !== HistoryTab.Creation} disabled={!active || creations.isFetching} onClick={() => { void creations.refetch(); }}>{copy("backups.refreshCreationJobs_96e3e0")}</button></header>
      <div className="backups-tabs" role="tablist" aria-label={copy("backups.backupOperationHistory_8f9f32")}>{historyTabs.map(tab => <button key={tab} ref={node => { if (node) tabButtons.current.set(tab, node); else tabButtons.current.delete(tab); }} role="tab" id={`${panelId}-tab-${tab}`} aria-controls={`${panelId}-panel-${tab}`} aria-selected={historyTab === tab} tabIndex={focusedTab === tab ? 0 : -1} onFocus={() => setFocusedTab(tab)} onKeyDown={event => moveTabFocus(event, tab)} onClick={() => { setFocusedTab(tab); setHistoryTab(tab); }}>{tab === HistoryTab.Creation ? copy("backups.creationJobs_dd6156") : copy("backups.deletionJobs_74156e")}</button>)}</div>
      <div role="tabpanel" id={`${panelId}-panel-creation`} aria-labelledby={`${panelId}-tab-creation`} hidden={historyTab !== HistoryTab.Creation}>
        <p>{copy("backups.creationContinuesAfterThisClientDisconnects_03823b")}</p>
        <Problem error={creations.error} />{creations.error && creations.data ? <p>{copy("backups.previousCreationObservationsAreShownCurrent_feb522")}</p> : null}
        {!creations.data && creations.isPending ? <p role="status">{copy("backups.readingCreationJobs_012c61")}</p> : null}{creations.data && creations.isFetching ? <p role="status">{copy("backups.updatingCreationJobs_d8faea")}</p> : null}
        {creations.data?.jobs.length === 0 ? <EmptyHistory>{copy("backups.noCreationJobsOnThisPage_3c155f")}</EmptyHistory> : null}
        {creations.data?.jobs.map(job => <article className="backups-job" key={job.id}><h3><LocalizedText id="backups.backup_181f9b" components={{ s0: <>{job.backupId}</> }} /></h3><p><LocalizedText id="backups.jobRevision_b89f71" components={{ s0: <>{job.id}</>, s1: <>{job.revision.toString()}</> }} /></p><p>{job.state === BackupCreationState.SUCCEEDED ? copy("backups.backupCreationCompleted_5ecd17") : job.state === BackupCreationState.PENDING ? copy("backups.backupCreationPending_74003f") : job.state === BackupCreationState.FAILED ? copy("backups.backupCreationFailed_f70ce5") : copy("backups.creationStateUnavailable_9f451d")}</p>{job.problemCode ? <p><LocalizedText id="backups.creationNeedsAttention_e81368" components={{ s0: <>{job.problemCode}</> }} /></p> : null}</article>)}
        {!hidePager(creationPage, creations.data?.jobs.length, creations.data?.nextPageToken, creations.error) ? <nav className="backups-actions" aria-label={copy("backups.creationJobPages_a5aa7a")}><button disabled={!active || !creationPage || creations.isFetching} onClick={() => setCreationPage("")}>{copy("backups.firstCreationPage_4bb97c")}</button><button disabled={!active || !creations.data?.nextPageToken || creations.isFetching || Boolean(creations.error)} onClick={() => setCreationPage(creations.data!.nextPageToken)}>{copy("backups.nextCreationPage_92e4d6")}</button></nav> : null}
      </div>
      <div role="tabpanel" id={`${panelId}-panel-deletion`} aria-labelledby={`${panelId}-tab-deletion`} hidden={historyTab !== HistoryTab.Deletion}>
        <p>{copy("backups.acceptedJobsSurviveServerRestartFailed_3787c0")}</p>
        <Problem error={deletions.error} />{deletions.error && deletions.data ? <p>{copy("backups.previousDeletionObservationsAreShownCurrent_b3d9d1")}</p> : null}
        {!deletions.data && deletions.isPending ? <p role="status">{copy("backups.readingDeletionJobs_24fef4")}</p> : null}{deletions.data && deletions.isFetching ? <p role="status">{copy("backups.updatingDeletionJobs_de4b91")}</p> : null}
        {deletions.data?.jobs.length === 0 ? <EmptyHistory>{copy("backups.noDeletionJobsOnThisPage_c7d742")}</EmptyHistory> : null}
        {deletions.data?.jobs.map(job => <article className="backups-job" key={job.id}><h3><LocalizedText id="backups.backup_181f9b" components={{ s0: <>{job.backupId}</> }} /></h3><p><LocalizedText id="backups.jobRevision_b89f71" components={{ s0: <>{job.id}</>, s1: <>{job.revision.toString()}</> }} /></p><p>{job.state === BackupDeletionState.SUCCEEDED ? copy("backups.deletionCompleted_80a98a") : job.state === BackupDeletionState.PENDING ? copy("backups.deletionPending_614524") : copy("backups.deletionStateUnavailable_ea52ae")}</p>{job.problemCode ? <p><LocalizedText id="backups.cleanupNeedsAttention_650005" components={{ s0: <>{job.problemCode}</> }} /></p> : null}{job.state === BackupDeletionState.SUCCEEDED ? <p>{job.removalObserved ? copy("backups.imageBytesRemoved_9f2ac1", { v0: bytes.format(job.imageBytes) }) : copy("backups.imageAbsenceConfirmedOriginalUnlinkByte_435895")}</p> : null}</article>)}
        {!hidePager(deletionPage, deletions.data?.jobs.length, deletions.data?.nextPageToken, deletions.error) ? <nav className="backups-actions" aria-label={copy("backups.deletionJobPages_68fad7")}><button disabled={!active || !deletionPage || deletions.isFetching} onClick={() => setDeletionPage("")}>{copy("backups.firstDeletionPage_b8a839")}</button><button disabled={!active || !deletions.data?.nextPageToken || deletions.isFetching || Boolean(deletions.error)} onClick={() => setDeletionPage(deletions.data!.nextPageToken)}>{copy("backups.nextDeletionPage_f9e500")}</button></nav> : null}
      </div>
    </section>
  </section>;
}
