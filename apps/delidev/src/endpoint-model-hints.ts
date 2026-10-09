// SPDX-License-Identifier: Apache-2.0
import { useMutation } from "@connectrpc/connect-query";
import { useLayoutEffect, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, supportsResourceSchema, ProviderQuery, type Resource, type ListEndpointModelsResponse } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
interface Scope { account: Resource; provider: Resource }
/** Explicit source-native hints are reads. They retain no saved Model or
 * catalog mutation authority, and an ignored original wait keeps its slot. */
export function useEndpointModelHints(account: Resource, provider: Resource | undefined, active: boolean) {
 const current=useRef({account,provider,active});current.current={account,provider,active};
 const generation=useRef(0),running=useRef(false),mounted=useRef(true);
 useLayoutEffect(()=>{mounted.current=true;return ()=>{mounted.current=false;};},[]);
 const [result,setResult]=useState<ListEndpointModelsResponse>(),[error,setError]=useState<unknown>(),[pending,setPending]=useState(false);
 const connection=text(object(document(account).connection).id);
 const scopeKey=JSON.stringify([account.id,connection,provider?.id,provider?.revision.toString(),active]);
 useLayoutEffect(()=>{generation.current++;current.current.active=active;setResult(undefined);setError(undefined);return ()=>{generation.current++;current.current.active=false;};},[scopeKey]);
 const mutation=useMutation(ProviderQuery.listEndpointModels);
 const read=async (scope?:Scope)=>{
  const selected=scope??(provider?{account,provider}:undefined);if(!active || !selected || running.current)return;
  const original=document(selected.account),connectionId=text(object(original.connection).id);
  if(!supportsResourceSchema(selected.account) || !supportsResourceSchema(selected.provider) || selected.account.kind!==EntityKind.ACCOUNT || selected.provider.kind!==EntityKind.PROVIDER || !connectionId || original.type!=="api" || original.enabled===false || original.removal || original.provider_id!==selected.provider.id || document(selected.provider).enabled===false)return;
  const ticket=generation.current;running.current=true;setPending(true);setError(undefined);
  const valid=()=>{const now=current.current,data=document(now.account);return ticket===generation.current && now.active && now.account.id===selected.account.id && now.account.revision<=selected.account.revision && text(object(data.connection).id)===connectionId && data.provider_id===selected.provider.id && now.provider?.id===selected.provider.id && now.provider.revision===selected.provider.revision && data.enabled!==false && !data.removal && document(now.provider).enabled!==false;};
  try{const response=await mutation.mutateAsync({accountId:selected.account.id,expectedAccountRevision:selected.account.revision,expectedProviderRevision:selected.provider.revision});if(!valid())return;
   if(response.accountId!==selected.account.id || response.accountRevision!==selected.account.revision || response.providerId!==selected.provider.id || response.providerRevision!==selected.provider.revision || response.connectionId!==connectionId || response.observedAtUnixMs<=0n || response.observedAtUnixMs>8640000000000000n || response.models.length>10000 || response.models.some(model=>!model.nativeId || new TextEncoder().encode(model.nativeId).length>256) || new Set(response.models.map(model=>model.nativeId)).size!==response.models.length)throw new ConnectError("Invalid original endpoint model hints.",Code.DataLoss);
   setResult(response);
  }catch(reason){if(valid())setError(reason);}finally{running.current=false;if(mounted.current)setPending(false);}
 };
 // A previously displayed hint is no longer current after any later Account edit.
 const retained=result && account.revision<=result.accountRevision && result.accountId===account.id && result.connectionId===connection && provider?.id===result.providerId && provider.revision===result.providerRevision && active && document(account).enabled!==false && !document(account).removal ? result : undefined;
 return {result:retained,error,pending,read};
}
