// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CodexDiagnosticSchema, CodexDiagnosticPhase, AccountService, ConfigurationService, EntityKind, ProviderService, ResourceSchema, ResourceService, SubscriptionService, SystemCapability, SystemService, SubscriptionLoginState, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { ManagedSubscriptionAccount, SubscriptionAccounts } from "./subscription-accounts";
import { MutationIntents } from "./mutation";
import { OAuthNativeProvider } from "./account-oauth";
import { encode } from "./documents";

function fixture() {
  let account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode({ alias: "Existing subscription", subscription_service: "chatgpt", type: "subscription", enabled: true, health: "disconnected", quota: [] }) });
  const machine = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Fixture runner" }) });
  const accounts = [account];
  let failure: Code | undefined;
  let release: (() => void) | undefined;
  const list = vi.fn((request) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? accounts : request.filter?.kind === EntityKind.MACHINE ? [machine] : [] }));
  const status = vi.fn(() => { if (failure) throw new ConnectError("Capability fixture failure", failure); return { capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1, SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1] }; });
  const provider = vi.fn(() => ({})), apiLifecycle = vi.fn(() => ({}));
  const save = vi.fn(async (request) => {
    const result = create(ResourceSchema, { id: newRequestId(), kind: request.kind, revision: 1n, schemaVersion: request.schemaVersion, documentJson: request.documentJson });
    accounts.push(result);
    await new Promise<void>((resolve) => { release = resolve; });
    return { requestId: request.mutation?.requestId, resource: result };
  });
  const login = vi.fn(async (request) => {
    account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ alias: "Existing subscription", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "disconnected", quota: [], subscription: { server_operation: { id: request.mutation?.requestId }, pending: { id: request.mutation?.requestId, action: "login", phase: "claimed" } } }) });
    accounts[0] = account;
    return { account, operationId: request.mutation?.requestId };
  });
  const progress = vi.fn(() => ({ state: SubscriptionLoginState.WAITING, url: "https://auth.openai.com/oauth/authorize?state=fixture-original-state-123456&redirect_uri=http%3A%2F%2Flocalhost%3A1457%2Fauth%2Fcallback" }));
  const native = vi.fn(async () => ({ generation: newRequestId() }));
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
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><button onClick={() => setVisible(true)}>Open Settings fixture</button><button onClick={() => setVisible(false)}>Leave Settings fixture</button><OAuthNativeProvider control={native}><Settings visible={visible} /></OAuthNativeProvider></QueryClientProvider></TransportProvider>;
  }
  return { Harness, client, machine, accounts, list, status, provider, apiLifecycle, save, login, cancel, progress, native, fail: (code?: Code) => { failure = code; }, finish: () => release?.() };
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

