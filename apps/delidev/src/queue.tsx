import { useState } from "react";
import { SessionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { RejectedInput } from "./startup-rejection";

enum Delivery { Queued = "queued", Claimed = "claimed", Accepted = "accepted", Uncertain = "uncertain", Removed = "removed", Rejected = "rejected-before-start" }
export function QueuedInput({ resource, session, refresh }: { resource: Resource; session?: Resource; refresh: () => void }) {
  const [accepted, setAccepted] = useState<Resource>();
  const current = accepted && accepted.revision > resource.revision ? accepted : resource;
  const data = document(current);
  const [edit, setEdit] = useState<{ prompt: string; revision: bigint }>();
  const saved = (input?: Resource) => { if (input) setAccepted(input); setEdit(undefined); refresh(); };
  const update = useRetainedMutation(`edit-input:${resource.id}`, SessionQuery.editQueuedInput, (result) => saved(result.change?.input));
  const remove = useRetainedMutation(`remove-input:${resource.id}`, SessionQuery.removeQueuedInput, (result) => saved(result.change?.input));
  const steer = useRetainedMutation(`steer-input:${resource.id}`, SessionQuery.steerQueuedInput, (result) => saved(result.change?.input));
  const busy = [update, remove, steer].some((operation) => operation.busy || operation.uncertain);
  const sessionData = document(session), execution = object(sessionData.execution);
  const canSteer = sessionData.outcome === "running" && sessionData.archive === "active" && text(sessionData.active_execution_id) === text(execution.execution_id) && Boolean(text(execution.execution_id) && text(execution.native_turn_id));
  const mutation = () => ({ id: resource.id, expectedRevision: current.revision, requestId: newRequestId() });
  return <article className="queue-item"><header><strong>{text(data.mode)} · {text(data.delivery)}</strong><small>Input {String(data.sequence ?? "")}</small></header>
    {text(data.delivery) === Delivery.Removed ? <p>Removed input · original ordering retained</p> : <p>{text(data.prompt)}</p>}
    {text(data.delivery) === Delivery.Rejected ? <RejectedInput resource={current} session={session} /> : null}
    {text(data.delivery) === Delivery.Queued ? <>
      <div className="actions"><button disabled={busy} onClick={() => setEdit({ prompt: text(data.prompt), revision: current.revision })}>Edit input</button><button disabled={busy} onClick={() => void remove.send({ mutation: mutation(), sessionId: resource.sessionId })}>Remove input</button><button disabled={busy || !canSteer} onClick={() => void steer.send({ mutation: mutation(), sessionId: resource.sessionId, expectedExecutionId: text(execution.execution_id), expectedTurnId: text(execution.native_turn_id) })}>Steer with this input</button></div>
      {edit ? <form onSubmit={(event) => { event.preventDefault(); if (busy || edit.revision !== current.revision) return; void update.send({ mutation: { id: resource.id, expectedRevision: edit.revision, requestId: newRequestId() }, sessionId: resource.sessionId, prompt: edit.prompt }); }}>
        <label>Edited input<textarea autoFocus rows={3} maxLength={65536} disabled={busy} value={edit.prompt} onChange={(event) => setEdit({ ...edit, prompt: event.target.value })} /></label>
        {edit.revision !== current.revision ? <p role="status">This input changed while you were editing. Review the current text before saving.</p> : null}
        <div className="actions"><button className="primary" disabled={busy || !edit.prompt.trim() || edit.revision !== current.revision}>Save input</button><button type="button" disabled={busy} onClick={() => setEdit(undefined)}>Cancel edit</button></div>
      </form> : null}
    </> : null}
    {[update, remove, steer].map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}>Retry the same {index === 0 ? "edit" : index === 1 ? "removal" : "Steer"}</button> : null}</div>)}
  </article>;
}
