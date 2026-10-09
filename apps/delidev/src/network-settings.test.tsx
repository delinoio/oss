// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, NetworkService, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId, type SelectNetworkProfileRequest } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { NetworkSettings } from "./network-settings";
import { WorkerNetworkAction, WorkerNetworkControlProvider } from "./worker-network-native";
import { encryptedInput, workerRecipient, workerRouteStatus } from "./worker-network";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsActionScope } from "./settings-action";

function fixture(lose = false, native?: (machine: string, action: WorkerNetworkAction, ciphertext: Uint8Array, digest: string) => Promise<unknown>, inline = false, scoped = false) {
  const authority = { endpoint: "https://server.example", serverId: newRequestId() }, machine = newRequestId();
  const row = create(ResourceSchema, { kind: EntityKind.NETWORK_PROFILE, id: newRequestId(), revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ name: "Pinned proxy", mode: "http", host: "proxy.example", port: 3128 }) });
  const route = create(ResourceSchema, { kind: EntityKind.NETWORK_ROUTE, id: newRequestId(), revision: 9007199254740994n, schemaVersion: 1, documentJson: encode({ machine_id: machine, profile: { name: "Direct", mode: "direct" } }) });
  const requests: SelectNetworkProfileRequest[] = [];
  const select = vi.fn(async (request: SelectNetworkProfileRequest) => { requests.push(request); if (lose && requests.length === 1) throw new ConnectError("Fixture lost response", Code.Unavailable); return { resource: route }; });
  const remove = vi.fn(() => ({}));
  const reads = vi.fn();
  const save = vi.fn(request => ({ resource: create(ResourceSchema, { ...row, documentJson: request.documentJson }) }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SERVER_OUTBOUND_PROXY_V1, SystemCapability.WORKER_NETWORK_BOOTSTRAP_V1, SystemCapability.WORKER_CODEX_PROXY_V1] }) });
    router.service(ResourceService, { getResource: () => ({ resource: row }), listResources: () => { reads(); return { resources: [row] }; } });
    router.service(NetworkService, { getNetworkRoute: () => ({ route }), getWorkerNetworkStatus: () => ({ statusJson: encode({ version: 1, machine_id: machine, desired_generation: "9007199254740994", effective_generation: "9007199254740993", native_generation: "9007199254740993", control_state: "stale", native_state: "stale" }) }), selectNetworkProfile: select, saveNetworkProfile: save, deleteNetworkProfile: remove });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = <SettingsLifetime>{() => <MutationIntents><NetworkSettings active machine={inline ? "" : machine} authority={authority} /></MutationIntents>}</SettingsLifetime>;
  const rendered = render(<TransportProvider transport={transport}><QueryClientProvider client={client}>{native ? <WorkerNetworkControlProvider control={native}>{view}</WorkerNetworkControlProvider> : scoped ? <SettingsActionScope>{view}</SettingsActionScope> : view}</QueryClientProvider></TransportProvider>);
  return { ...rendered, authority, machine, row, route, requests, select, reads, save, remove, client };
}
it("keeps network reads collapsed and distinct exact control/native generations", async () => {
  const f = fixture(); expect(f.reads).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  expect(await screen.findAllByText("9007199254740993 · stale")).toHaveLength(2);
  expect(screen.getByText("9007199254740994")).toBeTruthy();
  expect(f.select).not.toHaveBeenCalled();
});
it("retains the exact original route revision after response loss", async () => {
  const f = fixture(true);
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.click(await screen.findByRole("combobox", { name: "Profile to select" }));
  fireEvent.click(await screen.findByRole("option", { name: /Pinned proxy/ }));
  await waitFor(() => expect(screen.getByRole("combobox", { name: "Profile to select" }).dataset.value).toBe(f.row.id));
  fireEvent.click(screen.getByRole("button", { name: "Select this revision" }));
  await screen.findByRole("button", { name: "Retry original route selection" });
  expect(screen.getByRole("alert").textContent).toContain("retry only the original selection");
  fireEvent.click(screen.getByRole("button", { name: "Retry original route selection" }));
  await waitFor(() => expect(f.requests).toHaveLength(2));
  expect(f.requests[1]).toEqual(f.requests[0]);
  expect(f.requests[0]).toMatchObject({ mutation: { id: f.route.id, expectedRevision: f.route.revision }, machineId: f.machine, profileId: f.row.id, profileRevision: f.row.revision });
});
it("disposes a late native recipient result without authorizing another operation", async () => {
  let resolve!: (value: unknown) => void;
  const pending = new Promise<unknown>(done => { resolve = done; });
  const native = vi.fn(() => pending);
  const f = fixture(false, native);
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Prepare protected recipient" }));
  await waitFor(() => expect(native).toHaveBeenCalledTimes(1));
  f.unmount();
  resolve({ version: 1, authority: { server_id: f.authority.serverId, endpoint: f.authority.endpoint, machine_id: f.machine, device_id: newRequestId(), pairing_id: newRequestId() }, key_id: newRequestId(), recipient: `age1${"a".repeat(58)}` });
  await pending;
  expect(native).toHaveBeenCalledTimes(1);
  expect(f.client.getMutationCache().getAll()).toHaveLength(0);
});
it("rejects foreign recipient scope, secret fields, malformed status and oversized ciphertext", () => {
  const selected = { endpoint: "https://server.example", serverId: newRequestId() }, machine = newRequestId();
  const recipient = { version: 1, authority: { server_id: selected.serverId, endpoint: selected.endpoint, machine_id: machine, device_id: newRequestId(), pairing_id: newRequestId() }, key_id: newRequestId(), recipient: `age1${"a".repeat(58)}` };
  expect(workerRecipient(recipient, selected, machine)).toBeTruthy();
  expect(workerRecipient({ ...recipient, private_key: "forbidden" }, selected, machine)).toBeUndefined();
  expect(workerRecipient(recipient, { ...selected, endpoint: "https://foreign.example" }, machine)).toBeUndefined();
  expect(workerRouteStatus(encode({ version: 1, machine_id: machine, desired_generation: 9007199254740993, effective_generation: "1", native_generation: "1", control_state: "observed", native_state: "observed" }), machine)).toBeUndefined();
  expect(Array.from(encryptedInput(btoa("fixture cipher"))!)).toEqual(Array.from(new TextEncoder().encode("fixture cipher")));
  expect(encryptedInput("A".repeat(131073))).toBeUndefined();
});

