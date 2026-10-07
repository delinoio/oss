// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, ResourceService, SubscriptionService, EntityKind, ErrorDetailSchema, FailureCode, RequestSubscriptionRequestSchema, ResourceSchema, SaveConfigurationRequestSchema, SubscriptionLoginState as State, SubscriptionServiceId, newRequestId } from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
import { useSubscriptionLogin } from "./subscription-login";
import { SettingsLifetime } from "./settings-lifetime";
import { document, encode } from "./documents";
import { subscriptionAliasDocument } from "./subscription-resource";
const url = "https://auth.openai.com/oauth/authorize?state=fixture-original-state-123456&redirect_uri=http%3A%2F%2Flocalhost%3A1457%2Fauth%2Fcallback&response_type=code&code_challenge_method=S256";
function typedFailure(code: Code, failure: FailureCode) {
  return new ConnectError("Fixture request failure", code, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: failure }) }]);
}
function fixture(loginURL = url) {
  let current = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 1n, documentJson: encode({ alias: "ChatGPT", type: "subscription", subscription_service: "chatgpt" }) });
  let state = State.WAITING, suggested = "fixture@example.invalid", operation = "";
  const generation = newRequestId(), nativeGeneration = newRequestId();
  const save = vi.fn(async (request) => {
    current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: request.documentJson });
    return { requestId: request.mutation.requestId, resource: current };
  });
  const login = vi.fn((request) => {
    operation = request.mutation.requestId;
    current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: encode({ ...document(current), subscription: { server_operation: { id: operation }, pending: { id: operation } } }) });
    return { account: current, operationId: operation };
  });
  const progress = vi.fn((_request) => ({ state, url: state === State.WAITING ? loginURL : "", suggestedName: suggested, generation }));
  const cancel = vi.fn(() => { state = State.CANCELED; return { account: current }; });
  const forward = vi.fn(() => ({ accepted: true }));
  const read = vi.fn(() => ({ resource: current }));
  const native = vi.fn(async (_scope: string, _action: OAuthNativeAction, _generation: string, _attempt: string, _url: string) => ({ generation: nativeGeneration } as { generation: string; code?: number[] }));
  const transport = createRouterTransport(router => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { getResource: read });
    router.service(SubscriptionService, { requestSubscription: login, getSubscriptionProgress: progress, cancelSubscription: cancel, forwardSubscriptionCallback: forward });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const changed = vi.fn();
  function Body() {
    const flow = useSubscriptionLogin(true, changed);
    return <>{flow.body ?? <button onClick={() => flow.begin(SubscriptionServiceId.ChatGPT)}>Add account</button>}</>;
  }
  const view = () => <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><OAuthNativeProvider control={native}><SettingsLifetime>{() => <Body />}</SettingsLifetime></OAuthNativeProvider></QueryClientProvider></TransportProvider></StrictMode>;
  return { authorization: (value: string) => { loginURL = value; }, view, save, login, progress, cancel, forward, read, native, client, generation, current: () => current, success: () => { state = State.SUCCEEDED; current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: encode({ ...document(current), connection: { id: newRequestId() }, subscription: { generation, server_operation: { id: operation }, lease: { revision: "preserved" } } }) }); }, suggestion: (name: string) => { suggested = name; } };
}
async function start(f: ReturnType<typeof fixture>) {
  const rendered = render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  await screen.findByRole("button", { name: "Open browser again" }, { timeout: 3000 });
  return rendered;
}
it.each(["localhost", "127.0.0.1"])("starts once and preserves the original %s browser binding", async (host) => {
  const originalURL = url.replace("localhost", host);
  const f = fixture(originalURL); const rendered = render(f.view()); const add = screen.getByRole("button", { name: "Add account" });
  fireEvent.click(add); fireEvent.click(add);
  await screen.findByRole("button", { name: "Open browser again" }, { timeout: 3000 });
  expect(f.save).toHaveBeenCalledTimes(1); expect(f.login).toHaveBeenCalledTimes(1);
  expect(f.login.mock.calls[0][0]).toMatchObject({ machineId: "", deviceCode: false, action: 1 });
  expect(screen.queryByLabelText("Runner Device")).toBeNull(); expect(screen.queryByLabelText("Account name")).toBeNull();
  expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.SubscriptionOpen)).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() => expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.Reopen)).toHaveLength(1));
  const initial = f.native.mock.calls.find(c => c[1] === OAuthNativeAction.SubscriptionOpen)!;
  const again = f.native.mock.calls.find(c => c[1] === OAuthNativeAction.Reopen)!;
  expect(initial[4]).toBe(originalURL); expect(again[0]).toBe(initial[0]); expect(again[3]).toBe(initial[3]);
  rendered.unmount(); expect(f.cancel).not.toHaveBeenCalled();
});
it("suggests a name only after verified success, retains edits and saves current server state", async () => {
  const f = fixture(); await start(f); f.success();
  const input = await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  expect((input as HTMLInputElement).value).toBe("fixture@example.invalid"); await waitFor(() => expect(window.document.activeElement).toBe(input));
  fireEvent.change(input, { target: { value: "Edited name" } }); f.suggestion("later@example.invalid");
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect((input as HTMLInputElement).value).toBe("Edited name");
  fireEvent.click(screen.getByRole("button", { name: "Save account name" }));
  await screen.findByRole("button", { name: "Add account" });
  expect(document(f.current())).toMatchObject({ alias: "Edited name", subscription: { generation: f.generation, lease: { revision: "preserved" } } });
  expect(f.save).toHaveBeenCalledTimes(2); expect(f.login).toHaveBeenCalledTimes(1); expect(f.cancel).not.toHaveBeenCalled();
});
it("Later retains the default account name and accepted login", async () => {
  const f = fixture(); await start(f); f.success(); await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  fireEvent.click(screen.getByRole("button", { name: "Later" }));
  expect(document(f.current()).alias).toBe("ChatGPT"); expect(f.save).toHaveBeenCalledTimes(1); expect(f.cancel).not.toHaveBeenCalled();
});
it("keeps the edited name after a revision conflict and obtains a fresh revision on the next Save", async () => {
  const f = fixture(); await start(f); f.success();
  const input = await screen.findByLabelText("Account name", {}, { timeout: 3000 }); fireEvent.change(input, { target: { value: "Conflict edit" } });
  f.save.mockImplementationOnce(() => { throw typedFailure(Code.Aborted, FailureCode.Conflict); });
  fireEvent.click(screen.getByRole("button", { name: "Save account name" })); await screen.findByRole("alert");
  expect(screen.queryByRole("button", { name: "Retry original request" })).toBeNull();
  expect((input as HTMLInputElement).value).toBe("Conflict edit"); f.success();
  fireEvent.click(screen.getByRole("button", { name: "Save account name" })); await screen.findByRole("button", { name: "Add account" });
  expect(f.save.mock.calls[2][0].mutation.expectedRevision).toBeGreaterThan(f.save.mock.calls[1][0].mutation.expectedRevision);
});
it("retains a durably accepted login after typed Internal until exact explicit replay resumes polling", async () => {
  const f = fixture(), admit = f.login.getMockImplementation()!;
  let receipt: ReturnType<typeof admit>;
  f.login.mockImplementationOnce(request => {
    receipt = admit(request);
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  f.login.mockImplementationOnce(() => receipt);
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  const retry = await screen.findByRole("button", { name: "Retry original request" });
  expect((retry as HTMLButtonElement).disabled).toBe(false);
  const original = f.login.mock.calls[0][0], bytes = toBinary(RequestSubscriptionRequestSchema, original);
  expect(document(f.current()).subscription).toMatchObject({ pending: { id: original.mutation.requestId } });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(f.login).toHaveBeenCalledTimes(1); expect(f.save).toHaveBeenCalledTimes(1);
  expect(f.progress).not.toHaveBeenCalled(); expect(f.native).not.toHaveBeenCalled();
  fireEvent.click(retry);
  await screen.findByRole("button", { name: "Open browser again" }, { timeout: 3000 });
  expect(f.login).toHaveBeenCalledTimes(2); expect(f.save).toHaveBeenCalledTimes(1);
  expect(toBinary(RequestSubscriptionRequestSchema, f.login.mock.calls[1][0])).toEqual(bytes);
  expect(f.progress.mock.calls[0][0]).toMatchObject({ accountId: original.mutation.id, operationId: original.mutation.requestId });
  expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.SubscriptionOpen)).toHaveLength(1);
  expect(f.native.mock.calls.find(c => c[1] === OAuthNativeAction.SubscriptionOpen)![3]).toBe(original.mutation.requestId);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(f.progress.mock.calls.length).toBeGreaterThan(1);
  expect(f.login).toHaveBeenCalledTimes(2); expect(screen.queryByRole("button", { name: "Retry original request" })).toBeNull();
});
it("replays the exact account creation after typed Internal before starting login", async () => {
  const f = fixture(), admit = f.save.getMockImplementation()!;
  let receipt: Awaited<ReturnType<typeof admit>>;
  f.save.mockImplementationOnce(async request => {
    receipt = await admit(request);
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  f.save.mockImplementationOnce(async () => receipt);
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  const retry = await screen.findByRole("button", { name: "Retry original request" });
  const bytes = toBinary(SaveConfigurationRequestSchema, f.save.mock.calls[0][0]);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(f.save).toHaveBeenCalledTimes(1); expect(f.login).not.toHaveBeenCalled(); expect(f.progress).not.toHaveBeenCalled();
  fireEvent.click(retry);
  await screen.findByRole("button", { name: "Open browser again" }, { timeout: 3000 });
  expect(f.save).toHaveBeenCalledTimes(2); expect(f.login).toHaveBeenCalledTimes(1);
  expect(toBinary(SaveConfigurationRequestSchema, f.save.mock.calls[1][0])).toEqual(bytes);
  expect(f.login.mock.calls[0][0].mutation.id).toBe(receipt!.resource.id);
  expect(f.login.mock.calls[0][0].mutation.expectedRevision).toBe(receipt!.resource.revision);
});
it("retains the original alias save through typed Internal without adopting later edits or revisions", async () => {
  const f = fixture(); await start(f); f.success();
  const input = await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  fireEvent.change(input, { target: { value: "Original edit" } });
  const admit = f.save.getMockImplementation()!;
  let receipt: Awaited<ReturnType<typeof admit>>;
  f.save.mockImplementationOnce(async request => {
    receipt = await admit(request);
    throw typedFailure(Code.Internal, FailureCode.Internal);
  });
  f.save.mockImplementationOnce(async () => receipt);
  fireEvent.click(screen.getByRole("button", { name: "Save account name" }));
  const retry = await screen.findByRole("button", { name: "Retry original request" });
  const bytes = toBinary(SaveConfigurationRequestSchema, f.save.mock.calls[1][0]), reads = f.read.mock.calls.length;
  fireEvent.change(input, { target: { value: "Later edit" } }); f.success();
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(f.save).toHaveBeenCalledTimes(2); expect(f.read).toHaveBeenCalledTimes(reads);
  expect((input as HTMLInputElement).value).toBe("Later edit");
  fireEvent.click(retry); await screen.findByRole("button", { name: "Add account" });
  expect(f.save).toHaveBeenCalledTimes(3); expect(f.read).toHaveBeenCalledTimes(reads);
  expect(toBinary(SaveConfigurationRequestSchema, f.save.mock.calls[2][0])).toEqual(bytes);
  expect(document(f.current()).alias).toBe("Original edit"); expect(f.login).toHaveBeenCalledTimes(1);
});
it.each([
  [Code.InvalidArgument, FailureCode.InvalidArgument],
  [Code.PermissionDenied, FailureCode.PermissionDenied],
  [Code.Aborted, FailureCode.Conflict],
])("does not offer original login replay for a typed pre-admission rejection (%s)", async (code, failure) => {
  const f = fixture(); f.login.mockImplementation(() => { throw typedFailure(code, failure); });
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  await screen.findByRole("alert");
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(screen.queryByRole("button", { name: "Retry original request" })).toBeNull();
  expect(f.login).toHaveBeenCalledTimes(1); expect(f.progress).not.toHaveBeenCalled(); expect(f.native).not.toHaveBeenCalled();
});
it("forwards an original native callback once even when its result is uncertain", async () => {
  const f = fixture(); const code = Array.from(new TextEncoder().encode("code=fixture&state=fixture-original-state-123456"));
  // Retain the original binding ID, then return a single transient callback.
  const originalGeneration = newRequestId(); let delivered = false;
  f.native.mockImplementation(async (_scope, action) => { if (action === OAuthNativeAction.Take && !delivered) { delivered = true; return { generation: originalGeneration, code }; } return { generation: originalGeneration }; });
  f.forward.mockImplementation(() => { throw new ConnectError("fixture lost reply", Code.Unavailable); });
  await start(f); await waitFor(() => expect(f.forward).toHaveBeenCalledTimes(1), { timeout: 3000 });
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
  expect(f.forward).toHaveBeenCalledTimes(1); expect(code.every(v => v === 0)).toBe(true);
});
it("does not start login from a late creation acknowledgment after leaving", async () => {
  const f = fixture(); let finish!: () => void;
  const result = { requestId: "", resource: f.current() };
  f.save.mockImplementationOnce(async request => { result.requestId = request.mutation.requestId; await new Promise<void>(resolve => { finish = resolve; }); return result; });
  const rendered = render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" })); await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Back to AI Subscription" })); await act(async () => finish());
  expect(f.login).not.toHaveBeenCalled(); expect(screen.queryByLabelText("Account name")).toBeNull(); rendered.unmount();
});
it("patches only the alias without rounding protected revisions", () => {
  const raw = '{"alias":"Original","subscription":{"lease":{"revision":9007199254740993}},"unknown":[{"alias":"nested"}]}';
  const account = create(ResourceSchema, { documentJson: new TextEncoder().encode(raw) });
  const saved = new TextDecoder().decode(subscriptionAliasDocument(account, 'Changed "name"'));
  expect(saved).toBe(raw.replace('"Original"', '"Changed \\"name\\""')); expect(saved).toContain("9007199254740993");
});
it("recovers a lost native binding reply with one deliberate original reopen", async () => {
  const f = fixture(); const original = f.native.getMockImplementation()!;
  f.native.mockImplementation(async (...args) => { if (args[1] === OAuthNativeAction.SubscriptionOpen) throw new Error("fixture browser opening failed"); return original(...args); });
  await start(f); expect(screen.getByRole("alert").textContent).toContain("browser could not be opened");
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() => expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.SubscriptionReopen)).toHaveLength(1));
  expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.Reopen)).toHaveLength(0);
  expect(f.login).toHaveBeenCalledTimes(1);
});
it("clears a late native callback after departure without forwarding or canceling", async () => {
  const f = fixture(); const generation = newRequestId(); let finish!: (result: {generation:string;code:number[]}) => void;
  f.native.mockImplementation(async (_scope, action) => { if (action === OAuthNativeAction.Take) return new Promise(resolve => { finish = resolve; }); return { generation }; });
  await start(f); await waitFor(() => expect(finish).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "Back to AI Subscription" }));
  const code = Array.from(new TextEncoder().encode("code=fixture&state=fixture-original-state-123456"));
  await act(async () => finish({ generation, code }));
  expect(code.every(v => v === 0)).toBe(true); expect(f.forward).not.toHaveBeenCalled(); expect(f.cancel).not.toHaveBeenCalled();
});
it("uses the service name when successful login has no suggestion", async () => {
  const f = fixture(); f.suggestion(""); await start(f); f.success();
  const input = await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  expect((input as HTMLInputElement).value).toBe("ChatGPT");
});
it("requests cancellation only for the original accepted account operation", async () => {
  const f = fixture(); await start(f);
  fireEvent.click(screen.getByRole("button", { name: "Cancel login" }));
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(1));
  await screen.findByText("Login canceled", {}, { timeout: 3000 });
  fireEvent.click(screen.getByRole("button", { name: "Back to AI Subscription" }));
  expect(f.cancel).toHaveBeenCalledTimes(1);
});

