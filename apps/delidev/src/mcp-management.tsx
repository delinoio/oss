// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createClient } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { McpMutation, McpManagementService, McpQuery, McpAuthenticationState as AuthState, McpAuthenticationOperation as AuthOperation, newRequestId, type McpServer, type AuthenticateMcpServerRequest, type AuthenticateMcpServerResponse } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { OAuthNativeAction, useOAuthNativeControl } from "./account-oauth";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
interface OAuthTask {
 server: McpServer; opening: string; generation: string; callback: string; attempt: string; request: AuthenticateMcpServerRequest;
 state?: AuthState; busy: boolean; error?: unknown; bound: boolean; uncertain: boolean;
}
function useController() {
 const native=useOAuthNativeControl(), transport=useTransport(), queries=useQueryClient();
 const client=useMemo(()=>createClient(McpManagementService,transport),[transport]);
 const pending=useRef<OAuthTask | undefined>(undefined), alive=useRef(true);
 const [oauth,setOAuth]=useState<OAuthTask>();
 const publish=(task:OAuthTask)=>{if(alive.current&&pending.current===task)setOAuth({...task});};
 const mutation=useRetainedMutation("mcp:management",McpQuery.mutateMcpServer,()=>void queries.invalidateQueries({refetchType:"active"}),(result,request)=>result.requestId===request.mutation?.requestId&&(result.deleted?request.operation===McpMutation.DELETE:result.server?.machineId===request.machineId&&result.server?.definition?.id===request.mutation?.id));
 const dispose=(task:OAuthTask)=>{if(native)void native(task.opening,OAuthNativeAction.Dispose,task.generation,task.attempt,"").catch(()=>undefined);};
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;if(pending.current){dispose(pending.current);pending.current=undefined;}};},[native]);
 const verify=(task:OAuthTask,result:AuthenticateMcpServerResponse)=>{
  if(result.requestId!==task.request.mutation?.requestId||!result.attemptId||task.attempt&&task.attempt!==result.attemptId)throw new Error("MCP authentication receipt ownership");
  if(![AuthState.PENDING,AuthState.UNCERTAIN,AuthState.READY,AuthState.CANCELED].includes(result.state))throw new Error("MCP authentication state");
  task.attempt=result.attemptId;task.state=result.state;task.uncertain=result.state===AuthState.UNCERTAIN;
 };
 const dispatch=async(task:OAuthTask)=>{
  if(task.busy||!alive.current||pending.current!==task)return;
  task.busy=true;task.error=undefined;publish(task);
  try {
   if(!task.generation){if(!native)throw new Error("Native MCP authentication is unavailable");const result=await native(task.opening,OAuthNativeAction.BeginMcp,"","","");task.generation=result.generation;task.callback=result.callback_url??"";if(!task.generation||!task.callback)throw new Error("MCP callback ownership");task.request.callbackUrl=task.callback;}
   if(!alive.current||pending.current!==task){dispose(task);return;}
   if(task.request.operation!==AuthOperation.START){task.state=AuthState.UNCERTAIN;task.uncertain=true;publish(task);}
   const result=await client.authenticateMcpServer(task.request);verify(task,result);
   if(!alive.current||pending.current!==task){dispose(task);return;}
   if(task.request.operation===AuthOperation.START&&result.state===AuthState.PENDING){
    if(!native||!result.authorizationUrl)throw new Error("MCP authorization ownership");
    await native(task.opening,OAuthNativeAction.BindOpen,task.generation,task.attempt,result.authorizationUrl);task.bound=true;
   } else if(result.state!==AuthState.PENDING) {void queries.invalidateQueries({refetchType:"active"});dispose(task);if(result.state===AuthState.READY||result.state===AuthState.CANCELED)task.request.callbackUrl="";}
  } catch(error){task.error=error;task.uncertain=true;} finally {task.busy=false;publish(task);}
 };
 // Only an explicitly opened original authentication owns callback polling.
 // Settings visits, search, Runner changes and list reads never start this task.
 useEffect(()=>{if(!oauth?.bound||oauth.busy||oauth.state!==AuthState.PENDING||!native)return;const task=pending.current!;let stopped=false;
  const timer=setInterval(()=>{if(stopped||task.busy||!alive.current)return;task.busy=true;
   void native(task.opening,OAuthNativeAction.Take,task.generation,task.attempt,"").then(async result=>{
    if(stopped||!alive.current)return;
    const bytes=new Uint8Array(result.code??[]);try{
     if(result.denied){task.request={...task.request,mutation:{...task.request.mutation!,requestId:newRequestId()},operation:AuthOperation.CANCEL,attemptId:task.attempt,callbackUrl:""};task.bound=false;task.busy=false;await dispatch(task);}
     else if(bytes.length){const query=new TextDecoder().decode(bytes);task.request={...task.request,mutation:{...task.request.mutation!,requestId:newRequestId()},operation:AuthOperation.COMPLETE,attemptId:task.attempt,callbackUrl:task.callback+"?"+query};task.bound=false;task.busy=false;await dispatch(task);}
    }finally{bytes.fill(0);new Uint8Array(result.state??[]).fill(0);}
   }).catch(error=>{task.error=error;task.bound=false;task.uncertain=true;publish(task);}).finally(()=>{task.busy=false;publish(task);});
  },1000);return()=>{stopped=true;clearInterval(timer);};
 },[oauth?.bound,oauth?.state,native]);
 const start=(server:McpServer)=>{
  if(!native||mutation.busy||mutation.uncertain||pending.current?.busy||pending.current?.state===AuthState.PENDING||pending.current?.uncertain)return;
  const definition=server.definition!;const task:OAuthTask={server,opening:newRequestId(),generation:"",callback:"",attempt:"",busy:false,bound:false,uncertain:false,request:{ $typeName:"delidev.v1.AuthenticateMcpServerRequest",machineId:server.machineId,operation:AuthOperation.START,attemptId:"",callbackUrl:"",mutation:{$typeName:"delidev.v1.Mutation",id:definition.id,expectedRevision:definition.revision,requestId:newRequestId()}}};pending.current=task;publish(task);void dispatch(task);
 };
 const cancel=()=>{const task=pending.current;if(!task||task.busy||!task.attempt||task.state!==AuthState.PENDING||task.request.operation!==AuthOperation.START)return;dispose(task);task.bound=false;task.uncertain=false;task.request={...task.request,mutation:{...task.request.mutation!,requestId:newRequestId()},operation:AuthOperation.CANCEL,attemptId:task.attempt,callbackUrl:""};void dispatch(task);};
 const cancelServer=(server:McpServer)=>{
 if(pending.current?.attempt===server.authenticationAttemptId){cancel();return;}
 if(pending.current?.uncertain||pending.current?.state===AuthState.PENDING)return;
 if(!server.authenticationAttemptId||!server.authenticationAttemptRevision||pending.current?.busy||server.authenticationState!==AuthState.PENDING)return;
 const task:OAuthTask={server,opening:newRequestId(),generation:"recovery",callback:"",attempt:server.authenticationAttemptId,busy:false,bound:false,uncertain:false,state:AuthState.PENDING,request:{$typeName:"delidev.v1.AuthenticateMcpServerRequest",machineId:server.machineId,operation:AuthOperation.CANCEL,attemptId:server.authenticationAttemptId,callbackUrl:"",mutation:{$typeName:"delidev.v1.Mutation",id:server.definition!.id,expectedRevision:server.authenticationAttemptRevision,requestId:newRequestId()}}};pending.current=task;publish(task);void dispatch(task);
 };
 return {mutation,oauth,start,cancel,cancelServer,retry:()=>{if(pending.current)void dispatch(pending.current);},nativeAvailable:Boolean(native)};
}
const Context=createContext<ReturnType<typeof useController>|undefined>(undefined);
export function McpManagementProvider({children}:{children:ReactNode}){const controller=useController();return <Context.Provider value={controller}>{children}</Context.Provider>;}
export function useMcpManagement(){const value=useContext(Context);if(!value)throw new Error("MCP management requires the original connection owner");return value;}
export function McpPending(){useLocale();const owner=useMcpManagement();return <section data-settings-search-target="mcp-authentication">
 {owner.mutation.error?<Problem error={owner.mutation.error}/>:null}
 {owner.mutation.uncertain?<p role="alert">{copy("mcp.uncertain")} <SettingsActionButton icon={SettingsActionIcon.Retry} disabled={owner.mutation.busy} onClick={()=>void owner.mutation.retry()}>{copy("mcp.retryOriginal")}</SettingsActionButton></p>:null}
 {owner.oauth?<section aria-label={copy("mcp.authentication")}><p>{owner.oauth.server.definition?.name}: {copy(owner.oauth.state===AuthState.READY?"mcp.ready":owner.oauth.state===AuthState.CANCELED?"mcp.canceled":owner.oauth.uncertain?"mcp.uncertain":"mcp.pending")}</p><Problem error={owner.oauth.error}/>{owner.oauth.uncertain?<SettingsActionButton icon={SettingsActionIcon.Retry} disabled={owner.oauth.busy} onClick={owner.retry}>{copy("mcp.retryOriginal")}</SettingsActionButton>:null}{owner.oauth.state===AuthState.PENDING?<SettingsActionButton icon={SettingsActionIcon.Cancel} disabled={owner.oauth.busy} onClick={owner.cancel}>{copy("mcp.cancelAuthentication")}</SettingsActionButton>:null}</section>:null}
 </section>;}
export function useMcpCatalog(machineId:string,active:boolean){return useQuery(McpQuery.listMcpServers,{machineId},{enabled:active&&Boolean(machineId),retry:false});}
