// SPDX-License-Identifier: Apache-2.0
import { execFile } from "node:child_process";
import { writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { createHash } from "node:crypto";
import { Code, ConnectError, createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { DeviceService, DeviceType, NetworkService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { useSettingsFixture } from "./settings-test-fixture";
const fixture = useSettingsFixture();

it("selects one real exact route after response loss and exports a pending recipient without account/key-store access", async () => {
  const { transport, directory } = fixture;
  const status = await createClient(SystemService, transport).getStatus({});
  // Only a temporary age recipient is generated; its discarded private key
  // never enters this process, user storage, product state or validation logs.
  const helper = join(directory, "public-recipient.go");
  await writeFile(helper, 'package main\nimport("fmt";"filippo.io/age")\nfunc main(){key,err:=age.GenerateX25519Identity();if err!=nil{panic("fixture recipient")};fmt.Println(key.Recipient().String())}\n', { mode: 0o600 });
  const { stdout } = await promisify(execFile)("go", ["run", helper], { cwd: resolve(process.cwd(), "../.."), timeout: 60000 });
  let lost = false, profileSaved = false;
  let releaseRouteRead!: () => void;
  const routeRead = new Promise<void>(resolve => { releaseRouteRead = resolve; });
  const scoped: Transport = { ...transport, async unary(method, signal, timeout, headers, input, context) {
    const response = await transport.unary(method, signal, timeout, headers, input, context);
    if (method.parent.typeName === NetworkService.typeName) {
      if (method.name === "SaveNetworkProfile") profileSaved = true;
      if (method.name === "GetNetworkRoute" && profileSaved) await routeRead;
    }
    if (!lost && method.parent.typeName === NetworkService.typeName && method.name === "SelectNetworkProfile") { lost = true; throw new ConnectError("Fixture lost accepted response", Code.Unavailable); }
    return response;
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const authority = { serverId: status.serverId, endpoint: status.listener };
  render(<TransportProvider transport={scoped}><QueryClientProvider client={client}><MutationIntents><Settings pairingAuthority={authority} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  fireEvent.click(screen.getByRole("button", { name: "Network settings" }));
  fireEvent.click(await screen.findByRole("button", { name: "New network profile" }));
  fireEvent.change(screen.getByLabelText("Profile name"), { target: { value: "Integration Direct" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await screen.findByText("Integration Direct", { selector: "h4" });
  const selectButton = screen.getByRole("button", { name: "Select this revision" }) as HTMLButtonElement;
  // New profile rows can arrive before the independent route refetch. Wait for
  // its authoritative read instead of clicking an ancestor-disabled control.
  // The held real RPC makes this ordering deterministic without a sleep.
  expect(selectButton.closest("fieldset")?.disabled).toBe(true);
  releaseRouteRead();
  await waitFor(() => expect(selectButton.closest("fieldset")?.disabled).toBe(false));
  const profileOption = await screen.findByRole("option", { name: /Integration Direct/ });
  fireEvent.change(screen.getByRole("combobox", { name: "Profile to select" }), { target: { value: (profileOption as HTMLOptionElement).value } });
  await waitFor(() => expect(selectButton.closest("fieldset")?.disabled).toBe(false));
  fireEvent.click(selectButton);
  fireEvent.click(await screen.findByRole("button", { name: "Retry original route selection" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry original route selection" })).toBeNull());
  const network = createClient(NetworkService, transport);
  expect((await network.getNetworkRoute({})).route?.revision).toBe(1n);
  const grant = await createClient(DeviceService, transport).createPairing({ requestId: newRequestId(), name: "Encrypted pending UI fixture", type: DeviceType.WORKER, codeDigest: new Uint8Array(32).fill(7) });
  const recipient = { version: 1, authority: { server_id: authority.serverId, endpoint: authority.endpoint, machine_id: newRequestId(), device_id: newRequestId(), pairing_id: grant.pairing!.id }, key_id: newRequestId(), recipient: stdout.trim() };
  const file = Object.assign(new File([JSON.stringify(recipient)], "recipient.json", { type: "application/json" }), { text: async () => JSON.stringify(recipient) });
  fireEvent.change(screen.getByLabelText("Worker public recipient document"), { target: { files: [file] } });
  fireEvent.click(screen.getByText("Worker configuration transfer", { exact: true }));
  const exportButton = await screen.findByRole("button", { name: "Export current encrypted configuration" });
  await waitFor(() => expect((exportButton as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(exportButton);
  const ciphertext = (await screen.findByLabelText("Encrypted bundle (Base64)") as HTMLTextAreaElement).value;
  const digest = (screen.getByLabelText("Authenticated ciphertext digest") as HTMLInputElement).value;
  expect(createHash("sha256").update(Buffer.from(ciphertext, "base64")).digest("hex")).toBe(digest);
  expect((await network.getNetworkRoute({ machineId: recipient.authority.machine_id })).route?.revision).toBe(1n);
  expect(JSON.stringify(client.getQueryCache().getAll().map(query => query.state.data), (_key, value) => typeof value === "bigint" ? value.toString() : value)).not.toContain(ciphertext);
}, 90000);
