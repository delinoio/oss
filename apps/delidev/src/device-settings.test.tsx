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
it("excludes the current desktop from revocation and guards direct confirmations", async () => {
  const value = fixture();
  const view = render(value.view(<Settings visible currentDeviceId={value.original.id} />));
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  await screen.findByText("This desktop client cannot revoke its own registration.");
  expect(screen.queryByRole("button", { name: "Revoke Paired desktop" })).toBeNull();
  view.unmount();
  render(value.view(<DeviceRevocation initial={value.original} currentDeviceId={value.original.id} active close={() => {}} revoked={() => {}} />));
  await screen.findByText(/It cannot be revoked from this app/);
  const confirm = screen.getByRole("button", { name: "Confirm device revocation" }) as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  fireEvent.click(confirm);
  expect(value.revoke).not.toHaveBeenCalled();
});
it("discards another device's confirmation on close and reads its new revision", async () => {
  const value = fixture();
  const current = newRequestId();
  const view = render(value.view(<Settings visible currentDeviceId={current} />));
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  fireEvent.click(await screen.findByRole("button", { name: "Revoke Paired desktop" }));
  view.rerender(value.view(<Settings visible={false} currentDeviceId={current} />));
  value.state.current = create(ResourceSchema, { ...value.original, revision: 5n });
  view.rerender(value.view(<Settings visible currentDeviceId={current} />));
  expect(screen.getByRole("button", { name: "AI Subscription" }).getAttribute("aria-current")).toBe("page");
  fireEvent.click(screen.getByRole("button", { name: "Paired devices" }));
  await screen.findByRole("button", { name: "Revoke Paired desktop" });
  expect(screen.queryByRole("button", { name: "Confirm device revocation" })).toBeNull();
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

function expandedReport() {
  return { schema_version: 2, version: "0.1.0", observed_at: "2026-09-25T12:34:56Z", database: "ready", credential_store: "owner-credential-ready", inference_probes: false, protocol_version: 1, database_schema_version: 13, os: "linux", architecture: "amd64", storage: { result: { state: "failed", code: "permission_denied", guidance: "Inspect storage permissions." }, logical_database_bytes: "9007199254740993", volume_available_bytes: "0", resources: [{ kind: "session", count: "42" }] }, machines: [{ machine_id: newRequestId(), name: "Retained Worker", os: "darwin", architecture: "arm64", version: "0.1.0", active_stream: false, disabled: false, last_seen: "2026-09-25T01:00:00Z", installations: [{ harness: "codex", state: "detected", version: "0.151.0", capabilities: [], protocol_verified: false, protocol_state: "failed" }] }], more_machines: false, credentials: [{ account_id: newRequestId(), connection_id: newRequestId(), result: { state: "superseded", code: "conflict", guidance: "Refresh for the current connection." } }], more_credentials: true };
}
it("shows partial server storage and large counters without duplicating account observations", async () => {
  const value = fixture();
  value.doctor.mockResolvedValue({ reportJson: encode(expandedReport()) });
  render(value.view(<Doctor active />));
  await screen.findByText("Inspect storage permissions.");
  expect(screen.getByText(`${9007199254740993n.toLocaleString()} bytes`)).toBeTruthy();
  expect(screen.getByText("0 bytes")).toBeTruthy();
  expect(screen.getAllByText("Unavailable").length).toBe(3);
  expect(screen.getByText("session: 42")).toBeTruthy();
  expect(screen.getByText(/No active stream observed/)).toBeTruthy();
  expect(screen.getByText(/Handshake failed/)).toBeTruthy();
  expect(screen.queryByText("Connection changed during inspection")).toBeNull();
  expect(screen.queryByText(/Only the first 50 accounts are included/)).toBeNull();
  expect(screen.queryByText("Saved credential readable")).toBeNull();
  expect(value.revoke).not.toHaveBeenCalled();
});
it("rejects future report versions and imprecise byte numbers", async () => {
  const value = fixture();
  const report = expandedReport();
  value.doctor.mockResolvedValueOnce({ reportJson: encode({ ...report, schema_version: 999 }) });
  render(value.view(<Doctor active />));
  await screen.findByText(/diagnostic report version is unsupported/);
  expect(screen.queryByText("Read succeeded")).toBeNull();
  value.doctor.mockResolvedValueOnce({ reportJson: encode({ ...report, storage: { ...report.storage, logical_database_bytes: 9007199254740992, database_bytes: "18446744073709551616" } }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh diagnostics" }));
  await screen.findByText("Logical database size");
  expect(screen.getByText("Logical database size").nextElementSibling?.textContent).toBe("Unavailable");
  expect(screen.getByText("Database file").nextElementSibling?.textContent).toBe("Unavailable");
});
it("labels retained diagnostics when refresh fails and renders guidance only as text", async () => {
  const value = fixture();
  const report = expandedReport();
  report.storage.result.guidance = '<img src="x" onerror="alert(1)">';
  value.doctor.mockResolvedValueOnce({ reportJson: encode(report) });
  const view = render(value.view(<Doctor active />));
  await screen.findByText(report.storage.result.guidance);
  expect(view.container.querySelector("img")).toBeNull();
  value.doctor.mockRejectedValueOnce(new ConnectError("fixture offline", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh diagnostics" }));
  await screen.findByText(/report below is the last returned observation/);
  expect(screen.getByText("2026-09-25T12:34:56Z")).toBeTruthy();
});
