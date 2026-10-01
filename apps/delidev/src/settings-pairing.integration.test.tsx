// SPDX-License-Identifier: Apache-2.0
import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { webcrypto } from "node:crypto";
import { createClient, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceService, SystemService } from "@delinoio/delidev-api-client";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document } from "./documents";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

for (const kind of ["client", "worker"] as const) it(`issues a real single-use ${kind} grant from Settings and observes consumption`, async () => {
  const { directory, transport, scope, runCLI } = fixture;
  vi.stubGlobal("crypto", webcrypto);
  const endpoint = JSON.parse(await readFile(join(scope, "server.json"), "utf8"));
  const authority = { endpoint: endpoint.url as string, serverId: (await createClient(SystemService, transport).getStatus({})).serverId };
  let delayedRead = false;
  let releaseRead!: () => void;
  const readGate = new Promise<void>((resolve) => { releaseRead = resolve; });
  // Exercise the real issuance and verification path beyond Testing Library's
  // one-second default. Shared CI load can delay issuance or its fresh read; a grant
  // must still stay private until its first fresh observation arrives.
  const grantTransport: Transport = { ...transport, async unary(method, signal, timeout, header, input, context) {
    const response = await transport.unary(method, signal, timeout, header, input, context);
    if (!delayedRead && method.parent.typeName === ResourceService.typeName && method.name === "GetResource" && "kind" in input && input.kind === EntityKind.PAIRING) {
      delayedRead = true;
      await readGate;
      await new Promise<void>((resolve) => setTimeout(resolve, 1200));
    }
    return response;
  } };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  render(<TransportProvider transport={grantTransport}><QueryClientProvider client={client}><MutationIntents><Settings pairingAuthority={authority} /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  fireEvent.click(screen.getByRole("button", { name: "Create pairing document" }));
  fireEvent.change(screen.getByLabelText("Device name"), { target: { value: `Disposable ${kind}` } });
  fireEvent.change(screen.getByLabelText("Device type"), { target: { value: kind } });
  fireEvent.click(screen.getByRole("button", { name: "Issue single-use document" }));
  // Keep the private code hidden while a successful fresh server read is pending.
  // If that read is unavailable, exercise the explicit refresh path once.
  try {
    await waitFor(() => expect(delayedRead || screen.queryByText("Grant issued; current use status is unavailable.")).toBeTruthy(), { timeout: 5000 });
    if (delayedRead) {
      expect(screen.queryByRole("button", { name: "Reveal private document" })).toBeNull();
      expect(screen.queryByLabelText("Private pairing document")).toBeNull();
    }
  } finally { releaseRead(); }
  const reveal = screen.queryByRole("button", { name: "Reveal private document" });
  if (reveal) {
    fireEvent.click(reveal);
  } else {
    const refresh = screen.getByRole("button", { name: "Refresh pairing status" }) as HTMLButtonElement;
    await waitFor(() => expect(refresh.disabled).toBe(false), { timeout: 5000 });
    expect(screen.queryByLabelText("Private pairing document")).toBeNull();
    fireEvent.click(refresh);
    fireEvent.click(await screen.findByRole("button", { name: "Reveal private document" }, { timeout: 5000 }));
  }
  const raw = (screen.getByLabelText("Private pairing document") as HTMLTextAreaElement).value;
  const grant = JSON.parse(raw);
  const pair = (path: string) => runCLI([kind === "client" ? "device" : "worker", "pair", kind === "client" ? "--device-dir" : "--worker-dir", path, "--code-stdin", "--name", "Disposable UI grant"], raw);
  const target = join(directory, `ui-pairing-${kind}`);
  await pair(target);
  const credential = JSON.parse(await readFile(join(target, "device.json"), "utf8"));
  expect(credential.server_id).toBe(authority.serverId);
  expect(credential.pairing_id).toBe(grant.pairing_id);
  expect(credential.type).toBe(kind);
  await expect(pair(join(directory, `ui-reused-${kind}`))).rejects.toThrow("Owned fixture CLI failed");
  fireEvent.click(screen.getByRole("button", { name: "Refresh pairing status" }));
  await screen.findByText("Pairing document was used. Its private code has been cleared.", {}, { timeout: 5000 });
  expect(screen.queryByLabelText("Private pairing document")).toBeNull();
  expect(JSON.stringify(client.getQueryCache().getAll().map((query) => [query.queryKey, query.state.data]), (_key, value) => typeof value === "bigint" ? value.toString() : value)).not.toContain(grant.code);
}, 30000);

