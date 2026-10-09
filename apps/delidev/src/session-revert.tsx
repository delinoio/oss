// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState, type RefObject } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";

const revision = (value: unknown) => typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? BigInt(value) : value === undefined ? 0n : undefined;
export function revertEligible(session: Resource | undefined, message: Resource, supported: boolean): boolean {
 const s = document(session), e = object(s.execution), m = document(message), context = revision(s.context_revision), original = revision(m.context_revision);
 const retained = object(object(s.revert).result).retained_turn_ids;
 return Boolean(supported && session && session.schemaVersion === 1 && message.schemaVersion === 1 && message.kind === EntityKind.MESSAGE && message.sessionId === session.id && object(object(s.initial_execution).configuration).harness === "codex" && s.fork === undefined && s.archive === "active" && s.recovery === "none" && !s.active_execution_id && !s.compaction_job_id && !s.pending_steer_id && !s.pending_inputs && !s.pending_input_bytes && e.cleanup_verified === true && !e.unconfirmed_responses && !Object.keys(object(e.waiting)).some(key => object(e.waiting)[key]) && !Object.keys(object(e.subagents)).length && m.role === "user" && m.state === "complete" && m.inherited === undefined && text(m.input_id) && text(m.native_turn_id) && m.native_thread_id === e.native_thread_id && context !== undefined && original !== undefined && (original === context || original < context && Array.isArray(retained) && retained.includes(m.native_turn_id)));
}
export function verifiedRevertDraft(session: Resource | undefined, action: string, message: string, context: bigint): Document | undefined {
 const s = document(session), p = object(s.revert), result = object(p.result), target = object(result.target), prompt = object(target.prompt);
 if (p.action_id !== action || target.message_id !== message || revision(target.context_revision) !== context || revision(result.context_revision) !== context + 1n || revision(s.context_revision) !== context + 1n || s.compaction_job_id || s.recovery !== "none" || !text(p.job_id) || !Array.isArray(result.retained_turn_ids) || result.retained_turn_ids.includes(target.native_turn_id) || typeof prompt.prompt !== "string" || !["execute", "plan"].includes(text(prompt.mode))) return;
 return prompt;
}
export function useSessionRevert({session,active,draft,restore,composer,blocked,changed}:{session?:Resource;active:boolean;draft:string;restore:(prompt:string,mode:string)=>boolean|void;composer:RefObject<HTMLTextAreaElement|null>;blocked:boolean;changed:(resource:Resource)=>void}) {
 useLocale();
 const status=useQuery(SystemQuery.getStatus,{}, {enabled:active});
 const machine=useQuery(ResourceQuery.getResource,{kind:EntityKind.MACHINE,id:text(document(session).machine_id)},{enabled:active && Boolean(session)});
 const supported=Boolean(status.data?.capabilities.includes(SystemCapability.CODEX_SESSION_REVERT_V1) && !status.error && !machine.error && Array.isArray(document(machine.data?.resource).worker_capabilities) && (document(machine.data?.resource).worker_capabilities as unknown[]).includes("codex-session-revert-v1"));
 const [selected,setSelected]=useState<{row:Resource;source:Resource}>();
 const [pending,setPending]=useState<{action:string;message:string;context:bigint;draft:string}>();
 const [replacement,setReplacement]=useState<Document>();
 const restoredAction=useRef<string | undefined>(undefined);
 const originalSession=useRef(session?.id);
 useEffect(()=>{if(originalSession.current!==session?.id){originalSession.current=session?.id;setSelected(undefined);setPending(undefined);setReplacement(undefined);restoredAction.current=undefined;}},[session?.id]);
 const latestDraft=useRef(draft);latestDraft.current=draft;
 const current=useQuery(ResourceQuery.getResource,{kind:EntityKind.SESSION,id:session?.id ?? ""},{enabled:active && Boolean(pending),refetchInterval:pending?2000:false});
 useEffect(()=>{const row=current.data?.resource;if(row && row.id===session?.id && row.revision>session.revision)changed(row);},[current.data?.resource,session,changed]);
 const mutation=useRetainedMutation(`revert:${session?.id ?? ""}`,SessionQuery.revertSession,()=>{},(reply,request)=>{
  const input=object(document(reply.job).input),target=object(input.revert);
  return reply.requestId===request.mutation?.requestId && reply.job?.kind===EntityKind.JOB && reply.job.sessionId===request.mutation.id && input.version===4 && input.action_id===request.mutation.requestId && target.message_id===request.messageId && target.native_turn_id===request.beforeTurnId && revision(target.context_revision)===request.expectedContextRevision;
 });
 const original=session && current.data?.resource?.id===session.id && current.data.resource.revision>session.revision ? current.data.resource : session;
 useEffect(()=>{
  if(!active || !pending)return;
  const prompt=verifiedRevertDraft(original,pending.action,pending.message,pending.context);if(!prompt)return;
  // Native completion cannot overwrite a draft edited while the original job
  // was pending, or restore text into an inactive conversation.
  if(latestDraft.current===pending.draft && restore(text(prompt.prompt),text(prompt.mode))!==false){composer.current?.focus();restoredAction.current=pending.action;setReplacement(undefined);}else setReplacement(prompt);
  setPending(undefined);setSelected(undefined);
 },[original,pending,active]);
 useEffect(()=>{if(mutation.error && !mutation.uncertain && !mutation.busy)setPending(undefined);},[mutation.error,mutation.uncertain,mutation.busy]);
 useEffect(()=>{if(pending || replacement)return;const retained=object(document(original).revert),result=object(retained.result),target=object(result.target),context=revision(target.context_revision);if(text(retained.action_id) && restoredAction.current!==retained.action_id && context!==undefined){const prompt=verifiedRevertDraft(original,text(retained.action_id),text(target.message_id),context);if(prompt)setReplacement(prompt);}},[original,pending,replacement]);
 const open=(row:Resource)=>{if(session && !blocked && !mutation.busy && !mutation.uncertain && !pending && revertEligible(session,row,supported))setSelected({row,source:session});};
 const confirm=()=>{
  if(!selected || !session || selected.source.id!==session.id || selected.source.revision!==session.revision || blocked || mutation.busy || mutation.uncertain || pending || !revertEligible(session,selected.row,supported))return;
  const action=newRequestId(),target=document(selected.row),context=revision(document(session).context_revision)!;
  setPending({action,message:selected.row.id,context,draft:latestDraft.current});
  setSelected(undefined);
  void mutation.send({mutation:{id:session.id,expectedRevision:session.revision,requestId:action},messageId:selected.row.id,beforeTurnId:text(target.native_turn_id),expectedContextRevision:context});
 };
 const action=(row:Resource)=>revertEligible(session,row,supported)?<button type="button" disabled={blocked || mutation.busy || mutation.uncertain || Boolean(pending)} onClick={()=>open(row)}>{copy("session.revertAndEdit")}</button>:null;
 const content=<>
 {selected ? <Modal title={copy("session.revertAndEdit")} close={()=>setSelected(undefined)} focusClose trapFocus><p>{copy("session.revertConfirmation")}</p><pre className="revert-prompt">{text(document(selected.row).text)}</pre>{draft ? <p>{copy("session.revertDraftWarning")}</p>:null}<button type="button" disabled={blocked || mutation.busy || mutation.uncertain || Boolean(pending) || selected.source.revision!==session?.revision} onClick={confirm}>{copy("session.revertAndEdit")}</button></Modal>:null}
 {pending ? <p role="status">{copy("session.revertPending")}</p>:null}
 {replacement ? <div role="status"><p>{copy("session.revertDraftChanged")}</p><button type="button" onClick={()=>{if(restore(text(replacement.prompt),text(replacement.mode))!==false){restoredAction.current=text(object(document(original).revert).action_id);setReplacement(undefined);composer.current?.focus();}}}>{copy("session.restoreRevertedPrompt")}</button></div>:null}
 {object(object(document(original).revert).result).target ? <p>{copy("session.revertHistoricalAttachments")}</p>:null}
 <Problem error={mutation.error || current.error}/>{mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("session.retrySameRevert")}</button>:null}
 </>;
 return {action,content,pending:Boolean(pending),uncertain:mutation.uncertain};
}
