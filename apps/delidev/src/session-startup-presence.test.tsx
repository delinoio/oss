// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { EntityKind, ResourceSchema, ResourceService, SystemService, newRequestId, type GetResourceRequest } from "@delinoio/delidev-api-client";
import { create } from "@bufbuild/protobuf";
import { expect, it, vi } from "vitest";
import { readStartupPresence, useStartupPresence } from "./session-startup-presence";
const machineId = newRequestId();
function deferred<T>() { let resolve!: (value:T)=>void; const promise = new Promise<T>(done=>{resolve=done;}); return {promise, resolve}; }
function fixture() {
 const machine = deferred<void>(), clock = deferred<void>(), order:string[]=[];
 const get = vi.fn(async (request: GetResourceRequest) => { order.push("machine"); expect(request.kind).toBe(EntityKind.MACHINE); expect(request.id).toBe(machineId); await machine.promise; return {resource:create(ResourceSchema,{id:machineId,kind:EntityKind.MACHINE})}; });
 const overview = vi.fn(async () => { order.push("clock"); await clock.promise; return {observedAt:"2026-10-09T00:00:00Z"}; });
 const transport = createRouterTransport(router => { router.service(ResourceService,{getResource:get}); router.service(SystemService,{getOverview:overview}); });
 return {transport,machine,clock,get,overview,order};
}
it("reads the existing server clock only after the original Machine read settles", async () => {
 const f=fixture(), signal=new AbortController(); const pending=readStartupPresence(f.transport,machineId,signal.signal);
 await waitFor(()=>expect(f.get).toHaveBeenCalledTimes(1)); expect(f.overview).not.toHaveBeenCalled();
 f.machine.resolve(); await waitFor(()=>expect(f.overview).toHaveBeenCalledTimes(1)); f.clock.resolve();
 expect((await pending).observedAt).toBe("2026-10-09T00:00:00Z"); expect(f.order).toEqual(["machine","clock"]);
});
it("does not dispatch Overview after a canceled Machine read", async () => {
 const f=fixture(), abort=new AbortController(); const pending=readStartupPresence(f.transport,machineId,abort.signal).catch(error=>error);
 await waitFor(()=>expect(f.get).toHaveBeenCalledTimes(1)); abort.abort(); f.machine.resolve();
 expect(await pending).toBeInstanceOf(Error); expect(f.overview).not.toHaveBeenCalled();
});
it("rejects a late canceled clock response and failed Machine reads without a clock fallback", async () => {
 const f=fixture(), abort=new AbortController(); const pending=readStartupPresence(f.transport,machineId,abort.signal).catch(error=>error);
 f.machine.resolve(); await waitFor(()=>expect(f.overview).toHaveBeenCalledTimes(1)); abort.abort(); f.clock.resolve(); expect(await pending).toBeInstanceOf(Error);
 const failing=fixture(); failing.get.mockRejectedValueOnce(new Error("read failed")); await expect(readStartupPresence(failing.transport,machineId,new AbortController().signal)).rejects.toThrow(); expect(failing.overview).not.toHaveBeenCalled();
});
it.each(["inactive","removed","replaced"])("fences the paired read when its conversation is %s", async action => {
 const f=fixture(), client=new QueryClient({defaultOptions:{queries:{retry:false}}}); let result:ReturnType<typeof useStartupPresence>;
 function Owner({active,owner}:{active:boolean;owner:string}) { result=useStartupPresence(machineId,owner,active); return null; }
 const view=(active=true,owner="original")=><TransportProvider transport={f.transport}><QueryClientProvider client={client}><Owner active={active} owner={owner}/></QueryClientProvider></TransportProvider>;
 const mounted=render(view());
 try {
  await waitFor(()=>expect(f.get).toHaveBeenCalledTimes(1)); f.machine.resolve(); await waitFor(()=>expect(f.overview).toHaveBeenCalledTimes(1));
  if(action==="removed") mounted.unmount(); else mounted.rerender(view(false,action==="replaced"?"replacement":"original"));
  await act(async()=>{f.clock.resolve();await new Promise(done=>setTimeout(done,0));});
  expect(client.getQueryCache().findAll().every(query=>query.state.data===undefined)).toBe(true);
  if(action!=="removed") expect(result!.data).toBeUndefined();
  expect(f.get).toHaveBeenCalledTimes(1);expect(f.overview).toHaveBeenCalledTimes(1);
 } finally { mounted.unmount();client.clear(); }
});

it("fails closed on an unavailable server clock without reusing a prior observation", async () => {
 const f=fixture();f.machine.resolve();f.overview.mockRejectedValueOnce(new Error("clock unavailable"));
 await expect(readStartupPresence(f.transport,machineId,new AbortController().signal)).rejects.toThrow();
 expect(f.get).toHaveBeenCalledTimes(1);expect(f.overview).toHaveBeenCalledTimes(1);
});
it("cancels the original pair on authenticated connection replacement", async () => {
 const old=fixture(), next=fixture(), client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 let result:ReturnType<typeof useStartupPresence>;
 function Owner(){result=useStartupPresence(machineId,"same-original-generation",true);return null;}
 const view=(transport:typeof old.transport)=><TransportProvider transport={transport}><QueryClientProvider client={client}><Owner/></QueryClientProvider></TransportProvider>;
 const mounted=render(view(old.transport));
 try {
  old.machine.resolve();await waitFor(()=>expect(old.overview).toHaveBeenCalledTimes(1));
  mounted.rerender(view(next.transport));next.machine.resolve();await waitFor(()=>expect(next.overview).toHaveBeenCalledTimes(1));
  await act(async()=>{old.clock.resolve();await new Promise(done=>setTimeout(done,0));});expect(result!.data).toBeUndefined();
  next.clock.resolve();await waitFor(()=>expect(result!.data?.observedAt).toBe("2026-10-09T00:00:00Z"));
 } finally {mounted.unmount();client.clear();}
});
