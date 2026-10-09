// SPDX-License-Identifier: Apache-2.0
import { createClient, type Transport } from "@connectrpc/connect";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { useEffect, useMemo, useRef, useState } from "react";
import { SystemService } from "@delinoio/delidev-api-client";
import { DialogSurface, Problem } from "./ui";
import { copy, useLocale } from "./localization";
import "./quit-confirmation.css";
export interface QuitAttempt { id:string; checking:boolean; unknown:boolean; count:string; present:boolean; observe:boolean }
export function validQuitCount(value:unknown):value is string { return typeof value==="string" && /^(0|[1-9][0-9]{0,38})$/.test(value) && BigInt(value)<1n<<128n; }
export function QuitConfirmation({ ready, transport }: { ready:boolean; transport?:Transport }) {
 useLocale();const client=useMemo(()=>transport ? createClient(SystemService,transport) : undefined,[transport]);
 const [attempt,setAttempt]=useState<QuitAttempt>(), [error,setError]=useState<unknown>();
 const dialog=useRef<HTMLDialogElement>(null),cancel=useRef<HTMLButtonElement>(null),seen=useRef(new Set<string>());
 useEffect(()=>{
  if(!isTauri())return;let disposed=false, unlisten:(()=>void)|undefined;const controllers=new Set<AbortController>();let sequence=0;
  const update=async()=>{const generation=++sequence;try{const next=await invoke<QuitAttempt|null>("read_quit_attempt");if(disposed||generation!==sequence)return;
    if(next && (!validQuitCount(next.count) || !/^[a-f0-9-]{36}$/.test(next.id)))return;
    setAttempt(next?.present?next:undefined);setError(undefined);
    if(next?.checking && next.observe && !seen.current.has(next.id)) {seen.current.add(next.id);if(seen.current.size>128)seen.current.delete(seen.current.values().next().value!);const abort=new AbortController();controllers.add(abort);const timeout=setTimeout(()=>abort.abort(),4900);
      let count:string|null=null;try {if(ready && client){const result=await client.getOverview({}, {signal:abort.signal,timeoutMs:4900});if(result.activeSessions>=0n && result.activeSessions<1n<<64n && result.observedAt.length<=64 && Number.isFinite(Date.parse(result.observedAt)) && !abort.signal.aborted)count=result.activeSessions.toString();}}catch{/* Native retains unknown status for unavailable fresh observations. */}finally{clearTimeout(timeout);controllers.delete(abort);}
      if(!disposed&&!abort.signal.aborted)await invoke("observe_quit_attempt",{id:next.id,count});
    }
  }catch{if(!disposed)setError(copy("quit-confirmation.unavailable"));}};
  void listen("quit-attempt",()=>void update()).then(value=>{if(disposed)value();else{unlisten=value;void update();}}).catch(()=>{});
  const timer=setInterval(()=>void update(),1000);
  return()=>{clearInterval(timer);disposed=true;unlisten?.();controllers.forEach(value=>value.abort());};
 },[client,ready]);
 useEffect(()=>{if(!attempt?.present)return;const opener=document.activeElement as HTMLElement|null;try {dialog.current?.showModal();cancel.current?.focus();void invoke("present_quit_attempt",{id:attempt.id}).catch(()=>setError(copy("quit-confirmation.unavailable")));} catch { setError(copy("quit-confirmation.unavailable")); }return()=>{dialog.current?.close();if(opener?.isConnected&&!opener.closest('[hidden],[inert]'))opener.focus({preventScroll:true});};},[attempt?.id]);
 const decide=async(decision:"cancel"|"confirm")=>{if(!attempt)return;try{await invoke("decide_quit_attempt",{id:attempt.id,decision});if(decision==="cancel")setAttempt(undefined);}catch{setError(copy("quit-confirmation.unavailable"));}};
 if(!attempt?.present)return null;
 return <DialogSurface ref={dialog} className="quit-confirmation" role="alertdialog" aria-labelledby="quit-confirmation-title" aria-describedby="quit-confirmation-explanation" onCancel={()=>void decide("cancel")} onKeyDown={event=>{if(event.key!=="Tab")return;const controls=[...event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)")],first=controls[0],last=controls.at(-1);if(first&&last&&(event.shiftKey?document.activeElement===first:document.activeElement===last)){event.preventDefault();(event.shiftKey?last:first).focus();}}}>
  <h2 id="quit-confirmation-title">{copy("quit-confirmation.title")}</h2>
  {attempt.checking?<p role="status">{copy("quit-confirmation.checking")}</p>:<>{attempt.unknown?<p>{copy("quit-confirmation.unknown")}</p>:null}{BigInt(attempt.count)>0n?<p>{copy("quit-confirmation.count",{count:attempt.count})}</p>:null}</>}
  <p id="quit-confirmation-explanation">{copy("quit-confirmation.explanation")}</p><Problem error={error}/>
  <footer><button ref={cancel} onClick={()=>void decide("cancel")}>{copy("quit-confirmation.cancel")}</button><button className="danger" disabled={attempt.checking} onClick={()=>void decide("confirm")}>{copy("quit-confirmation.confirm")}</button></footer>
 </DialogSurface>;
}
