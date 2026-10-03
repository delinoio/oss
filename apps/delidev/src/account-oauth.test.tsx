// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, AccountOAuthAttemptSchema, AccountOAuthState as State, EntityKind, ResourceSchema, newRequestId, type CompleteAccountOAuthRequest } from "@delinoio/delidev-api-client";
import { OpenRouterOAuth, OAuthNativeAction, OAuthNativeProvider, useOpenRouterOAuth, type OAuthNativeControl, type OAuthNativeResult } from "./account-oauth";
import { SettingsLifetime } from "./settings-lifetime";
import type { AccountProviderSummary } from "./account-settings";
import { encode } from "./documents";

function fixture(args: { complete?: (request: CompleteAccountOAuthRequest) => Promise<void>; native?: OAuthNativeControl; startDelay?: Promise<void> } = {}) {
  const providerId = newRequestId(), attemptId = newRequestId(), nativeGeneration = newRequestId();
  const provider = create(ResourceSchema, { kind: EntityKind.PROVIDER, id: providerId, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "OpenRouter", preset_id: "openrouter", endpoint: "https://openrouter.ai/api/v1", protocol: "openai-chat", authentication: "bearer", enabled: true }) });
  const selected: AccountProviderSummary = { providerId, provider, displayName: "OpenRouter", enabled: true, oauthAvailable: true, keyGuidance: "", documentationUrl: "" };
  const attempt = (state = State.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION, revision = 1n) => create(AccountOAuthAttemptSchema, { id: attemptId, providerId, revision, state, expiresAt: new Date(Date.now() + 600000).toISOString() });
  let retained = attempt(), callback = false;
  const rawCode = Array.from(new TextEncoder().encode("renderer-oauth-code-sentinel"));
  const account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 2n, documentJson: encode({ alias: "OpenRouter", provider_id: providerId, type: "api", health: "unverified", enabled: true, recovery_notifications: true }) });
  const start = vi.fn(async (request) => { await args.startDelay; return { attempt: retained, requestId: request.provider?.requestId, authorizationUrl: "https://openrouter.ai/auth?fixture-live-start" }; });
  const complete = vi.fn(async (request: CompleteAccountOAuthRequest) => { if (args.complete) await args.complete(request); retained = attempt(State.ACCOUNT_OAUTH_STATE_CONNECTED, 5n); return { attempt: retained, account, requestId: request.mutation?.requestId }; });
  const cancel = vi.fn(async (request) => { retained = attempt(State.ACCOUNT_OAUTH_STATE_CANCELED, 2n); return { attempt: retained, requestId: request.mutation?.requestId }; });
  const status = vi.fn(async () => ({ attempt: retained, account: retained.state === State.ACCOUNT_OAUTH_STATE_CONNECTED ? account : undefined }));
  const transport = createRouterTransport(router => router.service(AccountService, { startAccountOAuth: start, completeAccountOAuth: complete, cancelAccountOAuth: cancel, getAccountOAuthStatus: status }));
  const native = vi.fn<OAuthNativeControl>(args.native ?? (async (_opening, action, generation): Promise<OAuthNativeResult> => {
    if (action === OAuthNativeAction.Begin) return { generation: nativeGeneration, callback_url: `http://localhost:55451/oauth/openrouter/${"a".repeat(64)}` };
    if (action === OAuthNativeAction.Take && callback) { callback = false; return { generation, code: rawCode }; }
    return { generation };
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const back = vi.fn(), manual = vi.fn(), edit = vi.fn(), manage = vi.fn(), done = vi.fn();
  function Harness() {
    const flow = useOpenRouterOAuth();
    return <><button onClick={() => flow.start(selected)}>Connect selected OpenRouter</button><OpenRouterOAuth flow={flow} back={back} manual={manual} edit={edit} manage={manage} done={done} /></>;
  }
  const view = render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><OAuthNativeProvider control={native}><SettingsLifetime>{() => <Harness />}</SettingsLifetime></OAuthNativeProvider></QueryClientProvider></TransportProvider></StrictMode>);
  return { start, complete, cancel, status, native, client, view, rawCode, manual, back, edit, account, trigger: () => { callback = true; }, change: (state: State, revision: bigint) => { retained = attempt(state, revision); } };
}

it("starts and opens exactly once on deliberate action under Strict Mode, with no mount authentication", async () => {
  const f = fixture();
  expect(f.start).not.toHaveBeenCalled(); expect(f.native).not.toHaveBeenCalled();
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
