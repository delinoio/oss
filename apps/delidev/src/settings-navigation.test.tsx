// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountOAuthAttemptSchema, AccountOAuthState, AccountService, EntityKind, ProviderConnectionMethod, ProviderInventoryCapability, ProviderInventoryEntrySchema, ProviderPresetId, ProviderService, ResourceSchema, ResourceService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { OAuthNativeAction, OAuthNativeProvider, type OAuthNativeControl } from "./account-oauth";
import { encode } from "./documents";
import { Settings } from "./settings";

function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done; }); return { promise, resolve }; }
function fixture(delay?: "begin" | "start") {
  const pending = deferred(), providerId = newRequestId(), generation = newRequestId();
  const provider = create(ResourceSchema, { kind: EntityKind.PROVIDER, id: providerId, schemaVersion: 1, revision: 3n, documentJson: encode({ name: "OpenRouter", preset_id: "openrouter", endpoint: "https://openrouter.ai/api/v1", protocol: "openai-chat", authentication: "bearer", enabled: true }) });
  const entry = create(ProviderInventoryEntrySchema, { presetId: ProviderPresetId.OPENROUTER, providerId, displayName: "OpenRouter", enabled: true, provider, accountCountsAvailable: true, connectionMethod: ProviderConnectionMethod.OAUTH_PKCE });
  const attempt = create(AccountOAuthAttemptSchema, { id: newRequestId(), providerId, revision: 1n, state: AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION, expiresAt: new Date(Date.now() + 600000).toISOString() });
  const start = vi.fn(async request => { if (delay === "start") await pending.promise; return { attempt, requestId: request.provider?.requestId, authorizationUrl: "https://openrouter.ai/auth?fixture" }; });
  let callback = false, connected = false;
  const account = create(ResourceSchema, { kind: EntityKind.ACCOUNT, id: newRequestId(), schemaVersion: 1, revision: 2n, documentJson: encode({ alias: "Saved OAuth account", provider_id: providerId, type: "api", enabled: true, health: "unverified", connection: { id: newRequestId(), authentication: "api-key" } }) });
  const cancel = vi.fn(async () => ({})), complete = vi.fn(async request => {
    connected = true;
    return { attempt: { ...attempt, revision: 2n, state: AccountOAuthState.ACCOUNT_OAUTH_STATE_CONNECTED }, account, requestId: request.mutation?.requestId };
  });
  const list = vi.fn(() => ({ resources: connected ? [account] : [] }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({}) });
    router.service(ResourceService, { listResources: list, getResource: () => ({ resource: provider }) });
    router.service(ProviderService, { listProviderPresets: () => ({ presetsJson: encode([]) }), listProviderInventory: () => ({ entries: [entry], capabilities: [ProviderInventoryCapability.PROVIDER_ACTIVATION, ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER, ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER, ProviderInventoryCapability.ACCOUNT_TYPE_FILTER, ProviderInventoryCapability.OPENROUTER_OAUTH_PKCE_V1] }) });
    router.service(AccountService, { startAccountOAuth: start, getAccountOAuthStatus: () => ({ attempt }), cancelAccountOAuth: cancel, completeAccountOAuth: complete });
  });
  const native = vi.fn<OAuthNativeControl>(async (_opening, action, original) => {
    if (action === OAuthNativeAction.Begin) { if (delay === "begin") await pending.promise; return { generation, callback_url: `http://localhost:55451/oauth/openrouter/${"a".repeat(64)}` }; }
    if (action === OAuthNativeAction.Take && callback) { callback = false; return { generation: original, code: Array.from(new TextEncoder().encode("fixture-code")) }; }
    return { generation: original };
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><OAuthNativeProvider control={native}><Settings /></OAuthNativeProvider></QueryClientProvider></TransportProvider></StrictMode>);
  return { pending, start, cancel, complete, native, list, trigger: () => { callback = true; } };
}
async function add() {
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add AI API key" }));
  expect(screen.getByRole("heading", { level: 1, name: "AI API Keys", hidden: true })).toBeTruthy();
  await waitFor(() => expect((screen.getByRole("button", { name: "Continue in browser" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Continue in browser" }));
}
it("starts a provider entry once after mounting its destination under Strict Mode and disposes only local callback authority", async () => {
  const value = fixture();
  expect(value.native.mock.calls.every(call => call[1] === OAuthNativeAction.Profiles)).toBe(true); expect(value.start).not.toHaveBeenCalled();
  await add(); await screen.findByText("Waiting for authorization…");
  await waitFor(() => expect(value.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BindOpen)).toHaveLength(1));
  expect(value.start).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  expect(screen.getByText("Waiting for authorization…")).toBeTruthy(); expect(value.start).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Projects" }));
  await waitFor(() => expect(value.native.mock.calls.some(call => call[1] === OAuthNativeAction.Dispose)).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  await screen.findByRole("button", { name: "Add AI API key" });
  expect(screen.queryByRole("heading", { name: "Connect OpenRouter" })).toBeNull();
  expect(value.start).toHaveBeenCalledTimes(1); expect(value.cancel).not.toHaveBeenCalled(); expect(value.complete).not.toHaveBeenCalled();
});
it.each(["begin", "start"] as const)("ignores a late OAuth %s after leaving the destination without opening a browser or canceling business work", async delay => {
  const value = fixture(delay); await add();
  await waitFor(() => expect(delay === "begin" ? value.native : value.start).toHaveBeenCalled());
  const projects = screen.getByRole("button", { name: "Projects" }); fireEvent.click(projects); projects.focus();
  await act(async () => value.pending.resolve());
  expect(screen.getByRole("heading", { level: 1, name: "Projects" })).toBeTruthy(); expect(document.activeElement).toBe(projects);
  expect(value.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BindOpen)).toHaveLength(0);
  expect(value.start).toHaveBeenCalledTimes(delay === "begin" ? 0 : 1); expect(value.cancel).not.toHaveBeenCalled(); expect(value.complete).not.toHaveBeenCalled();
});

it("closes live OAuth and disposes only its local callback owner without business cancellation", async () => {
  const value = fixture(); await add(); await screen.findByText("Waiting for authorization…");
  await waitFor(() => expect(value.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BindOpen)).toHaveLength(1));
  const disposed = value.native.mock.calls.filter(call => call[1] === OAuthNativeAction.Dispose).length;
  fireEvent.click(screen.getByRole("button", { name: "Close Add AI API key" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(value.cancel).not.toHaveBeenCalled();
  await waitFor(() => expect(value.native.mock.calls.filter(call => call[1] === OAuthNativeAction.Dispose)).toHaveLength(disposed + 1));
  expect(value.start).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Add AI API key" }).matches(":disabled")).toBe(false);
});

it.each(["begin", "start"] as const)("fences a closed OAuth %s from a fresh task in the same category", async delay => {
  const value = fixture(delay); await add();
  await waitFor(() => expect(delay === "begin" ? value.native.mock.calls.some(call => call[1] === OAuthNativeAction.Begin) : value.start.mock.calls.length > 0).toBe(true));
  fireEvent.click(screen.getByRole("button", { name: "Close Add AI API key" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Add AI API key" }).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  const fresh = screen.getByRole("dialog");
  await act(async () => value.pending.resolve());
  expect(screen.getByRole("dialog")).toBe(fresh);
  expect(screen.queryByText("Waiting for authorization…")).toBeNull();
  expect(value.native.mock.calls.filter(call => call[1] === OAuthNativeAction.BindOpen)).toHaveLength(0);
  expect(value.start).toHaveBeenCalledTimes(delay === "begin" ? 0 : 1);
  expect(value.cancel).not.toHaveBeenCalled(); expect(value.complete).not.toHaveBeenCalled();
});

it("automatically closes confirmed OAuth, refreshes the list and restores the available Add button", async () => {
  const value = fixture(); await add();
  await screen.findByText("Waiting for authorization…");
  const reads = value.list.mock.calls.length;
  value.trigger();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), { timeout: 2500 });
  expect(await screen.findByText("Saved OAuth account")).toBeTruthy();
  expect(value.list.mock.calls.length).toBeGreaterThan(reads);
  expect(value.complete).toHaveBeenCalledTimes(1); expect(value.start).toHaveBeenCalledTimes(1);
  expect(value.cancel).not.toHaveBeenCalled();
  expect(value.native.mock.calls.some(call => call[1] === OAuthNativeAction.Dispose)).toBe(true);
  // The task uses the current category toolbar when its provider opener departs.
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Add AI API key" })));
});