it("discards a late creation acknowledgment while keeping accepted default account metadata", async () => {
  const value = fixture(); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  fireEvent.click(screen.getByRole("button", { name: "ChatGPT · Add account" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = value.save.mock.calls[0][0];
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toMatchObject({ alias: "ChatGPT", subscription_service: "chatgpt", recovery_notifications: false });
  fireEvent.click(screen.getByRole("button", { name: "Leave Settings fixture" }));
  fireEvent.click(screen.getByRole("button", { name: "Open Settings fixture" }));
  await screen.findByRole("article", { name: "ChatGPT" });
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  await act(async () => value.finish());
  expect(screen.getByRole("heading", { level: 1, name: "Instructions" })).toBeTruthy();
  expect(value.save).toHaveBeenCalledTimes(1); expect(value.login).not.toHaveBeenCalled();
});

it("starts an existing account login without a Runner Device and retries the exact uncertain request", async () => {
  const value = fixture(); value.login.mockRejectedValueOnce(new ConnectError("response lost", Code.Unavailable)); render(<value.Harness />);
  await screen.findByRole("article", { name: "Existing subscription" });
  fireEvent.click(screen.getByRole("button", { name: "Manage login for Existing subscription" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Sign in to ChatGPT" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(await screen.findByRole("button", { name: "Sign in to ChatGPT" }));
  await screen.findByRole("button", { name: "Retry original request" });
  fireEvent.click(screen.getByRole("button", { name: "Retry original request" }));
  await screen.findByRole("button", { name: "Open browser again" }, { timeout: 3000 });
  expect(value.login).toHaveBeenCalledTimes(2); expect(value.login.mock.calls[0][0]).toEqual(value.login.mock.calls[1][0]);
  expect(value.login.mock.calls[0][0]).toMatchObject({ machineId: "", action: 1, deviceCode: false, mutation: { expectedRevision: 1n } });
  expect(screen.queryByLabelText("Runner Device")).toBeNull(); expect(screen.queryByLabelText("Login address")).toBeNull();
  expect(value.provider).not.toHaveBeenCalled(); expect(value.apiLifecycle).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Cancel login" }));
  await waitFor(() => expect(value.cancel).toHaveBeenCalledTimes(1));
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

it("preserves managed quota authority without the independent login capability", async () => {
  const owner = newRequestId(), connection = newRequestId(), generation = newRequestId();
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2,
    documentJson: encode({ alias: "Connected subscription", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "ready", quota: [], connection: { id: connection }, subscription: { generation, owner_machine_id: owner } }) });
  const refresh = vi.fn(async request => ({ account, operationId: request.mutation.requestId }));
  const login = vi.fn(() => ({}));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1, SystemCapability.SUBSCRIPTION_QUOTA_V1] }) });
    router.service(ResourceService, { getResource: () => ({ resource: account }) });
    router.service(SubscriptionService, { requestSubscriptionObservation: refresh, requestSubscription: login });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ManagedSubscriptionAccount initial={account} active close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  const button = await screen.findByRole("button", { name: "Refresh quota" });
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  expect((screen.getByRole("button", { name: "Refresh login" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(button);
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  expect(refresh.mock.calls[0]?.[0]).toMatchObject({ machineId: owner, connectionId: connection, generationId: generation });
  expect(login).not.toHaveBeenCalled();
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

it("projects terminal Codex diagnostics without another login or cached native progress", async () => {
 const value=fixture();
 value.progress.mockImplementation(() => ({state:SubscriptionLoginState.FAILED,url:"",diagnostic:create(CodexDiagnosticSchema,{detectedVersion:"0.159.2",minimumVersion:"0.151.0",phase:CodexDiagnosticPhase.INITIALIZE,code:"unsupported",message:"private-native-sentinel"})}));
 render(<value.Harness />);
 fireEvent.click(await screen.findByRole("button",{name:"Manage login for Existing subscription"}));
  await waitFor(() => expect((screen.getByRole("button", { name: "Sign in to ChatGPT" }) as HTMLButtonElement).disabled).toBe(false));
  await waitFor(() => expect((screen.getByRole("button", { name: "Sign in to ChatGPT" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(await screen.findByRole("button",{name:"Sign in to ChatGPT"}));
 await screen.findByText("ChatGPT sign-in failed",{}, {timeout:4000});
 expect(screen.getByRole("alert").textContent).toContain("0.159.2");
 expect(screen.getByRole("alert").textContent).toContain("Initialization");
 expect(screen.queryByText(/private-native-sentinel/)).toBeNull();
 expect(value.login).toHaveBeenCalledTimes(1);
 const reads=value.progress.mock.calls.length;
 await act(async()=>{await new Promise(resolve=>setTimeout(resolve,1100));});
 expect(value.progress).toHaveBeenCalledTimes(reads);
 expect(value.login).toHaveBeenCalledTimes(1);
 expect(JSON.stringify(value.client.getQueryCache().getAll().map(q=>q.state.data),(_,v)=>typeof v === "bigint" ? v.toString() : v)).not.toContain("private-native-sentinel");
 fireEvent.click(screen.getByRole("button",{name:"Leave Settings fixture"}));
 expect(value.cancel).not.toHaveBeenCalled();
});

it("keeps an uncertain original browser opening visible across later waiting polls", async () => {
 const value=fixture(); value.native.mockRejectedValue(new Error("private-native-url"));
 render(<value.Harness />);
 fireEvent.click(await screen.findByRole("button",{name:"Manage login for Existing subscription"}));
  await waitFor(() => expect((screen.getByRole("button", { name: "Sign in to ChatGPT" }) as HTMLButtonElement).disabled).toBe(false));
  await waitFor(() => expect((screen.getByRole("button", { name: "Sign in to ChatGPT" }) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(await screen.findByRole("button",{name:"Sign in to ChatGPT"}));
 await screen.findByText(/The browser could not be opened/,{}, {timeout:4000});
 const nativeCalls=value.native.mock.calls.length;
 await act(async()=>{await new Promise(resolve=>setTimeout(resolve,1100));});
 expect(screen.getByRole("alert").textContent).toContain("The browser could not be opened");
 expect(screen.queryByText(/private-native-url/)).toBeNull();
 expect(value.native).toHaveBeenCalledTimes(nativeCalls);
 expect(value.login).toHaveBeenCalledTimes(1);
});

it("a successful missing account observation is unavailable and offers read recovery without login", async () => {
 const account=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,revision:1n,schemaVersion:2,documentJson:encode({alias:"Original missing account",type:"subscription",subscription_service:"chatgpt",health:"disconnected"})});
 const read=vi.fn(()=>({}));const login=vi.fn();const native=vi.fn(async()=>({generation:newRequestId()}));
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1]})});router.service(ResourceService,{getResource:read});router.service(SubscriptionService,{requestSubscription:login});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><OAuthNativeProvider control={native}><MutationIntents><ManagedSubscriptionAccount initial={account} active close={vi.fn()} /></MutationIntents></OAuthNativeProvider></QueryClientProvider></TransportProvider>);
 await screen.findByText(/The current account could not be verified/);expect((screen.getByRole("button",{name:"Sign in to ChatGPT"}) as HTMLButtonElement).disabled).toBe(true);expect(login).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Refresh account status"}));await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));expect(login).not.toHaveBeenCalled();
});

it("blocks fresh subscription actions for successful reads older than the original or accepted revision", async () => {
 const account=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,revision:9007199254740993n,schemaVersion:2,documentJson:encode({alias:"Original subscription",type:"subscription",subscription_service:"chatgpt",health:"ready",connection:{id:newRequestId()},subscription:{owner_machine_id:newRequestId(),generation:newRequestId()}})});
 let observed=create(ResourceSchema,{...account,revision:account.revision-1n});const accepted=create(ResourceSchema,{...account,revision:account.revision+1n});
 const read=vi.fn(()=>({resource:observed}));const login=vi.fn();const quota=vi.fn(async request=>({account:accepted,operationId:request.mutation.requestId}));
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1,SystemCapability.SUBSCRIPTION_QUOTA_V1]})});router.service(ResourceService,{getResource:read});router.service(SubscriptionService,{requestSubscription:login,requestSubscriptionObservation:quota});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ManagedSubscriptionAccount initial={account} active close={vi.fn()} /></MutationIntents></QueryClientProvider></TransportProvider>);
 const unavailable=()=>screen.findByText(/The current account could not be verified/);
 const freshButtons=()=>["Refresh login","Log out","Refresh quota"].map(name=>screen.getByRole("button",{name}) as HTMLButtonElement);
 await unavailable();expect(freshButtons().every(button=>button.disabled)).toBe(true);expect(quota).not.toHaveBeenCalled();
 observed=account;fireEvent.click(screen.getByRole("button",{name:"Refresh account status"}));await waitFor(()=>expect(freshButtons().every(button=>!button.disabled)).toBe(true));
 fireEvent.click(screen.getByRole("button",{name:"Refresh quota"}));await waitFor(()=>expect(quota).toHaveBeenCalledOnce());await unavailable();
 expect(quota.mock.calls[0][0].mutation.expectedRevision).toBe(account.revision);expect(freshButtons().every(button=>button.disabled)).toBe(true);expect(login).not.toHaveBeenCalled();
 observed=accepted;fireEvent.click(screen.getByRole("button",{name:"Refresh account status"}));await waitFor(()=>expect(freshButtons().every(button=>!button.disabled)).toBe(true));
 expect(quota).toHaveBeenCalledOnce();expect(login).not.toHaveBeenCalled();view.unmount();client.clear();
});

