import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SubscriptionServiceId, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Prerequisites } from "./prerequisites";

function accountResource(subscriptionService?: SubscriptionServiceId): Resource {
  return create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.ACCOUNT, schemaVersion: subscriptionService ? 2 : 1, documentJson: encode({
    alias: "Saved account", type: subscriptionService ? "subscription" : "api",
    ...(subscriptionService ? { subscription_service: subscriptionService } : { provider_id: newRequestId() }),
    enabled: true, exclude_automatic: false, recovery_notifications: false, health: "ready", quota: [], confirmed_exhausted: false,
    connection: { id: newRequestId(), authentication: subscriptionService ? "subscription" : "bearer", connected_at: "2026-10-01T12:00:00Z" },
  }) });
}

function fixture() {
  const serverId = newRequestId();
  const report = { schema_version: 2, server_id: serverId, version: "0.1.0", protocol_version: 2, database_schema_version: 32, os: "darwin", architecture: "arm64", listener: "http://127.0.0.1:46310", credential_store: "owner-credential-ready", credentials: [], more_credentials: false, inference_probes: false, observed_at: new Date().toISOString(), database: "ready", storage: { result: { state: "observed" }, database_bytes: "4096", wal_bytes: "0", logical_database_bytes: "4096", volume_capacity_bytes: "8192", volume_available_bytes: "4096", resources: [] }, more_machines: false, machines: [{ machine_id: newRequestId(), name: "Worker", os: "darwin", architecture: "arm64", version: "0.1.0", last_seen: new Date().toISOString(), active_stream: true, disabled: false, installations: [{ harness: "codex", state: "detected", version: "0.151.0", protocol_verified: true, protocol_state: "verified", capabilities: [] as string[], observed_at: new Date().toISOString() }, ...["claude-code", "opencode", "grok-build"].map(harness => ({ harness, state: "unchecked", protocol_verified: false, capabilities: [] as string[] }))] }] };
  const account = accountResource();
  const agent = create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.AGENT, schemaVersion: 1, documentJson: encode({ harness: "codex", model_id: newRequestId(), accounts: [{ id: account.id, weight: 1 }] }) });
  const doctor = vi.fn(async () => ({ reportJson: encode(report) }));
  const accountPage = { resources: [account], nextPageToken: "" };
  const list = vi.fn(async (input: { filter?: { kind: EntityKind } }) => input.filter?.kind === EntityKind.ACCOUNT ? accountPage : { resources: [agent], nextPageToken: "" });
  const mutation = vi.fn();
  const settings = vi.fn();
  const status = vi.fn(async () => ({ serverId, version: "0.1.0", stopping: false }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: status, getDoctor: doctor, requestBackup: mutation });
    router.service(ResourceService, { listResources: list });
  });
  const rpc = vi.spyOn(transport, "unary");
  const stream = vi.spyOn(transport, "stream");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Prerequisites active={active} openSettings={settings} /></QueryClientProvider></TransportProvider>;
  return { serverId, report, account, accountPage, agent, doctor, list, mutation, rpc, stream, status, settings, view };
}

it.each(Object.values(SubscriptionServiceId))("observes saved ready %s subscriptions without invoking account or native operations", async service => {
  const f = fixture();
  f.accountPage.resources = [accountResource(service)];
  render(f.view());
  await screen.findByText("Connected to server 0.1.0.");
  expect(f.list).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("AI account: Observed");
  expect(screen.getByText("1 account(s) inspected; 1 enabled account(s) have a connection and saved ready status.")).toBeTruthy();
  expect(screen.getByText("Saved status does not prove current quota or provider access.")).toBeTruthy();
  expect(f.rpc.mock.calls.map(([method]) => method.name).sort()).toEqual(["GetDoctor", "GetStatus", "ListResources", "ListResources"]);
  expect(f.stream).not.toHaveBeenCalled();
  expect(f.mutation).not.toHaveBeenCalled();
});

it("observes a mixed API/subscription page with independent saved health", async () => {
  const f = fixture();
  const subscription = accountResource(SubscriptionServiceId.ChatGPT);
  const data = JSON.parse(new TextDecoder().decode(subscription.documentJson));
  subscription.documentJson = encode({ ...data, confirmed_exhausted: true });
  f.accountPage.resources = [f.account, subscription];
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("AI account: Observed");
  expect(screen.getByText("2 account(s) inspected; 2 enabled account(s) have a connection and saved ready status.")).toBeTruthy();
  expect(f.mutation).not.toHaveBeenCalled();
});

