// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  AccountService, BrowserService, ConfigurationService, EntityKind, ErrorDetailSchema, ResourceSchema, newRequestId,
  type DeleteConfigurationRequest, type DisconnectAccountRequest, type Resource,
} from "@delinoio/delidev-api-client";
import { ConfigurationDeletion } from "./configuration-actions";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskDialog } from "./settings-task";
import { i18n, SupportedLanguage } from "./localization";

const confirmLabel = "Disconnect and delete entry";
const cleanupLabel = "Retry original credential cleanup";
const typedError = (message: string, code = "conflict") => new ConnectError(message, code === "permission_denied" ? Code.PermissionDenied : Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: { code, guidance: "Original server guidance.", correlationId: newRequestId() } }]);
function fixture(connected = true, removalRevision?: bigint) {
  const preferences = { alias: "OpenRouter fixture", type: "api", provider_id: newRequestId(), enabled: true, exclude_automatic: false, recovery_notifications: true };
  const removalId = newRequestId();
  let current = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 9007199254740993n,
    documentJson: encode({ ...preferences, health: connected ? "unverified" : "disconnected", ...(connected ? { connection: { id: newRequestId(), authentication: "bearer" } } : {}) }) });
  if (removalRevision !== undefined) current = create(ResourceSchema, { ...current, revision: removalRevision + 1n, documentJson: new TextEncoder().encode(JSON.stringify({ ...preferences, health: "disconnected" }).replace(/}$/, `,"removal":{"request_id":"${removalId}","expected_revision":${removalRevision}}}`)) });
  const initial = current;
  const patch = (change: Record<string, unknown>) => { current = create(ResourceSchema, { ...current, revision: current.revision + 1n, documentJson: encode({ ...document(current), ...change }) }); return current; };
  const order: string[] = [];
  const status = vi.fn(async () => { order.push("status"); return { account: current }; });
  const disconnect = vi.fn(async (request: DisconnectAccountRequest): Promise<{ account: Resource; requestId: string; cleanupProblemJson: Uint8Array }> => { order.push("disconnect"); patch({ health: "disconnected", connection: undefined, removal: undefined }); return { account: current, requestId: request.mutation!.requestId, cleanupProblemJson: new Uint8Array() }; });
  const remove = vi.fn(async (request: DeleteConfigurationRequest) => { order.push("delete"); return { id: request.mutation!.id, requestId: request.mutation!.requestId }; });
  const browserCleanup = vi.fn(async () => ({ pending: 3 }));
  const transport = createRouterTransport(router => {
    router.service(AccountService, { getAccountStatus: status, disconnectAccount: disconnect });
    router.service(ConfigurationService, { deleteConfiguration: remove });
    router.service(BrowserService, { getAccountBrowserCleanup: browserCleanup });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const deleted = vi.fn(), closed = vi.fn();
  function Harness({ active = true }: { active?: boolean }) {
    const [visible, setVisible] = useState(true);
    return <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}>
      <button onClick={() => setVisible(false)}>Leave category</button>
      {visible ? <SettingsLifetime>{() => <MutationIntents><SettingsTaskDialog title="Delete entry" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => { closed(); setVisible(false); }}><ConfigurationDeletion initial={initial} active={active} deleted={() => { deleted(); setVisible(false); }} close={() => { closed(); setVisible(false); }} /></SettingsTaskDialog></MutationIntents>}</SettingsLifetime> : null}
    </QueryClientProvider></TransportProvider></StrictMode>;
  }
  return { Harness, initial, patch, status, disconnect, remove, deleted, closed, browserCleanup, order, removalId, get current() { return current; } };
}
function confirm(value: ReturnType<typeof fixture>) {
  const view = render(<value.Harness />);
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  return view;
}

it.each([true, false])("cleans a connected=%s API entry before fresh-revision deletion", async connected => {
  const value = fixture(connected);
  const view = render(<value.Harness />);
  expect(value.status).not.toHaveBeenCalled(); expect(value.disconnect).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
  await waitFor(() => expect(globalThis.document.activeElement).toBe(screen.getByRole("button", { name: "Keep entry" })));
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.order).toEqual(["status", "disconnect", "status", "delete"]);
  expect(value.disconnect.mock.calls[0][0].mutation!.expectedRevision).toBe(value.initial.revision);
  expect(value.remove.mock.calls[0][0].mutation!.expectedRevision).toBe(value.current.revision);
  expect(value.remove.mock.calls[0][0].mutation!.expectedRevision).toBeGreaterThan(9007199254740992n);
  expect(screen.queryByRole("dialog")).toBeNull(); expect(value.browserCleanup).not.toHaveBeenCalled();
  view.unmount();
});

