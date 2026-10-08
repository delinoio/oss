// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  BrowserService, ConfigurationService, EntityKind, ErrorDetailSchema, GetSubscriptionProgressResponseSchema,
  ResourceSchema, ResourceService, SubscriptionAction, SubscriptionLoginState, SubscriptionService, SystemCapability, SystemService,
  newRequestId, type DeleteConfigurationRequest, type GetAccountBrowserCleanupRequest, type GetSubscriptionProgressRequest, type RequestSubscriptionRequest,
} from "@delinoio/delidev-api-client";
import { ConfigurationDeletion } from "./configuration-actions";
import { document, encode, object } from "./documents";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { Settings } from "./settings";
import { SettingsDialogSize, SettingsTaskDialog } from "./settings-task";

const alias = "ChatGPT fixture";
const confirmLabel = "Disconnect and delete account";
function fixture(connected = true, service: "chatgpt" | "claude" = "chatgpt", failedLogin = false) {
  const machine = newRequestId(), profile = newRequestId(), operationKey = service === "claude" ? "native_operation" : "server_operation";
  const generation = newRequestId(), connection = newRequestId();
  const preferences = { alias, type: "subscription", subscription_service: service, enabled: true, exclude_automatic: false, recovery_notifications: false };
  let current = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 9007199254740993n,
    documentJson: encode({ ...preferences, health: failedLogin ? "failed" : connected ? "ready" : "disconnected", quota: [], ...(failedLogin ? { subscription: { recovery_required: true, server_operation: { id: newRequestId(), action: "login", state: "recovery-required", native_started: false } } } : {}), ...(connected ? { connection: { id: connection }, subscription: { generation, ...(service === "claude" ? { owner_machine_id: machine, native_profile_id: profile } : {}) } } : {}) }) });
  const initial = current;
  let removed = false;
  const patch = (change: Record<string, unknown>) => { current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: encode({ ...document(current), ...change }) }); return current; };
  const accept = (request: RequestSubscriptionRequest) => patch({ health: "revoked", subscription: { ...object(document(current).subscription), pending: { id: request.mutation!.requestId, action: "logout", phase: "queued", ...(service === "claude" ? {machine_id: machine} : {}) }, [operationKey]: { id: request.mutation!.requestId, action: "logout", state: "preparing", native_started: false } } });
  const read = vi.fn(async () => ({ resource: current }));
  const status = vi.fn(async () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1, SystemCapability.SERVER_SUBSCRIPTION_LOGIN_V1, SystemCapability.CLAUDE_SUBSCRIPTIONS_V1] }));
  const logout = vi.fn(async (request: RequestSubscriptionRequest) => ({ operationId: request.mutation!.requestId, account: accept(request) }));
  const progress = vi.fn(async (_request: GetSubscriptionProgressRequest) => create(GetSubscriptionProgressResponseSchema, { state: SubscriptionLoginState.PREPARING }));
  const remove = vi.fn(async (request: DeleteConfigurationRequest) => { removed = true; return { id: request.mutation!.id, requestId: request.mutation!.requestId }; });
  const cleanup = vi.fn(async (_request: GetAccountBrowserCleanupRequest) => ({ pending: 2, removed: 1 }));
  const list = vi.fn(async () => ({ resources: removed ? [] : [current] }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { getResource: read, listResources: list });
    router.service(SystemService, { getStatus: status });
    router.service(SubscriptionService, { requestSubscription: logout, getSubscriptionProgress: progress });
    router.service(ConfigurationService, { deleteConfiguration: remove });
    router.service(BrowserService, { getAccountBrowserCleanup: cleanup });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const closed = vi.fn(), deleted = vi.fn();
  function Harness({ settings = false, active = true, task = false }: { settings?: boolean; active?: boolean; task?: boolean }) {
    const [visible, setVisible] = useState(true);
    const content = <ConfigurationDeletion initial={initial} active={active} deleted={() => { deleted(); setVisible(false); }} close={() => { closed(); setVisible(false); }} />;
    return <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}>
      <button onClick={() => setVisible(false)}>Leave fixture</button><button onClick={() => setVisible(true)}>Reopen fixture</button>
      {settings ? <Settings visible={visible && active} /> : visible ? <SettingsLifetime>{() => <MutationIntents>{task ? <SettingsTaskDialog title="Delete configuration" size={SettingsDialogSize.Confirmation} close={() => { closed(); setVisible(false); }}>{content}</SettingsTaskDialog> : content}</MutationIntents>}</SettingsLifetime> : null}
    </QueryClientProvider></TransportProvider></StrictMode>;
  }
  const complete = () => {
    const operation = object(object(document(current).subscription)[operationKey]);
    patch({ health: "disconnected", connection: undefined, subscription: { [operationKey]: { ...operation, state: "succeeded", native_started: false } } });
    progress.mockResolvedValue(create(GetSubscriptionProgressResponseSchema, { state: SubscriptionLoginState.SUCCEEDED }));
  };
  return { Harness, client, initial, machine, profile, read, list, status, logout, progress, remove, cleanup, closed, deleted, patch, accept, complete, get current() { return current; } };
}
async function start(value: ReturnType<typeof fixture>) {
  render(<value.Harness />);
  expect(value.logout).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.progress).toHaveBeenCalledTimes(1));
}
async function tick() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 2100)); });
}
async function openSettingsDeletion(value: ReturnType<typeof fixture>) {
  render(<value.Harness settings />);
  await screen.findByRole("article", { name: alias });
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
}
function dismissTask(method: "X" | "Escape") {
  if (method === "X") fireEvent.click(screen.getByRole("button", { name: "Close Delete configuration" }));
  else fireEvent(screen.getByRole("dialog"), new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
}

it.each([
  ["Delete account", "X"], ["Delete account", "Escape"],
  ["Edit preferences", "X"], ["Edit preferences", "Escape"],
] as const)("keeps subscription content and disclosures visible beneath %s through %s dismissal", async (action, dismissal) => {
  const value = fixture();
  render(<value.Harness settings />);
  const inventory = await screen.findByRole("article", { name: alias });
  const opener = within(inventory).getByRole("button", { name: `More actions for ${alias}` });
  fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  expect(screen.getByRole("dialog", { name: "Account details" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close Account details" }));
  const advanced = screen.getByText("Advanced settings").closest("details")!;
  fireEvent.click(advanced.querySelector("summary")!);
  expect(advanced.open).toBe(true);
  fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: action }));
  const dialog = screen.getByRole("dialog");
  const background = inventory.closest("fieldset")!;
  expect(inventory.isConnected).toBe(true);
  expect(inventory.closest("[hidden]")).toBeNull();
  expect(screen.queryByRole("dialog", { name: "Account details" })).toBeNull();
  expect(advanced.open).toBe(true);
  expect(background.disabled).toBe(true);
  expect(background.hasAttribute("inert")).toBe(true);
  expect(background.getAttribute("aria-hidden")).toBe("true");
  expect(dialog.closest("fieldset")).toBeNull();
  if (dismissal === "X") fireEvent.click(within(dialog).getByRole("button", { name: /^Close / }));
  else if (dismissal === "Escape") fireEvent(dialog, new Event("cancel", { cancelable: true }));
  else fireEvent.click(within(dialog).getByRole("button", { name: action === "Delete account" ? "Keep account" : "Cancel edit" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(screen.getByRole("article", { name: alias })).toBe(inventory);
  expect(within(inventory).queryByRole("heading", { name: "Account details" })).toBeNull();
  expect(advanced.open).toBe(true);
  await waitFor(() => expect(globalThis.document.activeElement).toBe(opener));
  expect(background.disabled).toBe(false);
  expect(background.hasAttribute("inert")).toBe(false);
  expect(value.logout).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it.each(["X", "Escape"] as const)("disposes deferred logout after %s without starting follow-up deletion", async method => {
  const value = fixture(); let release!: () => void;
  value.logout.mockImplementationOnce(async request => {
    value.accept(request);
    await new Promise<void>(resolve => { release = resolve; });
    throw new ConnectError("lost logout acknowledgment", Code.Unavailable);
  });
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.logout).toHaveBeenCalledTimes(1));
  dismissTask(method);
  const destination = screen.getByRole("button", { name: "Reopen fixture" }); destination.focus();
  await act(async () => release());
  value.complete();
  await tick();
  expect(value.logout).toHaveBeenCalledTimes(1);
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.progress).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry original logout request" })).toBeNull();
  expect(globalThis.document.activeElement).toBe(destination);
  // A new explicit deletion observes the cleared account, without replaying logout.
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  // The category snapshot still has the original revision. Preserve the stale
  // confirmation guard and explicitly confirm the newly read server state.
  fireEvent.click(await screen.findByRole("button", { name: "Refresh account for confirmation" }));
  fireEvent.click(await screen.findByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1));
  expect(value.logout).toHaveBeenCalledTimes(1);
  expect(value.remove.mock.calls[0][0].mutation!.expectedRevision).toBe(value.current.revision);
});

it.each(["X", "Escape"] as const)("discards an uncertain deletion after %s and requires fresh confirmation", async method => {
  const value = fixture(false); let release!: () => void;
  value.remove.mockImplementationOnce(async () => {
    await new Promise<void>(resolve => { release = resolve; });
    throw new ConnectError("lost deletion acknowledgment", Code.Unavailable);
  });
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1));
  const original = value.remove.mock.calls[0][0];
  dismissTask(method);
  await act(async () => release());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  expect(screen.queryByRole("button", { name: "Retry the same deletion" })).toBeNull();
  expect(value.remove).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[1][0].mutation!.requestId).not.toBe(original.mutation!.requestId);
  expect(value.logout).not.toHaveBeenCalled();
});

