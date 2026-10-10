// SPDX-License-Identifier: Apache-2.0
import { useEffect } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { EntityKind, IntegrationQuery, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { encode, object, text, type Document } from "./documents";
import { githubResult } from "./github-items";
import { ItemKind, QueryOperation } from "./github-query-model";
import { usePaginationRefresh } from "./scroll-pagination-query";
import { copy, useLocale } from "./localization";
import { Timestamp } from "./timestamp-display";
import { Problem } from "./ui";

export enum SessionPRState { Open = "open", Merged = "merged", Closed = "closed", Unavailable = "unavailable" }
/** Connection/session-local admission. Aborted queued rows never obtain a slot;
 * in-flight slots remain occupied until their exact read settles. */
export class SessionPRReadQueue {
  private active = 0;
  private waiting: (() => void)[] = [];
  async run<T>(read: () => Promise<T>, signal: AbortSignal): Promise<T> {
    await new Promise<void>((resolve,reject)=>{
      const start=()=>{signal.removeEventListener("abort",abort);if(signal.aborted){reject(new DOMException("Read canceled","AbortError"));this.advance();return;}this.active++;resolve();};
      const abort=()=>{this.waiting=this.waiting.filter(task=>task!==start);reject(new DOMException("Read canceled","AbortError"));};
      if(signal.aborted){abort();return;}
      if(this.active<4)start();else{this.waiting.push(start);signal.addEventListener("abort",abort,{once:true});}
    });
    try { return await read(); } finally { this.active--;this.advance(); }
  }
  private advance(){if(this.active<4)this.waiting.shift()?.();}
}
export function sessionPRObservation(raw: Uint8Array, repository: Resource, association: Document): Document | undefined {
  const query={kind:ItemKind.PullRequest,operation:QueryOperation.Detail,number:text(association.number)};
  const result=githubResult(raw,repository,query), remote=object(result?.repository), item=object((result?.items as unknown[])?.[0]);
  if(!result||repository.id!==association.repository_id||remote.id!==association.remote_repository_id||remote.node_id!==association.repository_node_id||text(remote.owner).toLowerCase()!==text(association.owner).toLowerCase()||text(remote.name).toLowerCase()!==text(association.name).toLowerCase()||item.id!==association.pull_request_id||item.node_id!==association.pull_request_node_id||item.number!==association.number||Date.parse(text(result.observed_at))<Date.parse(text(association.observed_at))||item.merged===true&&item.state!=="closed")return;
  return result;
}
export function SessionPRStatus({ row, association, active, queue }: { row:Resource; association:Document; active:boolean; queue:SessionPRReadQueue }) {
  useLocale();
  const transport=useTransport(), client=useQueryClient();
  const request={repositoryId:text(association.repository_id),schemaVersion:1,queryJson:encode({kind:ItemKind.PullRequest,operation:QueryOperation.Detail,number:text(association.number)})};
  const options=createQueryOptions(IntegrationQuery.queryRepositoryIntegration,request,{transport});
  const queryKey=[...options.queryKey,{sessionPRAssociation:row.id,revision:row.revision.toString()}];
  const result=useQuery({queryKey,enabled:active,retry:false,gcTime:0,staleTime:0,refetchOnWindowFocus:false,refetchOnReconnect:false,queryFn:context=>queue.run(async()=>{
    const repoOptions=createQueryOptions(ResourceQuery.getResource,{kind:EntityKind.REPOSITORY,id:request.repositoryId},{transport});
    const repo=await repoOptions.queryFn({...context,queryKey:repoOptions.queryKey});
    if(context.signal.aborted)throw new DOMException("Read canceled","AbortError");
    if(!repo.resource||repo.resource.id!==request.repositoryId||repo.resource.kind!==EntityKind.REPOSITORY||repo.resource.schemaVersion!==1)throw new Error("Repository observation unavailable");
    const response=await options.queryFn({...context,queryKey:options.queryKey});
    const observed=response.schemaVersion===1?sessionPRObservation(response.documentJson,repo.resource,association):undefined;
    if(!observed||context.signal.aborted)throw new Error("Pull request observation unavailable");
    return observed;
  },context.signal)});
  useEffect(()=>()=>{void client.cancelQueries({queryKey,exact:true});},[client,transport,row.id,row.revision,active,queue]);
  usePaginationRefresh(ResourceQuery.listResources,{filter:{kind:EntityKind.PULL_REQUEST,sessionId:row.sessionId,pageSize:50,pageToken:""}},active,()=>{void result.refetch({cancelRefetch:false});});
  const item=object((result.data?.items as unknown[])?.[0]);
  const state=!active||result.error||result.isFetching||!result.data?SessionPRState.Unavailable:item.merged?SessionPRState.Merged:item.state==="open"?SessionPRState.Open:SessionPRState.Closed;
  const label=copy(state===SessionPRState.Open?"session.prStateOpen":state===SessionPRState.Merged?"session.prStateMerged":state===SessionPRState.Closed?"session.prStateClosed":"session.prStateUnavailable");
  return <><span className="session-pr-icon" role="img" aria-label={label} title={label} data-pr-state={state}><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75"><circle cx="6" cy="5" r="2.5"/><circle cx="6" cy="19" r="2.5"/><circle cx="18" cy="19" r="2.5"/><path d="M6 7.5v9M18 16.5V9a4 4 0 0 0-4-4h-2m2-3-3 3 3 3"/></svg><span className="session-pr-status-dot" aria-hidden="true"/></span>{result.data?<small><Timestamp value={text(result.data.observed_at)}/></small>:null}{result.error?<><Problem error={result.error}/><button disabled={result.isFetching||!active} onClick={()=>void result.refetch()}>{copy("session-name.retryRead")}</button></>:null}</>;
}
