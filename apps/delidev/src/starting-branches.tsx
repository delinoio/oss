// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, ResourceService, WorkerService, WorkerCapability, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text } from "./documents";
import { copy, useLocale } from "./localization";
import "./starting-branches.css";
import { Problem, ServiceProblem } from "./ui";

const maxInventoryBytes = 8 << 20;
const maxJobBytes = 9 << 20;
function compareUTF8(left: string, right: string): number {
  const encoder = new TextEncoder();
  const a = encoder.encode(left);
  const b = encoder.encode(right);
  for (let index = 0; index < Math.min(a.length, b.length); index++) {
    if (a[index] !== b[index]) return a[index] - b[index];
  }
  return a.length - b.length;
}

export function readBranchInventory(row: Resource | undefined, identity: {project: Resource; repository: Resource; machine: Resource}): { state: string; branches: string[]; remote: string; problem?: unknown } {
  if (!row || row.kind !== EntityKind.JOB || !supportsResourceSchema(row) || row.documentJson.byteLength > maxJobBytes) throw new ConnectError("Invalid branch discovery job", Code.InvalidArgument);
  const job = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(row.documentJson)));
  const input = object(job.input), output = object(job.output);
  const {project,repository,machine}=identity;
  if ([project,repository,machine].some(row=>!Number.isSafeInteger(Number(row.revision)))) throw new ConnectError("Unsafe branch revision",Code.InvalidArgument);
  if (job.type !== "discover-repository-branches" || input.project_id !== project.id || input.project_revision !== Number(project.revision) || input.repository_id !== repository.id || input.repository_revision !== Number(repository.revision) || input.machine_id !== machine.id || input.machine_revision !== Number(machine.revision)) throw new ConnectError("Stale branch discovery job", Code.InvalidArgument);
  const remote=text(input.remote);
  if (!remote) throw new ConnectError("Invalid branch discovery remote", Code.InvalidArgument);
  if (job.state !== "succeeded") return {state:text(job.state),branches:[],remote,problem:job.problem};
  if (output.project_id !== input.project_id || output.project_revision !== input.project_revision || output.repository_id !== input.repository_id || output.repository_revision !== input.repository_revision || output.machine_id !== input.machine_id || output.machine_revision !== input.machine_revision || output.remote !== remote || !text(output.observed_at) || new TextEncoder().encode(JSON.stringify(output)).byteLength > maxInventoryBytes || !Array.isArray(output.branches) || output.branches.length > 10000) throw new ConnectError("Invalid branch inventory", Code.InvalidArgument);
  const branches = output.branches;
  if (branches.some((branch,index) => typeof branch !== "string" || !branch || new TextEncoder().encode(branch).byteLength > 1024 || /[\x00-\x20\x7f-\x9f~^:?*\[\\]/.test(branch) || branch.includes("..") || branch.includes("@{") || branch.startsWith("/") || branch.endsWith("/") || branch.endsWith(".") || branch.split("/").some(part => !part || part.startsWith(".") || part.endsWith(".lock")) || index > 0 && compareUTF8(branches[index-1], branch) >= 0)) throw new ConnectError("Malformed branch inventory", Code.InvalidArgument);
  return {state:"succeeded",branches:branches as string[],remote};
}