it.each([
  "http://127.1:1457/auth/callback", "http://2130706433:1457/auth/callback", "http://[::1]:1457/auth/callback",
  "http://localhost:1455/auth/callback", "http://127.0.0.1:1457/other", "https://localhost:1457/auth/callback",
  "http://localhost.evil.invalid:1457/auth/callback",
])("rejects an unregistered callback %s before opening", async (callback) => {
  const f = fixture(url.replace("http%3A%2F%2Flocalhost%3A1457%2Fauth%2Fcallback", encodeURIComponent(callback)));
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  await screen.findByText("The original sign-in status could not be verified. Waiting for another status check.", {}, { timeout: 3000 });
  expect(f.native).not.toHaveBeenCalled(); expect(f.forward).not.toHaveBeenCalled();
});
it.each([
  url + "&redirect_uri=http%3A%2F%2F127.0.0.1%3A1457%2Fauth%2Fcallback",
  url + "&state=fixture-original-state-123456",
  url.replace("fixture-original-state-123456", "short"),
  url.replace("fixture-original-state-123456", "invalid.state-value-1234"),
])("rejects malformed authorization before opening (%#)", async (authorization) => {
  const f = fixture(authorization); render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  await screen.findByText("The original sign-in status could not be verified. Waiting for another status check.", {}, { timeout: 3000 });
  expect(f.native).not.toHaveBeenCalled();
});
it.each(["localhost", "127.0.0.1"])("refuses to replace an original %s callback with its other permitted spelling", async (host) => {
  const originalURL = url.replace("localhost", host), f = fixture(originalURL); await start(f);
  f.authorization(url.replace("localhost", host === "localhost" ? "127.0.0.1" : "localhost"));
  await screen.findByText("The original sign-in status could not be verified. Waiting for another status check.", {}, { timeout: 3000 });
  expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.SubscriptionOpen)).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() => expect(f.native.mock.calls.filter(c => c[1] === OAuthNativeAction.Reopen)).toHaveLength(1));
  expect(f.native.mock.calls.find(c => c[1] === OAuthNativeAction.SubscriptionOpen)![4]).toBe(originalURL);
  expect(f.login).toHaveBeenCalledTimes(1);
});
