// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, isEntityId, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation, useRetainedMutationIntents } from "./mutation";
import { Modal } from "./ui";
import { NativeShellDelivery, NativeShellJobState, readNativeShellObservation, shellText } from "./native-shell-action-model";
import "./native-shell-action.css";

export function ownedNativeShellJob(job: Resource | undefined, sessionId: string): job is Resource {
  const data = document(job), input = object(data.input), shell = object(input.shell);
  const observation = data.output == null ? undefined : readNativeShellObservation(data.output);
  return Boolean(job && job.kind === EntityKind.JOB && isEntityId(job.id) && supportsResourceSchema(job) && job.revision > 0n && job.sessionId === sessionId && (data.output == null || observation && observation.action_id === input.action_id) && isEntityId(text(input.action_id)) && data.type === "native-shell" && Object.values(NativeShellJobState).includes(data.state as NativeShellJobState) && object(input.assignment).session_id === sessionId && shell.full_access_confirmed === true && shellText(shell.command, 65536, true) && (shell.timeout_ms === undefined || Number.isSafeInteger(shell.timeout_ms) && (shell.timeout_ms as number) > 0 && (shell.timeout_ms as number) <= 3600000));
}
export function verifiedNativeShellRun(job: Resource | undefined, sessionId: string, requestId: string, command: string, timeoutMs = 0): boolean {
  const shell = object(object(document(job).input).shell);
  return Boolean(job?.id === requestId && ownedNativeShellJob(job, sessionId) && shell.command === command && shell.full_access_confirmed === true && (shell.timeout_ms ?? 0) === timeoutMs);
}
export function nativeShellEligible(session: Resource | undefined): boolean {
  const data = document(session), execution = object(data.execution);
  return Boolean(session && supportsResourceSchema(session) && session.kind === EntityKind.SESSION && session.revision > 0n && object(object(data.initial_execution).configuration).harness === "codex" && !data.fork && data.archive === "active" && data.recovery === "none" && !data.active_execution_id && !data.compaction_job_id && !data.pending_steer_id && !data.pending_inputs && !data.pending_input_bytes && shellText(execution.native_thread_id, 1024, true) && execution.cleanup_verified === true && !execution.unconfirmed_responses && !Object.keys(object(execution.subagents)).length && !Object.values(object(execution.waiting)).some(Boolean));
}