it("keeps a pending cleanup and explicitly retries its original request", async () => {
  const value = fixture();
  value.disconnect.mockImplementationOnce(async request => ({ account: value.patch({ health: "disconnected", connection: undefined, removal: { request_id: request.mutation!.requestId, expected_revision: "inert fixture token" } }), requestId: request.mutation!.requestId, cleanupProblemJson: encode({ code: "unavailable", message: "Protected cleanup is pending.", guidance: "Retry the original cleanup." }) }));
  confirm(value);
  await screen.findByText("Protected cleanup is pending.");
  expect(value.remove).not.toHaveBeenCalled();
  const original = value.disconnect.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: cleanupLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[1][0]).toEqual(original);
});

it.each([9007199254740993n, 18446744073709551613n])("restores a pending cleanup's exact uint64 revision %s", async revision => {
  const value = fixture(false, revision);
  confirm(value);
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0].mutation).toMatchObject({ requestId: value.removalId, expectedRevision: revision });
});

it.each(["null", "{}", "duplicate", "overflow"])("refuses a malformed %s removal marker", async variant => {
  const value = fixture(false);
  const marker = variant === "duplicate" ? `{"request_id":"${newRequestId()}","request_id":"${newRequestId()}","expected_revision":1}` : variant === "overflow" ? `{"request_id":"${newRequestId()}","expected_revision":18446744073709551616}` : variant;
  value.status.mockResolvedValue({ account: create(ResourceSchema, { ...value.initial, documentJson: new TextEncoder().encode(new TextDecoder().decode(value.initial.documentJson).replace(/}$/, `,"removal":${marker}}`)) }) });
  confirm(value);
  await screen.findByText("The entry changed or its cleanup could not be verified.");
  expect(value.disconnect).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});

