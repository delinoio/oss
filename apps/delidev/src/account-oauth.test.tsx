// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, AccountOAuthFlow, ErrorDetailSchema, AccountOAuthAttemptSchema, AccountOAuthState as State, EntityKind, ResourceSchema, newRequestId, type CompleteAccountOAuthRequest } from "@delinoio/delidev-api-client";
import { OpenRouterOAuth, AccountOAuthProfile, OAuthNativeAction, OAuthNativeProvider, useOpenRouterOAuth, type OAuthNativeControl, type OAuthNativeResult } from "./account-oauth";
import { SettingsLifetime } from "./settings-lifetime";
import type { AccountProviderSummary } from "./account-settings";
import { encode } from "./documents";

function fixture(args: { huggingFace?: boolean; gemini?: boolean; baseten?: boolean; wrongFlow?: boolean; oldNative?: boolean; complete?: (request: CompleteAccountOAuthRequest) => Promise<void>; native?: OAuthNativeControl; startDelay?: Promise<void>; startError?: ConnectError; interruptedStart?: boolean } = {}) {
  const name = args.huggingFace ? "Hugging Face Inference Providers" : args.gemini ? "Google Gemini" : args.baseten ? "Baseten" : "OpenRouter";
  const providerId = newRequestId(), attemptId = newRequestId(), nativeGeneration = newRequestId();
  const provider = create(ResourceSchema, { kind: EntityKind.PROVIDER, id: providerId, schemaVersion: 1, revision: 1n, documentJson: encode({ name, preset_id: args.huggingFace ? "hugging-face" : args.gemini ? "gemini" : args.baseten ? "baseten" : "openrouter", endpoint: "https://openrouter.ai/api/v1", protocol: "openai-chat", authentication: "bearer", enabled: true }) });
  const selected: AccountProviderSummary = { providerId, provider, displayName: name, enabled: true, oauthAvailable: true, keyGuidance: "", documentationUrl: "" };
  const attempt = (state = State.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION, revision = 1n) => create(AccountOAuthAttemptSchema, { id: attemptId, providerId, revision, state, expiresAt: new Date(Date.now() + 600000).toISOString() });
  let retained = attempt(), callback = false, deviceReceipt = "";
  const rawState = Array.from(new TextEncoder().encode("s".repeat(43)));
  const rawCode = Array.from(new TextEncoder().encode("renderer-oauth-code-sentinel"));
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 2n, documentJson: encode({ alias: "OpenRouter", provider_id: providerId, type: "api", health: "unverified", enabled: true, recovery_notifications: true }) });
  const start = vi.fn(async (request) => { await args.startDelay; if (args.startError) throw args.startError; if(args.interruptedStart) {retained=attempt(State.ACCOUNT_OAUTH_STATE_INTERRUPTED,2n);retained.problem=create(ErrorDetailSchema,{code:"conflict"});return {attempt:retained,requestId:request.provider?.requestId};} return { attempt: retained, requestId: request.provider?.requestId, flow: args.wrongFlow ? AccountOAuthFlow.ACCOUNT_OAUTH_FLOW_UNSPECIFIED : args.baseten ? AccountOAuthFlow.ACCOUNT_OAUTH_FLOW_DEVICE : AccountOAuthFlow.ACCOUNT_OAUTH_FLOW_PKCE, userCode: args.baseten ? "ABCD-EFGH" : undefined, authorizationUrl: "https://openrouter.ai/auth?fixture-live-start" }; });
  const complete = vi.fn(async (request: CompleteAccountOAuthRequest) => { if (args.complete) await args.complete(request); retained = attempt(State.ACCOUNT_OAUTH_STATE_CONNECTED, 5n); return { attempt: retained, account, requestId: request.mutation?.requestId }; });
  const cancel = vi.fn(async (request) => { retained = attempt(State.ACCOUNT_OAUTH_STATE_CANCELED, 2n); return { attempt: retained, requestId: request.mutation?.requestId }; });
  const status = vi.fn(async () => ({ attempt: retained, requestId:args.baseten ? deviceReceipt : "", account: retained.state === State.ACCOUNT_OAUTH_STATE_CONNECTED ? account : undefined }));
  const transport = createRouterTransport(router => router.service(AccountService, { startAccountOAuth: start, completeAccountOAuth: complete, cancelAccountOAuth: cancel, getAccountOAuthStatus: status }));
  const native = vi.fn<OAuthNativeControl>(args.native ?? (async (_opening, action, generation): Promise<OAuthNativeResult> => {
    if (action === OAuthNativeAction.Profiles) return { generation: "", profiles: args.oldNative ? [] : [AccountOAuthProfile.OpenRouter, AccountOAuthProfile.HuggingFace, AccountOAuthProfile.GoogleGemini, AccountOAuthProfile.Baseten] };
    if (action === OAuthNativeAction.BeginBaseten) return { generation:nativeGeneration };
    if (action === OAuthNativeAction.Begin || action === OAuthNativeAction.BeginHuggingFace || action === OAuthNativeAction.BeginGoogleGemini) return { generation: nativeGeneration, callback_url: `http://localhost:55451/oauth/openrouter/${"a".repeat(64)}` };
    if (action === OAuthNativeAction.Take && callback) { callback = false; return { generation, code: rawCode, state: args.huggingFace || args.gemini ? rawState : undefined }; }
    return { generation };
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const back = vi.fn(), manual = vi.fn(), edit = vi.fn(), manage = vi.fn(), done = vi.fn();
  function Harness() {
    const flow = useOpenRouterOAuth();
    return <><button onClick={() => flow.start(selected)}>Connect selected OpenRouter</button><OpenRouterOAuth flow={flow} back={back} manual={manual} edit={edit} manage={manage} done={done} /></>;
  }
  const view = render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><OAuthNativeProvider control={native}><SettingsLifetime>{() => <Harness />}</SettingsLifetime></OAuthNativeProvider></QueryClientProvider></TransportProvider></StrictMode>);
  return { start, complete, cancel, status, native, client, view, rawCode, rawState, manual, back, edit, account, recoverDevice: () => { deviceReceipt = newRequestId(); retained=attempt(State.ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED,3n); retained.problem=create(ErrorDetailSchema,{code:"recovery_required"}); }, trigger: () => { callback = true; }, change: (state: State, revision: bigint) => { retained = attempt(state, revision); } };
}

it("starts and opens exactly once on deliberate action under Strict Mode, with no mount authentication", async () => {
  const f = fixture();
  expect(f.start).not.toHaveBeenCalled(); expect(f.native.mock.calls.every(call => call[1] === OAuthNativeAction.Profiles)).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  expect(await screen.findByText("Waiting for authorization…")).toBeTruthy();
  await waitFor(() => expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BindOpen)).toHaveLength(1));
  expect(f.start).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.getByText("Your credential will be stored securely on the selected server.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
  await waitFor(() => expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.Reopen)).toHaveLength(1));
  expect(f.start).toHaveBeenCalledTimes(1);
});