it.each(["X", "Escape"] as const)("ignores late deletion acknowledgment after %s without cleanup reads or taking focus", async method => {
  const value = fixture(false); let release!: () => void;
  value.remove.mockImplementationOnce(async request => {
    await new Promise<void>(resolve => { release = resolve; });
    return { id: request.mutation!.id, requestId: request.mutation!.requestId };
  });
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1));
  dismissTask(method);
  const destination = screen.getByRole("button", { name: "Reopen fixture" });
  destination.focus();
  await act(async () => release());
  await waitFor(() => expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(globalThis.document.activeElement).toBe(destination);
  expect(value.remove).toHaveBeenCalledTimes(1);
  expect(value.cleanup).not.toHaveBeenCalled();
  expect(screen.getByRole("heading", { name: "AI Subscription" })).toBeTruthy();
});

it("closes confirmed deletion, refreshes the inventory and restores category focus", async () => {
  const value = fixture(false);
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await screen.findByRole("heading", { name: "No subscriptions yet" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("article", { name: alias })).toBeNull();
  await waitFor(() => expect(globalThis.document.activeElement).toBe(screen.getByRole("heading", { name: "AI Subscription" })));
  expect(value.remove).toHaveBeenCalledTimes(1);
  expect(value.cleanup).not.toHaveBeenCalled();
});

it("closes before a delayed inventory refresh and never reopens or repeats deletion when that read fails", async () => {
  const value = fixture(false); let release!: () => void;
  await openSettingsDeletion(value);
  value.list.mockImplementationOnce(async () => {
    await new Promise<void>(resolve => { release = resolve; });
    throw new ConnectError("Inventory refresh unavailable", Code.Unavailable);
  });
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(release).toBeTypeOf("function"));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(globalThis.document.activeElement).toBe(screen.getByRole("heading", { name: "AI Subscription" })));
  expect(value.remove).toHaveBeenCalledTimes(1);
  await act(async () => release());
  await screen.findByText("Unable to read subscriptions or server capabilities. Try again.");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(value.remove).toHaveBeenCalledTimes(1);
  expect(value.cleanup).not.toHaveBeenCalled();
});

