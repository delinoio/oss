// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { InstallationService, ResourceSchema, EntityKind, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SSHSetup } from "./ssh-setup";
function fixture(supported = true) {
 const id = newRequestId(), fingerprint = "SHA256:trusted-fixture";
 let resource = create(ResourceSchema, { id, kind: EntityKind.SSH_SETUP, schemaVersion: 1, revision: 1n, documentJson: encode({ state: "OBSERVED", target: { host: "host.example", port: 22, user: "runner" }, identity: { algorithm: "ssh-ed25519", fingerprint } }) });
 const inspect = vi.fn(async () => ({ setup: resource })), start = vi.fn(async () => { throw new ConnectError("Lost response", Code.Unavailable); });
 const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: () => ({ capabilities: supported ? [SystemCapability.SSH_WORKER_SETUP_V1] : [] }) }); router.service(InstallationService, { inspectSSHHost: inspect, getSSHSetup: () => ({ setup: resource }), startSSHSetup: start }); });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SSHSetup active /></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getByText("Set up a Worker over SSH"));
 return { inspect, start, client, fingerprint, setRequested: () => { resource = create(ResourceSchema, { ...resource, revision: 2n, documentJson: encode({ state: "REQUESTED", identity: { fingerprint } }) }); } };
}
it("checks capability before exposing write-only credentials", async () => { fixture(false); await screen.findByText("This server requires an update to support SSH Worker setup."); expect(screen.queryByLabelText("SSH private key")).toBeNull(); });
it("requires host confirmation, clears secrets and never retries an ambiguous installation", async () => {
 const f = fixture(); await screen.findByRole("button", { name: "Inspect host identity" });
 fireEvent.change(screen.getByLabelText("SSH host"), { target: { value: "host.example" } }); fireEvent.change(screen.getByLabelText("SSH user"), { target: { value: "runner" } }); fireEvent.click(screen.getByRole("button", { name: "Inspect host identity" }));
 const install = await screen.findByRole("button", { name: "Install and start Worker" }); expect((install as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByLabelText("I verified this exact host fingerprint through a trusted channel")); fireEvent.change(screen.getByLabelText("Runner name"), { target: { value: "Remote runner" } });
 const secret = screen.getByLabelText("SSH private key") as HTMLTextAreaElement; fireEvent.change(secret, { target: { value: "secret-sentinel" } });
 fireEvent.click(install); await screen.findByText(/original start response is unconfirmed/);
 expect(secret.value).toBe(""); expect(f.start).toHaveBeenCalledTimes(1);
 expect(JSON.stringify(f.client.getMutationCache().getAll().map(m => m.state.variables))).not.toContain("secret-sentinel");
 expect((install as HTMLButtonElement).disabled).toBe(true);
 f.setRequested(); fireEvent.click(screen.getByRole("button", { name: "Inspect original setup" })); await waitFor(() => expect(screen.queryByRole("button", { name: "Install and start Worker" })).toBeNull()); expect(f.start).toHaveBeenCalledTimes(1);
});
