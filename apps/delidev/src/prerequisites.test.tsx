import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Prerequisites } from "./prerequisites";

function fixture() {
  const serverId = newRequestId();
  const report = { schema_version: 2, server_id: serverId, version: "0.1.0", inference_probes: false, observed_at: new Date().toISOString(), database: "ready", storage: { result: { state: "observed" } }, more_machines: false, machines: [{ machine_id: newRequestId(), active_stream: true, disabled: false, installations: [{ harness: "codex", state: "detected", version: "0.151.0", protocol_verified: true, protocol_state: "verified" }] }] };
  const account = create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ provider_id: newRequestId(), enabled: true, health: "ready", connection: { id: newRequestId() } }) });
  const agent = create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.AGENT, schemaVersion: 1, documentJson: encode({ harness: "codex", model_id: newRequestId(), accounts: [] }) });
  const doctor = vi.fn(async () => ({ reportJson: encode(report) }));
  const list = vi.fn(async (input: { filter?: { kind: EntityKind } }) => ({ resources: input.filter?.kind === EntityKind.ACCOUNT ? [account] : [agent], nextPageToken: "" }));
  const mutation = vi.fn();
  const settings = vi.fn();
  const status = vi.fn(async () => ({ serverId, version: "0.1.0", stopping: false }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: status, getDoctor: doctor, createBackup: mutation });
    router.service(ResourceService, { listResources: list });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Prerequisites active={active} openSettings={settings} /></QueryClientProvider></TransportProvider>;
  return { serverId, report, account, agent, doctor, list, mutation, status, settings, view };
}

it("checks live read surfaces only after the user's action and distinguishes observations from readiness", async () => {
  const f = fixture();
  const view = render(f.view(false));
  expect(f.status).not.toHaveBeenCalled();
  expect(f.doctor).not.toHaveBeenCalled();
  expect(f.list).not.toHaveBeenCalled();
  view.rerender(f.view());
  await screen.findByText("Connected to server 0.1.0.");
  expect(f.doctor).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("AI account: Observed");
  expect(screen.getByText(/1 enabled Worker\(s\) had an active connection; 1 retained harness/)).toBeTruthy();
  expect(screen.getByText("Agent Worker configuration: Observed")).toBeTruthy();
  expect(screen.getByText(/not a successful execution test/)).toBeTruthy();
  expect(f.list).toHaveBeenCalledTimes(2);
  expect(f.doctor).toHaveBeenCalledTimes(1);
  expect(f.mutation).not.toHaveBeenCalled();
  view.rerender(f.view(false));
  expect((screen.getByRole("button", { name: "Refresh prerequisites" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.doctor).toHaveBeenCalledTimes(1);
});

it("does not retain successful checks after a failed refresh or count disabled/disconnected accounts as ready", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Server diagnostics: Observed");
  f.doctor.mockRejectedValueOnce(new ConnectError("private native failure", Code.Unavailable));
  f.list.mockResolvedValueOnce({ resources: [create(ResourceSchema, { ...f.account, documentJson: encode({ provider_id: newRequestId(), enabled: false, health: "ready", connection: { id: newRequestId() } }) })], nextPageToken: "" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh prerequisites" }));
  await screen.findByText("Server diagnostics: Check failed");
  expect(screen.queryByText("Execution Worker and harness: Observed")).toBeNull();
  expect(screen.queryByText("private native failure")).toBeNull();
  await screen.findByText("AI account: Needs setup");
});

it("keeps partial and malformed inventories unknown and rejects a foreign diagnostic identity", async () => {
  const f = fixture();
  f.report.server_id = newRequestId();
  f.list.mockImplementation(async input => input.filter?.kind === EntityKind.ACCOUNT ? { resources: [], nextPageToken: "more" } : { resources: [f.agent, f.agent], nextPageToken: "" });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await waitFor(() => expect(f.doctor).toHaveBeenCalledTimes(1));
  await screen.findByText("More accounts exist. Open AI accounts for the remaining records.");
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
  expect(screen.getByText("Agent Worker configuration: Unknown")).toBeTruthy();
  expect(screen.getByText("Server diagnostics: Unknown")).toBeTruthy();
  expect(screen.queryByText("Execution Worker and harness: Observed")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "View prerequisites in Settings" }));
  expect(f.settings).toHaveBeenCalledTimes(1);
  expect(f.mutation).not.toHaveBeenCalled();
});

it("does not mistake a verified but offline or disabled Worker for a connected harness", async () => {
  const f = fixture();
  f.report.machines[0]!.active_stream = false;
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Execution Worker and harness: Needs setup");
  expect(screen.getByText(/0 enabled Worker\(s\) had an active connection; 0 retained harness/)).toBeTruthy();
  f.report.machines[0]!.active_stream = true;
  f.report.machines[0]!.disabled = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh prerequisites" }));
  await waitFor(() => expect(f.doctor).toHaveBeenCalledTimes(2));
  await screen.findByText("Execution Worker and harness: Needs setup");
});