it.each(["id", "provider", "request", "revision"])("keeps the original disconnect after an unverified %s response", async field => {
  const value = fixture();
  value.disconnect.mockImplementationOnce(async request => {
    const account = value.patch({ health: "disconnected", connection: undefined, ...(field === "provider" ? { provider_id: newRequestId() } : {}) });
    return { account: create(ResourceSchema, { ...account, ...(field === "id" ? { id: newRequestId() } : field === "revision" ? { revision: value.initial.revision } : {}) }), requestId: field === "request" ? newRequestId() : request.mutation!.requestId, cleanupProblemJson: new Uint8Array() };
  });
  confirm(value);
  await screen.findByRole("button", { name: cleanupLabel });
  expect(value.remove).not.toHaveBeenCalled();
  const original = value.disconnect.mock.calls[0][0];
  if (field === "provider") value.patch({ provider_id: document(value.initial).provider_id });
  fireEvent.click(screen.getByRole("button", { name: cleanupLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[1][0]).toEqual(original);
});

it("retains uncertainty after a definite error on the original retry", async () => {
  const value = fixture();
  value.disconnect.mockRejectedValueOnce(new ConnectError("lost", Code.Unavailable)).mockRejectedValueOnce(typedError("Permission revoked.", "permission_denied"));
  confirm(value);
  fireEvent.click(await screen.findByRole("button", { name: cleanupLabel }));
  await screen.findByText("Permission revoked.");
  const original = value.disconnect.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: cleanupLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[1][0]).toEqual(original); expect(value.disconnect.mock.calls[2][0]).toEqual(original);
});

it.each(["connection", "removal", "preferences", "revision"])("refuses changed %s state at the fresh pre-delete read", async field => {
  const value = fixture();
  value.status.mockResolvedValueOnce({ account: value.initial }).mockImplementationOnce(async () => ({ account: field === "revision" ? value.initial : value.patch(field === "connection" ? { connection: { id: newRequestId() } } : field === "removal" ? { removal: {} } : { alias: "Changed alias" }) }));
  confirm(value);
  await screen.findByText("The entry changed or its cleanup could not be verified.");
  expect(value.disconnect).toHaveBeenCalledTimes(1); expect(value.remove).not.toHaveBeenCalled();
});

it("offers only a read retry when status fails after confirmed cleanup", async () => {
  const value = fixture();
  value.status.mockResolvedValueOnce({ account: value.initial }).mockRejectedValueOnce(new ConnectError("read lost", Code.Unavailable));
  confirm(value);
  fireEvent.click(await screen.findByRole("button", { name: "Retry original status check" }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect).toHaveBeenCalledTimes(1);
});

it("requires a new confirmation after a stale initial read without disconnecting", async () => {
  const value = fixture(); value.patch({ alias: "Updated entry" });
  confirm(value);
  await screen.findByText("The entry changed or its cleanup could not be verified.");
  expect(value.disconnect).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Refresh entry for confirmation" }));
  await screen.findByRole("heading", { name: "Delete Updated entry?" });
  expect(value.disconnect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: confirmLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
});

it("cannot infer cleanup success from a malformed cleanup problem", async () => {
  const value = fixture();
  value.disconnect.mockImplementationOnce(async request => ({ account: value.patch({ health: "disconnected", connection: undefined }), requestId: request.mutation!.requestId, cleanupProblemJson: encode({ message: "unknown problem without a code" }) }));
  confirm(value);
  await screen.findByText("Credential cleanup could not be confirmed. The entry has been kept.");
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: cleanupLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[1][0]).toEqual(value.disconnect.mock.calls[0][0]);
});

it("completes a hidden deletion once without keeping the task opener", async () => {
  const value = fixture(); let release!: () => void;
  value.remove.mockImplementationOnce(async request => { await new Promise<void>(resolve => { release = resolve; }); return { id: request.mutation!.id, requestId: request.mutation!.requestId }; });
  confirm(value);
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Close Delete entry" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => release());
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
});

it.each(["lost", "wrong-id", "wrong-request"])("retries only the original deletion after %s acknowledgment", async failure => {
  const value = fixture();
  value.remove.mockImplementationOnce(async request => {
    if (failure === "lost") throw new ConnectError("ack lost", Code.Unavailable);
    return { id: failure === "wrong-id" ? newRequestId() : request.mutation!.id, requestId: failure === "wrong-request" ? newRequestId() : request.mutation!.requestId };
  });
  confirm(value);
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.remove.mock.calls[1][0]).toEqual(value.remove.mock.calls[0][0]); expect(value.disconnect).toHaveBeenCalledTimes(1);
});

it("shows the original reference conflict prominently and requires fresh confirmation", async () => {
  const value = fixture();
  value.remove.mockRejectedValueOnce(typedError("This configuration is still referenced."));
  confirm(value);
  const reason = await screen.findByText("This configuration is still referenced.");
  expect(reason.closest("details")).toBeNull(); expect(screen.getByText("Original server guidance.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Retry the same deletion" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Refresh entry for confirmation" }));
  await screen.findByRole("button", { name: confirmLabel });
  expect(value.remove).toHaveBeenCalledTimes(1);
});

it.each(["X", "Escape"])("retains pending cleanup and exact retries through %s dismissal", async method => {
  const value = fixture(); let release!: () => void;
  value.disconnect.mockImplementationOnce(async () => { await new Promise<void>(resolve => { release = resolve; }); throw new ConnectError("ack lost", Code.Unavailable); });
  confirm(value);
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  if (method === "X") fireEvent.click(screen.getByRole("button", { name: "Close Delete entry" }));
  else fireEvent(screen.getByRole("dialog"), new Event("cancel", { cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => release());
  expect(await screen.findByText("Delete entry: The original result is unconfirmed.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "View original operation" }));
  fireEvent.click(screen.getByRole("button", { name: cleanupLabel }));
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[1][0]).toEqual(value.disconnect.mock.calls[0][0]);
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
});

it.each(["departure", "inactive"])("rejects late cleanup after %s and suppresses double clicks", async reason => {
  const value = fixture(); let release!: () => void;
  value.disconnect.mockImplementationOnce(async request => { await new Promise<void>(resolve => { release = resolve; }); return { account: value.patch({ health: "disconnected", connection: undefined }), requestId: request.mutation!.requestId, cleanupProblemJson: new Uint8Array() }; });
  const view = confirm(value);
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("button", { name: confirmLabel })).toBeNull();
  if (reason === "departure") fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
  else view.rerender(<value.Harness active={false} />);
  await act(async () => release());
  expect(value.disconnect).toHaveBeenCalledTimes(1); expect(value.remove).not.toHaveBeenCalled(); expect(value.deleted).not.toHaveBeenCalled();
});

it("admits only one original operation during rapid duplicate confirmation", async () => {
  const value = fixture(); let release!: () => void;
  value.status.mockImplementationOnce(async () => { await new Promise<void>(resolve => { release = resolve; }); return { account: value.initial }; });
  render(<value.Harness />);
  const button = screen.getByRole("button", { name: confirmLabel });
  fireEvent.click(button); fireEvent.click(button);
  await waitFor(() => expect(value.status).toHaveBeenCalledTimes(1));
  expect(value.disconnect).not.toHaveBeenCalled();
  await act(async () => release());
  await waitFor(() => expect(value.deleted).toHaveBeenCalledTimes(1));
  expect(value.disconnect).toHaveBeenCalledTimes(1); expect(value.remove).toHaveBeenCalledTimes(1);
});

it("changes English/Korean presentation without starting or replaying work", async () => {
  const value = fixture(); render(<value.Harness />);
  await act(async () => { await i18n.changeLanguage(SupportedLanguage.Korean); });
  expect(screen.getByRole("button", { name: "연결 해제 후 항목 삭제" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "항목 유지" })).toBeTruthy();
  expect(value.disconnect).not.toHaveBeenCalled();
  await act(async () => { await i18n.changeLanguage(SupportedLanguage.English); });
  fireEvent.click(screen.getByRole("button", { name: "Keep entry" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(value.disconnect).not.toHaveBeenCalled(); expect(value.remove).not.toHaveBeenCalled();
});
