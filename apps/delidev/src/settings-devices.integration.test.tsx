// SPDX-License-Identifier: Apache-2.0
import { readFile } from "node:fs/promises";
import { type Server } from "node:http";
import { join } from "node:path";
import { createClient } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { EntityKind, ResourceService, SystemService, createDeliDevTransport } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("revokes a real paired client through settings and reads bounded server diagnostics", async () => {
  const { directory, transport, runCLI } = fixture;
  const clientRoot = join(directory, "disposable-client");
  await runCLI(["device", "pair-local", "--device-dir", clientRoot]);
  const credential = JSON.parse(await readFile(join(clientRoot, "device.json"), "utf8"));
  const paired = createDeliDevTransport({ origin: credential.endpoint as string, getToken: () => credential.token as string });
  await createClient(SystemService, paired).getStatus({});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  fireEvent.click(await screen.findByRole("button", { name: "Revoke DeliDev desktop" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "Confirm device revocation" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Confirm device revocation" }));
  await screen.findByText("Authorization revoked for DeliDev desktop.");
  expect(screen.getByText("Retained sessions stay saved. Revocation does not confirm native cleanup or erase the device's private files.")).toBeTruthy();
  await expect(createClient(SystemService, paired).getStatus({})).rejects.toMatchObject({ code: 16 });
  const retained = await createClient(ResourceService, transport).getResource({ kind: EntityKind.DEVICE, id: credential.device_id as string });
  expect(document(retained.resource).revoked).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Return to devices" }));
  await waitFor(() => expect(window.document.activeElement).toBe(screen.getByRole("button", { name: "Details for DeliDev desktop" })));
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  await screen.findByText("Server owner credential loaded");
  expect(screen.getByText("Read succeeded")).toBeTruthy();
  expect(screen.getByText("Not performed")).toBeTruthy();
  expect(screen.getByRole("region", { name: "Storage diagnostics" })).toBeTruthy();
  expect(screen.getByText("Logical database size").nextElementSibling?.textContent).toMatch(/^[0-9,]+ bytes$/);
  expect(screen.getByRole("region", { name: "Worker diagnostics" })).toBeTruthy();
  expect(screen.queryByRole("region", { name: "Protected credential diagnostics" })).toBeNull();
  expect(screen.queryByRole("region", { name: "Account storage" })).toBeNull();
  expect(screen.queryByText(/legacy report/)).toBeNull();
}, 15000);