export function validStartingBranch(name: string): boolean {
 return Boolean(name) && name !== "@" && !name.startsWith("-") && new TextEncoder().encode(name).byteLength <= 1024 && !/[\x00-\x20\x7f-\x9f~^:?*\[\\]/.test(name) && !name.includes("..") && !name.includes("@{") && !name.startsWith("/") && !name.endsWith("/") && !name.endsWith(".") && !name.split("/").some(part => !part || part.startsWith(".") || part.endsWith(".lock"));
}
export function StartingBranches({project,machineId,starting,change,active,supported,validity}: {project:Resource;machineId:string;starting:unknown[];change:(value:unknown[])=>void;active:boolean;supported:boolean;validity?:(valid:boolean)=>void}) {
 useLocale(); const [additional,setAdditional]=useState(false); const invalid = useRef(new Set<string>()), latest = useRef(validity); latest.current = validity;
 const report = useCallback((id: string, valid: boolean) => { if (valid) invalid.current.delete(id); else invalid.current.add(id); latest.current?.(!invalid.current.size); }, []);
 useEffect(()=>()=>latest.current?.(true),[]);
 const ids=items(document(project).repositories).map(text),primary=text(document(project).primary_repository);
 const props={project,machineId,starting,change,active,supported,report};
 return <div className="starting-branches"><StartingBranch primary key={primary} repositoryId={primary} {...props}/>{ids.length>1 ? <Disclosure density={DisclosureDensity.Settings} open={additional} onToggle={event=>setAdditional(event.currentTarget.open)}><DisclosureSummary>{copy("new-session.additionalRepositories")}</DisclosureSummary>{ids.filter(id=>id!==primary).map(id=><StartingBranch key={id} repositoryId={id} {...props} active={active&&additional}/>)}</Disclosure>:null}</div>;
}
function StartingBranch({project,repositoryId,machineId,starting,change,active,supported,primary=false,report}: {report:(id:string,valid:boolean)=>void;primary?:boolean;project:Resource;repositoryId:string;machineId:string;starting:unknown[];change:(value:unknown[])=>void;active:boolean;supported:boolean}) {
  useLocale();const transport=useTransport();
  const repository=useQuery(ResourceQuery.getResource,{kind:EntityKind.REPOSITORY,id:repositoryId},{enabled:active && Boolean(repositoryId)});
  const machine=useQuery(ResourceQuery.getResource,{kind:EntityKind.MACHINE,id:machineId},{enabled:active && Boolean(machineId)});
  const [open,setOpen]=useState(false),[draft,setDraft]=useState(""),[inventory,setInventory]=useState<{branches:string[];remote:string}>(),[busy,setBusy]=useState(false),[error,setError]=useState<unknown>();
  const generation=useRef(0),controller=useRef<AbortController>(undefined);
  const repo=repository.data?.resource,runner=machine.data?.resource;
  const identity=`${project.id}:${project.revision}:${repo?.id}:${repo?.revision}:${machineId}:${runner?.revision}`;
  useLayoutEffect(()=>{generation.current++;controller.current?.abort();setInventory(undefined);setError(undefined);setBusy(false);return()=>{generation.current++;controller.current?.abort();};},[identity,transport,active]);
  const capable=supported && items(document(runner).worker_capabilities).some(value=>value === "repository-branch-discovery-v1" || value === WorkerCapability.REPOSITORY_BRANCH_DISCOVERY_V1);
  const lookup=async()=>{
    if(!active || input.current?.matches(":disabled") || !repo || repo.id !== repositoryId || repo.kind !== EntityKind.REPOSITORY || !supportsResourceSchema(repo) || !runner || runner.id !== machineId || runner.kind !== EntityKind.MACHINE || !supportsResourceSchema(runner) || !capable)return;
    controller.current?.abort();const abort=new AbortController();controller.current=abort;
    const original=++generation.current;setBusy(true);setError(undefined);
    try{
      const options={signal:abort.signal};
      let row=(await createClient(WorkerService,transport).discoverRepositoryBranches({requestId:newRequestId(),projectId:project.id,projectRevision:project.revision,repositoryId:repo.id,repositoryRevision:repo.revision,machineId:runner.id,machineRevision:runner.revision},options)).job;
      const deadline=Date.now()+120000;
      while(generation.current===original && !abort.signal.aborted){
        const result=readBranchInventory(row,{project,repository:repo,machine:runner});
        if(result.state==="succeeded"){setInventory({branches:result.branches,remote:result.remote});return;}
        if(!["queued","claimed"].includes(result.state))throw result.problem ?? new ConnectError("Branch inventory unavailable", Code.Unavailable);
        if(Date.now()>deadline)throw new ConnectError("Branch inventory timed out", Code.Unavailable);
        await new Promise(resolve=>setTimeout(resolve,500));
        if(abort.signal.aborted)return;
        row=(await createClient(ResourceService,transport).getResource({kind:EntityKind.JOB,id:row!.id},options)).resource;
      }
    }catch(problem){if(generation.current===original && !abort.signal.aborted)setError(problem);}
    finally{if(generation.current===original)setBusy(false);}
  };
  useEffect(()=>{if(open && capable && active)void lookup();},[open,capable,identity,active]);
  const reference=object(starting.map(object).find(value=>value.repository_id===repositoryId)?.reference),selected=text(reference.name),remote=text(reference.remote);
  const explicit=reference.type==="remote-branch";
  const repoReady=repo?.id===repositoryId && repo.kind===EntityKind.REPOSITORY && repo.revision>0n && supportsResourceSchema(repo);
  const managedRemote=repoReady ? text(document(repo).preferred_remote)||"origin" : "";
  const manual=Boolean(reference.type && (!explicit || remote!==managedRemote));
  const id=useId(),input=useRef<HTMLInputElement>(null),popup=useRef<HTMLDivElement>(null),composing=useRef(false),[highlight,setHighlight]=useState(-1),[typed,setTyped]=useState(false),[ime,setIme]=useState(false);
  useLayoutEffect(()=>{setDraft(explicit?selected:"");setTyped(false);},[selected,remote,reference.type]);
  const valid=!draft || validStartingBranch(draft) && (!typed || Boolean(managedRemote));
  const problem=draft && !validStartingBranch(draft) ? copy("new-session.invalidStartingBranch") : draft && typed && !managedRemote ? copy("new-session.branchRepositoryUnavailable") : "";
  useLayoutEffect(()=>{input.current?.setCustomValidity(problem);report(repositoryId,valid && !ime);return()=>report(repositoryId,true);},[repositoryId,valid,problem,ime,report]);
  const guarded=()=>active && !input.current?.matches(":disabled");
  const select=(name:string)=>{
    if(!guarded() || name && (!validStartingBranch(name) || !managedRemote))return;
    const others=starting.filter(value=>object(value).repository_id!==repositoryId);
    const values=name ? [...others,{repository_id:repositoryId,reference:{type:"remote-branch",remote:managedRemote,name}}]:others;
    const ids=items(document(project).repositories).map(text);
    change(values.sort((a,b)=>ids.indexOf(text(object(a).repository_id))-ids.indexOf(text(object(b).repository_id))));
  };
  // A valid typed draft can arrive before repository metadata. Commit only after
  // the original exact repository supplies its configured managed-clone remote.
  useEffect(()=>{if(typed && valid && !composing.current && active) { select(draft); setTyped(false); }},[typed,valid,managedRemote,active]);
  useEffect(()=>{if(!active)setOpen(false);},[active]);
  const close=()=>{setOpen(false);setHighlight(-1);};
  const filtered=(inventory?.branches??[]).filter(name=>name.toLocaleLowerCase().includes(draft.toLocaleLowerCase()));
  const candidates=[{name:"",label:copy("new-session.savedStartingReference")},...filtered.map(name=>({name,label:name})),...(draft && validStartingBranch(draft) && !inventory?.branches.includes(draft)?[{name:draft,label:copy("new-session.useTypedBranch",{name:draft})}]:[])];
  const choose=(name:string)=>{select(name);setDraft(name);setTyped(false);close();input.current?.focus();};
  useLayoutEffect(()=>{if(!open || !popup.current || !input.current)return;
    const panel=popup.current;panel.showPopover?.();
    const position=()=>{const bounds=input.current!.getBoundingClientRect();const below=window.innerHeight-bounds.bottom-8,above=bounds.top-8;const useBelow=below>=Math.min(280,panel.scrollHeight)||below>=above;panel.style.width=`${Math.min(bounds.width,window.innerWidth-16)}px`;panel.style.maxHeight=`${Math.max(0,Math.min(280,useBelow?below:above))}px`;panel.style.left=`${Math.max(8,Math.min(bounds.left,window.innerWidth-panel.offsetWidth-8))}px`;panel.style.top=`${useBelow?bounds.bottom+4:Math.max(8,bounds.top-panel.offsetHeight-4)}px`;};
    position();const observer=typeof ResizeObserver==="undefined"?undefined:new ResizeObserver(position);observer?.observe(panel);
    const outside=(event:PointerEvent)=>{if(!panel.contains(event.target as Node)&&event.target!==input.current)close();};
    window.document.addEventListener("pointerdown",outside);window.addEventListener("resize",position);window.addEventListener("scroll",position,true);
    return()=>{observer?.disconnect();panel.hidePopover?.();window.document.removeEventListener("pointerdown",outside);window.removeEventListener("resize",position);window.removeEventListener("scroll",position,true);};
  },[open]);
  useEffect(()=>{if(highlight>=candidates.length)setHighlight(-1);popup.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({block:"nearest"});},[highlight,candidates.length]);
  const unavailable=explicit && inventory && (remote!==inventory.remote || !inventory.branches.includes(selected));
  const label=primary?copy("new-session.startingBranch"):`${copy("new-session.startingBranch")} · ${text(document(repo).name)||repositoryId}`;
  return <div className="starting-branch-autocomplete">
    <label htmlFor={`${id}-input`}>{label}</label><input ref={input} id={`${id}-input`} type="text" role="combobox" aria-autocomplete="list" aria-controls={`${id}-choices`} aria-expanded={open} aria-activedescendant={open&&highlight>=0?`${id}-option-${highlight}`:undefined} aria-invalid={!valid||undefined} aria-describedby={`${id}-error`} disabled={!active} value={draft} placeholder={manual?copy("new-session.manualStartingReference"):copy("new-session.savedStartingReference")} onFocus={()=>{if(guarded())setOpen(true);}} onClick={()=>{if(guarded())setOpen(true);}} onChange={event=>{if(!guarded())return;setDraft(event.target.value);setTyped(true);setHighlight(-1);setOpen(true);if(!composing.current && (!event.target.value || validStartingBranch(event.target.value)&&managedRemote)) {select(event.target.value);setTyped(false);}}} onCompositionStart={()=>{composing.current=true;setIme(true);}} onCompositionEnd={event=>{composing.current=false;setIme(false);setDraft(event.currentTarget.value);if(!event.currentTarget.value || validStartingBranch(event.currentTarget.value)&&managedRemote){select(event.currentTarget.value);setTyped(false);}else setTyped(true);}} onKeyDown={event=>{
     if(!guarded())return;
     if(event.key==="Enter"){event.preventDefault();event.stopPropagation();if(composing.current||event.nativeEvent.isComposing||event.keyCode===229)return;if(open&&highlight>=0&&candidates[highlight])choose(candidates[highlight].name);else close();return;}
     if(composing.current||event.nativeEvent.isComposing||event.keyCode===229)return;
     if(event.key==="ArrowDown"||event.key==="ArrowUp"){event.preventDefault();setOpen(true);
      const refresh=popup.current?.querySelector<HTMLButtonElement>("[data-branch-refresh]");
      if(event.key==="ArrowDown"&&highlight===candidates.length-1&&refresh&&!refresh.disabled){setHighlight(-1);refresh.focus();return;}
      setHighlight(current=>current<0?event.key==="ArrowDown"?0:candidates.length-1:(current+(event.key==="ArrowDown"?1:-1)+candidates.length)%candidates.length);
     }
     if(event.key==="Escape"){event.preventDefault();event.stopPropagation();close();}if(event.key==="Tab")close();
    }}/>
    <span className="branch-validation" id={`${id}-error`} role={problem?"alert":undefined}>{problem}</span>
    {open?<div ref={popup} popover="manual" className="starting-branch-popup" onKeyDown={event=>{
      if(event.key==="Escape"){event.preventDefault();event.stopPropagation();close();input.current?.focus();}
      if(event.key==="Tab")close();
      if(event.target instanceof HTMLButtonElement&&event.target.hasAttribute("data-branch-refresh")&&(event.key==="ArrowUp"||event.key==="ArrowDown")){event.preventDefault();input.current?.focus();setHighlight(event.key==="ArrowUp"?candidates.length-1:0);}
     }}><p>{text(document(repo).name)} <code>{repositoryId}</code></p><p>{manual?copy("new-session.manualStartingReference"):copy("new-session.branchInputHelp")}</p>
     <div id={`${id}-choices`} role="listbox" aria-label={label}>{candidates.map((candidate,index)=><button type="button" role="option" tabIndex={-1} key={`${candidate.name}:${index}`} id={`${id}-option-${index}`} aria-selected={highlight===index} disabled={!active||Boolean(candidate.name&&!managedRemote)} onPointerDown={event=>event.preventDefault()} onClick={()=>choose(candidate.name)}>{candidate.label}</button>)}</div>
     <button data-branch-refresh type="button" disabled={!capable||!machineId||busy||!active} onPointerDown={event=>event.preventDefault()} onClick={()=>{input.current?.focus();void lookup();}}>{copy("new-session.refreshBranches")}</button>
     {busy?<p role="status">{copy("new-session.loadingBranches")}</p>:null}
     {!machineId?<p>{copy("new-session.chooseBranchWorker")}</p>:!capable?<p>{copy("new-session.branchUnsupported")}</p>:null}
     {inventory&&!inventory.branches.length?<p role="status">{copy("new-session.noBranches")}</p>:inventory&&!filtered.length?<p role="status">{copy("new-session.noMatchingBranches")}</p>:null}
     {unavailable?<p role="status">{copy("new-session.branchUnavailable")}</p>:null}
     {problem?<p role="alert">{problem}</p>:null}
     {text(object(error).code)?<ServiceProblem code={text(object(error).code)}>{copy("new-session.branchLookupFailed")}</ServiceProblem>:<Problem error={error}/>}<Problem error={repository.error||machine.error}/>
    </div>:null}
  </div>;
}
