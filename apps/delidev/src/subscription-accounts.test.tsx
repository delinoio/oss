// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountTypeFilter, EntityKind, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { SubscriptionAccounts } from "./subscription-accounts";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { encode } from "./documents";
import { copy } from "./localization";

function fixture() {
 let statusFailed = false, accountsFailed = false;
 const row = create(ResourceSchema, { id:newRequestId(),kind:EntityKind.ACCOUNT,schemaVersion:2,revision:1n,documentJson:encode({alias:"Retained subscription",type:"subscription",subscription_service:"chatgpt",enabled:true,health:"disconnected",quota:[]}) });
 const status = vi.fn(() => {
  if (statusFailed) throw new ConnectError("Synthetic capability read failure",Code.Unavailable);
  return {capabilities:[SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1]};
 });
 const accounts = vi.fn((request: { filter?: { kind?: EntityKind; pageSize?: number; pageToken?: string }; accountType?: AccountTypeFilter; providerId?: string }) => {
  if (accountsFailed) throw new ConnectError("Synthetic account read failure",Code.Unavailable);
  return {resources:[row]};
 });
 const transport = createRouterTransport(router => {
  router.service(SystemService,{getStatus:status});
  router.service(ResourceService,{listResources:accounts});
 });
 // Any mutation or unrelated RPC must remain absent during explicit read recovery.
 const unary = vi.spyOn(transport,"unary");
 const client = new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity,refetchOnWindowFocus:false},mutations:{retry:false,gcTime:0}}});
 const edit = vi.fn(), remove = vi.fn();
 const view = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><SettingsLifetime>{()=><MutationIntents><SubscriptionAccounts active editAccount={edit} deleteAccount={remove}/></MutationIntents>}</SettingsLifetime></QueryClientProvider></TransportProvider>);
 return {status,accounts,unary,client,edit,remove,row,view,fail:(capability:boolean,account:boolean)=>{statusFailed=capability;accountsFailed=account;}};
}

for (const failed of ["both","capabilities","accounts"] as const) {
 it(`retries only failed subscription read owners after ${failed} failure`,async()=>{
  const f=fixture();
  await screen.findByRole("article",{name:"Retained subscription"});
  f.fail(failed!=="accounts",failed!=="capabilities");
  await act(async()=>{await f.client.invalidateQueries({refetchType:"active"});});
  const retry=await screen.findByRole("button",{name:copy("subscription-settings.retrySubscriptionRead_3772f6")});
  const statusCount=f.status.mock.calls.length,accountCount=f.accounts.mock.calls.length;
  f.fail(false,false);
  fireEvent.click(retry);
  await waitFor(()=>expect(screen.queryByRole("button",{name:copy("subscription-settings.retrySubscriptionRead_3772f6")})).toBeNull());
  expect(f.status).toHaveBeenCalledTimes(statusCount+(failed==="accounts"?0:1));
  expect(f.accounts).toHaveBeenCalledTimes(accountCount+(failed==="capabilities"?0:1));
  expect(screen.getByRole("article",{name:"Retained subscription"})).toBeTruthy();
  for(const [request] of f.accounts.mock.calls) expect(request).toMatchObject({filter:{kind:EntityKind.ACCOUNT,pageSize:50,pageToken:""},accountType:AccountTypeFilter.SUBSCRIPTION,providerId:""});
  for(const [method] of f.unary.mock.calls) expect(["GetStatus","ListResources"]).toContain(method.name);
  expect(f.edit).not.toHaveBeenCalled();expect(f.remove).not.toHaveBeenCalled();
  f.view.unmount();f.client.clear();
 });
}

it("keeps cached subscription rows and the remaining account error after partial recovery",async()=>{
 const f=fixture();
 await screen.findByRole("article",{name:"Retained subscription"});
 f.fail(true,true);
 await act(async()=>{await f.client.invalidateQueries({refetchType:"active"});});
 const label=copy("subscription-settings.retrySubscriptionRead_3772f6");
 const retry=await screen.findByRole("button",{name:label});
 const statusCount=f.status.mock.calls.length,accountCount=f.accounts.mock.calls.length;
 f.fail(false,true);fireEvent.click(retry);
 await waitFor(()=>expect(f.accounts).toHaveBeenCalledTimes(accountCount+1));
 await waitFor(()=>expect(f.client.isFetching()).toBe(0));
 await waitFor(()=>expect(screen.getByRole("button",{name:label})).toBeTruthy());
 expect(f.status).toHaveBeenCalledTimes(statusCount+1);
 expect(screen.getByRole("article",{name:"Retained subscription"})).toBeTruthy();
 // After capability recovery, a second deliberate retry targets only the
 // still-failed original account page, with the same subscription scope.
 f.fail(false,false);fireEvent.click(screen.getByRole("button",{name:label}));
 await waitFor(()=>expect(screen.queryByRole("button",{name:label})).toBeNull());
 expect(f.status).toHaveBeenCalledTimes(statusCount+1);
 expect(f.accounts).toHaveBeenCalledTimes(accountCount+2);
 expect(f.edit).not.toHaveBeenCalled();expect(f.remove).not.toHaveBeenCalled();
 for(const [method] of f.unary.mock.calls) expect(["GetStatus","ListResources"]).toContain(method.name);
 f.view.unmount();f.client.clear();
});