it.each(["category", "Settings"])("ignores a late deletion acknowledgment after %s departure", async departure => {
  const value = fixture(false); let release!: () => void;
  value.remove.mockImplementationOnce(async request => {
    await new Promise<void>(resolve => { release = resolve; });
    return { id: request.mutation!.id, requestId: request.mutation!.requestId };
  });
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1));
  dismissTask("Escape");
  const destination = screen.getByRole("button", { name: departure === "category" ? "Appearance" : "Leave fixture" });
  fireEvent.click(destination); destination.focus();
  await act(async () => release());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(globalThis.document.activeElement).toBe(destination);
  expect(value.cleanup).not.toHaveBeenCalled();
  expect(value.remove).toHaveBeenCalledTimes(1);
});

it.each(["X", "Escape"] as const)("discards an idle deletion confirmation on %s", async method => {
  const value = fixture(); await openSettingsDeletion(value);
  dismissTask(method);
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  expect(screen.getByRole("button", { name: confirmLabel })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /^Close / }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(value.logout).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});

it("preserves explicit Back abandonment after a definite idle failure", async () => {
  const value = fixture(false); value.remove.mockRejectedValue(new ConnectError("referenced", Code.Aborted));
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  fireEvent.click(await screen.findByRole("button", { name: /^Close / }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(value.remove).toHaveBeenCalledTimes(1);
});

it.each(["category", "Settings"])("disposes hidden deletion follow-up authority on %s departure", async departure => {
  const value = fixture(); let release!: () => void;
  value.logout.mockImplementationOnce(async request => {
    value.accept(request);
    await new Promise<void>(resolve => { release = resolve; });
    return { operationId: request.mutation!.requestId, account: value.current };
  });
  await openSettingsDeletion(value);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.logout).toHaveBeenCalledTimes(1));
  dismissTask("Escape");
  fireEvent.click(screen.getByRole("button", { name: departure === "category" ? "Appearance" : "Leave fixture" }));
  value.complete(); await act(async () => release());
  expect(value.progress).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: departure === "category" ? "AI Subscription" : "Reopen fixture" }));
  await screen.findByRole("article", { name: alias });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(value.logout).toHaveBeenCalledTimes(1);
});

