// SPDX-License-Identifier: Apache-2.0
import { useState, StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionNativeAppsService, SystemCapability, SystemService, newRequestId, type Resource, type UpdateSessionAppsRequest } from "@delinoio/delidev-api-client";
import { encode, document } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionNativeApps, sessionAppsScope } from "./session-native-apps";
function fixture(supported = true) {
 const machineId = newRequestId(), account = newRequestId(), connection = newRequestId(), inventoryId = newRequestId();
 let session = create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:2n,documentJson:encode({archive:"active",recovery:"none",machine_id:machineId,initial_execution:{initial_account_id:account,connection_id:connection,configuration_digest:"a".repeat(64)}})});
 const scope = sessionAppsScope(session)!;
 const machine = create(ResourceSchema,{id:machineId,kind:EntityKind.MACHINE,schemaVersion:1,revision:1n,documentJson:encode({worker_capabilities:supported?["session-native-apps-v1"]:[]})});
 const read = vi.fn(async (request:{requestId:string;sessionId:string}) => {
  session = create(ResourceSchema,{...session,revision:session.revision+1n});
  return {requestId:request.requestId,sessionId:session.id,originalAccountId:account,originalConnectionId:connection,configurationDigest:"a".repeat(64),inventoryId,complete:true,observedAt:new Date().toISOString(),discovered:[{id:"available",name:"Available app",accessible:true,enabled:true},{id:"callable",name:"Calendar",accessible:true,enabled:true}],installed:[{id:"callable",enabled:true,callable:true}],selection:{scope,inventoryId,revision:1n,appIds:["callable"]}};
 });
 const update = vi.fn(async (request:UpdateSessionAppsRequest) => {
  session=create(ResourceSchema,{...session,revision:session.revision+1n});
  return {receipt:session,selection:{scope,inventoryId,revision:2n,appIds:request.selectedAppIds},replayed:false};
 });
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:supported?[SystemCapability.SESSION_NATIVE_APPS_V1]:[]})});
  router.service(ResourceService,{getResource:r=>({resource:r.id===machineId?machine:session})});
  router.service(SessionNativeAppsService,{readSessionApps:read,updateSessionApps:update});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 function Host(){const [current,setCurrent]=useState(session);return <SessionNativeApps session={current} changed={setCurrent} active/>;}
 const view=<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Host/></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
 return {view,read,update,session,scope};
}
async function inspect(v:ReturnType<typeof fixture>){render(v.view);fireEvent.click(screen.getByRole("button",{name:"Apps"}));await waitFor(()=>expect((screen.getByRole("button",{name:"Read Apps"}) as HTMLButtonElement).disabled).toBe(false));expect(v.read).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Read Apps"}));await screen.findByText("Calendar");}
test("separates original discovery/install/callability and submits explicit empty revocation with fresh source revision",async()=>{
 const v=fixture();await inspect(v);
 expect((screen.getByRole("checkbox",{name:"Available app"}) as HTMLInputElement).disabled).toBe(false);
 expect((screen.getByRole("checkbox",{name:"Calendar"}) as HTMLInputElement).checked).toBe(true);
 expect(globalThis.document.body.textContent).not.toContain(v.scope.originalAccountId);expect(globalThis.document.body.textContent).not.toContain(v.scope.originalConnectionId);
 fireEvent.click(screen.getByRole("button",{name:"Clear selection"}));fireEvent.click(screen.getByRole("button",{name:"Save selection"}));await waitFor(()=>expect(v.update).toHaveBeenCalledTimes(1));
 expect(v.update.mock.calls[0][0]).toMatchObject({sessionId:v.session.id,originalAccountId:v.scope.originalAccountId,originalConnectionId:v.scope.originalConnectionId,configurationDigest:v.scope.configurationDigest,selectedAppIds:[],mutation:{id:v.session.id,expectedRevision:3n}});
 await screen.findByText(/The original selection request was acknowledged/);
});
test("retains exact uncertain selection request and requires explicit same-request retry",async()=>{
 const v=fixture();v.update.mockRejectedValueOnce(new ConnectError("private",Code.Unavailable));await inspect(v);fireEvent.click(screen.getByRole("button",{name:"Save selection"}));await screen.findByRole("button",{name:"Retry the same selection request"});
 expect(v.update).toHaveBeenCalledTimes(1);expect((screen.getByRole("button",{name:"Refresh Apps"}) as HTMLButtonElement).disabled).toBe(true);
 const original=v.update.mock.calls[0][0];fireEvent.click(screen.getByRole("button",{name:"Retry the same selection request"}));await waitFor(()=>expect(v.update).toHaveBeenCalledTimes(2));expect(v.update.mock.calls[1][0]).toEqual(original);
});
test("missing independently negotiated capabilities never reads or updates native Apps",async()=>{
 const v=fixture(false);render(v.view);fireEvent.click(screen.getByRole("button",{name:"Apps"}));await screen.findByText("Apps are unavailable for this session or its current Worker.");expect(screen.queryByRole("button",{name:"Read Apps"})).toBeNull();expect(v.read).not.toHaveBeenCalled();expect(v.update).not.toHaveBeenCalled();
});
test("rejects foreign, missing digest and Fork scopes instead of inheriting original Apps",()=>{
 const v=fixture(); for(const patch of [{fork:{}},{initial_execution:{initial_account_id:v.scope.originalAccountId,connection_id:v.scope.originalConnectionId}},{current_execution:{account_id:"foreign",connection_id:v.scope.originalConnectionId}}]) expect(sessionAppsScope(create(ResourceSchema,{...v.session,documentJson:encode({...document(v.session),...patch})}))).toBeUndefined();
});

test("foreign original inventory receipt retains uncertainty without granting selection",async()=>{
 const v=fixture(), original=v.read.getMockImplementation()!;
 v.read.mockImplementation(async request=>({...await original(request),originalConnectionId:newRequestId()}));
 render(v.view);fireEvent.click(screen.getByRole("button",{name:"Apps"}));await screen.findByRole("button",{name:"Read Apps"});fireEvent.click(screen.getByRole("button",{name:"Read Apps"}));await screen.findByRole("button",{name:"Retry the same Apps read"});
 expect(screen.queryByRole("checkbox")).toBeNull();expect(v.update).not.toHaveBeenCalled();expect(v.read).toHaveBeenCalledTimes(1);
});
test("closing and reopening preserves the original uncertain read rather than creating another refresh",async()=>{
 const v=fixture();v.read.mockRejectedValueOnce(new ConnectError("lost",Code.Unavailable));render(v.view);fireEvent.click(screen.getByRole("button",{name:"Apps"}));await screen.findByRole("button",{name:"Read Apps"});fireEvent.click(screen.getByRole("button",{name:"Read Apps"}));await screen.findByRole("button",{name:"Retry the same Apps read"});
 const original=v.read.mock.calls[0][0];fireEvent.click(screen.getByRole("button",{name:"Close Apps"}));fireEvent.click(screen.getByRole("button",{name:"Apps"}));expect(v.read).toHaveBeenCalledTimes(1);fireEvent.click(screen.getByRole("button",{name:"Retry the same Apps read"}));await screen.findByText("Calendar");expect(v.read.mock.calls[1][0]).toEqual(original);
});

test("explicit selection can enable a verified accessible App without borrowing installation or callable authority",async()=>{
 const v=fixture(),original=v.read.getMockImplementation()!;
 v.read.mockImplementation(async request=>{const observed=await original(request);return {...observed,discovered:[{id:"available",name:"Available app",accessible:true,enabled:false},{id:"callable",name:"Calendar",accessible:true,enabled:false}],installed:[{id:"callable",enabled:false,callable:false}],selection:{scope:v.scope,inventoryId:observed.inventoryId,revision:1n,appIds:[]}};});
 await inspect(v);const available=screen.getByRole("checkbox",{name:"Available app"}) as HTMLInputElement;
 expect(available.disabled).toBe(false);expect(available.checked).toBe(false);fireEvent.click(available);fireEvent.click(screen.getByRole("button",{name:"Save selection"}));
 await waitFor(()=>expect(v.update).toHaveBeenCalledTimes(1));expect(v.update.mock.calls[0][0].selectedAppIds).toEqual(["available"]);
 expect(screen.queryByRole("button",{name:/execute|invoke|approve/i})).toBeNull();
});
