// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, ResourceService, WorkerService, WorkerCapability, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text } from "./documents";
import { copy, useLocale } from "./localization";
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

export function StartingBranches({project,machineId,starting,change,active,supported}: {project:Resource;machineId:string;starting:unknown[];change:(value:unknown[])=>void;active:boolean;supported:boolean}) {
  useLocale();
  const ids=items(document(project).repositories).map(text),primary=text(document(project).primary_repository);
  const props={project,machineId,starting,change,active,supported};
  return <div className="starting-branches"><StartingBranch key={primary} repositoryId={primary} {...props}/>{ids.length>1 ? <Disclosure density={DisclosureDensity.Settings}><DisclosureSummary>{copy("new-session.additionalRepositories")}</DisclosureSummary>{ids.filter(id=>id!==primary).map(id=><StartingBranch key={id} repositoryId={id} {...props}/>)}</Disclosure>:null}</div>;
}
function StartingBranch({project,repositoryId,machineId,starting,change,active,supported}: {project:Resource;repositoryId:string;machineId:string;starting:unknown[];change:(value:unknown[])=>void;active:boolean;supported:boolean}) {
  useLocale();const transport=useTransport();
  const repository=useQuery(ResourceQuery.getResource,{kind:EntityKind.REPOSITORY,id:repositoryId},{enabled:active && Boolean(repositoryId)});
  const machine=useQuery(ResourceQuery.getResource,{kind:EntityKind.MACHINE,id:machineId},{enabled:active && Boolean(machineId)});
  const [open,setOpen]=useState(false),[search,setSearch]=useState(""),[inventory,setInventory]=useState<{branches:string[];remote:string}>(),[busy,setBusy]=useState(false),[error,setError]=useState<unknown>();
  const generation=useRef(0),controller=useRef<AbortController>(undefined);
  const repo=repository.data?.resource,runner=machine.data?.resource;
  const identity=`${project.id}:${project.revision}:${repo?.id}:${repo?.revision}:${machineId}:${runner?.revision}`;
  useLayoutEffect(()=>{generation.current++;controller.current?.abort();setInventory(undefined);setError(undefined);setBusy(false);setSearch("");return()=>{generation.current++;controller.current?.abort();};},[identity,transport,active]);
  const capable=supported && items(document(runner).worker_capabilities).some(value=>value === "repository-branch-discovery-v1" || value === WorkerCapability.REPOSITORY_BRANCH_DISCOVERY_V1);
  const lookup=async()=>{
    if(!active || !repo || !runner || !capable)return;
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
  const select=(name:string)=>{
    const others=starting.filter(value=>object(value).repository_id!==repositoryId);
    const values=name ? [...others,{repository_id:repositoryId,reference:{type:"remote-branch",remote:inventory!.remote,name}}]:others;
    // Every override retains the configured repository ordering.
    const ids=items(document(project).repositories).map(text);
    change(values.sort((a,b)=>ids.indexOf(text(object(a).repository_id))-ids.indexOf(text(object(b).repository_id))));
  };
  const unavailable=explicit && inventory && (remote!==inventory.remote || !inventory.branches.includes(selected));
  const label=copy("new-session.startingBranch");
  return <Disclosure density={DisclosureDensity.Settings} className="starting-branch" open={open} onToggle={event=>{const expanded=event.currentTarget.open;setOpen(expanded);}}>
    <DisclosureSummary>{label}: {explicit ? selected : reference.type ? copy("new-session.manualStartingReference") : copy("new-session.savedStartingReference")}{repo ? <small>{text(document(repo).name)}</small>:null}</DisclosureSummary>
    <label>{copy("new-session.searchBranches")}<input type="search" value={search} onChange={event=>setSearch(event.target.value)} /></label>
    <label>{label}<select aria-label={label} value={explicit ? selected : ""} onChange={event=>select(event.target.value)} disabled={busy && !inventory}>
      <option value="">{copy("new-session.savedStartingReference")}</option>
      {explicit && !inventory?.branches.includes(selected) ? <option value={selected}>{selected} ({copy("new-session.branchUnavailable")})</option>:null}
      {(inventory?.branches ?? []).filter(name=>name===selected || name.toLocaleLowerCase().includes(search.toLocaleLowerCase())).map(name=><option key={name} value={name}>{name}</option>)}
    </select></label>
    <button type="button" disabled={!capable || !machineId || busy} onClick={()=>void lookup()}>{copy("new-session.refreshBranches")}</button>
    {busy ? <p role="status">{copy("new-session.loadingBranches")}</p>:null}
    {!machineId ? <p>{copy("new-session.chooseBranchWorker")}</p>:!capable ? <p>{copy("new-session.branchUnsupported")}</p>:null}
    {inventory && !inventory.branches.length ? <p role="status">{copy("new-session.noBranches")}</p>:null}
    {unavailable ? <p role="status">{copy("new-session.branchUnavailable")}</p>:null}
    {text(object(error).code) ? <ServiceProblem code={text(object(error).code)}>{copy("new-session.branchLookupFailed")}</ServiceProblem> : <Problem error={error}/>}<Problem error={repository.error || machine.error}/>
  </Disclosure>;
}