it("forwards a callback once through a write-only RPC and clears code buffers without cached credentials", async () => {
  let observed = "";
  const f = fixture({ complete: async request => { observed = new TextDecoder().decode(request.authorizationCode); } });
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  await screen.findByText("Waiting for authorization…"); f.trigger();
  await screen.findByText("OpenRouter connected", {}, { timeout: 2500 });
  expect(observed).toBe("renderer-oauth-code-sentinel"); expect(f.complete).toHaveBeenCalledTimes(1);
  expect(f.rawCode.every(byte => byte === 0)).toBe(true);
  expect(f.client.getMutationCache().getAll()).toHaveLength(0);
  const cache = JSON.stringify(f.client.getQueryCache().getAll(), (_key, value) => typeof value === "bigint" ? value.toString() : value);
  expect(cache).not.toContain("renderer-oauth-code-sentinel");
  expect(screen.queryByText(/verified|ready/i)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Edit account" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledWith(expect.objectContaining({ id: f.account.id, revision: f.account.revision, schemaVersion: 1 }))); expect(f.cancel).not.toHaveBeenCalled();
});

it("requires a confirmed business cancellation before manual fallback and retains original provider", async () => {
  const f = fixture(); fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  await screen.findByText("Waiting for authorization…"); fireEvent.click(screen.getByRole("button", { name: "Use an API key instead" }));
  await waitFor(() => expect(f.manual).toHaveBeenCalledTimes(1)); expect(f.cancel).toHaveBeenCalledTimes(1);
  expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Dispose)).toBe(true); expect(f.complete).not.toHaveBeenCalled();
});

it("disposes callback authority on Settings close without business Cancel, including a late Start result", async () => {
  let release!: () => void; const wait = new Promise<void>(resolve => { release = resolve; });
  const f = fixture({ startDelay: wait }); fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  await waitFor(() => expect(f.start).toHaveBeenCalledTimes(1));
  f.view.unmount(); release();
  await waitFor(() => expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Dispose)).toBe(true));
  expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BindOpen)).toHaveLength(0); expect(f.cancel).not.toHaveBeenCalled(); expect(f.complete).not.toHaveBeenCalled();
});

