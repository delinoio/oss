// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { DisclosureButton, DisclosureContent, DisclosureDensity, Disclosure, DisclosureSummary } from "./disclosure";
import { useId } from "react";
import { LocalConnectionHelp } from "./local-connection-presentation";
import { RunnerTaskRemediation } from "./session-runner-remediation";
import { RunnerWorkflow, useRunnerPreference } from "./runner-device-preferences";
import { SettingsTaskDismissButton } from "./settings-task";
import { SettingsTaskActions } from "./settings-task";
import { useSettingsTaskVisible, useCloseSettingsTask, useInSettingsTask } from "./settings-task-context";
import { useCallback, useEffect, useRef, useState } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ResourceQuery, ResourceService, SystemCapability, SystemQuery, WorkerQuery, supportsResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { RepositoryFields, ResourceChoice, TextField, newConfiguration } from "./configuration-fields";
import { document, encode, items, object, resourceName, text, type Document } from "./documents";
import { JobState, TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { useSettingsOpening } from "./settings-lifetime";
import type { ReadLocalWorkerProof } from "./local-worker";
import { LocalWorkerAction, LocalWorkerState, type ControlLocalWorker } from "./local-worker-controls";
import { Problem } from "./ui";
import { copy, useLocale, ownedMessage, ProductError, useProductMessage, type OwnedMessage } from "./localization";
import "./repository-registration.css";
import { RepositoryCloneFields, repositoryCloneURL, repositoryCloneDirectory, repositoryCloneParent, type RepositoryCloneDraft } from "./repository-clone-fields";

import { RepositoryGitHubPicker, type GitHubCloneSelection } from "./repository-github-picker";

export type ChooseRepositoryFolder = () => Promise<string | null>;
enum Computer { Local = "local", Remote = "remote" }
enum SelectionStage { Picker, Worker }
interface Source { path: string; machine: string; name: string; local: boolean }
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

class WorkerVerificationProblem extends ProductError {}
function workerVerificationProblem(error: unknown): OwnedMessage {
  if (error instanceof ProductError) return error.productMessage;
  if (error === "permission-denied" || error instanceof ConnectError && error.code === Code.PermissionDenied) return ownedMessage("repository-registration.workerPermission");
  return ownedMessage("repository-registration.workerUnavailable");
}
function folderSelectionProblem(error: unknown): OwnedMessage {
  if (error instanceof ProductError) return error.productMessage;
  if (error instanceof ConnectError) return folderSelectionProblem(error.code === Code.PermissionDenied ? "permission-denied" : "unexpected");
  switch (error) {
    case "busy": return ownedMessage("repository-registration.extra.725492fb2d32");
    case "invalid-evidence": return ownedMessage("repository-registration.extra.79de1a72a2e7");
    case "permission-denied": return ownedMessage("repository-registration.extra.0cb2cb5d6d57");
    default: return ownedMessage("repository-registration.extra.3a5c3abe26a1");
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
export interface RegisteredRepository { id: string; revision: bigint }
export function confirmedRepository(output: Document, clone = false): RegisteredRepository | undefined {
  const id = output[clone ? "repository_id" : "id"], revision = output[clone ? "repository_revision" : "revision"];
  if (typeof id !== "string" || !uuid.test(id)) return;
  const decimal = typeof revision === "number" && Number.isSafeInteger(revision) ? String(revision) : typeof revision === "string" ? revision : "";
  if (!/^[1-9][0-9]{0,19}$/.test(decimal) || BigInt(decimal) > 18446744073709551615n) return;
  return { id, revision: BigInt(decimal) };
}
function SaveCompletion({ state, output, clone = false, saved }: { state: string; output: Document; clone?: boolean; saved: (repository: RegisteredRepository) => void }) {
  const completed = useRef(false), identity = confirmedRepository(output, clone);
  useEffect(() => { if (state === JobState.Succeeded && identity && !completed.current) { completed.current = true; saved(identity); } }, [state, identity?.id, identity?.revision, saved]);
  return state === JobState.Succeeded && !identity ? <p role="alert">{copy("repository-registration.confirmationUnavailable")}</p> : null;
}
function repositoryRegistrationDocument(data: Document, legacy: boolean): Document {
  if (!legacy) return data;
  // Servers before capability 37 reject the URL-only field as an unknown
  // property. Their inspected-folder registration contract already accepts
  // the remaining repository document, so omit only the newly allocated field.
  const { remote_url: _remoteURL, ...legacyData } = data;
  return legacyData;
}

export function RepositoryRegistration({ active, readLocalWorker, controlLocalWorker, chooseFolder, saved, cancel }: {
  active: boolean; readLocalWorker?: ReadLocalWorkerProof; controlLocalWorker?: ControlLocalWorker; chooseFolder?: ChooseRepositoryFolder; saved: (repository: RegisteredRepository) => void; cancel: () => void;
}) {
  const disclosureContentId1 = useId();
  const disclosureContentId2 = useId();
  const manualContentId = useId();
  useLocale();
  const opening = useSettingsOpening();
  const activeRef = useRef(active); activeRef.current = active;
  const alive = useRef(false), gate = useRef(false), initialAction = useRef<HTMLInputElement>(null);
  const [data, setData] = useState<Document>(() => newConfiguration(EntityKind.REPOSITORY));
  const [manual, setManual] = useState(false), [computer, setComputer] = useState(Computer.Local);
  const [path, setPath] = useState(""), [machine, setMachine] = useState(""), [machineName, setMachineName] = useState("");
  const runner = useRunnerPreference(RunnerWorkflow.RemoteRepository, active && computer === Computer.Remote, undefined, true);
  const runnerTouched = useRef(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useProductMessage("");
  const [workerProblem, setWorkerProblem] = useState(false), [verifiedAgain, setVerifiedAgain] = useState(false);
  const [verifiedMachine, setVerifiedMachine] = useState("");
  const [runnerRemediationPending, setRunnerRemediationPending] = useState(false);
  const [source, setSource] = useState<Source>();
  const [inspection, setInspection] = useState<{ job: Resource; source: Source }>();
  const [summary, setSummary] = useState<{ output: Document; source: Source }>();
  const [unknown, setUnknown] = useState(false), [saveJob, setSaveJob] = useState<Resource | "unknown">();
  const [connectFolder, setConnectFolder] = useState(false), [cloneLocally, setCloneLocally] = useState(false);
  const nameEdited = useRef(false);
  // Clone registration is one server-owned atomic operation. Its existing
  // request carries only clone inputs, so do not let it replace a draft that
  // already contains editable repository settings. A future protocol field
  // can remove this guard after its allocation and atomic server handling are
  // established.
  const draftEdited = useRef(false);
  const [options, setOptions] = useState(false), [childPending, setChildPending] = useState(false);
  const [cloneDraft, setCloneDraft] = useState<RepositoryCloneDraft>({ url: "", parent: "" });
  const [cloneJob, setCloneJob] = useState<Resource | "unknown">();
  const [githubSelection, setGitHubSelection] = useState<GitHubCloneSelection>();
  const transport = useTransport();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const remoteSupported = status.data?.capabilities.includes(SystemCapability.REMOTE_REPOSITORIES_V1) === true;
  const remoteUnsupported = status.data !== undefined && !remoteSupported;
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
  const save = useRetainedMutation("repository-add:save", ConfigurationQuery.saveConfiguration, (result, request) => {
    if (result.requestId === request.mutation?.requestId && result.job?.kind === EntityKind.JOB && uuid.test(result.job.id) && supportsResourceSchema(result.job) && result.job.revision > 0n && pendingSource.current && !pendingSource.current.local) { const submitted = object(JSON.parse(new TextDecoder().decode(request.documentJson))); const first = object(items(submitted.checkouts)[0]); if (first.machine_id === pendingSource.current.machine) runner.remember(text(first.machine_id)); }
    if (result.job) setSaveJob(result.job);
    else setSaveJob("unknown");
  });
  const serverMachine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: source?.machine ?? "" }, { enabled: active && Boolean(source), refetchInterval: active && source ? 5000 : false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const workerData = document(serverMachine.data?.resource);
  const lastSeen = Date.parse(text(workerData.last_seen));
  const offline = Boolean(serverMachine.data?.resource && Number.isFinite(lastSeen) && Date.now() - lastSeen > 45_000);
  const blocked = runnerRemediationPending || busy || clone.busy || clone.uncertain || Boolean(cloneJob) || inspect.busy || inspect.uncertain || Boolean(inspection) || unknown || save.busy || save.uncertain || Boolean(saveJob) || childPending;
  useEffect(() => { if (active && !runnerTouched.current && !blocked && computer === Computer.Remote && !machine && runner.suggestion) { setMachine(runner.suggestion.id); setMachineName(resourceName(runner.suggestion)); } }, [active, runner.suggestion, blocked, computer, machine]);
  const taskVisible = useSettingsTaskVisible(), cancelTask = useCloseSettingsTask(cancel), inTask = useInSettingsTask();
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => { if (taskVisible) initialAction.current?.focus(); }, [taskVisible]);
  const live = () => alive.current && activeRef.current && !opening?.disposed;
  const native = <T,>(operation: () => Promise<T>) => opening ? opening.native(operation) : operation();
  const inspectSource = async (selected: Source) => {
    if (!live()) return;
    pendingSource.current = selected;
    setSource(selected);
    await inspect.send({ requestId: newRequestId(), machineId: selected.machine, path: selected.path, preferredRemote: "" });
  };
  const verifyLocal = async (expectedMachine?: string) => {
    if (!readLocalWorker) throw new WorkerVerificationProblem("repository-registration.workerVerificationUnavailable");
    const proof = await native(readLocalWorker);
    if (!live()) throw new WorkerVerificationProblem("repository-registration.workerVerificationClosed");
    if (!uuid.test(proof.machineId) || !/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(proof.token)) throw new WorkerVerificationProblem("repository-registration.workerProofInvalid");
    // Retain only the non-secret machine identity. Status is a separate native
    // read, never a registration/start action or a substitute-machine fallback.
    const id = proof.machineId;
    if (expectedMachine && id !== expectedMachine) throw new WorkerVerificationProblem("repository-registration.workerOriginalChanged");
    setVerifiedMachine(id);
    if (controlLocalWorker) {
      const status = await native(() => controlLocalWorker(LocalWorkerAction.Status));
      if (!live()) throw new WorkerVerificationProblem("repository-registration.workerVerificationClosed");
      if (status.machine_id !== id) throw new WorkerVerificationProblem("repository-registration.workerStatusChanged");
      if (status.state === LocalWorkerState.Uncertain) throw new WorkerVerificationProblem("repository-registration.workerExitUncertain");
      if ([LocalWorkerState.NotStarted, LocalWorkerState.Exited, LocalWorkerState.Stopping].includes(status.state)) throw new WorkerVerificationProblem(status.state === LocalWorkerState.Exited ? "repository-registration.workerExited" : "repository-registration.workerStopped");
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
      setProblem(""); setWorkerProblem(false); setVerifiedAgain(false);
      if (!selectedPath || selectedPath.length > 4096 || selectedPath.includes("\0")) throw new ProductError(picker ? "repository-registration.extra.79de1a72a2e7" : "repository-registration.boundedCheckoutPath");
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
      if (!uuid.test(selectedMachine)) throw new ProductError("repository-registration.selectCheckoutComputer");
      await inspectSource({ path: selectedPath, machine: selectedMachine, name: local ? copy("repository-registration.thisComputer_26f9f9") : machineName || copy("repository-registration.extra.6501182438e2"), local });
    } catch (error) {
      if (live()) { setWorkerProblem(stage === SelectionStage.Worker && (picker || computer === Computer.Local)); setProblem(stage === SelectionStage.Picker ? folderSelectionProblem(error) : workerVerificationProblem(error)); }
    } finally { gate.current = false; if (live()) setBusy(false); }
  };
  const completeInspection = useCallback((output: Document) => {
    if (!inspection || !alive.current || opening?.disposed) return;
    if (!validRepositoryInspection(output)) { setUnknown(true); setProblem(ownedMessage("repository-registration.extra.e8a01a7b61e7")); return; }
    setData(current => ({ ...current, name: text(current.name) || text(output.name), checkouts: [{ machine_id: inspection.source.machine, path: text(output.root) }] }));
    setSummary({ output, source: inspection.source });
    setOptions(false); setManual(false); setInspection(undefined);
  }, [inspection, opening]);
  const change = (next: Document) => {
    if (encode(next).byteLength > 1 << 20) { setProblem(ownedMessage("repository-registration.extra.eede23d37ef3")); return; }
    draftEdited.current = true;
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
    gate.current = true; setBusy(true); setProblem(""); setWorkerProblem(false); setVerifiedAgain(false);
    try {
      const proof = await verifyLocal();
      if (!live()) return;
      const response = await createClient(ResourceService, transport).getResource({ kind: EntityKind.MACHINE, id: proof.machineId });
      if (!live()) return;
      if (!items(document(response.resource).worker_capabilities).includes("repository-clone-v1")) throw new WorkerVerificationProblem("repository-registration.workerCloneUnsupported");
      await clone.send({ requestId: newRequestId(), machineId: proof.machineId, localWorkerToken: proof.token, parentPath: cloneDraft.parent, url: cloneDraft.url, directoryName: cloneDirectory, githubSelection });
    } catch (error) { if (live()) { setWorkerProblem(true); setProblem(workerVerificationProblem(error)); } }
    finally { gate.current = false; if (live()) setBusy(false); }
  };
  const recheckWorker = async () => {
    if (blocked || gate.current || !live()) return;
    gate.current = true; setBusy(true); setVerifiedAgain(false);
    try { await verifyLocal(verifiedMachine); if (live()) { setVerifiedAgain(true); setWorkerProblem(false); setProblem(""); } }
    catch (error) { if (live()) setProblem(workerVerificationProblem(error)); }
    finally { gate.current = false; if (live()) setBusy(false); }
  };
  const remote = summary ? selectedInspectionRemote(summary.output, text(data.preferred_remote)) : "";
  const github = summary ? object(object(summary.output.github_repositories)[remote]) : {};
  const primaryCheckout = summary ? { machine_id: summary.source.machine, path: text(summary.output.root) } : undefined;
  // The confirmation and inferred metadata describe this exact inspected checkout.
  // Additional checkout edits cannot make a different checkout its silent replacement.
  const checkoutConfirmed = Boolean(primaryCheckout && items(data.checkouts).some(raw => {
    const checkout = object(raw);
    return checkout.machine_id === primaryCheckout.machine_id && checkout.path === primaryCheckout.path;
  }));
  // Capability 37 is a URL-first admission gate. A successful status response
  // without it retains the older inspected-folder registration path; a failed
  // status read leaves both paths disabled until the user retries the read.
  const legacyReady = Boolean(!cloneLocally && remoteUnsupported && checkoutConfirmed && text(data.name) && new TextEncoder().encode(text(data.name)).byteLength <= 256);
  const ready = Boolean(!cloneLocally && ((remoteSupported && parsedClone && text(data.name) && new TextEncoder().encode(text(data.name)).byteLength <= 256 && (!primaryCheckout || checkoutConfirmed)) || legacyReady));
  const cloneModeBlocked = options || draftEdited.current;
  return <section className="repository-registration" aria-label={copy("repository-registration.addRepository_2eda4d")}>
    <SettingsTaskDismissButton type="button" disabled={blocked} onClick={cancelTask}>{copy("repository-registration.backToRepositories_92a79b")}</SettingsTaskDismissButton>
    <h2 hidden={inTask}>{copy("repository-registration.addRepository_2eda4d")}</h2>
    {cloneJob ? <>{cloneJob === "unknown" ? <p role="alert">{copy("repository-registration.inline.296ac69de7")}</p> : <TrackedJob initial={cloneJob} active={active}>{(state, output) => <><SaveCompletion state={state} output={output} clone saved={repository => { if (live()) saved(repository); }} />{text(object(output.inspection).root) && state !== JobState.Succeeded ? <p role="alert">{copy("repository-registration.inline.a0600eeb9e")} {text(object(output.inspection).root)}{copy("repository-registration.inline.999469a947")}</p> : null}{state === JobState.Failed || state === JobState.Canceled ? <SettingsActionButton icon={SettingsActionIcon.Back} type="button" onClick={() => setCloneJob(undefined)}>{copy("repository-registration.inline.dc52059dec")}</SettingsActionButton> : null}</>}</TrackedJob>}<SettingsTaskActions><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={cancelTask}>{copy("repository-registration.inline.19766ed6cc")}</SettingsTaskDismissButton></SettingsTaskActions></> : saveJob ? <>{saveJob === "unknown" ? <p role="alert">{copy("repository-registration.theSaveWasAcknowledgedWithoutA_186074")}</p> : <TrackedJob initial={saveJob} active={active}>{(state, output) => <><SaveCompletion state={state} output={output} saved={repository => { if (live()) saved(repository); }} />{state === JobState.Failed || state === JobState.Canceled ? <SettingsActionButton icon={SettingsActionIcon.Back} type="button" onClick={() => setSaveJob(undefined)}>{copy("repository-registration.returnToCurrentDraft_0d5f4c")}</SettingsActionButton> : null}</>}</TrackedJob>}<SettingsTaskActions><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={cancelTask}>{copy("repository-registration.inline.19766ed6cc")}</SettingsTaskDismissButton></SettingsTaskActions></> : <>
      <section className="repository-remote-fields" aria-label={copy("repository-registration.inline.9112065139")}>
        <label>{copy("repository-registration.inline.cd01c2ef6a")}<input ref={initialAction} type="text" value={cloneDraft.url} maxLength={4096} placeholder={copy("repository-registration.inline.a2116e2c72")} disabled={blocked} autoComplete="off" spellCheck={false} onChange={event => changeCloneDraft({ ...cloneDraft, url: event.target.value, directory: undefined })} /></label>
        {cloneDraft.url && !parsedClone ? <p role="alert">{copy("repository-registration.inline.2f00f15706")}</p> : null}
        <RepositoryGitHubPicker active={active} supported={pickerSupported} disabled={blocked} choose={(selection, url) => { changeCloneDraft({ ...cloneDraft, url, directory: undefined }); setGitHubSelection(selection); setData(current => ({ ...current, integration_id: selection.profileId, github_owner: selection.owner, github_name: selection.name })); }} />
        {cloneLocally ? <p className="repository-clone-name">{copy("repository-registration.inline.d214dccd1f")} <strong>{cloneDirectory}</strong>.</p> : <TextField label={copy("repository-registration.inline.a2b1b3f24a")} value={data.name} required change={name => { nameEdited.current = true; change({ ...data, name }); }} />}
        <p>{copy("repository-registration.inline.3b8c8f1549")}</p>
        {remoteUnsupported ? <p role="status">{copy("repository-registration.inline.add2f25d8c")}</p> : null}
        <Problem error={status.error} />
        {status.error ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={status.isFetching} onClick={() => void status.refetch()}>{copy("repository-registration.inline.df8634ddd0")}</SettingsActionButton> : null}
      </section>
      <DisclosureButton aria-controls={disclosureContentId1} density={DisclosureDensity.Settings} type="button" disabled={blocked} aria-expanded={connectFolder} onClick={() => setConnectFolder(value => !value)}>{copy("repository-registration.inline.534642120d")}</DisclosureButton>
      <DisclosureContent id={disclosureContentId1} hidden={!connectFolder}>{connectFolder ? <>
      {summary ? <section className="repository-summary" aria-label={copy("repository-registration.repositoryDetected_998040")}>
        <div className="repository-summary-heading"><div><h3>{text(data.name)}</h3><p>{copy("repository-registration.repositoryDetected_998040")}</p></div><SettingsActionButton icon={SettingsActionIcon.Folder} type="button" disabled={blocked} onClick={() => void start(true)}>{copy("repository-registration.changeFolder_0eb4a7")}</SettingsActionButton></div>
        <p className="repository-path">{text(summary.output.root)}</p>
        <dl><div><dt>{copy("repository-registration.computer_76ed42")}</dt><dd>{summary.source.name}</dd></div><div><dt>{copy("repository-registration.gitRemote_915936")}</dt><dd>{remote || copy("repository-registration.unavailable_ca1844")}</dd></div><div><dt>{copy("repository-registration.defaultBranch_411aa6")}</dt><dd>{text(object(summary.output.default_refs)[remote]) || copy("repository-registration.extra.3471f15c181b")}</dd></div><div><dt>{copy("repository-registration.github_f911e4")}</dt><dd>{text(github.owner) && text(github.name) ? `${text(github.owner)}/${text(github.name)}` : copy("repository-registration.unavailable_ca1844")}</dd></div></dl>
        <p>{copy("repository-registration.githubDetectionIsMetadataOnlySelect_8699ce")}</p>
      </section> : <section className="repository-folder-card"><h3>{copy("repository-registration.inline.b53e4dccdf")}</h3><p>{copy("repository-registration.inline.5419242059")}</p><div className="repository-folder-actions"><SettingsActionButton icon={SettingsActionIcon.Folder} type="button" disabled={blocked || !chooseFolder} onClick={() => void start(true)}>{copy("repository-registration.chooseFolder_3db741")}</SettingsActionButton><DisclosureButton density={DisclosureDensity.Settings} type="button" disabled={blocked} aria-controls={manualContentId} aria-expanded={manual} focusWhenCollapsing={() => Boolean(summary || !path)} onClick={() => setManual(value => !value)}>{copy("repository-registration.enterAPath_fe3f3a")}</DisclosureButton></div></section>}
      {summary && !manual ? <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={blocked} onClick={() => setManual(true)}>{copy("repository-registration.enterAPath_fe3f3a")}</SettingsActionButton> : null}
      {manual || (!summary && path) ? <fieldset id={manualContentId} disabled={blocked}><legend>{copy("repository-registration.repositoryFolder_26c354")}</legend>{computer === Computer.Remote ? runner.guidance : null}<label>{copy("repository-registration.computer_76ed42")}<select value={computer} onChange={event => setComputer(event.target.value as Computer)}><option value={Computer.Local}>{copy("repository-registration.thisComputer_26f9f9")}</option><option value={Computer.Remote}>{copy("repository-registration.anotherComputer_e97a34")}</option></select></label>{computer === Computer.Remote ? <ResourceChoice label={copy("repository-registration.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={machine} active={active} showStatus change={(id, value, resource) => { runnerTouched.current = true; runner.touch(); setMachine(id); setMachineName(resource ? resourceName(resource) : text(value?.name)); }} /> : null}<TextField label={copy("repository-registration.absoluteCheckoutPath_a88fe7")} value={path} max={4096} change={setPath} /><SettingsActionButton icon={SettingsActionIcon.Folder} type="button" disabled={!path || (computer === Computer.Remote && !machine)} onClick={() => void start(false)}>{copy("repository-registration.inspectFolder_83fcd0")}</SettingsActionButton></fieldset> : null}
      </> : null}</DisclosureContent>
      <DisclosureButton aria-controls={disclosureContentId2} density={DisclosureDensity.Settings} type="button" disabled={blocked || (!cloneLocally && cloneModeBlocked)} aria-expanded={cloneLocally} onClick={() => { if (!cloneLocally) { nameEdited.current = false; setData(current => ({ ...current, name: cloneDirectory })); } setCloneLocally(value => !value); }}>{copy("repository-registration.inline.57aeb91729")}</DisclosureButton>
      {!cloneLocally && cloneModeBlocked ? <p role="status">{copy("repository-registration.inline.3afa0d5420")}</p> : null}
      <DisclosureContent id={disclosureContentId2} hidden={!cloneLocally}>{cloneLocally ? <><RepositoryCloneFields draft={cloneDraft} change={changeCloneDraft} busy={blocked} showURL={false} browse={() => void browseCloneParent()} supported={cloneSupported} /><SettingsActionButton icon={SettingsActionIcon.Add} type="button" disabled={blocked || !cloneReady} onClick={() => void startClone()}>{copy("repository-registration.inline.91a5165017")}</SettingsActionButton></> : null}</DisclosureContent>
      {inspection ? <TrackedJob initial={inspection.job} active={active}>{(state, output) => <><InspectionCompletion state={state} output={output} completed={completeInspection} />{state === JobState.Failed || state === JobState.Canceled ? <SettingsActionButton icon={SettingsActionIcon.Back} type="button" onClick={() => setInspection(undefined)}>{copy("repository-registration.returnToSelectedFolder_275370")}</SettingsActionButton> : null}</>}</TrackedJob> : null}
      {busy || inspect.busy ? <p role="status">{busy ? copy("repository-registration.selectingFolderAndVerifyingThisComputer_88e910") : copy("repository-registration.inspectingRepository_ca1086")}</p> : null}
      {offline ? <p role="status">{copy("repository-registration.workerOffline")}</p> : null}
      {serverMachine.error ? <Problem error={serverMachine.error} /> : null}
      {offline || serverMachine.error ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={blocked || serverMachine.isFetching} onClick={() => void serverMachine.refetch()}>{copy("repository-registration.recheckHeartbeat")}</SettingsActionButton> : null}
      {workerProblem ? <section aria-label={copy("repository-registration.workerRecovery")}><p>{copy("repository-registration.workerManual")}</p><LocalConnectionHelp active={active && taskVisible} />{verifiedMachine ? <Disclosure density={DisclosureDensity.Settings}><DisclosureSummary>{copy("repository-registration.originalRunner")}</DisclosureSummary><p>{verifiedMachine}</p></Disclosure> : null}<SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={blocked || !readLocalWorker} onClick={() => void recheckWorker()}>{copy("repository-registration.recheckWorker")}</SettingsActionButton></section> : null}
      {verifiedAgain ? <p role="status">{copy("repository-registration.workerRechecked")}</p> : null}
      {unknown ? <p role="alert">{copy("repository-registration.inspectionWasAcknowledgedWithoutAReadable_28ee95")}</p> : null}
      {cloneLocally ? <p>{copy("repository-registration.inline.57dbb8a56f")}</p> : null}
      {ready && !cloneLocally ? <><DisclosureButton density={DisclosureDensity.Settings} type="button" className="repository-options-toggle" aria-expanded={options} aria-controls="repository-options" onClick={() => setOptions(value => !value)}>{copy("repository-registration.optionalSettings_e88b5c")}</DisclosureButton><DisclosureContent id="repository-options" hidden={!options}><fieldset disabled={save.busy || save.uncertain || busy || Boolean(inspection) || inspect.uncertain}><RepositoryFields data={data} change={change} active={active && options} existing={false} pendingOperation={setChildPending} registration requiredCheckout={primaryCheckout} /></fieldset></DisclosureContent></> : null}
      {problem ? <p role="alert">{problem}</p> : null}<Problem error={inspect.error || save.error || clone.error} />
      {inspect.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={inspect.busy} onClick={inspect.retry}>{copy("repository-registration.retryTheSameInspection_8ce3eb")}</SettingsActionButton> : null}
      <SettingsTaskActions className="repository-add-footer"><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={cancelTask}>{copy("repository-registration.inline.19766ed6cc")}</SettingsTaskDismissButton><SettingsActionButton icon={SettingsActionIcon.Add} type="button" className="primary" disabled={blocked || !ready} onClick={() => void save.send({ mutation: { requestId: newRequestId(), expectedRevision: 0n }, kind: EntityKind.REPOSITORY, schemaVersion: 1, documentJson: encode(repositoryRegistrationDocument(data, legacyReady)) })}>{copy("repository-registration.addRepository_2eda4d")}</SettingsActionButton>{clone.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={clone.busy} onClick={clone.retry}>{copy("repository-registration.inline.3f0ac0a40a")}</SettingsActionButton> : null}{save.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={save.busy} onClick={save.retry}>{copy("repository-registration.retryTheSameRepositorySave_b78084")}</SettingsActionButton> : null}</SettingsTaskActions>
    </>}
      <RunnerTaskRemediation active={active && taskVisible} machineId={verifiedMachine} disabled={blocked} visible={workerProblem} onPending={setRunnerRemediationPending} />
  </section>;
}