it("logs out once and deletes only after the original cleanup succeeds, preserving bigint revisions and independent browser cleanup", async () => {
  const value = fixture(); render(<value.Harness />);
  expect(value.read).not.toHaveBeenCalled(); expect(value.logout).not.toHaveBeenCalled();
  const confirmation = screen.getByRole("button", { name: confirmLabel });
  act(() => { fireEvent.click(confirmation); fireEvent.click(confirmation); });
  await waitFor(() => expect(value.progress).toHaveBeenCalledTimes(1));
  expect(value.remove).not.toHaveBeenCalled(); expect(value.cleanup).not.toHaveBeenCalled();
  expect(value.logout).toHaveBeenCalledTimes(1);
  expect(value.logout.mock.calls[0][0]).toMatchObject({ machineId: "", action: SubscriptionAction.LOGOUT, mutation: { id: value.initial.id, expectedRevision: 9007199254740993n } });
  value.complete(); await tick();
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.remove).toHaveBeenCalledTimes(1);
  expect(value.remove.mock.calls[0][0]).toMatchObject({ mutation: { id: value.initial.id, expectedRevision: value.current.revision } });
  expect(value.remove.mock.calls[0][0].mutation!.requestId).not.toBe(value.logout.mock.calls[0][0].mutation!.requestId);
  expect(value.cleanup).not.toHaveBeenCalled();
  expect(screen.queryByRole("heading", { name: "Account configuration deleted" })).toBeNull();
});

it("deletes a confirmed disconnected account without native capability or logout", async () => {
  const value = fixture(false); value.status.mockResolvedValue({ capabilities: [] }); render(<value.Harness />);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.logout).not.toHaveBeenCalled(); expect(value.progress).not.toHaveBeenCalled(); expect(value.status).not.toHaveBeenCalled();
});

it("observes an existing server logout without submitting another request", async () => {
  const value = fixture(), operation = newRequestId();
  value.initial.documentJson = encode({ ...document(value.initial), subscription: { generation: newRequestId(), pending: { id: operation, action: "logout" }, server_operation: { id: operation, action: "logout", state: "preparing" } } });
  await start(value); expect(value.logout).not.toHaveBeenCalled(); expect(value.progress.mock.calls[0][0].operationId).toBe(operation);
  value.complete(); await tick(); await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
});

it("waits for the original execution lease before deleting", async () => {
  const value = fixture(); value.initial.documentJson = encode({ ...document(value.initial), subscription: { ...object(document(value.initial).subscription), lease: { action: "execute", id: newRequestId() } } });
  await start(value); expect(value.remove).not.toHaveBeenCalled(); expect(value.logout).toHaveBeenCalledTimes(1);
  value.complete(); await tick(); await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
});