it("clears and discards a late one-shot callback after the opening closes", async () => {
  let release!: (result: OAuthNativeResult) => void;
  const code = Array.from(new TextEncoder().encode("late-callback-sentinel"));
  const native: OAuthNativeControl = async (_opening, action, generation) => action === OAuthNativeAction.Begin ? { generation: newRequestId(), callback_url: `http://localhost:55451/oauth/openrouter/${"b".repeat(64)}` } : action === OAuthNativeAction.Take ? new Promise(resolve => { release = resolve; }) : { generation };
  const f = fixture({ native }); fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  await screen.findByText("Waiting for authorization…"); await waitFor(() => expect(release).toBeTypeOf("function"), { timeout: 2500 });
  f.view.unmount(); release({ generation: "old", code });
  await waitFor(() => expect(code.every(byte => byte === 0)).toBe(true)); expect(f.complete).not.toHaveBeenCalled(); expect(f.cancel).not.toHaveBeenCalled();
});

it("an uncertain completion uses only its original code-free recovery identity", async () => {
  let first = true;
  const f = fixture({ complete: async request => { if (first) { first = false; throw new ConnectError("lost response", Code.Unavailable); } expect(request.authorizationCode.byteLength).toBe(0); } });
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" })); await screen.findByText("Waiting for authorization…"); f.trigger();
  await screen.findByText("Completion was not confirmed. Inspect the original attempt. An uncertain exchange is never repeated.", {}, { timeout: 2500 });
  const original = f.complete.mock.calls[0][0].mutation;
  fireEvent.click(screen.getByRole("button", { name: "Recover saved result" })); await screen.findByText("OpenRouter connected");
  expect(f.complete.mock.calls[1][0].mutation).toEqual(original); expect(f.complete).toHaveBeenCalledTimes(2); expect(f.start).toHaveBeenCalledTimes(1);
});

it("a native preparation failure allows explicit cleanup and manual fallback before any server Start", async () => {
  const f = fixture({ native: async (_opening, action, generation) => { if (action === OAuthNativeAction.Begin) throw new Error("listener unavailable"); return { generation }; } });
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" })); await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "Use an API key instead" })); await waitFor(() => expect(f.manual).toHaveBeenCalledTimes(1));
  expect(f.start).not.toHaveBeenCalled(); expect(f.cancel).not.toHaveBeenCalled();
});

it("retries the exact browser binding after a native failure before admission", async () => {
 let binds = 0;
 const generation = newRequestId();
 const f = fixture({ native: async (_opening, action, current) => {
  if (action === OAuthNativeAction.Profiles) return { generation: "", profiles: [AccountOAuthProfile.OpenRouter, AccountOAuthProfile.HuggingFace, AccountOAuthProfile.GoogleGemini] };
    if (action === OAuthNativeAction.Begin || action === OAuthNativeAction.BeginHuggingFace || action === OAuthNativeAction.BeginGoogleGemini) return { generation, callback_url: `http://localhost:55451/oauth/openrouter/${"c".repeat(64)}` };
  if (action === OAuthNativeAction.BindOpen && ++binds === 1) throw new Error("native identity temporarily busy");
  return { generation: current };
 }});
 fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
 await screen.findByRole("alert");
 fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
 await waitFor(() => expect(binds).toBe(2));
 expect(f.start).toHaveBeenCalledTimes(2);
 expect(f.start.mock.calls[0][0].provider?.requestId).toBe(f.start.mock.calls[1][0].provider?.requestId);
 expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.Reopen)).toHaveLength(0);
 fireEvent.click(screen.getByRole("button", { name: "Open browser again" }));
 await waitFor(() => expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.Reopen)).toHaveLength(1));
 expect(f.complete).not.toHaveBeenCalled();
});

it("a definitive admission rejection allows explicit native disposal and manual fallback", async () => {
 const rejection=new ConnectError("provider changed",Code.Unimplemented,undefined,[{desc:ErrorDetailSchema,value:create(ErrorDetailSchema,{code:"unsupported",cause:"oauth_start_not_admitted"})}]);
 const f=fixture({startError:rejection});fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 await screen.findByText("Authorization was not started. Cancel or return to providers to refresh this provider, or use an API key instead.");
 fireEvent.click(screen.getByRole("button",{name:"Use an API key instead"}));
 await waitFor(()=>expect(f.manual).toHaveBeenCalledTimes(1));
 expect(f.cancel).not.toHaveBeenCalled();expect(f.complete).not.toHaveBeenCalled();
 expect(f.native.mock.calls.filter(call=>call[1]===OAuthNativeAction.Dispose).length).toBeGreaterThan(0);
});

