// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import type { ReactNode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId, type DiscoverHarnessesRequest } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { useMachineSettingsController } from "./machine-settings";
import { MutationIntents } from "./mutation";

function fixture() {
 const id=newRequestId();
 const runner=(revision:bigint,lastSeen?:string,name="Original Runner")=>create(ResourceSchema,{id,kind:EntityKind.MACHINE,schemaVersion:1,revision,documentJson:encode({name,installations:[],last_seen:lastSeen})});
 const initial=runner(7n,"2026-10-10T00:00:00Z");
 let observed=initial;
 const read=vi.fn(async()=>({resource:observed}));
 const discover=vi.fn(async(_request:DiscoverHarnessesRequest)=>({machine:runner(8n,"2026-10-10T00:00:02Z","Acknowledged Runner")}));
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:read});router.service(WorkerService,{discoverHarnesses:discover});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const wrapper=({children}:{children:ReactNode})=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
 const hook=renderHook(()=>useMachineSettingsController(initial,true),{wrapper});
 const refresh=async(next:typeof initial)=>{await waitFor(()=>expect(hook.result.current.loading).toBe(false));observed=next;await act(async()=>{await hook.result.current.refetch();await new Promise(resolve=>setTimeout(resolve,0));});};
 return {hook,runner,initial,read,discover,refresh};
}
it("refreshes an equal-revision heartbeat while preserving editable path draft and outgoing revision",async()=>{
 const f=fixture();await waitFor(()=>expect(f.read).toHaveBeenCalledOnce());
 await act(async()=>{f.hook.result.current.setEdit({revision:7n,paths:{codex:"/fixture/edited-codex"}});});
 await f.refresh(f.runner(7n,"2026-10-10T00:00:05Z"));
 expect(f.hook.result.current.data.last_seen).toBe("2026-10-10T00:00:05Z");
 expect(f.hook.result.current.current?.revision).toBe(7n);
 expect(f.hook.result.current.edit).toEqual({revision:7n,paths:{codex:"/fixture/edited-codex"}});
 expect(f.hook.result.current.stale).toBe(false);
 await act(async()=>{await f.hook.result.current.discovery.send({mutation:{id:f.initial.id,expectedRevision:f.hook.result.current.edit!.revision,requestId:newRequestId()},verifyProtocol:false});});
 expect(f.discover.mock.calls[0][0].mutation!.expectedRevision).toBe(7n);
});
it("retains acknowledged higher configuration through older polls then accepts compatible heartbeat",async()=>{
 const f=fixture();await waitFor(()=>expect(f.read).toHaveBeenCalledOnce());
 await act(async()=>{await f.hook.result.current.discovery.send({mutation:{id:f.initial.id,expectedRevision:7n,requestId:newRequestId()},verifyProtocol:false});});
 expect(f.hook.result.current.current?.revision).toBe(8n);
 expect(f.hook.result.current.data.name).toBe("Acknowledged Runner");
 await f.refresh(f.runner(7n,"2026-10-10T00:00:10Z"));
 expect(f.hook.result.current.current?.revision).toBe(8n);
 expect(f.hook.result.current.data.name).toBe("Acknowledged Runner");
 await f.refresh(f.runner(8n,"2026-10-10T00:00:15Z","Cached label"));
 expect(f.hook.result.current.data.last_seen).toBe("2026-10-10T00:00:15Z");
 expect(f.hook.result.current.data.name).toBe("Acknowledged Runner");
});
it("retains a newer read through a later older, foreign, malformed or failed poll",async()=>{
 const f=fixture();await waitFor(()=>expect(f.read).toHaveBeenCalledOnce());await f.refresh(f.runner(9n,"2026-10-10T00:00:20Z","Newest Runner"));
 await f.refresh(f.runner(7n,"2026-10-10T00:00:25Z"));
 expect(f.hook.result.current.current?.revision).toBe(9n);
 expect(f.hook.result.current.data.name).toBe("Newest Runner");
 for(const invalid of [create(ResourceSchema,{...f.runner(10n),id:newRequestId()}),create(ResourceSchema,{...f.runner(10n),documentJson:encode({name:"Malformed",disabled:"false"})})]){
  await f.refresh(invalid);
  expect(f.hook.result.current.current?.revision).toBe(9n);
  expect(f.hook.result.current.readError).toBeDefined();
 }
 f.read.mockRejectedValueOnce(new ConnectError("Unavailable",Code.Unavailable));
 await act(async()=>{await f.hook.result.current.refetch();await new Promise(resolve=>setTimeout(resolve,0));});
 expect(f.hook.result.current.readError).toBeDefined();
 expect(document(f.hook.result.current.current).last_seen).toBe("2026-10-10T00:00:20Z");
});
it("refreshes heartbeat without releasing an uncertain operation or changing its exact retry",async()=>{
 const f=fixture();await waitFor(()=>expect(f.hook.result.current.loading).toBe(false));
 const original={mutation:{id:f.initial.id,expectedRevision:7n,requestId:newRequestId()},verifyProtocol:false};
 f.discover.mockRejectedValueOnce(new ConnectError("Unknown original outcome",Code.Unavailable));
 await act(async()=>{await f.hook.result.current.discovery.send(original);});
 expect(f.hook.result.current.discovery.uncertain).toBe(true);
 await f.refresh(f.runner(7n,"2026-10-10T00:00:05Z"));
 expect(f.hook.result.current.data.last_seen).toBe("2026-10-10T00:00:05Z");
 expect(f.hook.result.current.discovery.uncertain).toBe(true);
 expect(f.hook.result.current.pending).toBe(true);
 await act(async()=>{await f.hook.result.current.discovery.retry();});
 expect(f.discover.mock.calls[1][0]).toEqual(f.discover.mock.calls[0][0]);
 expect(f.discover.mock.calls[1][0].mutation!.requestId).toBe(original.mutation.requestId);
});