it.each([SubscriptionLoginState.FAILED, SubscriptionLoginState.RECOVERY_REQUIRED, SubscriptionLoginState.UNSUPPORTED, SubscriptionLoginState.EXPIRED, SubscriptionLoginState.CANCELED, SubscriptionLoginState.UNSPECIFIED])("keeps the account after terminal logout state %s", async (state) => {
  const value = fixture(); value.progress.mockResolvedValue(create(GetSubscriptionProgressResponseSchema, { state }));
  await start(value); await screen.findByText("Account deletion paused.");
  expect(value.remove).not.toHaveBeenCalled(); expect(value.logout).toHaveBeenCalledTimes(1);
});

it.each(["connection", "generation", "pending", "lease", "removal", "recovery", "native", "operation", "preferences"])("rejects succeeded progress with remaining or changed %s ownership", async (field) => {
  const value = fixture(); await start(value); value.complete();
  const state = object(document(value.current).subscription);
  const change = {
    connection: { connection: { id: newRequestId() } }, generation: { subscription: { ...state, generation: newRequestId() } },
    pending: { subscription: { ...state, pending: { id: newRequestId() } } }, lease: { subscription: { ...state, lease: { id: newRequestId() } } },
    removal: { removal: { request_id: newRequestId() } }, recovery: { subscription: { ...state, recovery_required: true } },
    native: { subscription: { ...state, server_operation: { ...object(state.server_operation), native_started: true } } },
    operation: { subscription: { ...state, server_operation: { ...object(state.server_operation), id: newRequestId() } } },
    preferences: { alias: "Changed preference" },
  }[field]!;
  value.patch(change); await tick();
  expect(screen.getByText("Account deletion paused.")).toBeTruthy(); expect(value.remove).not.toHaveBeenCalled();
});

it("retries an accepted logout with the original request after response loss", async () => {
  const value = fixture();
  value.logout.mockImplementationOnce(async (request) => { value.accept(request); throw new ConnectError("lost response", Code.Unavailable); });
  value.logout.mockImplementationOnce(async (request) => ({ operationId: request.mutation!.requestId, account: value.current }));
  render(<value.Harness />); fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original logout request" }));
  await waitFor(() => expect(value.progress).toHaveBeenCalledTimes(1));
  expect(value.logout.mock.calls[0][0]).toEqual(value.logout.mock.calls[1][0]);
  value.complete(); await tick(); await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
});

it("retries only the original deletion after its response is lost", async () => {
  const value = fixture(); value.remove.mockRejectedValueOnce(new ConnectError("lost deletion", Code.Unavailable));
  await start(value); value.complete(); await tick();
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.remove.mock.calls[0][0]).toEqual(value.remove.mock.calls[1][0]); expect(value.logout).toHaveBeenCalledTimes(1);
});

it("retries a failed progress read without resubmitting logout", async () => {
  const value = fixture(); value.progress.mockRejectedValueOnce(new ConnectError("lost status", Code.Unavailable));
  await start(value); value.complete();
  fireEvent.click(await screen.findByRole("button", { name: "Retry original status check" }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1)); expect(value.logout).toHaveBeenCalledTimes(1);
});

it.each([Code.PermissionDenied, Code.Unauthenticated])("does not delete or retry effects after authorization failure %s", async (code) => {
  const value = fixture(); value.progress.mockRejectedValue(new ConnectError("private failure", code));
  await start(value); await screen.findByText("Account deletion paused.");
  expect(screen.queryByRole("button", { name: "Retry original status check" })).toBeNull(); expect(value.remove).not.toHaveBeenCalled();
});

it("preserves a definite server reference conflict and requires new confirmation", async () => {
  const value = fixture(); value.remove.mockRejectedValue(new ConnectError("This configuration is still referenced.", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: { code: "conflict", guidance: "Reconfigure its dependents before deleting it." } }]));
  await start(value); value.complete(); await tick();
  await screen.findAllByText("This configuration is still referenced.");
  expect(screen.queryByRole("button", { name: "Retry the same deletion" })).toBeNull(); expect(value.remove).toHaveBeenCalledTimes(1);
});

