import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { DeviceService, EntityKind, ResourceSchema, ResourceService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { DeviceRevocation, Doctor } from "./device-settings";
import { Settings } from "./settings";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";

function fixture() {
  const original = create(ResourceSchema, { kind: EntityKind.DEVICE, id: newRequestId(), revision: 3n, schemaVersion: 1, documentJson: encode({ name: "Paired desktop", type: "client", revoked: false, paired_at: "2026-09-25T00:00:00Z" }) });
  const state = { current: original };
  const revoke = vi.fn(async (_request: unknown) => ({ device: create(ResourceSchema, { ...original, revision: 4n, documentJson: encode({ ...document(original), revoked: true }) }) }));
  const doctor = vi.fn(async () => ({ reportJson: encode({ version: "0.1.0", database: "ready", credential_store: "owner-credential-ready", inference_probes: false, server_id: newRequestId(), listener: "http://127.0.0.1:46310" }) }));
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, { getResource: () => ({ resource: state.current }), listResources: (request) => ({ resources: request.filter?.kind === EntityKind.DEVICE ? [state.current] : [] }) });
    router.service(DeviceService, { revokeDevice: revoke });
    router.service(SystemService, { getDoctor: doctor });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const view = (children: React.ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{children}</MutationIntents></QueryClientProvider></TransportProvider>;
  return { original, state, revoke, doctor, client, view };
}
it("retains the original revocation after uncertainty and a peer revision", async () => {
  const value = fixture();
  value.revoke.mockRejectedValueOnce(new ConnectError("lost response", Code.Unavailable));
  render(value.view(<DeviceRevocation initial={value.original} active close={() => {}} revoked={() => {}} />));
  await waitFor(() => expect((screen.getByRole("button", { name: "Confirm device revocation" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Confirm device revocation" }));
  await screen.findByRole("button", { name: "Retry original device revocation" });
  value.state.current = create(ResourceSchema, { ...value.original, revision: 4n });
  await value.client.invalidateQueries();
  await screen.findByText(/This device changed after the confirmation/);
  fireEvent.click(screen.getByRole("button", { name: "Retry original device revocation" }));
  await screen.findByText("Authorization revoked for Paired desktop.");
  expect(value.revoke).toHaveBeenCalledTimes(2);
  expect(value.revoke.mock.calls[0][0]).toEqual(value.revoke.mock.calls[1][0]);
  expect(value.revoke.mock.calls[0][0]).toMatchObject({ mutation: { id: value.original.id, expectedRevision: 3n } });
});
it("preserves self-revocation confirmation across modal visibility and blocks a stale new request", async () => {
  const value = fixture();
  const view = render(value.view(<Settings visible currentDeviceId={value.original.id} close={() => {}} />));
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  fireEvent.click(await screen.findByRole("button", { name: "Revoke Paired desktop" }));
  await screen.findByText(/This is the current desktop's authorization/);
  view.rerender(value.view(<Settings visible={false} currentDeviceId={value.original.id} close={() => {}} />));
  value.state.current = create(ResourceSchema, { ...value.original, revision: 5n });
  view.rerender(value.view(<Settings visible currentDeviceId={value.original.id} close={() => {}} />));
  await value.client.invalidateQueries();
  await screen.findByText(/This device changed after the confirmation/);
  expect((screen.getByRole("button", { name: "Confirm device revocation" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.revoke).not.toHaveBeenCalled();
});
it("runs diagnostics only while selected and distinguishes owner evidence from credential-store health", async () => {
  const value = fixture();
  const view = render(value.view(<Doctor active={false} />));
  expect(value.doctor).not.toHaveBeenCalled();
  view.rerender(value.view(<Doctor active />));
  await screen.findByText("Server owner credential loaded");
  expect(screen.getByText(/Capacity and protected-storage health are not yet reported/)).toBeTruthy();
  expect(screen.getByText("Not performed")).toBeTruthy();
  expect(value.revoke).not.toHaveBeenCalled();
  value.doctor.mockResolvedValueOnce({ reportJson: new Uint8Array([255]) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh diagnostics" }));
  await screen.findByText(/diagnostic report is unavailable or malformed/);
  expect(screen.queryByText("Read succeeded")).toBeNull();
});
