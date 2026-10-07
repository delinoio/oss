// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, NetworkService, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId, type SelectNetworkProfileRequest } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { NetworkSettings } from "./network-settings";
import { WorkerNetworkAction, WorkerNetworkControlProvider } from "./worker-network-native";
import { encryptedInput, workerRecipient, workerRouteStatus } from "./worker-network";
import { SettingsLifetime } from "./settings-lifetime";

function fixture(lose = false, native?: (machine: string, action: WorkerNetworkAction, ciphertext: Uint8Array, digest: string) => Promise<unknown>) {
  const authority = { endpoint: "https://server.example", serverId: newRequestId() }, machine = newRequestId();
  const row = create(ResourceSchema, { kind: EntityKind.NETWORK_PROFILE, id: newRequestId(), revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ name: "Pinned proxy", mode: "http", host: "proxy.example", port: 3128 }) });
  const route = create(ResourceSchema, { kind: EntityKind.NETWORK_ROUTE, id: newRequestId(), revision: 9007199254740994n, schemaVersion: 1, documentJson: encode({ machine_id: machine, profile: { name: "Direct", mode: "direct" } }) });
  const requests: SelectNetworkProfileRequest[] = [];
  const select = vi.fn(async (request: SelectNetworkProfileRequest) => { requests.push(request); if (lose && requests.length === 1) throw new ConnectError("Fixture lost response", Code.Unavailable); return { resource: route }; });
  const reads = vi.fn();
  const save = vi.fn(request => ({ resource: create(ResourceSchema, { ...row, documentJson: request.documentJson }) }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SERVER_OUTBOUND_PROXY_V1, SystemCapability.WORKER_NETWORK_BOOTSTRAP_V1, SystemCapability.WORKER_CODEX_PROXY_V1] }) });
    router.service(ResourceService, { listResources: () => { reads(); return { resources: [row] }; } });
    router.service(NetworkService, { getNetworkRoute: () => ({ route }), getWorkerNetworkStatus: () => ({ statusJson: encode({ version: 1, machine_id: machine, desired_generation: "9007199254740994", effective_generation: "9007199254740993", native_generation: "9007199254740993", control_state: "stale", native_state: "stale" }) }), selectNetworkProfile: select, saveNetworkProfile: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = <SettingsLifetime>{() => <MutationIntents><NetworkSettings active machine={machine} authority={authority} /></MutationIntents>}</SettingsLifetime>;
  const rendered = render(<TransportProvider transport={transport}><QueryClientProvider client={client}>{native ? <WorkerNetworkControlProvider control={native}>{view}</WorkerNetworkControlProvider> : view}</QueryClientProvider></TransportProvider>);
  return { ...rendered, authority, machine, row, route, requests, select, reads, save, client };
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
  await screen.findByRole("option", { name: /Pinned proxy/ });
  fireEvent.change(screen.getByLabelText("Profile to select"), { target: { value: f.row.id } });
  fireEvent.click(screen.getByRole("button", { name: "Select this revision" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original route selection" }));
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
