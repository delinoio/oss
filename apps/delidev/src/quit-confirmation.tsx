// SPDX-License-Identifier: Apache-2.0
import { createClient, type Transport } from "@connectrpc/connect";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { SystemService } from "@delinoio/delidev-api-client";
import { DialogSurface, Problem } from "./ui";
import { copy, useLocale } from "./localization";
import "./quit-confirmation.css";
interface QuitConnection { transport?:Transport; ready:boolean }
const QuitConnectionContext=createContext<((value:QuitConnection)=>void)|undefined>(undefined);
/** The presenter precedes role discovery; transport ownership remains explicit. */
export function QuitConnectionProvider({children}:{children:ReactNode}) {
 const [connection,setConnection]=useState<QuitConnection>({ready:false});
 return <QuitConnectionContext.Provider value={setConnection}><QuitConfirmation {...connection}/>{children}</QuitConnectionContext.Provider>;
}
export function useQuitConnection(transport:Transport|undefined,ready:boolean) {
 const publish=useContext(QuitConnectionContext);
 useEffect(()=>{publish?.({transport,ready});return()=>publish?.({ready:false});},[publish,transport,ready]);
}
export interface QuitAttempt { id:string; checking:boolean; unknown:boolean; count:string; present:boolean; observe:boolean }
export function validQuitCount(value:unknown):value is string { return typeof value==="string" && /^(0|[1-9][0-9]{0,38})$/.test(value) && BigInt(value)<1n<<128n; }
export function QuitConfirmation({ ready, transport }: { ready:boolean; transport?:Transport }) {
 useLocale();const client=useMemo(()=>transport ? createClient(SystemService,transport) : undefined,[transport]);
 const [attempt,setAttempt]=useState<QuitAttempt>(), [error,setError]=useState<unknown>();
 const dialog=useRef<HTMLDialogElement>(null),cancel=useRef<HTMLButtonElement>(null),seen=useRef(new Set<string>()),currentAttempt=useRef<string|null>(null),controllersRetired=useRef<(()=>void)|undefined>(undefined);
 useEffect(()=>{
  if(!isTauri())return;let disposed=false, unlisten:(()=>void)|undefined, timer:ReturnType<typeof setInterval>|undefined;const controllers=new Set<AbortController>();controllersRetired.current=()=>{controllers.forEach(value=>value.abort());clearInterval(timer);timer=undefined;};let sequence=0;
  const update=async()=>{const generation=++sequence;try{const next=await invoke<QuitAttempt|null>("read_quit_attempt");if(disposed||generation!==sequence)return;
    if(next && (!validQuitCount(next.count) || !/^[a-f0-9-]{36}$/.test(next.id)))return;
    if(currentAttempt.current!==(next?.id??null)){controllers.forEach(value=>value.abort());currentAttempt.current=next?.id??null;}
    setAttempt(next?.present?next:undefined);setError(undefined);
    if(next?.present&&timer===undefined)timer=setInterval(()=>void update(),1000);
    else if(!next?.present){clearInterval(timer);timer=undefined;}
    if(next?.checking && next.observe && !seen.current.has(next.id)) {seen.current.add(next.id);if(seen.current.size>128)seen.current.delete(seen.current.values().next().value!);const abort=new AbortController();controllers.add(abort);const timeout=setTimeout(()=>abort.abort(),4900);
      let count:string|null=null;try {if(ready && client){const result=await client.getOverview({}, {signal:abort.signal,timeoutMs:4900});if(result.activeSessions>=0n && result.activeSessions<1n<<64n && result.observedAt.length<=64 && Number.isFinite(Date.parse(result.observedAt)) && !abort.signal.aborted)count=result.activeSessions.toString();}}catch{/* Native retains unknown status for unavailable fresh observations. */}finally{clearTimeout(timeout);controllers.delete(abort);}
      if(!disposed&&!abort.signal.aborted&&currentAttempt.current===next.id)await invoke("observe_quit_attempt",{id:next.id,count});
    }
  }catch{if(!disposed&&generation===sequence)setError(copy("quit-confirmation.unavailable"));}};
  void listen("quit-attempt",()=>void update()).then(value=>{if(disposed)value();else{unlisten=value;void update();}}).catch(()=>{});
  return()=>{clearInterval(timer);disposed=true;unlisten?.();controllers.forEach(value=>value.abort());controllersRetired.current=undefined;};
 },[client,ready]);
 useEffect(()=>{if(!attempt?.present)return;const opener=document.activeElement as HTMLElement|null;try {dialog.current?.showModal();cancel.current?.focus();void invoke("present_quit_attempt",{id:attempt.id}).catch(()=>{if(currentAttempt.current===attempt.id)setError(copy("quit-confirmation.unavailable"));});} catch { setError(copy("quit-confirmation.unavailable")); }return()=>{dialog.current?.close();if(opener?.isConnected&&!opener.closest('[hidden],[inert]'))opener.focus({preventScroll:true});};},[attempt?.id]);
 const decide=async(decision:"cancel"|"confirm")=>{if(!attempt)return;try{await invoke("decide_quit_attempt",{id:attempt.id,decision});if(currentAttempt.current===attempt.id&&decision==="cancel"){currentAttempt.current=null;setAttempt(undefined);setError(undefined);controllersRetired.current?.();}}catch{if(currentAttempt.current===attempt.id)setError(copy("quit-confirmation.unavailable"));}};
 if(!attempt?.present)return null;
 return <DialogSurface ref={dialog} className="quit-confirmation" role="alertdialog" aria-labelledby="quit-confirmation-title" aria-describedby="quit-confirmation-explanation" onCancel={()=>void decide("cancel")} onKeyDown={event=>{if(event.key!=="Tab")return;const controls=[...event.currentTarget.querySelectorAll<HTMLElement>("summary,button,input,select,textarea,a[href],[tabindex]")].filter(node=>node.tabIndex>=0&&!node.matches(":disabled")&&node.getClientRects().length>0&&!node.closest("[hidden],[inert]")),first=controls[0],last=controls.at(-1);if(first&&last&&(event.shiftKey?document.activeElement===first:document.activeElement===last)){event.preventDefault();(event.shiftKey?last:first).focus();}}}>
  <h2 id="quit-confirmation-title">{copy("quit-confirmation.title")}</h2>
  {attempt.checking?<p role="status">{copy("quit-confirmation.checking")}</p>:<>{attempt.unknown?<p>{copy("quit-confirmation.unknown")}</p>:null}{BigInt(attempt.count)>0n?<p>{copy("quit-confirmation.count",{count:attempt.count})}</p>:null}</>}
  <p id="quit-confirmation-explanation">{copy("quit-confirmation.explanation")}</p><Problem error={error}/>
  <footer><button ref={cancel} onClick={()=>void decide("cancel")}>{copy("quit-confirmation.cancel")}</button><button className="danger" disabled={attempt.checking} onClick={()=>void decide("confirm")}>{copy("quit-confirmation.confirm")}</button></footer>
 </DialogSurface>;
}
