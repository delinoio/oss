import { LocalizedText, copy, useLocale } from "./localization";
import { useRef, useState } from "react";
import { SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { RejectedInput } from "./startup-rejection";

enum Delivery { Queued = "queued", Claimed = "claimed", Accepted = "accepted", Uncertain = "uncertain", Removed = "removed", Rejected = "rejected-before-start" }
export type QueuedInputDraft = { prompt: string; revision: bigint };
export function QueuedInput({ resource, session, refresh, draft, changeDraft, readOnly = false }: { resource: Resource; session?: Resource; refresh: () => void; draft?: QueuedInputDraft; changeDraft?: (value?: QueuedInputDraft) => void; readOnly?: boolean }) {
  useLocale();
  const [accepted, setAccepted] = useState<Resource>();
  const current = accepted && accepted.revision > resource.revision ? accepted : resource;
  const data = document(current);
  const explicitEdit = useRef(false);
  const [localEdit, setLocalEdit] = useState<QueuedInputDraft>();
  const edit = changeDraft ? draft : localEdit;
  const setEdit = changeDraft ?? setLocalEdit;
  const saved = (input?: Resource) => { if (input) setAccepted(input); setEdit(undefined); refresh(); };
  const update = useRetainedMutation(`edit-input:${resource.id}`, SessionQuery.editQueuedInput, (result) => saved(result.change?.input));
  const remove = useRetainedMutation(`remove-input:${resource.id}`, SessionQuery.removeQueuedInput, (result) => saved(result.change?.input));
  const steer = useRetainedMutation(`steer-input:${resource.id}`, SessionQuery.steerQueuedInput, (result) => saved(result.change?.input));
  const busy = readOnly || [update, remove, steer].some((operation) => operation.busy || operation.uncertain);
  const sessionData = document(session), execution = object(sessionData.execution);
  const canSteer = sessionData.outcome === "running" && sessionData.archive === "active" && text(sessionData.active_execution_id) === text(execution.execution_id) && Boolean(text(execution.execution_id) && text(execution.native_turn_id));
  const mutation = () => ({ id: resource.id, expectedRevision: current.revision, requestId: newRequestId() });
  return <article className="queue-item"><header><strong>{text(data.mode)} · {text(data.delivery)}</strong><small><LocalizedText id="queue.input_3547c5" components={{ s0: <>{String(data.sequence ?? "")}</> }} /></small></header>
    {text(data.delivery) === Delivery.Removed ? <p>{copy("queue.removedInputOriginalOrderingRetained_3f3155")}</p> : <p>{text(data.prompt)}</p>}
    {text(data.delivery) === Delivery.Rejected ? <RejectedInput resource={current} session={session} /> : null}
    {text(data.delivery) === Delivery.Queued ? <>
      <div className="actions"><button disabled={busy} onClick={() => { explicitEdit.current = true; setEdit({ prompt: text(data.prompt), revision: current.revision }); }}>{copy("queue.editInput_f7680c")}</button><button disabled={busy} onClick={() => void remove.send({ mutation: mutation(), sessionId: resource.sessionId })}>{copy("queue.removeInput_95e788")}</button><button disabled={busy || !canSteer} onClick={() => void steer.send({ mutation: mutation(), sessionId: resource.sessionId, expectedExecutionId: text(execution.execution_id), expectedTurnId: text(execution.native_turn_id) })}>{copy("queue.steerWithThisInput_d835aa")}</button></div>
      {edit ? <form onSubmit={(event) => { event.preventDefault(); if (busy || edit.revision !== current.revision) return; void update.send({ mutation: { id: resource.id, expectedRevision: edit.revision, requestId: newRequestId() }, sessionId: resource.sessionId, prompt: edit.prompt }); }}>
        <label>{copy("queue.editedInput_e6f7fe")}<textarea autoFocus={explicitEdit.current} rows={3} maxLength={65536} disabled={busy} value={edit.prompt} onChange={(event) => setEdit({ ...edit, prompt: event.target.value })} /></label>
        {edit.revision !== current.revision ? <p role="status">{copy("queue.thisInputChangedWhileYouWere_cfe47a")}</p> : null}
        <div className="actions"><button className="primary" disabled={busy || !edit.prompt.trim() || edit.revision !== current.revision}>{copy("queue.saveInput_9f11a2")}</button><button type="button" disabled={busy} onClick={() => setEdit(undefined)}>{copy("queue.cancelEdit_6fa271")}</button></div>
      </form> : null}
    </> : null}
    {[update, remove, steer].map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="queue.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("queue.edit_262121") : index === 1 ? copy("queue.removal_e57388") : copy("queue.steer_1cf39e")}</> }} /></button> : null}</div>)}
  </article>;
}