it.each([
  { enabled: false }, { health: "unverified" }, { health: "expired" },
  { health: "revoked" }, { health: "failed" },
  { health: "disconnected", connection: undefined },
  { health: "disconnected", connection: undefined, removal: { request_id: newRequestId(), expected_revision: 1 } },
])("keeps valid unavailable subscription accounts in Needs setup: %j", async changes => {
  const f = fixture();
  const account = accountResource(SubscriptionServiceId.ChatGPT);
  account.documentJson = encode({ ...JSON.parse(new TextDecoder().decode(account.documentJson)), ...changes });
  f.accountPage.resources = [account];
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("AI account: Needs setup");
  expect(screen.getByText("1 account(s) inspected; 0 enabled account(s) have a connection and saved ready status.")).toBeTruthy();
});

it.each([
  { type: undefined }, { type: "future" }, { type: "api" },
  { subscription_service: undefined }, { subscription_service: "future" }, { subscription_service: "" },
  { provider_id: newRequestId() }, { provider_id: "" }, { provider_id: null },
  { retired: true }, { health: "future" }, { health: undefined }, { enabled: "true" },
  { connection: null }, { connection: { id: "invalid" } },
  { connection: { id: newRequestId(), authentication: "future", connected_at: "2026-10-01T12:00:00Z" } },
  { connection: { id: newRequestId(), authentication: "subscription", connected_at: "2026-02-30T12:00:00Z" } },
  { validation: {} }, { catalog: {} }, { future_field: true },
])("invalidates the whole mixed page for malformed subscription identity/status: %j", async changes => {
  const f = fixture();
  const account = accountResource(SubscriptionServiceId.ChatGPT);
  account.documentJson = encode({ ...JSON.parse(new TextDecoder().decode(account.documentJson)), ...changes });
  f.accountPage.resources = [f.account, account];
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
  expect(screen.queryByText(/account\(s\) inspected/)).toBeNull();
});

it.each([
  "alias", "exclude_automatic", "recovery_notifications", "quota", "confirmed_exhausted",
])("requires the complete schema-2 account record before observing it: missing %s", async field => {
  const f = fixture();
  const account = accountResource(SubscriptionServiceId.ChatGPT);
  const data = JSON.parse(new TextDecoder().decode(account.documentJson)) as Record<string, unknown>;
  delete data[field];
  account.documentJson = encode(data);
  f.accountPage.resources = [account];
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
  expect(screen.queryByText(/account\(s\) inspected/)).toBeNull();
});

it.each([
  { alias: 1 }, { enabled: "true" }, { exclude_automatic: "false" },
  { recovery_notifications: 0 }, { quota: {} }, { confirmed_exhausted: "false" },
])("requires valid schema-2 account field types: %j", async change => {
  const f = fixture();
  const account = accountResource(SubscriptionServiceId.ChatGPT);
  account.documentJson = encode({ ...JSON.parse(new TextDecoder().decode(account.documentJson)), ...change });
  f.accountPage.resources = [account];
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
  expect(screen.queryByText(/account\(s\) inspected/)).toBeNull();
});

it.each([
  { type: undefined }, { type: "subscription" }, { type: "future" },
  { provider_id: undefined }, { provider_id: "invalid" },
  { subscription_service: "chatgpt" }, { subscription_service: "" }, { subscription: {} },
])("rejects malformed or mixed schema-1 API identity: %j", async changes => {
  const f = fixture();
  f.account.documentJson = encode({ ...JSON.parse(new TextDecoder().decode(f.account.documentJson)), ...changes });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
});

it.each([
  { schemaVersion: 0 }, { schemaVersion: 1 }, { schemaVersion: 3 },
  { revision: 0n }, { kind: EntityKind.MODEL }, { id: "invalid" },
  { projectId: newRequestId() }, { sessionId: newRequestId() },
  { documentJson: new TextEncoder().encode("{invalid") }, { documentJson: new Uint8Array([0xff]) },
  { documentJson: new Uint8Array((1 << 20) + 1) },
])("rejects unsupported, foreign or unreadable subscription resources: $schemaVersion $kind", async changes => {
  const f = fixture();
  f.accountPage.resources = [f.account, create(ResourceSchema, { ...accountResource(SubscriptionServiceId.ChatGPT), ...changes })];
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
});

it.each([2, 51])("rejects duplicate or oversized account inventories (%i records)", async count => {
  const f = fixture();
  f.accountPage.resources = count === 2 ? [f.account, f.account] : Array.from({ length: count }, () => accountResource(SubscriptionServiceId.ChatGPT));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
});

it("preserves partial-page uncertainty and does not infer later account health", async () => {
  const f = fixture();
  f.accountPage.resources = [accountResource(SubscriptionServiceId.ChatGPT)];
  f.accountPage.nextPageToken = "more";
  f.accountPage.resources[0]!.documentJson = encode({ type: "subscription", subscription_service: "chatgpt", enabled: true, health: "disconnected" });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText(/not a successful execution test/);
  expect(screen.getByText("AI account: Unknown")).toBeTruthy();
  expect(screen.getByText("More accounts exist. Open AI accounts for the remaining records.")).toBeTruthy();
  f.accountPage.resources = [accountResource(SubscriptionServiceId.ChatGPT)];
  fireEvent.click(screen.getByRole("button", { name: "Refresh prerequisites" }));
  await screen.findByText("AI account: Observed");
  expect(screen.getByText("More accounts exist. Open AI accounts for the remaining records.")).toBeTruthy();
  expect(f.list).toHaveBeenCalledTimes(4);
});

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