it.each(["capability", "recovery", "pending", "revision"])("does not start logout when initial %s validation fails", async (field) => {
  const value = fixture();
  if (field === "capability") value.status.mockResolvedValue({ capabilities: [] });
  if (field === "recovery") value.initial.documentJson = encode({ ...document(value.initial), subscription: { recovery_required: true } });
  if (field === "pending") value.initial.documentJson = encode({ ...document(value.initial), subscription: { pending: { id: newRequestId(), action: "login" } } });
  if (field === "revision") value.patch({ alias: "New alias" });
  render(<value.Harness />); fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await screen.findByText("Account deletion paused."); expect(value.logout).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});

it("fresh inspection requires an explicit new confirmation", async () => {
  const value = fixture(false); value.patch({ alias: "New alias" }); render(<value.Harness />);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  fireEvent.click(await screen.findByRole("button", { name: "Refresh account for confirmation" }));
  await screen.findByRole("heading", { name: "Delete New alias?" }); expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: confirmLabel })); await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
});

it("ignores a late accepted logout after the screen is left and reopened", async () => {
  const value = fixture(); let release!: () => void;
  value.logout.mockImplementationOnce(async (request) => { value.accept(request); await new Promise<void>((resolve) => { release = resolve; }); return { operationId: request.mutation!.requestId, account: value.current }; });
  render(<value.Harness />); fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.logout).toHaveBeenCalledTimes(1)); fireEvent.click(screen.getByRole("button", { name: "Back to subscriptions" }));
  value.complete(); fireEvent.click(screen.getByRole("button", { name: "Reopen fixture" })); await act(async () => release());
  expect(value.remove).not.toHaveBeenCalled(); expect(value.progress).not.toHaveBeenCalled(); expect(screen.getByRole("button", { name: confirmLabel })).toBeTruthy();
});