it("retains the exact uncertain subscription operation while a successful older read blocks fresh actions", async () => {
 const account=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,revision:9007199254740993n,schemaVersion:2,documentJson:encode({alias:"Original subscription",type:"subscription",subscription_service:"chatgpt",health:"ready",connection:{id:newRequestId()}})});
 let observed=account;const read=vi.fn(()=>({resource:observed}));const operation=vi.fn(async request=>({account:create(ResourceSchema,{...account,revision:account.revision+1n}),operationId:request.mutation.requestId}));operation.mockRejectedValueOnce(new ConnectError("original reply lost",Code.Unavailable));
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1]})});router.service(ResourceService,{getResource:read});router.service(SubscriptionService,{requestSubscription:operation});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});const view=render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ManagedSubscriptionAccount initial={account} active close={vi.fn()} /></MutationIntents></QueryClientProvider></TransportProvider>);
 await waitFor(()=>expect((screen.getByRole("button",{name:"Refresh login"}) as HTMLButtonElement).disabled).toBe(false));fireEvent.click(screen.getByRole("button",{name:"Refresh login"}));
 const retry=await screen.findByRole("button",{name:"Retry original subscription operation"});const original=operation.mock.calls[0][0];
 observed=create(ResourceSchema,{...account,revision:account.revision-1n});await act(async()=>{await client.invalidateQueries({refetchType:"active"});});await screen.findByText(/The current account could not be verified/);
 expect((screen.getByRole("button",{name:"Refresh login"}) as HTMLButtonElement).disabled).toBe(true);expect((retry as HTMLButtonElement).disabled).toBe(false);
 fireEvent.click(retry);await waitFor(()=>expect(operation).toHaveBeenCalledTimes(2));expect(operation.mock.calls[1][0]).toEqual(original);expect(original.mutation.expectedRevision).toBe(9007199254740993n);
 view.unmount();client.clear();
});
