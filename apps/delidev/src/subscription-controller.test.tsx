// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, EntityKind, ProviderService, ResourceSchema, ResourceService, SubscriptionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { SubscriptionAccounts } from "./subscription-accounts";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  let account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode({ alias: "Existing subscription", subscription_service: "chatgpt", type: "subscription", enabled: true, health: "disconnected", quota: [] }) });
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture runner" }) });
  const accounts = [account];
  let failure: Code | undefined;
  let release: (() => void) | undefined;
  const list = vi.fn((request) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? accounts : request.filter?.kind === EntityKind.MACHINE ? [machine] : [] }));
  const status = vi.fn(() => { if (failure) throw new ConnectError("Capability fixture failure", failure); return { capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1] }; });
  const provider = vi.fn(() => ({})), apiLifecycle = vi.fn(() => ({}));
  const save = vi.fn(async (request) => {
    const result = create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: request.schemaVersion, documentJson: request.documentJson });
    accounts.push(result);
    await new Promise<void>((resolve) => { release = resolve; });
    return { requestId: request.mutation?.requestId, resource: result };
  });
  const login = vi.fn(async (request) => {
    account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ alias: "Existing subscription", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "disconnected", quota: [], subscription: { pending: { id: request.mutation?.requestId, action: "login", phase: "claimed" } } }) });
    accounts[0] = account;
    return { account, operationId: request.mutation?.requestId };
  });
  const progress = vi.fn(() => ({ url: "https://auth.openai.com/device", userCode: "ABCD-EFGH" }));
  const cancel = vi.fn((request) => ({ account: create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ alias: "Existing subscription", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "disconnected", quota: [], subscription: { pending: { id: "original", action: "login", canceled: true } } }) }) }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(ProviderService, { listProviderInventory: provider, listProviderPresets: provider, discoverModels: provider, searchModels: provider });
    router.service(ResourceService, { listResources: list, getResource: (request) => ({ resource: accounts.find((row) => row.id === request.id) ?? (request.id === machine.id ? machine : undefined) }) });
    router.service(AccountService, { connectAccount: apiLifecycle, disconnectAccount: apiLifecycle, validateAccount: apiLifecycle });
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(SubscriptionService, { requestSubscription: login, getSubscriptionProgress: progress, cancelSubscription: cancel });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  function Harness() {
    const [visible, setVisible] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><button onClick={() => setVisible(true)}>Open Settings fixture</button><button onClick={() => setVisible(false)}>Leave Settings fixture</button><Settings visible={visible} /></QueryClientProvider></TransportProvider>;
  }
  return { Harness, client, machine, accounts, list, status, provider, apiLifecycle, save, login, cancel, progress, fail: (code?: Code) => { failure = code; }, finish: () => release?.() };
}

it.each([Code.Unavailable, Code.PermissionDenied, Code.Unauthenticated])("keeps capability failure %s retryable without granting native lifecycle", async (code) => {
  const value = fixture(); value.fail(code); render(<value.Harness />);
  await screen.findByRole("button", { name: "Retry subscription read" });
  expect(screen.queryByText(/Update the selected server to manage subscriptions/)).toBeNull();
  expect(screen.queryByRole("heading", { name: "No subscriptions yet" })).toBeNull();
  expect(value.list.mock.calls.some(([request]) => request.filter?.kind === EntityKind.ACCOUNT)).toBe(false);
  value.fail(); fireEvent.click(screen.getByRole("button", { name: "Retry subscription read" }));
  await screen.findByRole("article", { name: "Existing subscription" });
  expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ accountType: 2, providerId: "", filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: "" } });
  expect(value.provider).not.toHaveBeenCalled(); expect(value.apiLifecycle).not.toHaveBeenCalled(); expect(value.login).not.toHaveBeenCalled();
});

it("retains subscriptions after a capability read failure without querying providers", async () => {
  const value = fixture(); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  value.fail(Code.Unavailable);
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByText("Showing the last successfully loaded subscriptions.");
  expect(screen.getByRole("article", { name: "Existing subscription" })).toBeTruthy(); expect(value.provider).not.toHaveBeenCalled();
});

