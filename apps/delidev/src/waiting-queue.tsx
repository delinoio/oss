// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, FailureCode, SessionQuery, newRequestId, isEntityId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document } from "./documents";
import { copy, useLocale } from "./localization";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation } from "./scroll-continuation";
import { QueuedInput, type QueuedInputDraft } from "./queue";
import { useRetainedMutation } from "./mutation";
import { Failure, Problem } from "./ui";

type WaitingRow = { id: string; revision: bigint; generation: bigint; count: number };
export function WaitingQueue({ sessionId, session, active, revision, drafts, saveDraft, readOnly, refreshHistory }: {
 sessionId: string; session?: Resource; active: boolean; revision: string;
 drafts: ReadonlyMap<string, QueuedInputDraft>; saveDraft: (id: string, value?: QueuedInputDraft) => void;
 readOnly: boolean; refreshHistory: () => void;
}) {
 useLocale();
 const root=useRef<HTMLDivElement>(null);
 const [dragged,setDragged]=useState<string>(),[insertion,setInsertion]=useState<string>(),[announcement,setAnnouncement]=useState("");
 const request=useCallback((token:string)=>({sessionId,pageSize:50,pageToken:token}),[sessionId]);
 const project=useCallback((response:{inputs:Resource[];nextPageToken:string;currentQueueGeneration:bigint;waitingCount:number})=>{
  if(response.inputs.length>50 || response.waitingCount>1000 || response.waitingCount<response.inputs.length || typeof response.currentQueueGeneration!=="bigint") throw new ConnectError("Waiting queue metadata is unavailable.",Code.DataLoss);
  const seen=new Set<string>();
  for(const row of response.inputs){if(!isEntityId(row.id) || seen.has(row.id) || row.kind!==EntityKind.QUEUE || row.sessionId!==sessionId || row.revision<1n || !supportsResourceSchema(row) || document(row).delivery!=="queued") throw new ConnectError("Waiting queue ownership is unavailable.",Code.DataLoss);seen.add(row.id);}
  return {rows:response.inputs.map(row=>({id:row.id,revision:row.revision,generation:response.currentQueueGeneration,count:response.waitingCount})),payload:response.inputs,nextPageToken:response.nextPageToken};
 },[sessionId]);
 const reader=useConnectPaginationReader(SessionQuery.listWaitingQueue,request,project);
 const query=usePaginationChain<WaitingRow,Resource>(`waiting:${sessionId}`,active,reader);
 const pendingFocus=useRef<string>();
 const pendingDown=useRef<{id:string;revision:bigint;generation:bigint}>();
 const accepted=useCallback(()=>{setAnnouncement(copy("queue.moved"));query.reload();refreshHistory();},[query.reload,refreshHistory]);
 // Session scope owns the original request even after row eviction or removal.
 const move=useRetainedMutation(`move-waiting:${sessionId}`,SessionQuery.moveQueuedInput,accepted,(result,request)=>result.change?.requestId===request.mutation?.requestId && result.change.session?.id===request.sessionId && typeof result.currentQueueGeneration==="bigint");
 const locked=readOnly || !active || move.busy || move.uncertain || !query.loaded || Boolean(query.loading || query.error);
 const previousRevision=useRef(revision);
 useEffect(()=>{if(previousRevision.current!==revision){previousRevision.current=revision;query.reload();}},[revision,query.reload]);
 useEffect(()=>{if(query.error?.failure.code===FailureCode.CursorExpired) query.reload();},[query.error,query.reload]);
 useEffect(()=>{if(move.error && !move.uncertain) query.reload();},[move.error,move.uncertain,query.reload]);
 useEffect(()=>{if(!query.loading && pendingFocus.current){const id=pendingFocus.current;pendingFocus.current=undefined;root.current?.querySelector<HTMLButtonElement>(`[data-move-id="${id}"]`)?.focus({preventScroll:true});}},[query.loading,query.rows]);
 const send=(id:string,before:string)=>{
  if(locked || id===before)return;
  const selected=query.rows.find(row=>row.id===id),anchor=query.rows.find(row=>row.id===before);
  if(!selected || before && (!anchor || anchor.generation!==selected.generation))return;
  pendingFocus.current=id;setDragged(undefined);setInsertion(undefined);
  void move.send({mutation:{id,expectedRevision:selected.revision,requestId:newRequestId()},sessionId,expectedQueueGeneration:selected.generation,beforeInputId:before,beforeInputRevision:anchor?.revision??0n});
 };
 const movement=(row:Resource)=>{
  const index=query.rows.findIndex(item=>item.id===row.id),previous=query.rows[index-1],next=query.rows[index+1],after=query.rows[index+2];
  return {disabled:locked,up:previous?()=>send(row.id,previous.id):undefined,down:index>=0 && index+1<(query.rows[index]?.count??0)?()=>{ if(next && (after || !query.nextPageToken))send(row.id,after?.id??""); else { const original=query.rows[index];pendingDown.current={id:original.id,revision:original.revision,generation:original.generation};query.append(); } }:undefined,
   start:()=>{if(!locked)setDragged(row.id);},end:()=>{setDragged(undefined);setInsertion(undefined);}};
 };
 useEffect(()=>{
  const intent=pendingDown.current;
  if(!intent || query.loading)return;
  pendingDown.current=undefined;
  const index=query.rows.findIndex(row=>row.id===intent.id),selected=query.rows[index],next=query.rows[index+1],after=query.rows[index+2];
  if(!query.error && !locked && selected?.revision===intent.revision && selected.generation===intent.generation && next && (after || !query.nextPageToken))send(intent.id,after?.id??"");
 },[query.loading,query.rows,query.error,locked]);
 const drop=(before:string)=>{if(dragged)send(dragged,before);};
 const completeEmpty=query.loaded && !query.loading && !query.error && !query.nextPageToken && !query.rows.length;
 return <><p className="visually-hidden" role="status" aria-live="polite">{announcement}</p><Problem error={move.error}/>{move.uncertain?<button disabled={move.busy} onClick={move.retry}>{copy("queue.retryMove")}</button>:null}
  <div ref={root} className="session-tray-content queue-compact-list" hidden={completeEmpty} aria-label={copy("queue.waitingInputs")}>
   {!query.loaded && !query.error?<p role="status">{copy("session.loadingQueue")}</p>:null}<Failure failure={query.error?.failure}/>
   <ScrollPayloadWindow identity={(row:WaitingRow)=>row.id} revision={(row:WaitingRow)=>row.revision} query={query} root={root} active={active}>{payload=>payload.map(row=><div key={row.id} className={insertion===row.id?"queue-insertion":undefined} onDragOver={event=>{if(dragged && !locked && dragged!==row.id){event.preventDefault();setInsertion(row.id);}}} onDrop={event=>{event.preventDefault();drop(row.id);}}><QueuedInput compact resource={row} session={session} active={active} refresh={()=>{query.reload();refreshHistory();}} draft={drafts.get(row.id)} changeDraft={value=>saveDraft(row.id,value)} readOnly={locked} movement={movement(row)}/></div>)}</ScrollPayloadWindow>
   {!query.nextPageToken && query.rows.length?<div className={`queue-drop-end${insertion==="end"?" queue-insertion":""}`} onDragOver={event=>{if(dragged && !locked){event.preventDefault();setInsertion("end");}}} onDrop={event=>{event.preventDefault();drop("");}}/>:null}
   <ScrollContinuation query={query} root={root} active={active && !completeEmpty} label={copy("session.queuePages_1acdd8")}/>
  </div></>;
}
