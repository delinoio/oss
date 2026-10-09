// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ProviderService, ResourceSchema, newRequestId, type ListEndpointModelsRequest, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useEndpointModelHints } from "./endpoint-model-hints";
function fixture() {
 const provider=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.PROVIDER,revision:1n,schemaVersion:1,documentJson:encode({enabled:true,discovery:false})});
 const connection=newRequestId();const account=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,revision:2n,schemaVersion:1,documentJson:encode({type:"api",provider_id:provider.id,enabled:true,connection:{id:connection}})});
 const response={accountId:account.id,accountRevision:account.revision,providerId:provider.id,providerRevision:provider.revision,connectionId:connection,observedAtUnixMs:1n,models:[{nativeId:"exact/native:ID"}]};
 const read=vi.fn(async (_request:ListEndpointModelsRequest)=>response);
 const transport=createRouterTransport(router=>router.service(ProviderService,{listEndpointModels:read}));const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const wrapper=({children}:{children:React.ReactNode})=><TransportProvider transport={transport}><QueryClientProvider client={client}>{children}</QueryClientProvider></TransportProvider>;
 const hook=renderHook(({row,active}:{row:Resource;active:boolean})=>useEndpointModelHints(row,provider,active),{wrapper,initialProps:{row:account,active:true}});return {provider,account,response,read,hook};
}
it("reads only after explicit action with original revisions despite compatibility discovery flags",async()=>{const f=fixture();expect(f.read).not.toHaveBeenCalled();await act(async()=>{await f.hook.result.current.read();});expect(f.read.mock.calls[0][0]).toMatchObject({accountId:f.account.id,expectedAccountRevision:2n,expectedProviderRevision:1n});expect(f.hook.result.current.result?.models[0].nativeId).toBe("exact/native:ID");});
it("rejects a response belonging to another connection",async()=>{const f=fixture();f.read.mockResolvedValueOnce({...f.response,connectionId:newRequestId()});await act(async()=>{await f.hook.result.current.read();});expect(f.hook.result.current.result).toBeUndefined();expect(f.hook.result.current.error).toBeTruthy();});
it("retains the original wait through deactivation and ignores its late result",async()=>{const f=fixture();let release!:(value:typeof f.response)=>void;f.read.mockImplementationOnce(()=>new Promise(resolve=>{release=resolve;}));let pending!:Promise<void>;act(()=>{pending=f.hook.result.current.read();});f.hook.rerender({row:f.account,active:false});f.hook.rerender({row:f.account,active:true});await act(async()=>{await f.hook.result.current.read();});expect(f.read).toHaveBeenCalledTimes(1);await act(async()=>{release(f.response);await pending;});expect(f.hook.result.current.result).toBeUndefined();expect(f.hook.result.current.pending).toBe(false);await act(async()=>{await f.hook.result.current.read();});expect(f.read).toHaveBeenCalledTimes(2);});
it("drops a result after a newer account revision",async()=>{const f=fixture();let release!:(value:typeof f.response)=>void;f.read.mockImplementationOnce(()=>new Promise(resolve=>{release=resolve;}));let pending!:Promise<void>;act(()=>{pending=f.hook.result.current.read();});await act(async()=>{await Promise.resolve();});f.hook.rerender({row:create(ResourceSchema,{...f.account,revision:3n}),active:true});await act(async()=>{release(f.response);await pending;});expect(f.hook.result.current.result).toBeUndefined();});

it("stops displaying prior hints after an account edit without refreshing implicitly",async()=>{const f=fixture();await act(async()=>{await f.hook.result.current.read();});expect(f.hook.result.current.result).toBeTruthy();f.hook.rerender({row:create(ResourceSchema,{...f.account,revision:3n}),active:true});expect(f.hook.result.current.result).toBeUndefined();expect(f.read).toHaveBeenCalledTimes(1);});
