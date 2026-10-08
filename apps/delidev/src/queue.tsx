import { RetainedImages } from "./image-attachments";
import { acknowledgeImages, retainedImages } from "./image-input";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useRef, useState } from "react";
import { SessionQuery, newRequestId, type Resource, type SkillSelection } from "@delinoio/delidev-api-client";
import { document, items, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { useSkillCompletion, type SkillTokenBinding } from "./skill-completion";
import { Problem } from "./ui";
import { RejectedInput } from "./startup-rejection";

enum Delivery { Queued = "queued", Claimed = "claimed", Accepted = "accepted", Uncertain = "uncertain", Removed = "removed", Rejected = "rejected-before-start" }
export type QueuedInputDraft = { prompt: string; revision: bigint; skills?: SkillTokenBinding[] };
export function QueuedInput({ resource, session, refresh, draft, changeDraft, readOnly = false, active = true }: { resource: Resource; session?: Resource; refresh: () => void; draft?: QueuedInputDraft; changeDraft?: (value?: QueuedInputDraft) => void; readOnly?: boolean; active?: boolean }) {
  useLocale();
  const [accepted, setAccepted] = useState<Resource>();
  const current = accepted && accepted.revision > resource.revision ? accepted : resource;
  const data = document(current);
  const images = retainedImages(data.attachments);
  const imageBound = data.attachments !== undefined && (!images || images.length > 0);
  
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
  const canSteer = !items(data.skills).length && !imageBound && sessionData.outcome === "running" && sessionData.archive === "active" && text(sessionData.active_execution_id) === text(execution.execution_id) && Boolean(text(execution.execution_id) && text(execution.native_turn_id));
  const mutation = () => ({ id: resource.id, expectedRevision: current.revision, requestId: newRequestId() });
  return <article className="queue-item"><header><strong>{text(data.mode)} · {text(data.delivery)}</strong><small><LocalizedText id="queue.input_3547c5" components={{ s0: <>{String(data.sequence ?? "")}</> }} /></small></header>
    {text(data.delivery) === Delivery.Removed ? <p>{copy("queue.removedInputOriginalOrderingRetained_3f3155")}</p> : <p>{text(data.prompt)}</p>}
    {text(data.delivery) !== Delivery.Removed ? <RetainedImages value={data.attachments} sessionId={resource.sessionId} active={active} /> : null}
    {imageBound ? <p>{copy("image-input.steerUnavailable")}</p> : null}
    {text(data.delivery) === Delivery.Rejected ? <RejectedInput resource={current} session={session} /> : null}
    {text(data.delivery) === Delivery.Queued ? <>
      <div className="actions"><button disabled={busy} onClick={() => { explicitEdit.current = true; setEdit({ prompt: text(data.prompt), revision: current.revision, skills: queuedSkillBindings(data) }); }}>{copy("queue.editInput_f7680c")}</button><button disabled={busy} onClick={() => void remove.send({ mutation: mutation(), sessionId: resource.sessionId })}>{copy("queue.removeInput_95e788")}</button><button disabled={busy || !canSteer} onClick={() => void steer.send({ mutation: mutation(), sessionId: resource.sessionId, expectedExecutionId: text(execution.execution_id), expectedTurnId: text(execution.native_turn_id) })}>{copy("queue.steerWithThisInput_d835aa")}</button></div>
      {edit ? <QueuedInputEditor key={resource.id} edit={edit} setEdit={setEdit} current={current} session={session} busy={busy} imageBound={imageBound} autoFocus={explicitEdit.current} save={(prompt, selections) => { void update.send({ mutation: { id: resource.id, expectedRevision: edit.revision, requestId: newRequestId() }, sessionId: resource.sessionId, prompt, skills: { selections } }, images?.length ? (reply, request) => acknowledgeImages(reply.change, request.mutation!.requestId, images, request.sessionId, request.mutation!.id) : undefined); }} /> : null}
    </> : null}
    {[update, remove, steer].map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="queue.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("queue.edit_262121") : index === 1 ? copy("queue.removal_e57388") : copy("queue.steer_1cf39e")}</> }} /></button> : null}</div>)}
  </article>;
}

function queuedSkillBindings(data: Record<string, unknown>): SkillTokenBinding[] {
 const prompt=text(data.prompt), names=object(data.skill_names), values=items(data.skills).map(object);
 return values.map(binding=>{
  const name=text(names[text(binding.skill_id)]),token=`$${name}`,positions:number[]=[];
  for(let start=prompt.indexOf(token);start>=0;start=prompt.indexOf(token,start+1)) {
   if((start===0 || /\s/u.test(prompt[start-1]!)) && (start+token.length===prompt.length || /\s/u.test(prompt[start+token.length]!))) positions.push(start);
  }
  // Accepted package identity does not prove which same-named visible token was selected.
  // Preserve ambiguous bindings as stale until explicit clear and reselection.
  const ambiguous=positions.length!==1 || values.filter(value=>text(names[text(value.skill_id)])===name).length!==1;
  const start=positions[0]??0;
  return {start,end:start+token.length,token,stale:!name || ambiguous,ambiguous,selection:{$typeName:"delidev.v1.SkillSelection",inventoryId:text(binding.inventory_id),skillId:text(binding.skill_id),contentRevision:text(binding.content_revision),workerDeviceId:text(binding.worker_device_id)}};
 });
}
function QueuedInputEditor({edit,setEdit,current,session,busy,imageBound,autoFocus,save}: {edit:QueuedInputDraft;setEdit:(draft?:QueuedInputDraft)=>void;current:Resource;session?:Resource;busy:boolean;imageBound:boolean;autoFocus:boolean;save:(prompt:string,selections:SkillSelection[])=>void}) {
 const [textLimit,setTextLimit]=useState(false);
 const textarea=useRef<HTMLTextAreaElement>(null), data=document(session);
 // Draft ownership remains outside disposable payloads; only token bindings change.
 const bindingsChanged=useCallback((bindings:SkillTokenBinding[])=>{if(edit.skills!==bindings) setEdit({...edit,skills:bindings});},[edit,setEdit]);
 const skills=useSkillCompletion({value:edit.prompt,change:prompt=>{if(new TextEncoder().encode(prompt).byteLength>(256<<10)){setTextLimit(true);return false;}setTextLimit(false);setEdit({...edit,prompt});return true;},textarea,machineId:text(data.machine_id),agentId:text(data.agent_id),sessionId:current.sessionId,initialBindings:edit.skills,bindingsChanged,disabled:busy});
 return <form onSubmit={event=>{event.preventDefault();if(busy || skills.blocked || edit.revision!==current.revision) return;save(edit.prompt,skills.selections);}}>
  <label>{copy("queue.editedInput_e6f7fe")}<textarea ref={textarea} autoFocus={autoFocus} rows={3} maxLength={65536} disabled={busy} value={edit.prompt} onChange={event=>skills.onChange(event.target.value,event.target.selectionStart)} onSelect={skills.onSelect} onKeyDown={skills.onKeyDown} onCompositionStart={skills.onCompositionStart} onCompositionEnd={skills.onCompositionEnd} {...skills.attributes}/></label>
  {skills.list}{skills.warning}{textLimit?<p role="alert">{copy("image-input.textLimit")}</p>:null}{edit.skills?.length ? <button type="button" disabled={busy} onClick={skills.clear}>{copy("skills.clear")}</button>:null}
  {edit.revision!==current.revision ? <p role="status">{copy("queue.thisInputChangedWhileYouWere_cfe47a")}</p>:null}
  <div className="actions"><button className="primary" disabled={busy || skills.blocked || (!edit.prompt.trim() && !imageBound) || edit.revision!==current.revision}>{copy("queue.saveInput_9f11a2")}</button><button type="button" disabled={busy} onClick={()=>setEdit(undefined)}>{copy("queue.cancelEdit_6fa271")}</button></div>
 </form>;
}