/** Explicit human action only. Closing presentation never cancels native work. */
export function NativeShellAction({ session, active, blocked }: { session: Resource; active: boolean; blocked: boolean }) {
  useLocale();
  const [open, setOpen] = useState(false), [command, setCommand] = useState(""), [confirmed, setConfirmed] = useState(false);
  const [source, setSource] = useState<Resource>(), [selected, setSelected] = useState<string>(), [job, setJob] = useState<Resource>();
  const [invalidRead, setInvalidRead] = useState(false);
  const commandInput = useRef<HTMLTextAreaElement>(null);
  const scope = session.id, key = `native-shell:${scope}`;
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && open, retry: false });
  const systemSupported = Boolean(status.data?.capabilities.includes(SystemCapability.CODEX_NATIVE_SHELL_V1) && !status.error);
  const machine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: text(document(session).machine_id) }, { enabled: active && open && systemSupported && Boolean(text(document(session).machine_id)), retry: false });
  const supported = Boolean(systemSupported && !machine.error && machine.data?.resource?.kind === EntityKind.MACHINE && machine.data.resource.id === text(document(session).machine_id) && supportsResourceSchema(machine.data.resource) && machine.data.resource.revision > 0n && Array.isArray(document(machine.data?.resource).worker_capabilities) && (document(machine.data?.resource).worker_capabilities as unknown[]).includes("codex-native-shell-v1"));
  const pending = useRetainedMutationIntents(key).find(intent => intent.key === key);
  const original = pending ? object(pending.input) : undefined;
  const originalMutation = object(original?.mutation);
  const originalId = text(originalMutation.requestId);
  const acceptJob = (next: Resource) => {
    if (!ownedNativeShellJob(next, scope)) return false;
    const observedThread = readNativeShellObservation(document(next).output)?.native_thread_id;
    if (observedThread && observedThread !== text(object(document(session).execution).native_thread_id)) return false;
    if (job && job.id === next.id && next.revision < job.revision) return false;
    const oldObservation = readNativeShellObservation(document(job).output), nextObservation = readNativeShellObservation(document(next).output);
    if (job?.id === next.id && oldObservation && (!nextObservation || nextObservation.action_id !== oldObservation.action_id || nextObservation.native_thread_id !== oldObservation.native_thread_id || nextObservation.sequence < oldObservation.sequence || oldObservation.terminal && !nextObservation.terminal || oldObservation.cleanup_verified && !nextObservation.cleanup_verified)) return false;
    setSelected(next.id); setJob(next); setInvalidRead(false); return true;
  };
  const run = useRetainedMutation(key, SessionQuery.runNativeShell, result => { if (result.job) acceptJob(result.job); }, (result, request) => {
    const observedThread = readNativeShellObservation(document(result.job).output)?.native_thread_id;
    return Boolean((!observedThread || observedThread === text(object(document(session).execution).native_thread_id)) && request.mutation && request.fullAccessConfirmed && result.requestId === request.mutation.requestId && verifiedNativeShellRun(result.job, request.mutation.id, request.mutation.requestId, request.command, request.timeoutMs));
  });
  const cancel = useRetainedMutation(`native-shell-cancel:${scope}`, SessionQuery.cancelNativeShell, result => { if (result.job) acceptJob(result.job); }, (result, request) => Boolean(request.mutation && result.requestId === request.mutation.requestId && result.job?.id === request.mutation.id && ownedNativeShellJob(result.job, scope)));
  const observedId = originalId || selected || text(document(session).native_shell_job_id);
  const current = useQuery(SessionQuery.getNativeShell, { jobId: observedId }, { enabled: active && open && supported && isEntityId(observedId), retry: false, refetchInterval: job && readNativeShellObservation(document(job).output)?.cleanup_verified ? false : 2000 });
  useEffect(() => { if (!active) setOpen(false); }, [active]);
  useEffect(() => {
    const next = current.data?.job;
    if (!next) return;
    const mismatchedOriginal = originalId && (originalMutation.id !== scope || original?.fullAccessConfirmed !== true || !verifiedNativeShellRun(next, scope, originalId, text(original?.command), typeof original?.timeoutMs === "number" ? original.timeoutMs : 0));
    if (next.id !== observedId || mismatchedOriginal || !acceptJob(next)) { console.warn("delidev.native_shell.observation_rejected", { classification: "original_scope" }); setInvalidRead(true); return; }
    if (run.uncertain && originalId && !run.busy) run.acceptObserved(create(SessionQuery.runNativeShell.output, { job: next, requestId: originalId, replayed: true }));

  }, [current.data, observedId, run.uncertain, run.busy, cancel.uncertain, cancel.busy]);
  const observation = readNativeShellObservation(document(job).output);
  const unfinished = Boolean(job && !(observation?.terminal && observation.cleanup_verified));
  const changed = source?.id !== session.id || source?.revision !== session.revision;
  const canRun = !invalidRead && !current.error && (!observedId || job?.id === observedId) && supported && nativeShellEligible(session) && !blocked && !changed && !run.busy && !run.uncertain && !cancel.busy && !cancel.uncertain && !unfinished && confirmed && shellText(command, 65536, true);
  const openAction = () => { setSource(session); setConfirmed(false); setOpen(true); };
  return <>
    <button type="button" disabled={!active} onClick={openAction}>{copy("native-shell-action.open")}</button>
    {open ? createPortal(<div onClick={event => event.stopPropagation()}><Modal title={copy("native-shell-action.open")} close={() => setOpen(false)} initialFocus={commandInput} trapFocus className="native-shell-action">
      <p>{copy("native-shell-action.source", { name: resourceName(source) })}</p>
      <p className="native-shell-warning">{copy("native-shell-action.fullAccess")}</p>
      {status.isPending || systemSupported && machine.isPending ? <p role="status">{copy("native-shell-action.loading")}</p> : !supported ? <p role="status">{copy("native-shell-action.unsupported")}</p> : null}
      {!nativeShellEligible(session) ? <p role="status">{copy("native-shell-action.ineligible")}</p> : null}
      {changed ? <p role="alert">{copy("native-shell-action.changed")}</p> : null}
      <form onSubmit={event => { event.preventDefault(); if (!canRun || !source) return; const requestId = newRequestId(); setSelected(requestId); setJob(undefined); setConfirmed(false); void run.send({ mutation: { id: source.id, expectedRevision: source.revision, requestId }, command, fullAccessConfirmed: true }); }}>
        <label>{copy("native-shell-action.command")}<textarea ref={commandInput} value={command} maxLength={65536} disabled={run.busy || run.uncertain || unfinished} onChange={event => { setCommand(event.target.value); setConfirmed(false); }} /></label>
        <label className="native-shell-confirm"><input type="checkbox" checked={confirmed} disabled={run.busy || run.uncertain || unfinished} onChange={event => setConfirmed(event.target.checked)} />{copy("native-shell-action.confirm")}</label>
        <button type="submit" disabled={!canRun}>{copy("native-shell-action.run")}</button>
      </form>
      {run.busy ? <p role="status">{copy("native-shell-action.sending")}</p> : null}
      {run.uncertain || cancel.uncertain || observation?.delivery === NativeShellDelivery.Uncertain ? <p role="alert">{copy("native-shell-action.uncertain")}</p> : null}
      {run.error && !run.uncertain || cancel.error && !cancel.uncertain || current.error || machine.error || status.error || invalidRead ? <p role="alert">{copy("native-shell-action.readFailed")}</p> : null}
      {observedId ? <button type="button" disabled={current.isFetching || run.busy || cancel.busy} onClick={() => void current.refetch()}>{copy("native-shell-action.inspect")}</button> : null}
      {job ? <section aria-label={copy("native-shell-action.observation")} aria-live="polite">
        <p>{copy(`native-shell-action.state.${text(document(job).state) as NativeShellJobState}`)}</p>
        {observation ? <>{observation.processes.map(process => <article key={process.item_id}><p>{copy(`native-shell-action.process.${process.status}`)}</p><pre>{process.command}</pre>{process.cwd ? <p>{copy("native-shell-action.cwd", { cwd: process.cwd })}</p> : null}<pre className="native-shell-output">{process.output}</pre>{process.aggregated_output !== undefined ? <><p>{copy("native-shell-action.aggregate")}</p><pre className="native-shell-output">{process.aggregated_output}</pre></> : null}{process.exit_code !== undefined ? <p>{copy("native-shell-action.exit", { code: process.exit_code })}</p> : null}</article>)}<p>{copy(observation.cleanup_verified ? "native-shell-action.cleanupComplete" : "native-shell-action.cleanupPending")}</p></> : <p>{copy("native-shell-action.noObservation")}</p>}
        <button type="button" disabled={!supported || run.busy || run.uncertain || cancel.busy || cancel.uncertain || observation?.terminal === true || [NativeShellJobState.Succeeded, NativeShellJobState.Failed, NativeShellJobState.Canceled].includes(document(job).state as NativeShellJobState)} onClick={() => void cancel.send({ mutation: { id: job.id, expectedRevision: job.revision, requestId: newRequestId() } })}>{copy("native-shell-action.cancel")}</button>
      </section> : null}
    </Modal></div>, globalThis.document.body) : null}
  </>;
}