it.each([Code.Unavailable,Code.Aborted,Code.Unimplemented])("an unproven Start failure %s retains its exact receipt and blocks fallback",async code=>{
 const f=fixture({startError:new ConnectError("unknown outcome",code)});
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));await screen.findByRole("alert");
 expect((screen.getByRole("button",{name:"Use an API key instead"}) as HTMLButtonElement).disabled).toBe(true);
 const original=f.start.mock.calls[0][0].provider?.requestId;
 fireEvent.click(screen.getByRole("button",{name:"Retry original start"}));
 await waitFor(()=>expect(f.start).toHaveBeenCalledTimes(2));
 expect(f.start.mock.calls[1][0].provider?.requestId).toBe(original);
 expect(f.native.mock.calls.filter(call=>call[1]===OAuthNativeAction.Begin)).toHaveLength(1);
 expect(f.cancel).not.toHaveBeenCalled();expect(f.manual).not.toHaveBeenCalled();
});

it("a changed-provider original Start returns interruption ownership for explicit cancellation",async()=>{
 const f=fixture({interruptedStart:true});
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 await screen.findByText("Authorization was interrupted");
 expect(f.native.mock.calls.filter(call=>call[1]===OAuthNativeAction.BindOpen)).toHaveLength(0);
 fireEvent.click(screen.getByRole("button",{name:"Use an API key instead"}));
 await waitFor(()=>expect(f.manual).toHaveBeenCalledTimes(1));
 expect(f.cancel).toHaveBeenCalledTimes(1);expect(f.complete).not.toHaveBeenCalled();expect(f.start).toHaveBeenCalledTimes(1);
});

it("uses the selected Hugging Face copy and forwards state outside caches", async () => {
  let observedState = "";
  const f = fixture({ huggingFace: true, complete: async request => { observedState = new TextDecoder().decode(request.authorizationState); } });
  await waitFor(() => expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Profiles)).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  await screen.findByRole("heading", { name: "Connect Hugging Face Inference Providers" });
  expect(screen.getByText("Approve access on Hugging Face. DeliDev will finish connecting automatically.")).toBeTruthy();
  await screen.findByText("Waiting for authorization…"); f.trigger();
  await screen.findByText("Hugging Face Inference Providers connected", {}, { timeout: 2500 });
  expect(observedState).toBe("s".repeat(43));
  expect(f.rawState.every(byte => byte === 0)).toBe(true);
  expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BeginHuggingFace)).toHaveLength(1);
  expect(f.client.getMutationCache().getAll()).toHaveLength(0);
});
it("does not start Hugging Face with an older native profile inventory", async () => {
  const f = fixture({ huggingFace: true, oldNative: true });
  await waitFor(() => expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Profiles)).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
  expect(f.start).not.toHaveBeenCalled();
  expect(screen.queryByRole("heading", { name: "Connect Hugging Face Inference Providers" })).toBeNull();
});

it("asks for the Google quota project before any browser or server Start", async () => {
 const f = fixture({ gemini: true });
 await waitFor(() => expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Profiles)).toBe(true));
 fireEvent.click(screen.getByRole("button", { name: "Connect selected OpenRouter" }));
 await screen.findByRole("heading", { name: "Connect Google Gemini" });
 expect(screen.getByText("Use the Google Cloud project that will pay for API usage.")).toBeTruthy();
 expect(f.start).not.toHaveBeenCalled();
 expect(f.native.mock.calls.every(call => call[1] === OAuthNativeAction.Profiles)).toBe(true);
 const project = screen.getByRole("textbox", { name: "Google Cloud project ID" });
 fireEvent.change(project, { target: { value: "BadProject" } });
 fireEvent.click(screen.getByRole("button", { name: "Continue in browser" }));
 expect(screen.getByRole("alert")).toBeTruthy(); expect(f.start).not.toHaveBeenCalled();
 fireEvent.change(project, { target: { value: "my-ai-project" } });
 fireEvent.click(screen.getByRole("button", { name: "Continue in browser" }));
 await screen.findByText("Waiting for authorization…");
 expect(f.start).toHaveBeenCalledWith(expect.objectContaining({ google: expect.objectContaining({ quotaProjectId: "my-ai-project" }) }), expect.anything());
 expect(f.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BeginGoogleGemini)).toHaveLength(1);
 f.trigger(); await screen.findByText("Google Gemini connected", {}, { timeout: 2500 });
});