it("prevents deletion after category departure even if a pending status read later succeeds", async () => {
  const value = fixture(); let release!: () => void;
  value.progress.mockImplementationOnce(async () => { await new Promise<void>((resolve) => { release = resolve; }); return create(GetSubscriptionProgressResponseSchema, { state: SubscriptionLoginState.SUCCEEDED }); });
  render(<value.Harness settings />); await screen.findByRole("article", { name: alias });
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${alias}` })); fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  fireEvent.click(screen.getByRole("button", { name: confirmLabel })); await waitFor(() => expect(value.progress).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Appearance" })); value.complete(); await act(async () => release());
  expect(value.remove).not.toHaveBeenCalled(); expect(screen.queryByRole("heading", { name: "Account configuration deleted" })).toBeNull();
});

it("never overlaps original progress reads while a server response is delayed", async () => {
  const value = fixture(); let release!: () => void;
  value.progress.mockImplementationOnce(async () => { await new Promise<void>((resolve) => { release = resolve; }); return create(GetSubscriptionProgressResponseSchema, { state: SubscriptionLoginState.PREPARING }); });
  await start(value); await tick(); expect(value.progress).toHaveBeenCalledTimes(1);
  await act(async () => release()); value.complete(); await tick();
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.progress).toHaveBeenCalledTimes(2); expect(value.remove).toHaveBeenCalledTimes(1);
});

it("loses follow-up authority when inactive even if a fresh cleared account arrives later", async () => {
  const value = fixture(); let release!: () => void;
  const rendered = render(<value.Harness />);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.progress).toHaveBeenCalledTimes(1));
  value.complete();
  value.read.mockImplementationOnce(async () => { await new Promise<void>((resolve) => { release = resolve; }); return { resource: value.current }; });
  await tick(); expect(value.read).toHaveBeenCalledTimes(2);
  rendered.rerender(<value.Harness active={false} />); await act(async () => release());
  rendered.rerender(<value.Harness />);
  expect(value.remove).not.toHaveBeenCalled(); expect(value.logout).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("status").textContent).toBe("Account deletion paused.");
});

it("retains the original logout request when its acknowledgment names another operation", async () => {
  const value = fixture();
  value.logout.mockImplementationOnce(async (request) => ({ operationId: newRequestId(), account: value.accept(request) }));
  value.logout.mockImplementationOnce(async (request) => ({ operationId: request.mutation!.requestId, account: value.current }));
  render(<value.Harness />); fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  const retry = await screen.findByRole("button", { name: "Retry original logout request" });
  expect(value.progress).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
  value.complete(); fireEvent.click(retry);
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.logout.mock.calls[0][0]).toEqual(value.logout.mock.calls[1][0]);
});

it("retains the original deletion request when its acknowledgment has another receipt", async () => {
  const value = fixture(false); value.remove.mockImplementationOnce(async (request) => ({ id: request.mutation!.id, requestId: newRequestId() }));
  render(<value.Harness />); fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.remove.mock.calls[0][0]).toEqual(value.remove.mock.calls[1][0]);
});

it("keeps the account when delete permission is revoked after cleanup", async () => {
  const value = fixture(); value.remove.mockRejectedValue(new ConnectError("revoked", Code.PermissionDenied));
  await start(value); value.complete(); await tick();
  await screen.findByText("Account deletion paused.");
  expect(screen.queryByRole("button", { name: "Retry the same deletion" })).toBeNull();
  expect(value.remove).toHaveBeenCalledTimes(1); expect(value.cleanup).not.toHaveBeenCalled();
});

it("logs Claude out on its original Runner before deleting the fresh cleared account", async () => {
  const value = fixture(true, "claude");
  await start(value);
  expect(value.logout.mock.calls[0][0].machineId).toBe(value.machine);
  expect(value.remove).not.toHaveBeenCalled();
  value.complete();
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1), { timeout: 4000 });
  expect(value.remove.mock.calls[0][0].mutation!.expectedRevision).toBe(value.current.revision);
  expect(value.cleanup).not.toHaveBeenCalled();
});

it("keeps Claude configuration while the original Runner cleanup is uncertain", async () => {
  const value = fixture(true, "claude");
  await start(value);
  value.progress.mockResolvedValue(create(GetSubscriptionProgressResponseSchema, { state: SubscriptionLoginState.RECOVERY_REQUIRED }));
  await tick();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.logout).toHaveBeenCalledTimes(1);
});
it("deletes an initial failed login through server-owned cleanup without submitting logout", async () => {
 const value = fixture(false, "chatgpt", true);
 render(<value.Harness />);
 fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
 await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
 expect(value.logout).not.toHaveBeenCalled();
 expect(value.progress).not.toHaveBeenCalled();
 expect(value.remove.mock.calls[0][0].mutation!.expectedRevision).toBe(value.initial.revision);
});

it("retains a failed login when the server cannot confirm cleanup", async () => {
 const value = fixture(false, "chatgpt", true);
 value.remove.mockRejectedValue(new ConnectError("Cleanup requires original recovery.", Code.FailedPrecondition));
 render(<value.Harness />);
 fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
 await screen.findByText("Account deletion paused.");
 expect(value.deleted).not.toHaveBeenCalled();
 expect(value.logout).not.toHaveBeenCalled();
 expect(value.remove).toHaveBeenCalledTimes(1);
});

it("retries only the original failed-login deletion after an uncertain response", async () => {
 const value = fixture(false, "chatgpt", true);
 value.remove.mockRejectedValueOnce(new ConnectError("Response unavailable.", Code.Unavailable));
 render(<value.Harness />);
 fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
 fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
 await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
 expect(value.remove).toHaveBeenCalledTimes(2);
 expect(value.remove.mock.calls[1][0]).toEqual(value.remove.mock.calls[0][0]);
 expect(value.logout).not.toHaveBeenCalled();
});

it("disposes late failed-login deletion completion when its task closes", async () => {
 const value = fixture(false, "chatgpt", true); let release!: () => void;
 value.remove.mockImplementationOnce(async request => {
  await new Promise<void>(resolve => { release = resolve; });
  return { id: request.mutation!.id, requestId: request.mutation!.requestId };
 });
 render(<value.Harness />);
 fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
 await screen.findByText("Cleaning up credentials and deleting the account...");
 expect(screen.getByText("Closing this screen does not cancel accepted server cleanup or deletion.")).toBeTruthy();
 fireEvent.click(screen.getByRole("button", { name: "Back to subscriptions" }));
 await act(async () => release());
 expect(value.deleted).not.toHaveBeenCalled();
 expect(value.remove).toHaveBeenCalledTimes(1);
 expect(value.logout).not.toHaveBeenCalled();
});


it("omits empty top-level deletion actions during checking, logout and deleting", async () => {
  const value = fixture();
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const read = value.read.getMockImplementation()!;
  value.read.mockImplementationOnce(async () => { await pending; return read(); });
  render(<value.Harness task />);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  expect(screen.getByText("Checking the current account...")).toBeTruthy();
  expect(screen.getByRole("dialog").querySelector(".account-deletion .actions")).toBeNull();
  await act(async () => release());
  await waitFor(() => expect(value.progress).toHaveBeenCalledTimes(1));
  expect(screen.getByRole("dialog").querySelector(".account-deletion .actions")).toBeNull();
  expect(value.remove).not.toHaveBeenCalled();
  let finish!: () => void;
  const deleting = new Promise<void>(resolve => { finish = resolve; });
  const remove = value.remove.getMockImplementation()!;
  value.remove.mockImplementationOnce(async request => { await deleting; return remove(request); });
  value.complete(); await tick();
  await screen.findByText("Deleting the account configuration...");
  expect(screen.getByRole("dialog").querySelector(".account-deletion .actions")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Close Delete configuration" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => finish());
  expect(value.deleted).not.toHaveBeenCalled();
});

it("retains the original paused deletion recovery action", async () => {
  const value = fixture(); value.read.mockRejectedValueOnce(new ConnectError("Unavailable", Code.Unavailable));
  render(<value.Harness task />);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  const recovery = await screen.findByRole("button", { name: "Refresh account for confirmation" });
  expect(screen.getByRole("dialog").querySelector(".account-deletion .actions")?.contains(recovery)).toBe(true);
  expect(screen.queryByRole("button", { name: "Back to subscriptions" })).toBeNull();
  expect(value.logout).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});

it("subscription details opens and closes without reads, then one management task uses the original fresh reader under Strict Mode", async () => {
  const value = fixture();
  render(<value.Harness settings />);
  await screen.findByRole("article", { name: alias });
  const reads = value.read.mock.calls.length, lists = value.list.mock.calls.length;
  const opener = screen.getByRole("button", { name: `More actions for ${alias}` });
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Close Account details" }));
  expect(value.read).toHaveBeenCalledTimes(reads); expect(value.list).toHaveBeenCalledTimes(lists);
  fireEvent.click(opener); fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  fireEvent.click(screen.getByRole("button", { name: "Manage metadata" }));
  await screen.findByRole("dialog", { name: "Manage subscription" });
  await waitFor(() => expect(value.read).toHaveBeenCalledTimes(reads + 2));
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(screen.queryByRole("dialog", { name: "Account details" })).toBeNull();
  expect(value.logout).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Close Manage subscription" }));
  await waitFor(() => expect(globalThis.document.activeElement).toBe(opener));
});


it("subscription details retains its proven revision floor after stale inventory refresh before metadata management", async () => {
  const value = fixture();
  render(<value.Harness settings />);
  await screen.findByRole("article", { name: alias });
  fireEvent.click(screen.getByRole("button", { name: `More actions for ${alias}` }));
  fireEvent.click(screen.getByRole("button", { name: "Account details" }));
  const provenRevision = value.current.revision;
  const staleInventory = create(ResourceSchema, { ...value.current, revision: provenRevision - 2n });
  value.list.mockResolvedValue({ resources: [staleInventory] });
  const lists = value.list.mock.calls.length;
  await act(async () => { await value.client.invalidateQueries({ predicate: query => query.queryKey.some(part => typeof part === "object" && part !== null && "scrollPaginationRefresh" in part) }); });
  await waitFor(() => expect(value.list.mock.calls.length).toBeGreaterThan(lists));
  expect(screen.getByRole("dialog", { name: "Account details" })).toBeTruthy();
  // A fresh response above the regressed list but below the displayed proof
  // cannot authorize controls for this original account.
  value.read.mockResolvedValue({ resource: create(ResourceSchema, { ...value.current, revision: provenRevision - 1n }) });
  fireEvent.click(screen.getByRole("button", { name: "Manage metadata" }));
  const management = await screen.findByRole("dialog", { name: "Manage subscription" });
  await waitFor(() => expect(within(management).getByRole("alert")).toBeTruthy());
  expect(within(management).queryByRole("button", { name: "Disconnect" })).toBeNull();
  expect(value.logout).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});
