// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskActions } from "./settings-task";
import { useRetainSettingsTask, useSettingsTaskVisible, useCloseSettingsTask, useInSettingsTask } from "./settings-task-context";
import { useCallback, useEffect, useRef, useState } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ResourceQuery, ResourceService, SystemCapability, SystemQuery, WorkerQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { RepositoryFields, ResourceChoice, TextField, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { JobState, TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { useSettingsOpening } from "./settings-lifetime";
import type { ReadLocalWorkerProof } from "./local-worker";
import { LocalWorkerAction, LocalWorkerState, type ControlLocalWorker } from "./local-worker-controls";
import { Problem } from "./ui";
import "./repository-registration.css";
import { RepositoryCloneFields, repositoryCloneURL, repositoryCloneDirectory, repositoryCloneParent, type RepositoryCloneDraft } from "./repository-clone-fields";

import { RepositoryGitHubPicker, type GitHubCloneSelection } from "./repository-github-picker";

export type ChooseRepositoryFolder = () => Promise<string | null>;
enum Computer { Local = "local", Remote = "remote" }
enum SelectionStage { Picker, Worker }
interface Source { path: string; machine: string; name: string; local: boolean }
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function folderSelectionProblem(error: unknown): string {
  if (error instanceof ConnectError) return error.rawMessage;
  switch (error) {
    case "busy": return "Another window is choosing a folder. Wait for it to finish, then try Choose folder again.";
    case "invalid-evidence": return "The selected folder cannot be used. Choose another folder or enter a valid absolute checkout path.";
    case "permission-denied": return "Folder selection was denied. Check this window's authorization and folder access, then try Choose folder again.";
    default: return "Folder selection failed. Try Choose folder again or enter an absolute checkout path.";
  }
}

export function selectedInspectionRemote(output: Document, preferred: string): string {
  const remotes = items(output.remotes).map(text);
  return preferred || (remotes.includes("origin") ? "origin" : remotes.length === 1 ? remotes[0] : "");
}
// Validate the whole observation before deriving configuration, including legacy
// omission. This presentation check grants no filesystem or GitHub authority.
export function validRepositoryInspection(output: Document): boolean {
  if (Object.keys(output).some(key => !["root", "name", "remotes", "default_refs", "github_repositories"].includes(key))) return false;
  if (!text(output.root) || text(output.root).length > 4096 || !text(output.name) || text(output.name).length > 256) return false;
  if (!Array.isArray(output.remotes) || output.remotes.length > 128 || output.remotes.some(remote => typeof remote !== "string" || !remote || remote.length > 256)) return false;
  const remotes = items(output.remotes).map(text);
  if (new Set(remotes).size !== remotes.length) return false;
  if (!output.default_refs || typeof output.default_refs !== "object" || Array.isArray(output.default_refs)) return false;
  if (Object.entries(object(output.default_refs)).some(([remote, ref]) => !remotes.includes(remote) || !text(ref) || text(ref).length > 4096)) return false;
  if (output.github_repositories !== undefined && (!output.github_repositories || typeof output.github_repositories !== "object" || Array.isArray(output.github_repositories))) return false;
  return Object.entries(object(output.github_repositories)).length <= 128 && Object.entries(object(output.github_repositories)).every(([remote, raw]) => {
    const value = object(raw);
    return remotes.includes(remote) && Object.keys(value).length === 2 && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(value.owner)) && /^[A-Za-z0-9_.-]{1,100}$/.test(text(value.name)) && ![".", ".."].includes(text(value.name));
  });
}

function InspectionCompletion({ state, output, completed }: { state: string; output: Document; completed: (output: Document) => void }) {
  useEffect(() => { if (state === JobState.Succeeded) completed(output); }, [state, output, completed]);
  return null;
}
function SaveCompletion({ state, saved }: { state: string; saved: () => void }) {
  useEffect(() => { if (state === JobState.Succeeded) saved(); }, [state, saved]);
  return null;
}

export function RepositoryRegistration({ active, readLocalWorker, controlLocalWorker, chooseFolder, saved, cancel }: {
  active: boolean; readLocalWorker?: ReadLocalWorkerProof; controlLocalWorker?: ControlLocalWorker; chooseFolder?: ChooseRepositoryFolder; saved: () => void; cancel: () => void;
}) {
  const opening = useSettingsOpening();
  const alive = useRef(false), gate = useRef(false), initialAction = useRef<HTMLInputElement>(null);
  const [data, setData] = useState<Document>(() => newConfiguration(EntityKind.REPOSITORY));
  const [manual, setManual] = useState(false), [computer, setComputer] = useState(Computer.Local);
  const [path, setPath] = useState(""), [machine, setMachine] = useState(""), [machineName, setMachineName] = useState("");
  const [busy, setBusy] = useState(false), [problem, setProblem] = useState("");
  const [source, setSource] = useState<Source>();
  const [inspection, setInspection] = useState<{ job: Resource; source: Source }>();
  const [summary, setSummary] = useState<{ output: Document; source: Source }>();
  const [unknown, setUnknown] = useState(false), [saveJob, setSaveJob] = useState<Resource | "unknown">();
  const [connectFolder, setConnectFolder] = useState(false), [cloneLocally, setCloneLocally] = useState(false);
  const nameEdited = useRef(false);
  const [options, setOptions] = useState(false), [childPending, setChildPending] = useState(false);
  const [cloneDraft, setCloneDraft] = useState<RepositoryCloneDraft>({ url: "", parent: "" });
  const [cloneJob, setCloneJob] = useState<Resource | "unknown">();
  const [githubSelection, setGitHubSelection] = useState<GitHubCloneSelection>();
  const transport = useTransport();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const remoteSupported = status.data?.capabilities.includes(SystemCapability.REMOTE_REPOSITORIES_V1) === true;
  const cloneSupported = status.data?.capabilities.includes(SystemCapability.REPOSITORY_CLONE_V1) === true && Boolean(readLocalWorker);
  const pickerSupported = status.data?.capabilities.includes(SystemCapability.GITHUB_REPOSITORY_PICKER_V1) === true;
  const clone = useRetainedMutation("repository-add:clone", WorkerQuery.cloneRepository, (result, request) => {
    if (result.job && result.job.kind === EntityKind.JOB && text(document(result.job).machine_id) === request.machineId && text(document(result.job).type) === "clone-repository") setCloneJob(result.job);
    else setCloneJob("unknown");
  }, (result, request) => Boolean(result.requestId === request.requestId && result.job && uuid.test(result.job.id) && result.job.kind === EntityKind.JOB && text(document(result.job).machine_id) === request.machineId && text(document(result.job).type) === "clone-repository"));
  const pendingSource = useRef<Source | undefined>(undefined);
  const inspect = useRetainedMutation("repository-add:inspect", WorkerQuery.inspectRepository, (result, request) => {
    const selected = pendingSource.current;
    if (selected && selected.machine === request.machineId && result.job && text(document(result.job).machine_id) === selected.machine) setInspection({ job: result.job, source: selected });
    else setUnknown(true);
  });
  const save = useRetainedMutation("repository-add:save", ConfigurationQuery.saveConfiguration, (result) => {
    if (result.job) setSaveJob(result.job);
    else setSaveJob("unknown");
  });
  const serverMachine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: source?.machine ?? "" }, { enabled: active && Boolean(source), refetchInterval: active && source ? 5000 : false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const workerData = document(serverMachine.data?.resource);
  const lastSeen = Date.parse(text(workerData.last_seen));
  const offline = Boolean(serverMachine.data?.resource && Number.isFinite(lastSeen) && Date.now() - lastSeen > 45_000);
  const blocked = busy || clone.busy || clone.uncertain || Boolean(cloneJob) || inspect.busy || inspect.uncertain || Boolean(inspection) || unknown || save.busy || save.uncertain || Boolean(saveJob) || childPending;
  const taskVisible = useSettingsTaskVisible(), cancelTask = useCloseSettingsTask(cancel), inTask = useInSettingsTask();
  useRetainSettingsTask(Boolean(saveJob || cloneJob) || unknown || childPending);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => { if (taskVisible) initialAction.current?.focus(); }, [taskVisible]);
  const live = () => alive.current && !opening?.disposed;
  const native = <T,>(operation: () => Promise<T>) => opening ? opening.native(operation) : operation();
  const inspectSource = async (selected: Source) => {
    if (!live()) return;
    pendingSource.current = selected;
    setSource(selected);
    await inspect.send({ requestId: newRequestId(), machineId: selected.machine, path: selected.path, preferredRemote: "" });
  };
  const verifyLocal = async () => {
    if (!readLocalWorker) throw new ConnectError("This computer's Worker verification is unavailable. Check Runner Devices and retry.", Code.Unavailable);
    const proof = await native(readLocalWorker);
    if (!live()) throw new ConnectError("Settings closed.", Code.Canceled);
    if (!uuid.test(proof.machineId) || !/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(proof.token)) throw new ConnectError("This computer's Worker proof is invalid. Check its registration and retry.", Code.FailedPrecondition);
    // Retain only the non-secret machine identity. Status is a separate native
    // read, never a registration/start action or a substitute-machine fallback.
    const id = proof.machineId;
    if (controlLocalWorker) {
      const status = await native(() => controlLocalWorker(LocalWorkerAction.Status));
      if (!live()) throw new ConnectError("Settings closed.", Code.Canceled);
      if (status.machine_id !== id) throw new ConnectError("This computer's Worker changed. Verify its registration and retry.", Code.FailedPrecondition);
      if (status.state === LocalWorkerState.Uncertain) throw new ConnectError("This computer's Worker exit is unconfirmed. Inspect its private log and original session recovery before retrying.", Code.FailedPrecondition);
      if ([LocalWorkerState.NotStarted, LocalWorkerState.Exited, LocalWorkerState.Stopping].includes(status.state)) throw new ConnectError(status.state === LocalWorkerState.Exited ? "This computer's Worker exited. Open Runner Devices to start it, then retry." : "This computer's Worker is stopped. Open Runner Devices to start it, then retry.", Code.FailedPrecondition);
    }
    return proof;
  };
  const start = async (picker: boolean) => {
    if (gate.current || blocked || !live()) return;
    gate.current = true; setBusy(true);
    let stage = picker ? SelectionStage.Picker : SelectionStage.Worker;
    try {
      const selectedPath = picker ? await native(chooseFolder ?? (() => Promise.reject(new ConnectError("Folder selection is unavailable. Enter a path to continue.", Code.Unavailable)))) : path;
      if (!live() || selectedPath === null) return;
      setProblem("");
      if (!selectedPath || selectedPath.length > 4096 || selectedPath.includes("\0")) throw new ConnectError(picker ? folderSelectionProblem("invalid-evidence") : "Enter a bounded absolute checkout path.", Code.InvalidArgument);
      // A picker failure has no accepted selection or Worker observation. Only
      // a validated selection advances to Worker verification and replaces draft state.
      stage = SelectionStage.Worker;
      setPath(selectedPath);
      setSummary(undefined); setData(current => ({ ...current, checkouts: [] })); setOptions(false);
      setSource(undefined);
      if (picker) setComputer(Computer.Local);
      const local = picker || computer === Computer.Local;
      const selectedMachine = local ? (await verifyLocal()).machineId : machine;
      if (!live()) return;
      if (!uuid.test(selectedMachine)) throw new ConnectError("Select the computer that owns this checkout.", Code.InvalidArgument);
      await inspectSource({ path: selectedPath, machine: selectedMachine, name: local ? "This computer" : machineName || "Selected remote computer", local });
    } catch (error) {
      if (live()) setProblem(stage === SelectionStage.Picker ? folderSelectionProblem(error) : error instanceof ConnectError ? error.rawMessage : error === "permission-denied" ? "Access to this computer's Worker was denied. Check device authorization and private-state permissions, then retry." : "This computer's Worker could not be verified. Check its registration and connection in Runner Devices, then retry. The selected folder is retained.");
    } finally { gate.current = false; if (live()) setBusy(false); }
  };
  const completeInspection = useCallback((output: Document) => {
    if (!inspection || !alive.current || opening?.disposed) return;
    if (!validRepositoryInspection(output)) { setUnknown(true); setProblem("The inspection summary is unreadable. Inspect the original operation before continuing."); return; }
    setData(current => ({ ...current, checkouts: [{ machine_id: inspection.source.machine, path: text(output.root) }] }));
    setSummary({ output, source: inspection.source });
    setOptions(false); setManual(false); setInspection(undefined);
  }, [inspection, opening]);
  const change = (next: Document) => {
    if (encode(next).byteLength > 1 << 20) { setProblem("This configuration is too large. Reduce its options."); return; }
    if (next.name !== data.name) nameEdited.current = true;
    setData(next); setProblem("");
  };
  const browseCloneParent = async () => {
    if (gate.current || blocked || !live()) return;
    gate.current = true; setBusy(true);
    try {
      const selected = await native(chooseFolder ?? (() => Promise.reject(new ConnectError("Folder selection is unavailable. Enter a path to continue.", Code.Unavailable))));
      if (!live() || selected === null) return;
      setCloneDraft(current => ({ ...current, parent: selected })); setProblem("");
    } catch (error) { if (live()) setProblem(folderSelectionProblem(error)); }
    finally { gate.current = false; if (live()) setBusy(false); }
  };
  const changeCloneDraft = (next: RepositoryCloneDraft) => {
    if (blocked) return;
    const identity = repositoryCloneURL(next.url);
    if (githubSelection && (identity?.githubOwner.toLowerCase() !== githubSelection.owner.toLowerCase() || identity?.githubName.toLowerCase() !== githubSelection.name.toLowerCase())) setGitHubSelection(undefined);
    setCloneDraft(next);
    setData(current => {
      const result: Document = { ...current, remote_url: next.url, name: nameEdited.current ? current.name : identity?.directory ?? "", github_owner: identity?.githubOwner ?? "", github_name: identity?.githubName ?? "" };
      if (githubSelection && (identity?.githubOwner.toLowerCase() !== githubSelection.owner.toLowerCase() || identity?.githubName.toLowerCase() !== githubSelection.name.toLowerCase())) { delete result.integration_id; delete result.github_owner; delete result.github_name; }
      return result;
    });
    setProblem("");
  };
  const parsedClone = repositoryCloneURL(cloneDraft.url), cloneDirectory = cloneDraft.directory ?? parsedClone?.directory ?? "";
  const cloneReady = Boolean(cloneSupported && parsedClone && repositoryCloneParent(cloneDraft.parent) && repositoryCloneDirectory(cloneDirectory));
  const startClone = async () => {
    if (!cloneReady || blocked || gate.current || !live()) return;
    gate.current = true; setBusy(true); setProblem("");
    try {
      const proof = await verifyLocal();
      if (!live()) return;
      const response = await createClient(ResourceService, transport).getResource({ kind: EntityKind.MACHINE, id: proof.machineId });
      if (!live()) return;
      if (!items(document(response.resource).worker_capabilities).includes("repository-clone-v1")) throw new ConnectError("Update and reconnect this computer's Worker to clone repositories. Existing folder registration remains available.", Code.Unimplemented);
      await clone.send({ requestId: newRequestId(), machineId: proof.machineId, localWorkerToken: proof.token, parentPath: cloneDraft.parent, url: cloneDraft.url, directoryName: cloneDirectory, githubSelection });
    } catch (error) { if (live()) setProblem(error instanceof ConnectError ? error.rawMessage : "This computer's Worker could not be verified. Check Runner Devices and retry."); }
    finally { gate.current = false; if (live()) setBusy(false); }
  };
  const remote = summary ? selectedInspectionRemote(summary.output, text(data.preferred_remote)) : "";
  const github = summary ? object(object(summary.output.github_repositories)[remote]) : {};
  const primaryCheckout = summary ? { machine_id: summary.source.machine, path: text(summary.output.root) } : undefined;
  // The confirmation and inferred metadata describe this exact inspected checkout.
  // Additional checkout edits cannot make a different checkout its silent replacement.
  const ready = Boolean(remoteSupported && parsedClone && text(data.name) && new TextEncoder().encode(text(data.name)).byteLength <= 256 && (!primaryCheckout || items(data.checkouts).some(raw => {
    const checkout = object(raw);
    return checkout.machine_id === primaryCheckout.machine_id && checkout.path === primaryCheckout.path;
  })));
  return <section className="repository-registration" aria-label="Add repository">
    <button type="button" disabled={blocked} onClick={cancelTask}>Back to repositories</button>
    <h2 hidden={inTask}>Add repository</h2>
    {cloneJob ? <><h3>Repository clone accepted</h3>{cloneJob === "unknown" ? <p role="alert">The clone was acknowledged without a readable job. Inspect its original request before another clone.</p> : <TrackedJob initial={cloneJob} active={active}>{(state, output) => <><SaveCompletion state={state} saved={saved} />{text(object(output.inspection).root) && state !== JobState.Succeeded ? <p role="alert">Checkout preserved at {text(object(output.inspection).root)}. Registration did not complete. Add this existing folder after resolving the reported problem.</p> : null}{state === JobState.Failed || state === JobState.Canceled ? <button type="button" onClick={() => setCloneJob(undefined)}>Return to clone draft</button> : null}</>}</TrackedJob>}<SettingsTaskActions><button type="button" data-settings-task-cancel onClick={cancelTask}>Cancel</button></SettingsTaskActions></> : saveJob ? <><h3>Repository save accepted</h3>{saveJob === "unknown" ? <p role="alert">The save was acknowledged without a readable job. Inspect its receipt before another save.</p> : <TrackedJob initial={saveJob} active={active}>{state => <><SaveCompletion state={state} saved={saved} />{state === JobState.Failed || state === JobState.Canceled ? <button type="button" onClick={() => setSaveJob(undefined)}>Return to current draft</button> : null}</>}</TrackedJob>}<SettingsTaskActions><button type="button" data-settings-task-cancel onClick={cancelTask}>Cancel</button></SettingsTaskActions></> : <>
      <section className="repository-remote-fields" aria-label="Remote repository">
        <label>Git URL<input ref={initialAction} type="text" value={cloneDraft.url} maxLength={4096} placeholder="https://github.com/owner/repository.git" disabled={blocked} autoComplete="off" spellCheck={false} onChange={event => changeCloneDraft({ ...cloneDraft, url: event.target.value, directory: undefined })} /></label>
        {cloneDraft.url && !parsedClone ? <p role="alert">Enter a credential-free HTTPS or SSH Git URL.</p> : null}
        <RepositoryGitHubPicker active={active} supported={pickerSupported} disabled={blocked} choose={(selection, url) => { changeCloneDraft({ ...cloneDraft, url, directory: undefined }); setGitHubSelection(selection); setData(current => ({ ...current, integration_id: selection.profileId, github_owner: selection.owner, github_name: selection.name })); }} />
        <TextField label="Repository name" value={data.name} required change={name => { nameEdited.current = true; change({ ...data, name }); }} />
        <p>The selected Runner Device clones this repository when a Worktree session starts.</p>
        {!remoteSupported ? <p role="status">Update the selected server to add repositories by URL.</p> : null}
        <Problem error={status.error} />
      </section>
      <button type="button" disabled={blocked} aria-expanded={connectFolder} onClick={() => setConnectFolder(value => !value)}>Connect a Local folder (optional)</button>
      {connectFolder ? <>
      {summary ? <section className="repository-summary" aria-label="Repository detected">
        <div className="repository-summary-heading"><div><h3>{text(data.name)}</h3><p>Repository detected</p></div><button type="button" disabled={blocked} onClick={() => void start(true)}>Change folder</button></div>
        <p className="repository-path">{text(summary.output.root)}</p>
        <dl><div><dt>Computer</dt><dd>{summary.source.name}</dd></div><div><dt>Git remote</dt><dd>{remote || "Unavailable"}</dd></div><div><dt>Default branch</dt><dd>{text(object(summary.output.default_refs)[remote]) || "Unavailable locally"}</dd></div><div><dt>GitHub</dt><dd>{text(github.owner) && text(github.name) ? `${text(github.owner)}/${text(github.name)}` : "Unavailable"}</dd></div></dl>
        <p>GitHub detection is metadata only. Select a profile in Optional settings to configure access.</p>
      </section> : <section className="repository-folder-card"><h3>Local folder</h3><p>Add an existing Git repository on this computer.</p><div className="repository-folder-actions"><button type="button" disabled={blocked || !chooseFolder} onClick={() => void start(true)}>Choose folder</button><button type="button" disabled={blocked} aria-expanded={manual} onClick={() => setManual(value => !value)}>Enter a path…</button></div></section>}
      {summary && !manual ? <button type="button" disabled={blocked} onClick={() => setManual(true)}>Enter a path…</button> : null}
      {manual || (!summary && path) ? <fieldset disabled={blocked}><legend>Repository folder</legend><label>Computer<select value={computer} onChange={event => setComputer(event.target.value as Computer)}><option value={Computer.Local}>This computer</option><option value={Computer.Remote}>Another computer</option></select></label>{computer === Computer.Remote ? <ResourceChoice label="Runner Device" kind={EntityKind.MACHINE} value={machine} active={active} showStatus change={(id, value, resource) => { setMachine(id); setMachineName(resource ? resourceName(resource) : text(value?.name)); }} /> : null}<TextField label="Absolute checkout path" value={path} max={4096} change={setPath} /><button type="button" disabled={!path || (computer === Computer.Remote && !machine)} onClick={() => void start(false)}>Inspect folder</button></fieldset> : null}
      </> : null}
      <button type="button" disabled={blocked} aria-expanded={cloneLocally} onClick={() => setCloneLocally(value => !value)}>Clone to this computer (optional)</button>
      {cloneLocally ? <><RepositoryCloneFields draft={cloneDraft} change={changeCloneDraft} busy={blocked} showURL={false} browse={() => void browseCloneParent()} supported={cloneSupported} /><button type="button" disabled={blocked || !cloneReady} onClick={() => void startClone()}>Clone &amp; add repository</button></> : null}
      {inspection ? <TrackedJob initial={inspection.job} active={active}>{(state, output) => <><InspectionCompletion state={state} output={output} completed={completeInspection} />{state === JobState.Failed || state === JobState.Canceled ? <button type="button" onClick={() => setInspection(undefined)}>Return to selected folder</button> : null}</>}</TrackedJob> : null}
      {busy || inspect.busy ? <p role="status">{busy ? "Selecting folder and verifying this computer…" : "Inspecting repository…"}</p> : null}
      {offline ? <p role="status">The selected Worker is registered; its server heartbeat is offline. Keep this folder and check Runner Devices before retrying.</p> : null}
      {serverMachine.error ? <Problem error={serverMachine.error} /> : null}
      {unknown ? <p role="alert">Inspection was acknowledged without a readable result. Observe the original operation before another request.</p> : null}
      {ready ? <><button type="button" className="repository-options-toggle" aria-expanded={options} aria-controls="repository-options" onClick={() => setOptions(value => !value)}>Optional settings</button><div id="repository-options" hidden={!options}><fieldset disabled={save.busy || save.uncertain || busy || Boolean(inspection) || inspect.uncertain}><RepositoryFields data={data} change={change} active={active && options} existing={false} pendingOperation={setChildPending} registration requiredCheckout={primaryCheckout} /></fieldset></div></> : null}
      {problem ? <p role="alert">{problem}</p> : null}<Problem error={inspect.error || save.error || clone.error} />
      {inspect.uncertain ? <button type="button" disabled={inspect.busy} onClick={inspect.retry}>Retry the same inspection</button> : null}
      <SettingsTaskActions className="repository-add-footer"><button type="button" data-settings-task-cancel onClick={cancelTask}>Cancel</button><button type="button" className="primary" disabled={blocked || !ready} onClick={() => void save.send({ mutation: { requestId: newRequestId(), expectedRevision: 0n }, kind: EntityKind.REPOSITORY, schemaVersion: 1, documentJson: encode(data) })}>Add repository</button>{clone.uncertain ? <button type="button" disabled={clone.busy} onClick={clone.retry}>Retry the same clone request</button> : null}{save.uncertain ? <button type="button" disabled={save.busy} onClick={save.retry}>Retry the same repository save</button> : null}</SettingsTaskActions>
    </>}
  </section>;
}
