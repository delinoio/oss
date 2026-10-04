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
