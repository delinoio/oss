import { SettingsTaskDismissButton } from "./settings-task";
import { LocalizedText, copy, displayLocale, formatNumber, formatTimestamp, useLocale } from "./localization";
import { SettingsTaskDialog, SettingsTaskScope, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { SettingsHeading } from "./settings-presentation";
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery, BackupCreationState, BackupDeletionState, newRequestId, type BackupCreationJob, type BackupDeletionJob, type InspectBackupResponse, type ListBackupsResponse, type ListBackupCreationsResponse, type ListBackupDeletionsResponse } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { BackupJob, BackupJobKind } from "./backup-job";

import { usePaginationChain, useConnectPaginationReader, usePaginationRefresh } from "./scroll-pagination-query";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import "./backups.css";

const maxTrackedJobs = 20;
enum HistoryTab { Creation = "creation", Deletion = "deletion" }
const historyTabs = [HistoryTab.Creation, HistoryTab.Deletion];

function modificationLabel(value: string) {
  const date = new Date(value);
  const modifiedDate = new Intl.DateTimeFormat(displayLocale(), { timeZone: "UTC", month: "short", day: "numeric", year: "numeric" });
  const modifiedTime = new Intl.DateTimeFormat(displayLocale() === "en-US" ? "en-GB" : displayLocale(), { timeZone: "UTC", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
  return Number.isNaN(date.getTime()) ? value : copy("backups.sentence.e6f7491a0231", { v0: modifiedDate.format(date), v1: modifiedTime.format(date) });
}

// All three existing list RPCs are requested with pageSize 20. Reject a
// malformed envelope before the chain can adopt any row or continuation.
function boundedBackupPage<T extends { id: string }>(items: T[], nextPageToken: string) {
  if (items.length > 20 || items.some(item => !item.id) || typeof nextPageToken !== "string") throw new Error("Invalid backup page");
  return items;
}

function BackupTable({ label, children }: { label: string; children: (id: string) => ReactNode }) {
  const id = useId();
  return <table role="table" aria-labelledby={label}>{children(id)}</table>;
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
  const listHeading = useRef<HTMLHeadingElement>(null);
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
  const content = useRef<HTMLElement>(null);
  const root = useScrollRoot(content);
  const [selected, setSelected] = useState("");
  const [inspected, setInspected] = useState("");
  useLayoutEffect(() => {
    if (!selected && returnInspectionFocus.current) {
      returnInspectionFocus.current = false;
      // Wait for the close commit so an in-flight inspection no longer disables
      // the originating row button before returning keyboard focus.
      const origin = inspectButtons.current.get(inspectionOrigin.current);
      (origin?.isConnected && !origin.disabled ? origin : listHeading.current)?.focus();
    }
  }, [selected]);
  const [created, setCreated] = useState("");
  const inventoryActive = active && !selected;
  const request = useCallback((pageToken: string) => ({ pageSize: 20, pageToken }), []);
  const projectInventory = useCallback((response: ListBackupsResponse) => ({ rows: boundedBackupPage(response.backups, response.nextPageToken).map(item => ({ id: item.id, revision: 0n })), payload: response.backups, nextPageToken: response.nextPageToken }), []);
  const projectCreations = useCallback((response: ListBackupCreationsResponse) => ({ rows: boundedBackupPage(response.jobs, response.nextPageToken).map(job => ({ id: job.id, revision: job.revision })), payload: response.jobs, nextPageToken: response.nextPageToken }), []);
  const projectDeletions = useCallback((response: ListBackupDeletionsResponse) => ({ rows: boundedBackupPage(response.jobs, response.nextPageToken).map(job => ({ id: job.id, revision: job.revision })), payload: response.jobs, nextPageToken: response.nextPageToken }), []);
  const inventory = usePaginationChain("backups-inventory", inventoryActive, useConnectPaginationReader(SystemQuery.listBackups, request, projectInventory), true, true);
  const creations = usePaginationChain("backups-creations", inventoryActive, useConnectPaginationReader(SystemQuery.listBackupCreations, request, projectCreations), true, true);
  const deletions = usePaginationChain("backups-deletions", inventoryActive, useConnectPaginationReader(SystemQuery.listBackupDeletions, request, projectDeletions), true, true);
  usePaginationRefresh(SystemQuery.listBackups, request(""), inventoryActive, inventory.refresh);
  usePaginationRefresh(SystemQuery.listBackupCreations, request(""), inventoryActive, creations.refresh, 2000);
  usePaginationRefresh(SystemQuery.listBackupDeletions, request(""), inventoryActive, deletions.refresh, 2000);
  const refresh = inventory.refreshExplicit;
  const create = useRetainedMutation("backup-create", SystemQuery.requestBackup, (result) => { setCreated(result.job?.id ?? ""); if (result.job) { const job = result.job; setTrackedCreations(current => current.some(item => item.id === job.id) ? current : [...current, job]); } creations.refresh(); });
  const completed = creations.rows.filter(job => creations.payloadPages.some(page => page.payload.some(item => item.id === job.id && item.state === BackupCreationState.SUCCEEDED))).map(job => job.id).join(",") ?? "";
  useEffect(() => { if (inventoryActive && completed) inventory.refresh(); }, [inventoryActive, completed, inventory.refresh]);
  return <section ref={content} className="backups-settings" aria-label={copy("backups.managedDatabaseBackups_322e74")}>
    <SettingsHeading title={copy("backups.backups_3334fe")} description={copy("backups.manageDatabaseBackupsAndFollowBackup_8b4b4f")} actions={<>
        <button disabled={!active || Boolean(inventory.loading)} onClick={refresh}>{copy("backups.refreshBackups_47e8b1")}</button>
        <button className="backups-primary" disabled={!active || create.busy || create.uncertain || trackedCreations.length >= maxTrackedJobs} onClick={() => { setCreated(""); void create.send({ requestId: newRequestId() }); }}>{copy("backups.createDatabaseBackup_9e717f")}</button>
      </>} />
    <p className="backups-scope">{copy("backups.backupsContainPrivateServerDataAnd_e910aa")}</p>
    <Problem error={create.error} />
    {create.uncertain ? <button disabled={!active || create.busy} onClick={create.retry}>{copy("backups.retryTheSameBackupCreation_52ba47")}</button> : null}
    {created ? <p role="status"><LocalizedText id="backups.backupCreationAccepted_77b91d" components={{ s0: <>{created}</> }} /></p> : null}
    <section className="backups-panel backups-inventory" aria-labelledby={`${panelId}-inventory`}>
      <header className="backups-panel-header"><h2 id={`${panelId}-inventory`} ref={listHeading} tabIndex={-1}>{copy("backups.databaseBackups_e6ded7")}</h2>{inventory.loaded ? <span><LocalizedText id="backups.onThisPage_3ff888" components={{ s0: <>{inventory.rows.length}</> }} /></span> : null}</header>
      <div className="backups-read-state">
        <Problem error={inventory.error?.failure} />
        {inventory.error && inventory.loaded ? <p>{copy("backups.thePreviousBackupListIsShown_756d12")}</p> : null}
        {!inventory.loaded && Boolean(inventory.loading) ? <p role="status">{copy("backups.readingManagedBackups_60b272")}</p> : null}
        {inventory.loaded && Boolean(inventory.loading) ? <p role="status">{copy("backups.updatingManagedBackups_4d6219")}</p> : null}
        {inventory.loaded && inventory.rows.length === 0 ? <p>{copy("backups.noManagedBackups_c08d9c")}</p> : null}
      </div>
      <ScrollPayloadWindow query={inventory} root={root} active={inventoryActive} identity={item => item.id}>{items => <BackupTable label={`${panelId}-inventory`}>{tableId => <>
        <thead><tr role="row"><th role="columnheader" id={`${tableId}-modified`} scope={"col"}>{copy("backups.modifiedUtcBackupId_bacb2a")}</th><th role="columnheader" id={`${tableId}-size`} scope={"col"}>{copy("backups.size_1af851")}</th><th role="columnheader" id={`${tableId}-integrity`} scope={"col"}>{copy("backups.integrity_ad5ea6")}</th><th role="columnheader" id={`${tableId}-action`} scope={"col"}><span className="backups-sr-only">{copy("backups.action_64cff1")}</span></th></tr></thead>
        <tbody>{items.map(item => <tr role="row" key={item.id} className={selected === item.id ? "backups-selected" : undefined}>
          <td role="cell" headers={`${tableId}-modified`}><time dateTime={item.modifiedAt}>{modificationLabel(item.modifiedAt)}</time><span className="backups-sr-only"><LocalizedText id="backups.originalModificationTimestamp_06e09f" components={{ s0: <>{item.modifiedAt}</> }} /></span><code className="backups-id">{item.id}</code></td>
          <td role="cell" headers={`${tableId}-size`}><LocalizedText id="backups.bytes_825916" components={{ s0: <span className="backups-cell-label" aria-hidden="true">{copy("backups.size_1af851")}</span>, s1: <>{formatNumber(item.sizeBytes)}</> }} /></td>
          <td role="cell" headers={`${tableId}-integrity`}><span className="backups-cell-label" aria-hidden="true">{copy("backups.integrity_ad5ea6")}</span><span className="backups-integrity">{selected === item.id && inspected === item.id ? copy("backups.verifiedInspection_14f284") : copy("backups.notChecked_d16948")}</span></td>
          <td role="cell" headers={`${tableId}-action`}><button ref={node => { if (node) inspectButtons.current.set(item.id, node); else inspectButtons.current.delete(item.id); }} aria-label={copy("backups.inspectBackup_78fbba", { v0: item.id })} disabled={!active || Boolean(inventory.error) || Boolean(inventory.loading)} onClick={() => {
            inspectionOrigin.current = item.id;
            setInspected("");
            setSelected(item.id);
          }}>{copy("backups.inspect_e0723a")}</button></td>
        </tr>)}</tbody>
      </>}</BackupTable>}</ScrollPayloadWindow>
      <footer className="backups-inventory-footer"><p>{copy("backups.integrityNotEstablishedByThisListing_299564")}</p>
        <ScrollContinuation query={inventory} root={root} active={inventoryActive} label={copy("backups.databaseBackups_e6ded7")} />
      </footer>
    </section>
    {selected ? <SettingsTaskScope key={selected}><BackupInspection selected={selected} active={active} inspected={setInspected} canDelete={trackedDeletions.length < maxTrackedJobs}
      close={() => { returnInspectionFocus.current = true; setSelected(""); }}
      deleted={job => { if (job) setTrackedDeletions(current => current.some(item => item.id === job.id) ? current : [...current, job]); setSelected(""); refresh(); deletions.refresh(); }} />
    </SettingsTaskScope> : null}
    {trackedCreations.length || trackedDeletions.length ? <section className="backups-panel backups-accepted" aria-label={copy("backups.acceptedBackupOperations_aab6f8")}><h2>{copy("backups.acceptedOperations_bc91ab")}</h2><p>{copy("backups.theseJobsAreObservedDirectlyIndependently_2dfe12")}</p>
      {trackedCreations.map(job => <BackupJob key={job.id} kind={BackupJobKind.Creation} accepted={job} active={active} completed={refresh} dismiss={() => setTrackedCreations(current => current.filter(item => item.id !== job.id))} />)}
      {trackedDeletions.map(job => <BackupJob key={job.id} kind={BackupJobKind.Deletion} accepted={job} active={active} completed={refresh} dismiss={() => setTrackedDeletions(current => current.filter(item => item.id !== job.id))} />)}
      {trackedCreations.length >= maxTrackedJobs || trackedDeletions.length >= maxTrackedJobs ? <p>{copy("backups.dismissCompletedTrackingEntriesToAccept_4bacdc")}</p> : null}
    </section> : null}
    <section className="backups-panel backups-history" aria-label={copy("backups.operationHistory_93bf53")}>
      <header className="backups-history-heading"><h2>{copy("backups.operationHistory_93bf53")}</h2><button hidden={historyTab !== HistoryTab.Creation} disabled={!active || Boolean(creations.loading)} onClick={creations.refreshExplicit}>{copy("backups.refreshCreationJobs_96e3e0")}</button></header>
      <div className="backups-tabs" role="tablist" aria-label={copy("backups.backupOperationHistory_8f9f32")}>{historyTabs.map(tab => <button key={tab} ref={node => { if (node) tabButtons.current.set(tab, node); else tabButtons.current.delete(tab); }} role="tab" id={`${panelId}-tab-${tab}`} aria-controls={`${panelId}-panel-${tab}`} aria-selected={historyTab === tab} tabIndex={focusedTab === tab ? 0 : -1} onFocus={() => setFocusedTab(tab)} onKeyDown={event => moveTabFocus(event, tab)} onClick={() => { setFocusedTab(tab); setHistoryTab(tab); }}>{tab === HistoryTab.Creation ? copy("backups.creationJobs_dd6156") : copy("backups.deletionJobs_74156e")}</button>)}</div>
      <div role="tabpanel" id={`${panelId}-panel-creation`} aria-labelledby={`${panelId}-tab-creation`} hidden={historyTab !== HistoryTab.Creation}>
        <p>{copy("backups.creationContinuesAfterThisClientDisconnects_03823b")}</p>
        <Problem error={creations.error?.failure} />{creations.error && creations.loaded ? <p>{copy("backups.previousCreationObservationsAreShownCurrent_feb522")}</p> : null}
        {!creations.loaded && Boolean(creations.loading) ? <p role="status">{copy("backups.readingCreationJobs_012c61")}</p> : null}{creations.loaded && Boolean(creations.loading) ? <p role="status">{copy("backups.updatingCreationJobs_d8faea")}</p> : null}
        {creations.loaded && creations.rows.length === 0 ? <EmptyHistory>{copy("backups.noCreationJobsOnThisPage_3c155f")}</EmptyHistory> : null}
        <ScrollPayloadWindow query={creations} root={root} active={active && historyTab === HistoryTab.Creation} identity={job => job.id} revision={job => job.revision}>{jobs => jobs.map(job => <article className="backups-job" key={job.id}><h3><LocalizedText id="backups.backup_181f9b" components={{ s0: <>{job.backupId}</> }} /></h3><p><LocalizedText id="backups.jobRevision_b89f71" components={{ s0: <>{job.id}</>, s1: <>{job.revision.toString()}</> }} /></p><p>{job.state === BackupCreationState.SUCCEEDED ? copy("backups.backupCreationCompleted_5ecd17") : job.state === BackupCreationState.PENDING ? copy("backups.backupCreationPending_74003f") : job.state === BackupCreationState.FAILED ? copy("backups.backupCreationFailed_f70ce5") : copy("backups.creationStateUnavailable_9f451d")}</p>{job.problemCode ? <p><LocalizedText id="backups.creationNeedsAttention_e81368" components={{ s0: <>{job.problemCode}</> }} /></p> : null}</article>)}</ScrollPayloadWindow>
        <ScrollContinuation query={creations} root={root} active={active && historyTab === HistoryTab.Creation} label={copy("backups.creationJobs_dd6156")} />
      </div>
      <div role="tabpanel" id={`${panelId}-panel-deletion`} aria-labelledby={`${panelId}-tab-deletion`} hidden={historyTab !== HistoryTab.Deletion}>
        <p>{copy("backups.acceptedJobsSurviveServerRestartFailed_3787c0")}</p>
        <Problem error={deletions.error?.failure} />{deletions.error && deletions.loaded ? <p>{copy("backups.previousDeletionObservationsAreShownCurrent_b3d9d1")}</p> : null}
        {!deletions.loaded && Boolean(deletions.loading) ? <p role="status">{copy("backups.readingDeletionJobs_24fef4")}</p> : null}{deletions.loaded && Boolean(deletions.loading) ? <p role="status">{copy("backups.updatingDeletionJobs_de4b91")}</p> : null}
        {deletions.loaded && deletions.rows.length === 0 ? <EmptyHistory>{copy("backups.noDeletionJobsOnThisPage_c7d742")}</EmptyHistory> : null}
        <ScrollPayloadWindow query={deletions} root={root} active={active && historyTab === HistoryTab.Deletion} identity={job => job.id} revision={job => job.revision}>{jobs => jobs.map(job => <article className="backups-job" key={job.id}><h3><LocalizedText id="backups.backup_181f9b" components={{ s0: <>{job.backupId}</> }} /></h3><p><LocalizedText id="backups.jobRevision_b89f71" components={{ s0: <>{job.id}</>, s1: <>{job.revision.toString()}</> }} /></p><p>{job.state === BackupDeletionState.SUCCEEDED ? copy("backups.deletionCompleted_80a98a") : job.state === BackupDeletionState.PENDING ? copy("backups.deletionPending_614524") : copy("backups.deletionStateUnavailable_ea52ae")}</p>{job.problemCode ? <p><LocalizedText id="backups.cleanupNeedsAttention_650005" components={{ s0: <>{job.problemCode}</> }} /></p> : null}{job.state === BackupDeletionState.SUCCEEDED ? <p>{job.removalObserved ? copy("backups.imageBytesRemoved_9f2ac1", { v0: formatNumber(job.imageBytes) }) : copy("backups.imageAbsenceConfirmedOriginalUnlinkByte_435895")}</p> : null}</article>)}</ScrollPayloadWindow>
        <ScrollContinuation query={deletions} root={root} active={active && historyTab === HistoryTab.Deletion} label={copy("backups.deletionJobs_74156e")} />
      </div>
    </section>
  </section>;
}

function BackupInspection({ selected, active, canDelete, close, deleted, inspected }: {
  selected: string; active: boolean; canDelete: boolean; close: () => void; deleted: (job?: BackupDeletionJob) => void; inspected: (id: string) => void;
}) {
  useLocale();
  const heading = useRef<HTMLHeadingElement>(null);
  useLayoutEffect(() => { heading.current?.focus(); }, []);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [confirmation, setConfirmation] = useState<InspectBackupResponse>();
  const inspection = useQuery(SystemQuery.inspectBackup, { id: selected }, { enabled: active && Boolean(selected), retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const remove = useRetainedMutation("backup-delete", SystemQuery.deleteBackup, result => deleted(result.job));
  const checked = inspection.data?.backup?.id === selected && !inspection.error && !inspection.isFetching ? inspection.data : undefined;
  useEffect(() => { inspected(checked ? selected : ""); }, [checked, selected, inspected]);
  const confirm = active && checked !== undefined && confirmation === checked;
  useEffect(() => {
    if (!active || inspection.isFetching || (confirmation !== undefined && confirmation !== inspection.data)) { setConfirmation(undefined); if (inspection.isFetching) setDeleteOpen(false); }
  }, [active, inspection.data, inspection.isFetching, confirmation]);
  return <SettingsTaskDialog title={copy("backups.backupIntegrityInspection_6b0728")} size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={close}><section className="backups-panel backups-inspection" aria-label={copy("backups.backupIntegrityInspection_6b0728")}>
      <h2 ref={heading} tabIndex={-1}><LocalizedText id="backups.inspection_39c73e" components={{ s0: <>{selected}</> }} /></h2>
      <Problem error={inspection.error} />
      {inspection.isFetching ? <p role="status">{copy("backups.checkingTheSelectedBackup_ca89e6")}</p> : null}
      {checked ? <>
        <p role="status">{copy("backups.databaseIntegrityAndOriginalServerIdentity_26b0d4")}</p>
        <dl><div><dt>{copy("backups.backupId_8c6f39")}</dt><dd><code>{checked.backup!.id}</code></dd></div><div><dt>{copy("backups.modifiedUtc_d81af6")}</dt><dd><time dateTime={checked.backup!.modifiedAt}>{formatTimestamp(checked.backup!.modifiedAt)}</time></dd></div><div><dt>{copy("backups.size_1af851")}</dt><dd><LocalizedText id="backups.bytes_e17732" components={{ s0: <>{formatNumber(checked.backup!.sizeBytes)}</> }} /></dd></div><div><dt>{copy("backups.schema_07b091")}</dt><dd>{checked.schemaVersion}</dd></div><div><dt>{copy("backups.sha256_bbd07c")}</dt><dd><code>{checked.sha256}</code></dd></div></dl>
        <p>{copy("backups.thisObservationDoesNotRestoreData_2a506c")}</p>
        <button type="button" className="backups-destructive" onClick={() => setDeleteOpen(true)}>{copy("backups.deleteSelectedBackup_4ad7f5")}</button>
        {deleteOpen ? <SettingsTaskDialog title={copy("backups.permanentBackupDeletion_7640fe")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => { setDeleteOpen(false); setConfirmation(undefined); }}><fieldset className="backups-deletion"><legend>{copy("backups.permanentBackupDeletion_7640fe")}</legend><p>{copy("backups.theSelectedImageWillBeDeleted_214572")}</p><label><input type="checkbox" checked={confirm} disabled={remove.busy || remove.uncertain} onChange={event => setConfirmation(event.target.checked ? checked : undefined)} /><LocalizedText id="backups.iConfirmPermanentDeletionOfBackup_6eefd3" components={{ s0: <>{selected}</> }} /></label><Problem error={remove.error} /><SettingsTaskActions><button className="backups-destructive" disabled={!active || !confirm || remove.busy || remove.uncertain || !canDelete} onClick={() => { void remove.send({ requestId: newRequestId(), backup: checked.backup!, sha256: checked.sha256 }); }}>{copy("backups.permanentlyDeleteSelectedBackup_6bc00c")}</button><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={() => { setDeleteOpen(false); setConfirmation(undefined); }}>{copy("backups.keepBackup_1210fc")}</SettingsTaskDismissButton>{remove.uncertain ? <button disabled={!active || remove.busy} onClick={remove.retry}>{copy("backups.retryTheSameBackupDeletion_0f7462")}</button> : null}</SettingsTaskActions></fieldset></SettingsTaskDialog> : null}
      </> : null}
      <SettingsTaskActions className="backups-actions"><button disabled={!active || inspection.isFetching} onClick={() => { setConfirmation(undefined); void inspection.refetch(); }}>{copy("backups.recheckSelectedBackup_2db613")}</button><SettingsTaskDismissButton data-settings-task-cancel disabled={remove.busy || remove.uncertain} onClick={close}>{copy("backups.closeBackupInspection_acadd7")}</SettingsTaskDismissButton></SettingsTaskActions>
      <Problem error={remove.error} />{remove.uncertain ? <button disabled={!active || remove.busy} onClick={remove.retry}>{copy("backups.retryTheSameBackupDeletion_0f7462")}</button> : null}
    </section></SettingsTaskDialog>;
}
