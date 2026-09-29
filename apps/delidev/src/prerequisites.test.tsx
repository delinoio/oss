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
  const report = { schema_version: 2, server_id: serverId, version: "0.1.0", protocol_version: 1, database_schema_version: 24, os: "darwin", architecture: "arm64", listener: "http://127.0.0.1:46310", credential_store: "owner-credential-ready", credentials: [], more_credentials: false, inference_probes: false, observed_at: new Date().toISOString(), database: "ready", storage: { result: { state: "observed" }, database_bytes: "4096", wal_bytes: "0", logical_database_bytes: "4096", volume_capacity_bytes: "8192", volume_available_bytes: "4096", resources: [] }, more_machines: false, machines: [{ machine_id: newRequestId(), name: "Worker", os: "darwin", architecture: "arm64", version: "0.1.0", last_seen: new Date().toISOString(), active_stream: true, disabled: false, installations: [{ harness: "codex", state: "detected", version: "0.151.0", protocol_verified: true, protocol_state: "verified", capabilities: [] as string[], observed_at: new Date().toISOString() }, ...["claude-code", "opencode", "grok-build"].map(harness => ({ harness, state: "unchecked", protocol_verified: false, capabilities: [] as string[] }))] }] };
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

it.each([
  { state: "future" }, { protocol_state: "future" }, { protocol_verified: false },
  { protocol_state: "failed", problem_code: "unavailable" }, { state: "missing" },
  { version: "bad-version" }, { version: 151 }, { capabilities: undefined },
  { capabilities: "execute" }, { capabilities: ["future"] }, { capabilities: ["usage", "usage"] },
  { observed_at: undefined }, { observed_at: 1 }, { observed_at: "not-a-time" },
  { problem_code: "not_found" }, { guidance: false }, { guidance: "bad\0guidance" },
  { native_output: "unrecognized shape" },
])("keeps malformed installation observations unknown: %j", async changes => {
  const f = fixture();
  Object.assign(f.report.machines[0]!.installations[0]!, changes);
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("Server diagnostics: Unknown")).toBeTruthy();
  expect(screen.getByText("Execution Worker and harness: Unknown")).toBeTruthy();
  expect(screen.queryByText(/retained harness handshake/)).toBeNull();
});

it.each([
  { harness: "codex", state: "unchecked", protocol_verified: false, capabilities: [] },
  { harness: "codex", state: "missing", protocol_verified: false, capabilities: [], observed_at: new Date().toISOString(), problem_code: "not_found" },
  { harness: "codex", state: "detected", version: "0.151.0", protocol_verified: false, capabilities: [], observed_at: new Date().toISOString() },
  { harness: "codex", state: "detected", version: "0.151.0", protocol_verified: false, protocol_state: "unsupported", capabilities: [], observed_at: new Date().toISOString(), problem_code: "unsupported" },
  { harness: "codex", state: "detected", version: "0.151.0", protocol_verified: false, protocol_state: "failed", capabilities: [], observed_at: new Date().toISOString(), problem_code: "unavailable" },
])("keeps a valid non-ready installation distinct from malformed data: %j", async installation => {
  const f = fixture();
  f.doctor.mockImplementation(async () => ({ reportJson: encode({ ...f.report, machines: [{ ...f.report.machines[0], installations: [installation, ...f.report.machines[0]!.installations.slice(1)] }] }) }));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Execution Worker and harness: Needs setup");
  expect(screen.getByText("Server diagnostics: Observed")).toBeTruthy();
});

it.each([
  { database: undefined }, { database: "future" }, { storage: undefined }, { storage: {} },
  { protocol_version: undefined }, { protocol_version: "1" }, { protocol_version: 0 },
  { database_schema_version: undefined }, { database_schema_version: 1.5 },
  { version: undefined }, { os: undefined }, { os: "future" }, { architecture: "x86" },
  { listener: undefined }, { listener: "https://secret:token@example.test" },
  { credential_store: undefined }, { credential_store: "ready" },
  { observed_at: "2026-02-30T12:00:00Z" }, { observed_at: "2026-09-29" },
  { credentials: undefined }, { credentials: null }, { more_credentials: undefined },
  { credentials: [{ account_id: newRequestId(), result: { state: "future" } }] },
  { credentials: [{ account_id: newRequestId(), result: { state: "observed" } }] },
  { credentials: [{ account_id: newRequestId(), result: { state: "failed", code: "future" } }] },
  { native_output: "unexpected report field" },
])("rejects an incomplete or invalid enclosing report: %j", async changes => {
  const f = fixture(); Object.assign(f.report, changes);
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("Server diagnostics: Unknown")).toBeTruthy();
  expect(screen.getByText("Execution Worker and harness: Unknown")).toBeTruthy();
});
it.each([
  { name: undefined }, { name: "" }, { os: undefined }, { os: "future" },
  { architecture: undefined }, { architecture: "x64" }, { version: undefined },
  { last_seen: undefined }, { last_seen: "2026-02-30T12:00:00Z" },
  { installations: [] }, { native_path: "unexpected field" },
])("rejects a partial machine despite its valid detected installation: %j", async changes => {
  const f = fixture(); Object.assign(f.report.machines[0]!, changes);
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("Execution Worker and harness: Unknown")).toBeTruthy();
});
it.each([
  { result: { state: "future" } }, { result: { state: "observed", code: "unavailable" } },
  { database_bytes: undefined }, { wal_bytes: 0 }, { logical_database_bytes: "18446744073709551616" },
  { resources: undefined }, { resources: [{ kind: "future", count: "1" }] },
  { resources: [{ kind: "session", count: "01" }] },
  { resources: [{ kind: "session", count: "1" }, { kind: "session", count: "2" }] },
])("rejects malformed storage without granting Worker readiness: %j", async changes => {
  const f = fixture(); Object.assign(f.report.storage, changes);
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("Execution Worker and harness: Unknown")).toBeTruthy();
});
it("retains valid failed storage separately from a complete Worker observation", async () => {
  const f = fixture();
  f.doctor.mockResolvedValue({ reportJson: encode({ ...f.report, database: "failed", storage: { result: { state: "failed", code: "permission_denied" }, resources: [] } }) });
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Execution Worker and harness: Observed");
  expect(screen.getByText("Server diagnostics: Needs setup")).toBeTruthy();
});
