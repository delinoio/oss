// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionDirectoryQuery, SystemCapability, SystemQuery, newRequestId, type Resource, type ChangeSessionDirectoryRequest, type SessionDirectoryOperation } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation, useRetainedMutationReceipt } from "./mutation";
import { useWorkspaceReader } from "./session-files";
import { FileOperation, type Root } from "./session-files-observation";
import { canonicalDirectory, directoryDisplayPath, directoryEligible, directorySource, directorySettled, safeDirectoryRoots, verifiedDirectoryOperation } from "./session-directory-model";
import { Modal } from "./ui";
import { validRunnerObservation } from "./runner-observation";

export function useSessionDirectory(session: Resource | undefined, active: boolean, blocked: boolean) {
 useLocale();
 const status=useQuery(SystemQuery.getStatus,{}, {enabled:active});
 const machineId=text(document(session).machine_id);
 const machine=useQuery(ResourceQuery.getResource,{kind:EntityKind.MACHINE,id:machineId},{enabled:active&&Boolean(machineId)});
 const supported=Boolean(!status.error && !machine.error && machine.data?.resource?.id===machineId && validRunnerObservation(machine.data.resource) && document(machine.data.resource).disabled!==true && status.data?.capabilities.includes(SystemCapability.SESSION_DIRECTORY_V1) && Array.isArray(document(machine.data.resource).worker_capabilities) && (document(machine.data.resource).worker_capabilities as unknown[]).includes("session-directory-v1"));
 const key=`session-directory:${session?.id ?? ""}`;
 const [selection,setSelection]=useState<{session:Resource;roots?:Root[];repository:string;path:string;error?:boolean}>();
 const [knownRoots,setKnownRoots]=useState<Root[]>();
 const [invalidReceipt,setInvalidReceipt]=useState(false);
 const [observed,setObserved]=useState<SessionDirectoryOperation>();
 const source=session ? directorySource(session) : {execution:"",previous:"",sourceJob:""};
 const mutation=useRetainedMutation(key,SessionDirectoryQuery.changeSessionDirectory,reply=>setObserved(reply.operation),undefined,true,reply=>directorySettled(reply.operation));
 const acceptReceipt=useRetainedMutationReceipt(key);
 const retained=mutation.input as ChangeSessionDirectoryRequest | undefined;
 const operation=observed;
 const recovery=useQuery(SessionDirectoryQuery.getSessionDirectoryOperation,{sessionId:retained?.mutation?.id ?? "",requestId:retained?.mutation?.requestId ?? ""},{enabled:false,retry:false});
 const lastRead=useRef<unknown>(undefined);
 useEffect(()=>{
  if(!recovery.data || recovery.data===lastRead.current || !retained)return;
  lastRead.current=recovery.data;
  if(acceptReceipt(recovery.data)){setObserved(recovery.data.operation);setInvalidReceipt(false);}else {setInvalidReceipt(true);console.warn("delidev.session_directory.receipt_rejected",{classification:"identity-mismatch"});}
 },[recovery.data,retained,acceptReceipt]);
 const rootsRead=useWorkspaceReader(session?.id ?? "");
 useEffect(()=>{
  if(!selection || selection.roots || selection.error)return;
  const controller=new AbortController(),original=selection.session;
  void rootsRead({operation:FileOperation.Roots},controller.signal).then(value=>{
   if(controller.signal.aborted)return;
   const roots=safeDirectoryRoots(value.roots);
   if(roots)setKnownRoots(roots);
   setSelection(previous=>previous?.session===original ? {...previous,roots,repository:roots?.find(root=>root.primary)?.repository_id ?? roots?.[0]?.repository_id ?? "",error:!roots}:previous);
  },()=>{if(!controller.signal.aborted)setSelection(previous=>previous?.session===original?{...previous,error:true}:previous);});
  return()=>controller.abort();
 },[selection,rootsRead]);
 const pending=Boolean(retained || document(session).directory_job_id);
 const open=()=>{if(session && !blocked && !pending && directoryEligible(session,supported)){setObserved(undefined);setSelection({session,repository:"",path:"."});}};
 const confirm=()=>{
  if(!selection?.roots || !session || selection.session.id!==session.id || selection.session.revision!==session.revision || blocked || pending || !directoryEligible(session,supported) || !canonicalDirectory(selection.path) || !selection.roots.some(root=>root.repository_id===selection.repository))return;
  const originalSource={...source};let originalJob:string|undefined;
  const request={mutation:{id:session.id,expectedRevision:session.revision,requestId:newRequestId()},repositoryId:selection.repository,relativePath:selection.path};
  const selectedIndex=selection.roots.findIndex(root=>root.repository_id===selection.repository);
  const selectedRoot=selection.roots[selectedIndex];
  const label=selectedRoot.name+(selection.roots.filter(root=>root.name===selectedRoot.name).length>1?` (${selectedIndex+1})`:"");
  setSelection(undefined);
  void mutation.send(request,(reply,original)=>{
   if(!verifiedDirectoryOperation(reply.operation,original,originalSource,originalJob))return false;
   originalJob ??=reply.operation!.job!.id;
   return true;
  },label);
 };
 const current=object(document(session).directory),verified=operation?.generation;
 const displayRepository=knownRoots?.find(root=>root.repository_id===(verified?.repositoryId ?? text(current.repository_id)))?.name;
 const pendingRepository=mutation.label ?? knownRoots?.find(root=>root.repository_id===retained?.repositoryId)?.name;
 const displayPath=verified?.relativePath ?? (canonicalDirectory(text(current.relative_path))?text(current.relative_path):"");
 const content=<>
 {supported ? <button type="button" disabled={blocked||pending||!directoryEligible(session,supported)} onClick={open}>{copy("session.directoryChange")}</button>:null}
 {displayPath ? <p>{copy("session.directoryCurrent",{path:directoryDisplayPath(displayPath),repository:displayRepository ?? ""})}</p>:null}
 {pending ? <p role="status">{copy("session.directoryPending",{path:directoryDisplayPath(retained?.relativePath ?? ""),repository:pendingRepository ?? ""})}</p>:null}
 {retained ? <button type="button" disabled={mutation.busy||recovery.isFetching} onClick={()=>void recovery.refetch()}>{copy("session.directoryObserve")}</button>:null}
 {invalidReceipt||mutation.error||recovery.error ? <p role="alert">{copy("session.directoryUncertain")}</p>:null}
 {operation && directorySettled(operation) ? <p role="status">{copy(verified?"session.directoryVerified":"session.directoryFailed")}</p>:null}
 {selection ? <Modal title={copy("session.directoryChange")} close={()=>setSelection(undefined)} trapFocus><p>{copy("session.directoryScope")}</p>
 {selection.roots ? <><label>{copy("session.directoryRepository")}<select value={selection.repository} onChange={event=>setSelection({...selection,repository:event.target.value})}>{selection.roots.map((root,index)=><option key={root.repository_id} value={root.repository_id}>{root.name}{selection.roots!.filter(other=>other.name===root.name).length>1?` (${index+1})`:""}</option>)}</select></label>
 <label>{copy("session.directoryRelative")}<input autoFocus value={selection.path} onChange={event=>setSelection({...selection,path:event.target.value})} autoComplete="off" spellCheck={false}/></label>
 {!canonicalDirectory(selection.path)?<p role="alert">{copy("session.directoryInvalid")}</p>:null}
 <button type="button" disabled={blocked||pending||selection.session.revision!==session?.revision||!canonicalDirectory(selection.path)} onClick={confirm}>{copy("session.directoryChange")}</button></>:<p role="status">{copy(selection.error?"session.directoryRootsFailed":"session.directoryLoading")}</p>}
 </Modal>:null}
 </>;
 return {pending,content};
}