it("observes server-owned Device approval without taking a callback and clears the temporary code", async () => {
 const f = fixture({ baseten:true });
 await waitFor(() => expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Profiles)).toBe(true));
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 expect(await screen.findByText("ABCD-EFGH")).toBeTruthy();
 expect(screen.getByLabelText("Temporary authorization code")).toBeTruthy();
 await waitFor(() => expect(f.status).toHaveBeenCalled(),{timeout:2000});
 expect(f.native.mock.calls.some(call => call[1] === OAuthNativeAction.Take)).toBe(false);
 expect(f.complete).not.toHaveBeenCalled();
 expect(f.start.mock.calls[0][0].callbackUrl).toBe("");
 f.change(State.ACCOUNT_OAUTH_STATE_CONNECTED,5n);
 await screen.findByText("Baseten connected",{},{timeout:2000});
 expect(screen.queryByText("ABCD-EFGH")).toBeNull();
 expect(f.client.getMutationCache().getAll()).toHaveLength(0);
 expect(JSON.stringify(f.client.getQueryCache().getAll())).not.toContain("ABCD-EFGH");
});

it("Device cancellation keeps its own receipt and waits before API-key fallback", async () => {
 const f=fixture({baseten:true});
 await waitFor(() => expect(f.native.mock.calls.some(call => call[1]===OAuthNativeAction.Profiles)).toBe(true));
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 await screen.findByText("ABCD-EFGH");
 fireEvent.click(screen.getByRole("button",{name:"Use an API key instead"}));
 await waitFor(() => expect(f.manual).toHaveBeenCalledTimes(1));
 expect(f.complete).not.toHaveBeenCalled();
 expect(f.cancel).toHaveBeenCalledTimes(1);
});
it("recovers the original protected Device receipt without a callback or poll dispatch", async () => {
 const f=fixture({baseten:true});
 await waitFor(() => expect(f.native.mock.calls.some(call => call[1]===OAuthNativeAction.Profiles)).toBe(true));
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 await screen.findByText("ABCD-EFGH");f.recoverDevice();
 fireEvent.click(await screen.findByRole("button",{name:"Recover saved result"},{timeout:2000}));
 await screen.findByText("Baseten connected");
 expect(f.complete).toHaveBeenCalledTimes(1);
 expect(f.complete.mock.calls[0][0].authorizationCode).toHaveLength(0);
 expect(f.complete.mock.calls[0][0].mutation?.expectedRevision).toBe(1n);
 expect(f.native.mock.calls.some(call => call[1]===OAuthNativeAction.Take)).toBe(false);
});

it("retains the Device approval code when native browser opening fails", async () => {
 const generation=newRequestId();
 const f=fixture({baseten:true,native:async (_opening,action) => {
  if(action===OAuthNativeAction.Profiles) return {generation:"",profiles:[AccountOAuthProfile.Baseten]};
  if(action===OAuthNativeAction.BindOpen) throw new Error("synthetic opener failure");
  return {generation};
 }});
 await waitFor(() => expect(f.native.mock.calls.some(call=>call[1]===OAuthNativeAction.Profiles)).toBe(true));
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 expect((await screen.findByRole("alert")).textContent).toContain("browser could not be opened");
 expect(screen.getByText("ABCD-EFGH")).toBeTruthy();
 expect(f.complete).not.toHaveBeenCalled();
});
it("retains an admitted attempt for cancellation when its new-provider flow is unsupported", async () => {
 const f=fixture({huggingFace:true,wrongFlow:true});
 await waitFor(() => expect(f.native.mock.calls.some(call=>call[1]===OAuthNativeAction.Profiles)).toBe(true));
 fireEvent.click(screen.getByRole("button",{name:"Connect selected OpenRouter"}));
 await screen.findByRole("alert");
 expect(f.native.mock.calls.some(call=>call[1]===OAuthNativeAction.BindOpen)).toBe(false);
 fireEvent.click(screen.getByRole("button",{name:"Use an API key instead"}));
 await waitFor(() => expect(f.manual).toHaveBeenCalledTimes(1));
 expect(f.cancel).toHaveBeenCalledTimes(1);
 expect(f.complete).not.toHaveBeenCalled();
});