it("discards proxy secrets and uncertain retry authority on close without replaying the request", async () => {
  const f = fixture();
  f.save.mockRejectedValueOnce(new ConnectError("Lost profile acknowledgment", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "New network profile" }));
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  fireEvent.change(screen.getByLabelText("Profile name"), { target: { value: "Original proxy" } });
  fireEvent.change(screen.getByLabelText("Connection mode"), { target: { value: "http" } });
  fireEvent.change(screen.getByLabelText("Proxy host"), { target: { value: "proxy.example" } });
  fireEvent.change(screen.getByLabelText("Proxy port"), { target: { value: "3128" } });
  fireEvent.change(screen.getByLabelText("Proxy username"), { target: { value: "fixture-user" } });
  fireEvent.change(screen.getByLabelText("Proxy password"), { target: { value: "fixture-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await screen.findByRole("button", { name: "Retry original profile save" });
  const original = f.save.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Close New network profile" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(f.save).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(f.client.getMutationCache().getAll()).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "New network profile" }));
  expect((screen.getByLabelText("Profile name") as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry original profile save" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Connection mode"), { target: { value: "http" } });
  expect((screen.getByLabelText("Proxy username") as HTMLInputElement).value).toBe("");
  expect((screen.getByLabelText("Proxy password") as HTMLInputElement).value).toBe("");
  fireEvent.change(screen.getByLabelText("Profile name"), { target: { value: "Fresh proxy" } });
  fireEvent.change(screen.getByLabelText("Proxy host"), { target: { value: "proxy.example" } });
  fireEvent.change(screen.getByLabelText("Proxy port"), { target: { value: "3128" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(2));
  expect(f.save.mock.calls[1][0]).not.toEqual(original);
  expect(f.save.mock.calls[1][0].credentialJson).toHaveLength(0);
});

it.each(["uncertain", "completed"] as const)("discards a %s native import on dismissal and permits exact retry only while open", async state => {
  const digest = "a".repeat(64), ciphertext = btoa("synthetic encrypted fixture");
  const native = vi.fn(async (_machine: string, action: WorkerNetworkAction, _bytes: Uint8Array, expectedDigest: string) => {
    if (action !== WorkerNetworkAction.Import) throw new Error("No replacement prepare is expected");
    if (native.mock.calls.length === 1) throw new Error("Lost native result");
    return { version: 1, ciphertext_digest: expectedDigest, generation: "2" };
  });
  fixture(false, native);
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.change(await screen.findByLabelText("Encrypted bundle to import (Base64)"), { target: { value: ciphertext } });
  fireEvent.change(screen.getByLabelText("Separately authenticated digest"), { target: { value: digest } });
  fireEvent.click(screen.getByRole("checkbox", { name: /^I verified this digest independently/ }));
  fireEvent.click(screen.getByRole("button", { name: "Import confirmed encrypted configuration" }));
  await screen.findByRole("button", { name: "Retry original encrypted import" });
  expect((screen.getByRole("button", { name: "Prepare protected recipient" }) as HTMLButtonElement).disabled).toBe(true);
  if (state === "completed") {
    fireEvent.click(screen.getByRole("button", { name: "Retry original encrypted import" }));
    await screen.findByText(/Encrypted generation 2 imported/);
    expect(native.mock.calls[1]).toEqual(native.mock.calls[0]);
  }
  fireEvent.click(screen.getByRole("button", { name: "Close Runner Device network" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  expect((await screen.findByLabelText("Encrypted bundle to import (Base64)") as HTMLTextAreaElement).value).toBe("");
  expect((screen.getByLabelText("Separately authenticated digest") as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry original encrypted import" })).toBeNull();
  expect(native).toHaveBeenCalledTimes(state === "completed" ? 2 : 1);
});

it("retains three network profile payload pages and restores an older accepted range without a mutation", async () => {
  const pages = [1, 2, 3, 4].map(number => create(ResourceSchema, { kind: EntityKind.NETWORK_PROFILE, id: newRequestId(), revision: 1n, schemaVersion: 1, documentJson: encode({ name: `Profile ${number}`, mode: "direct" }) }));
  const list = vi.fn(request => { const index = request.filter?.pageToken ? Number(request.filter.pageToken) : 0; return { resources: [pages[index]], nextPageToken: index < 3 ? String(index + 1) : "" }; });
  const select = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SERVER_OUTBOUND_PROXY_V1] }) });
    router.service(ResourceService, { listResources: list, getResource: request => ({ resource: pages.find(row => row.id === request.id) }) });
    router.service(NetworkService, { getNetworkRoute: () => ({}), selectNetworkProfile: select });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NetworkSettings active /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  await screen.findByRole("heading", { name: "Profile 1" });
  for (let number = 2; number <= 4; number++) {
    fireEvent.click(screen.getByRole("button", { name: "Load more Network profile pages" }));
    await screen.findByRole("heading", { name: `Profile ${number}` });
  }
  expect(screen.queryByRole("heading", { name: "Profile 1" })).toBeNull();
  expect(document.querySelectorAll("[data-payload-page] article")).toHaveLength(3);
  fireEvent.click(screen.getByRole("button", { name: "Restore previously loaded items" }));
  await screen.findByRole("heading", { name: "Profile 1" });
  expect(document.querySelectorAll("[data-payload-page] article")).toHaveLength(3);
  expect(list.mock.calls.at(-1)?.[0].filter?.pageToken).toBe("");
  expect(select).not.toHaveBeenCalled();
});

it.each(["Edit profile", "Delete profile"])("pauses both network inventories beneath %s while retaining the original row", async action => {
  const f = fixture(); fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  const opener = await screen.findByRole("button", { name: action });
  const row = opener.closest("article")!;
  fireEvent.click(opener);
  await screen.findByRole("dialog", { name: action === "Edit profile" ? "Edit network profile" : action });
  const reads = f.reads.mock.calls.length;
  await act(async () => { await f.client.invalidateQueries(); await new Promise(resolve => setTimeout(resolve, 20)); });
  expect(f.reads).toHaveBeenCalledTimes(reads); expect(row.isConnected).toBe(true);
  expect(f.select).not.toHaveBeenCalled(); expect(f.save).not.toHaveBeenCalled();
});

it("keeps the inline server owner alive after an independent profile dialog closes", async () => {
  const f = fixture(false, undefined, true);
  expect(f.reads).not.toHaveBeenCalled();
  const disclosure = screen.getByRole("button", { name: "Network settings" });
  fireEvent.click(disclosure);
  await screen.findByRole("heading", { name: "Current route" });
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.querySelector<HTMLDetailsElement>(".network-transfer")?.open).toBe(false));
  await screen.findByRole("option", { name: /Pinned proxy/ });
  fireEvent.change(await screen.findByRole("combobox", { name: "Profile to select" }), { target: { value: f.row.id } });
  expect(f.select).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "New network profile" }));
  await screen.findByRole("dialog", { name: "New network profile" });
  fireEvent.change(screen.getByLabelText("Profile name"), { target: { value: "Disposed draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Close New network profile" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(screen.getByRole("button", { name: "Select this revision" }).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Select this revision" }));
  await waitFor(() => expect(f.requests).toHaveLength(1));
  expect(f.requests[0]).toMatchObject({ machineId: "", profileId: f.row.id, profileRevision: f.row.revision, mutation: { expectedRevision: f.route.revision } });
  fireEvent.click(disclosure);
  expect(screen.queryByRole("heading", { name: "Current route" })).toBeNull();
  fireEvent.click(disclosure);
  await screen.findByRole("heading", { name: "Current route" });
  expect((await screen.findByRole("combobox", { name: "Profile to select" }) as HTMLSelectElement).value).toBe("");
  expect(f.requests).toHaveLength(1);
});
it("does not fabricate Direct or generation zero before the server route read", async () => {
  let release!: (value: object) => void;
  const routeRead = new Promise<object>(resolve => { release = resolve; });
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SERVER_OUTBOUND_PROXY_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [] }) });
    router.service(NetworkService, { getNetworkRoute: () => routeRead });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><SettingsLifetime>{() => <MutationIntents><NetworkSettings active /></MutationIntents>}</SettingsLifetime></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  await screen.findByText("Reading current route…");
  expect(screen.queryByText(/Selected route:/)).toBeNull();
  expect(screen.getByRole("button", { name: "Select this revision" }).matches(":disabled")).toBe(true);
  release({});
  await screen.findByText(/Selected route: Direct · Generation 0/);
  await screen.findByText("No saved network profiles");
});
it("drops inline uncertain selections on collapse without replay", async () => {
  const f = fixture(true, undefined, true);
  const disclosure = screen.getByRole("button", { name: "Network settings" });
  fireEvent.click(disclosure);
  await screen.findByRole("heading", { name: "Current route" });
  await waitFor(() => expect(screen.getByRole("button", { name: "Select this revision" }).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Select this revision" }));
  await screen.findByRole("button", { name: "Retry original route selection" });
  fireEvent.click(disclosure); fireEvent.click(disclosure);
  await screen.findByRole("heading", { name: "Current route" });
  expect(screen.queryByRole("button", { name: "Retry original route selection" })).toBeNull();
  expect(f.select).toHaveBeenCalledTimes(1);
});

it("disposes an inline deletion confirmation's uncertain request independently", async () => {
  const f = fixture(false, undefined, true);
  f.remove.mockRejectedValueOnce(new ConnectError("Lost deletion response", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "Delete profile" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm profile deletion" }));
  await screen.findByRole("button", { name: "Retry original profile deletion" });
  fireEvent.click(screen.getByRole("button", { name: "Close Delete profile" }));
  expect(screen.queryByRole("button", { name: "Retry original profile deletion" })).toBeNull();
  await screen.findByRole("heading", { name: "Current route" });
  fireEvent.click(screen.getByRole("button", { name: "Delete profile" }));
  expect(screen.queryByRole("button", { name: "Retry original profile deletion" })).toBeNull();
  expect(f.remove).toHaveBeenCalledTimes(1);
});
it("fences an accepted inline selection response after collapse", async () => {
  const f = fixture(false, undefined, true);
  let release!: (value: { resource: typeof f.route }) => void;
  f.select.mockImplementationOnce(() => new Promise(resolve => { release = resolve; }));
  const disclosure = screen.getByRole("button", { name: "Network settings" });
  fireEvent.click(disclosure);
  await screen.findByText(/Selected route:/);
  await waitFor(() => expect(screen.getByRole("button", { name: "Select this revision" }).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Select this revision" }));
  await waitFor(() => expect(f.select).toHaveBeenCalledTimes(1));
  fireEvent.click(disclosure); fireEvent.click(disclosure);
  await screen.findByText(/Selected route:/);
  await act(async () => { release({ resource: create(ResourceSchema, { ...f.route, revision: 9007199254740999n, documentJson: encode({ profile: { name: "Disposed response", mode: "direct" } }) }) }); });
  expect(screen.queryByText(/Disposed response/)).toBeNull();
  expect(f.select).toHaveBeenCalledTimes(1);
});

it("names icon-only profile deletion with the original target and opens only its confirmation", async () => {
 const f = fixture(false, undefined, true, true);
 fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
 const opener = await screen.findByRole("button", { name: `Delete profile · Pinned proxy · ${f.row.id}` });
 expect(opener.getAttribute("data-settings-action-presentation")).toBe("icon");
 act(() => opener.focus());
 fireEvent.click(opener);
 await screen.findByRole("dialog", { name: "Delete profile" });
 expect(f.remove).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Close Delete profile" }));
 await waitFor(() => expect(document.activeElement).toBe(opener));
 expect(f.remove).not.toHaveBeenCalled();
});