it("accepts a valid schema-3 routed Worker alongside a current single-source Worker", async () => {
  const f = fixture();
  const routed = create(ResourceSchema, { ...f.agent, id: newRequestId(), schemaVersion: 3, documentJson: encode({ harness: "codex", routes: [{ model_id: newRequestId(), accounts: [{ id: newRequestId(), weight: 1 }], routing: "priority" }] }) });
  f.list.mockImplementation(async input => ({ resources: input.filter?.kind === EntityKind.ACCOUNT ? [f.account] : [f.agent, routed], nextPageToken: "" }));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Agent Worker configuration: Observed");
});

it("accepts a valid schema-2 subscription account alongside an API account", async () => {
  const f = fixture();
  const subscription = create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ alias: "Subscription account", type: "subscription", subscription_service: "chatgpt", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [], confirmed_exhausted: false }) });
  f.list.mockImplementation(async input => ({ resources: input.filter?.kind === EntityKind.ACCOUNT ? [f.account, subscription] : [f.agent], nextPageToken: "" }));
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("AI account: Observed");
  expect(screen.getByText("2 account(s) inspected; 1 enabled account(s) have a connection and saved ready status.")).toBeTruthy();
});

it("does not retain successful checks after a failed refresh or count disabled/disconnected accounts as ready", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Server diagnostics: Observed");
  f.doctor.mockRejectedValueOnce(new ConnectError("private native failure", Code.Unavailable));
  f.list.mockResolvedValueOnce({ resources: [create(ResourceSchema, { ...f.account, documentJson: encode({ ...JSON.parse(new TextDecoder().decode(f.account.documentJson)), enabled: false }) })], nextPageToken: "" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh prerequisites" }));
  await screen.findByText("Server diagnostics: Check failed");
  expect(screen.queryByText("Runner Device and harness: Observed")).toBeNull();
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
  expect(screen.queryByText("Runner Device and harness: Observed")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "View prerequisites in Settings" }));
  expect(f.settings).toHaveBeenCalledTimes(1);
  expect(f.mutation).not.toHaveBeenCalled();
});

it("does not mistake a verified but offline or disabled Worker for a connected harness", async () => {
  const f = fixture();
  f.report.machines[0]!.active_stream = false;
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Runner Device and harness: Needs setup");
  expect(screen.getByText(/0 enabled Worker\(s\) had an active connection; 0 retained harness/)).toBeTruthy();
  f.report.machines[0]!.active_stream = true;
  f.report.machines[0]!.disabled = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh prerequisites" }));
  await waitFor(() => expect(f.doctor).toHaveBeenCalledTimes(2));
  await screen.findByText("Runner Device and harness: Needs setup");
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
  expect(screen.getByText("Runner Device and harness: Unknown")).toBeTruthy();
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
  await screen.findByText("Runner Device and harness: Needs setup");
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
  expect(screen.getByText("Runner Device and harness: Unknown")).toBeTruthy();
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
  expect(screen.getByText("Runner Device and harness: Unknown")).toBeTruthy();
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
  expect(screen.getByText("Runner Device and harness: Unknown")).toBeTruthy();
});
it("retains valid failed storage separately from a complete Worker observation", async () => {
  const f = fixture();
  f.doctor.mockResolvedValue({ reportJson: encode({ ...f.report, database: "failed", storage: { result: { state: "failed", code: "permission_denied" }, resources: [] } }) });
  render(f.view()); fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Runner Device and harness: Observed");
  expect(screen.getByText("Server diagnostics: Needs setup")).toBeTruthy();
});


it("accepts every retained resource kind including native children without inferring readiness", async () => {
  const f = fixture();
  const kinds = ["pairing", "project", "repository", "agent", "account", "provider", "model", "machine", "session", "template", "settings", "schedule", "occurrence", "message", "queue", "steer", "interaction", "review", "snapshot", "device", "integration", "pull_request", "problem", "inbox", "usage", "job", "routing", "forward", "subagent"];
  f.doctor.mockResolvedValue({ reportJson: encode({ ...f.report, storage: { ...f.report.storage, resources: kinds.map(kind => ({ kind, count: "18446744073709551615" })) } }) });
  render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Check prerequisites" }));
  await screen.findByText("Server diagnostics: Observed");
  expect(screen.getByText("Runner Device and harness: Observed")).toBeTruthy();
  expect(screen.getByText(/not a successful execution test/)).toBeTruthy();
  expect(f.mutation).not.toHaveBeenCalled();
});
