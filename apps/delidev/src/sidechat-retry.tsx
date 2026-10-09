// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, items } from "./documents";
import { useSessionQuery } from "./session-activity";
import { useRetainedMutation } from "./mutation";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";
import type { ConversationProjection } from "./tool-turn-projection";
import "./sidechat-retry.css";

const uuid=(v:unknown):v is string=>typeof v==="string"&&/^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(v);
const revision=(v:unknown):v is string=>typeof v==="string"&&/^[1-9][0-9]{0,18}$/.test(v)&&BigInt(v)<(1n<<63n);
interface RetryView { candidate:boolean; eligible:boolean; child_revision:string; question_id?:string; question_revision:string; parent_id?:string; parent_revision:string; parent_turn_id?:string; current_answer?:string; generations:Record<string,unknown>[]; observed_generation?:string; phase?:string; problem?:Record<string,unknown> }
export function parseSidechatRetryView(bytes:Uint8Array):RetryView|undefined {
 if(bytes.length>1<<20)return;
 try {
  const v=JSON.parse(new TextDecoder("utf-8",{fatal:true}).decode(bytes));
  if(!v||typeof v!=="object"||Array.isArray(v)||Object.keys(v).some(k=>!["candidate","eligible","child_revision","question_id","question_revision","parent_id","parent_revision","parent_turn_id","current_answer","generations","observed_generation","phase","problem"].includes(k))||typeof v.candidate!=="boolean"||typeof v.eligible!=="boolean"||!revision(v.child_revision)||!Array.isArray(v.generations)||v.generations.length>4096)return;
  if(v.candidate&&(!uuid(v.question_id)||!revision(v.question_revision)||!uuid(v.parent_id)))return;
  if(v.eligible&&(!v.candidate||!revision(v.parent_revision)||typeof v.parent_turn_id!=="string"||!v.parent_turn_id||v.parent_turn_id.length>1024))return;
  if(v.current_answer!==undefined&&!uuid(v.current_answer)||v.observed_generation!==undefined&&!uuid(v.observed_generation))return;
  if(v.generations.some((g:unknown)=>{const d=object(g);return !uuid(d.id)||!uuid(d.fork_job_id)||!uuid(d.runtime_id)||!uuid(d.question_id)||!revision(d.question_revision)||!revision(d.parent_revision)||!uuid(d.parent_execution_id)||!uuid(d.worker_device_id)||!uuid(d.worker_instance_id)||typeof d.parent_turn_id!=="string"||d.execution_id!==undefined&&!uuid(d.execution_id)||d.execution_job_id!==undefined&&!uuid(d.execution_job_id)||d.completed!==undefined&&typeof d.completed!=="boolean";}))return;
  if(v.observed_generation&&!v.generations.some((g:Record<string,unknown>)=>g.id===v.observed_generation))return;
  return v;
 }catch{return;}
}

export function useSidechatQuestionRetry(session:Resource|undefined,active:boolean) {
 const data=document(session),sidechat=Boolean(object(data.fork).sidechat_parent_snapshot),id=session?.id??"";
 const client=useQueryClient();
 const [observed,setObserved]=useState<string>();
 const status=useSessionQuery(SystemQuery.getStatus,{}, {enabled:sidechat&&active});
 const supported=status.data?.capabilities.includes(SystemCapability.SIDECHAT_QUESTION_RETRY_V1)===true;
 const mutation=useRetainedMutation(`sidechat-question-retry:${id}`,SessionQuery.retrySidechatQuestion,(response)=>{setObserved(response.requestId);void client.invalidateQueries({refetchType:"active"});},(response,request)=>{
  const view=parseSidechatRetryView(response.documentJson);
  const generation=view?.generations.find(g=>g.id===request.mutation?.requestId);
  return response.requestId===request.mutation?.requestId&&view?.observed_generation===response.requestId&&generation?.question_id===request.questionId&&generation?.question_revision===request.expectedQuestionRevision.toString()&&generation?.parent_revision===request.expectedParentRevision.toString()&&generation?.parent_turn_id===request.expectedParentTurnId;
 });
 const retained=text(object(object(mutation.input).mutation).requestId);
 const generation=retained||observed||text(data.sidechat_active_retry);
 const query=useSessionQuery(SessionQuery.getSidechatQuestionRetry,{sessionId:id,requestId:generation},{enabled:sidechat&&supported&&active,refetchInterval:active?1500:false});
 const view=query.data?parseSidechatRetryView(query.data.documentJson):undefined;
 const blocked=!active||!supported||!view?.eligible||query.isFetching||view.child_revision!==session?.revision.toString()||mutation.busy||mutation.uncertain;
 const send=()=>{
  if(blocked||!view||!session||!uuid(view.question_id)||!revision(view.question_revision)||!revision(view.parent_revision)||!view.parent_turn_id)return;
  void mutation.send({mutation:{id,expectedRevision:BigInt(view.child_revision),requestId:newRequestId()},questionId:view.question_id,expectedQuestionRevision:BigInt(view.question_revision),expectedParentRevision:BigInt(view.parent_revision),expectedParentTurnId:view.parent_turn_id});
 };
 return {view,blocked,send,mutation,query,supported,sidechat,active,unsupported:status.isSuccess&&!supported};
}
export type SidechatRetryController=ReturnType<typeof useSidechatQuestionRetry>;

