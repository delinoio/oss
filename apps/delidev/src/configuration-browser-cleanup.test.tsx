// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, BrowserService, ConfigurationService, EntityKind, ResourceSchema, newRequestId, type DeleteConfigurationRequest, type DisconnectAccountRequest } from "@delinoio/delidev-api-client";
import { ConfigurationDeletion } from "./configuration-actions";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogSize, SettingsTaskBackground, SettingsTaskDialog, SettingsTasks } from "./settings-task";

function fixture(type: "subscription" | "api") {
  const initial = create(ResourceSchema, {
    id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 3n, schemaVersion: type === "api" ? 1 : 2,
    documentJson: encode({ alias: "Original fixture", type, ...(type === "subscription" ? { subscription_service: "claude" } : { provider_id: newRequestId(), health: "connected", connection: { id: newRequestId() } }) }),
  });
  let current = initial;
  const acknowledgment = (request: DeleteConfigurationRequest) => ({ id: initial.id, requestId: request.mutation!.requestId });
  const remove = vi.fn(async (request: DeleteConfigurationRequest) => acknowledgment(request));
  const disconnect = vi.fn(async (request: DisconnectAccountRequest) => {
    const data = JSON.parse(new TextDecoder().decode(initial.documentJson));
    delete data.connection; data.health = "disconnected";
    current = create(ResourceSchema, { ...initial, revision: 4n, documentJson: encode(data) });
    return { account: current, requestId: request.mutation!.requestId };
  });
  const cleanup = vi.fn(async (_request: unknown) => ({ pending: 2, removed: 1 }));
  const deleted = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(AccountService, { getAccountStatus: async () => ({ account: current }), disconnectAccount: disconnect });
    router.service(ConfigurationService, { deleteConfiguration: remove });
    router.service(BrowserService, { getAccountBrowserCleanup: cleanup });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Task() {
    const [open, setOpen] = useState(false);
    return <SettingsTasks><div className="settings-content"><SettingsTaskBackground><h1 tabIndex={-1}>Fixture accounts</h1><button onClick={() => setOpen(true)}>Delete fixture account</button></SettingsTaskBackground></div>
      {open ? <SettingsTaskDialog title={type === "api" ? "Delete entry" : "Delete configuration"} size={SettingsDialogSize.Confirmation} close={() => setOpen(false)}><ConfigurationDeletion initial={initial} deleted={() => { deleted(); setOpen(false); }} close={() => setOpen(false)} /></SettingsTaskDialog> : null}
    </SettingsTasks>;
  }
  function View() {
    const [active, setActive] = useState(true);
    return <StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}>
      <button onClick={() => setActive(false)}>Leave category</button><button onClick={() => setActive(true)}>Return to category</button>
      {active ? <SettingsLifetime>{() => <MutationIntents><Task /></MutationIntents>}</SettingsLifetime> : null}
    </QueryClientProvider></TransportProvider></StrictMode>;
  }
  render(<View />);
  const opener = screen.getByRole("button", { name: "Delete fixture account" });
  opener.focus(); fireEvent.click(opener);
  return { initial, remove, acknowledgment, disconnect, cleanup, deleted, client, View, opener };
}
function dismiss(method: "X" | "Escape") {
  if (method === "X") fireEvent.click(screen.getByRole("button", { name: /^Close Delete (entry|configuration)$/ }));
  else fireEvent(screen.getByRole("dialog"), new Event("cancel", { cancelable: true }));
}

it.each(["subscription", "api"] as const)("closes %s deletion once without waiting for browser cleanup and restores opener focus", async type => {
  const f = fixture(type);
  f.cleanup.mockRejectedValue(new ConnectError("Cleanup observation unavailable", Code.Unavailable));
  if (type === "api") {
    expect(screen.getByRole("heading", { name: "Delete entry", level: 2 })).toBeTruthy();
    expect(screen.getByText(/This disconnects the entry/)).toBeTruthy();
  }
  expect(screen.getByText(type === "api" ? /Browser profile cleanup continues independently/ : /Browser profile cleanup remains pending/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: type === "api" ? "Disconnect and delete entry" : "Confirm configuration deletion" }));
  await waitFor(() => expect(f.deleted).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(f.opener));
  expect(f.remove).toHaveBeenCalledTimes(1);
  expect(f.disconnect).toHaveBeenCalledTimes(type === "api" ? 1 : 0);
  expect(f.remove.mock.calls[0][0]).toMatchObject({ kind: EntityKind.ACCOUNT, mutation: { id: f.initial.id, expectedRevision: type === "api" ? 4n : 3n } });
  expect(f.cleanup).not.toHaveBeenCalled();
  expect(screen.queryByText("API key entry deleted")).toBeNull();
  expect(screen.queryByText("Account configuration deleted")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
});

it.each(["X", "Escape"] as const)("ignores a closed API deletion after %s without completion or taking focus", async method => {
  const f = fixture("api"); let release!: () => void;
  f.remove.mockImplementationOnce(async request => { await new Promise<void>(resolve => { release = resolve; }); return f.acknowledgment(request); });
  fireEvent.click(screen.getByRole("button", { name: "Disconnect and delete entry" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
  dismiss(method);
  const destination = screen.getByRole("button", { name: "Return to category" }); destination.focus();
  await act(async () => release());
  expect(f.deleted).not.toHaveBeenCalled();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(document.activeElement).toBe(destination);
  expect(f.cleanup).not.toHaveBeenCalled();
  expect(f.remove).toHaveBeenCalledTimes(1);
});

it("keeps the exact uncertain deletion retry within its open task", async () => {
  const f = fixture("api");
  f.remove.mockRejectedValueOnce(new ConnectError("Lost deletion acknowledgment", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Disconnect and delete entry" }));
  await screen.findByRole("button", { name: "Retry the same deletion" });
  const original = f.remove.mock.calls[0][0];
  expect(f.deleted).not.toHaveBeenCalled();
  expect(f.remove).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same deletion" }));
  await waitFor(() => expect(f.deleted).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(f.remove.mock.calls[1][0]).toEqual(original);
  expect(f.cleanup).not.toHaveBeenCalled();
});

it.each([Code.Aborted, Code.PermissionDenied, Code.Unauthenticated])("keeps the dialog after a definite deletion failure %s", async code => {
  const f = fixture("api"); f.remove.mockRejectedValue(new ConnectError("Deletion rejected", code));
  fireEvent.click(screen.getByRole("button", { name: "Disconnect and delete entry" }));
  await screen.findByRole("button", { name: "Refresh entry for confirmation" });
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(f.deleted).not.toHaveBeenCalled();
  expect(f.cleanup).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Retry the same deletion" })).toBeNull();
});

it("ignores a late API deletion acknowledgment after category disposal", async () => {
  const f = fixture("api"); let release!: () => void;
  f.remove.mockImplementationOnce(async request => { await new Promise<void>(resolve => { release = resolve; }); return f.acknowledgment(request); });
  fireEvent.click(screen.getByRole("button", { name: "Disconnect and delete entry" }));
  await waitFor(() => expect(f.remove).toHaveBeenCalledTimes(1));
  dismiss("Escape");
  fireEvent.click(screen.getByRole("button", { name: "Leave category" }));
  await act(async () => release());
  expect(f.deleted).not.toHaveBeenCalled();
  expect(f.cleanup).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Return to category" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(f.remove).toHaveBeenCalledTimes(1);
});