it("discards service drafts and late acknowledgments while accepted server work continues", async () => {
  const value = fixture(); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  fireEvent.click(screen.getByRole("button", { name: "Claude · Add account" }));
  fireEvent.change(screen.getByLabelText("Account name"), { target: { value: "Accepted subscription metadata" } });
  fireEvent.click(screen.getByRole("button", { name: "Save subscription account" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0];
  expect(request.schemaVersion).toBe(2); expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toMatchObject({ subscription_service: "claude", recovery_notifications: false });
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).not.toHaveProperty("provider_id");
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  fireEvent.click(screen.getByRole("button", { name: "Open Settings fixture" }));
  await screen.findByRole("article", { name: "Accepted subscription metadata" });
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  await act(async () => value.finish());
  expect(screen.getByRole("heading", { level: 1, name: "Instructions" })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1); expect(value.login).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "AI Subscription" }));
  expect(screen.queryByLabelText("Account name")).toBeNull(); expect(screen.queryByRole("button", { name: "Retry original subscription account creation" })).toBeNull();
});

it("runs only an explicit managed Codex login and retains its exact uncertain operation", async () => {
  const value = fixture(); value.login.mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable)); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  fireEvent.click(screen.getByRole("button", { name: "Manage login for Existing subscription" }));
  await screen.findByRole("option", { name: "Fixture runner" });
  fireEvent.change(screen.getByLabelText("Runner Device"), { target: { value: value.machine.id } });
  fireEvent.click(screen.getByRole("button", { name: "Log in with Codex" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original subscription operation" }));
  await screen.findByLabelText("Login address");
  expect(value.login).toHaveBeenCalledTimes(2); expect(value.login.mock.calls[0][0]).toEqual(value.login.mock.calls[1][0]);
  expect(value.login.mock.calls[0][0]).toMatchObject({ machineId: value.machine.id, action: 1, deviceCode: true, mutation: { expectedRevision: 1n } });
  expect(value.provider).not.toHaveBeenCalled(); expect(value.apiLifecycle).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Cancel login" }));
  await waitFor(() => expect(value.cancel).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(screen.queryByLabelText("Login address")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  expect(value.cancel).toHaveBeenCalledTimes(1);
});


it("routes row quota refresh to the original active execution lease machine", async () => {
 const value=fixture(), owner=newRequestId(), runner=newRequestId(), connection=newRequestId(), generation=newRequestId();
 const row=value.accounts[0]!;
 value.accounts[0]=create(ResourceSchema,{...row,documentJson:encode({alias:"Existing subscription",type:"subscription",subscription_service:"chatgpt",enabled:true,health:"ready",quota:[],connection:{id:connection},subscription:{generation,owner_machine_id:owner,lease:{action:"execute",machine_id:runner}}})});
 const refresh=vi.fn(async request=>({account:value.accounts[0],operationId:request.mutation.requestId}));
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,SystemCapability.SUBSCRIPTION_QUOTA_V1]})});
  router.service(ResourceService,{listResources:value.list});
  router.service(SubscriptionService,{requestSubscriptionObservation:refresh});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SubscriptionAccounts active editAccount={()=>{}} deleteAccount={()=>{}} /></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(await screen.findByRole("button",{name:"Refresh Existing subscription"}));
 await waitFor(()=>expect(refresh).toHaveBeenCalledTimes(1));
 expect(refresh.mock.calls[0]?.[0]).toMatchObject({machineId:runner,connectionId:connection,generationId:generation});
 expect(refresh.mock.calls[0]?.[0].machineId).not.toBe(owner);
});

it.each([
 {observation:{phase:"queued"}}, {observation:{phase:"sending"}}, {observation:{phase:"uncertain"}},
 {pending:{id:newRequestId()}}, {recovery_required:true}, {removal:{id:newRequestId()}},
])("disables row quota refresh while original ownership is active (%j)",async(blocker)=>{
 const value=fixture(), row=value.accounts[0]!, {removal,...state}=blocker as Record<string,unknown>;
 value.accounts[0]=create(ResourceSchema,{...row,documentJson:encode({alias:"Existing subscription",type:"subscription",subscription_service:"chatgpt",enabled:true,health:"ready",quota:[],connection:{id:newRequestId()},removal,subscription:{generation:newRequestId(),owner_machine_id:newRequestId(),...state}})});
 const refresh=vi.fn(()=>({}));
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,SystemCapability.SUBSCRIPTION_QUOTA_V1]})});
  router.service(ResourceService,{listResources:value.list});
  router.service(SubscriptionService,{requestSubscriptionObservation:refresh});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SubscriptionAccounts active editAccount={()=>{}} deleteAccount={()=>{}} /></MutationIntents></QueryClientProvider></TransportProvider>);
 await screen.findByRole("article",{name:"Existing subscription"});
 const button=screen.getByRole("button",{name:"Refresh Existing subscription"}) as HTMLButtonElement;
 expect(button.disabled).toBe(true);
 fireEvent.click(button);
 expect(refresh).not.toHaveBeenCalled();
});