export function SidechatRetryAction({controller,inputId}:{controller:SidechatRetryController;inputId:string}) {
 useLocale();
 if(!controller.view?.candidate||controller.view.question_id!==inputId)return null;
 const reason=controller.mutation.uncertain?copy("sidechat.retry.uncertain"):!controller.supported?copy("sidechat.retry.unsupported"):controller.view.problem?.code==="unsupported"?copy("sidechat.retry.unsupported"):controller.view.problem?.code==="recovery_required"?copy("sidechat.retry.recovery"):controller.view.phase==="failed"||controller.view.phase==="canceled"?copy("sidechat.retry.failed"):controller.blocked?copy("sidechat.retry.busy"):undefined;
 return <div className="sidechat-retry-actions"><SidechatRetryPopover active={controller.active} disabled={controller.blocked} onRetry={controller.send}/>{reason?<small role="status">{reason}</small>:null}<Problem error={controller.query.error}/><Problem error={controller.mutation.error}/>{controller.mutation.uncertain?<button type="button" disabled={controller.mutation.busy||!controller.active} onClick={controller.mutation.retry}>{copy("sidechat.retry.originalRequest")}</button>:null}</div>;
}

export function SidechatRetryPopover({active=true,disabled,onRetry}:{active?:boolean;disabled:boolean;onRetry:()=>void}) {
 useLocale();
 const id=useId(),timer=useRef<ReturnType<typeof setTimeout>|undefined>(undefined),node=useRef<HTMLSpanElement>(null);
 const [open,setOpen]=useState(false);
 const clear=()=>{if(timer.current!==undefined){clearTimeout(timer.current);timer.current=undefined;}};
 const close=()=>{clear();setOpen(false);};
 useEffect(()=>{if(!active)close();return clear;},[active]);
 return <span className="sidechat-retry-help" ref={node} onPointerEnter={()=>{clear();if(active&&!open)timer.current=setTimeout(()=>setOpen(true),300);}} onPointerLeave={()=>{if(!node.current?.contains(window.document.activeElement))close();}} onFocusCapture={()=>{clear();if(active)setOpen(true);}} onBlurCapture={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node|null))close();}} onKeyDown={event=>{if(event.key==="Escape"){event.preventDefault();event.stopPropagation();close();}}}>
  <button type="button" aria-disabled={disabled||!active} aria-describedby={id} onClick={()=>{if(active&&!disabled)onRetry();}}>{copy("sidechat.retry.label")}</button>
  <span id={id} role="tooltip" hidden={!open}>{copy("sidechat.retry.explanation")}</span>
 </span>;
}

// Canonical execution/input IDs are presentation selectors only. No messages
// are removed and pagination keeps its original bounded payload window.
export function sidechatAnswerFilter(session:Resource|undefined,history=false):(row:ConversationProjection)=>boolean {
 const d=document(session),generations=items(d.sidechat_retries).map(object),current=text(d.sidechat_current_answer);
 const question=text(generations[0]?.question_id);
 if(!current||!question||!object(d.fork).sidechat_parent_snapshot)return ()=>!history;
 return row=>{
  if(row.inherited)return !history;
  if(row.role==="user")return !history&&row.inputId===question;
  if(!row.executionId)return !history;
  return history?row.executionId!==current:row.executionId===current;
 };
}
