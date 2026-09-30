import { createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { StrictMode } from "react";
import { Desktop } from "./desktop";
import { SavedConnectionState, type SavedConnection } from "./saved-connections";
import { LocalWorkerAction, LocalWorkerState } from "./local-worker-controls";
import { LocalServerState } from "./local-server";

const bridge = vi.hoisted(() => ({ invoke: vi.fn(), createTransport: vi.fn(), listen: vi.fn() }));
vi.mock("@tauri-apps/api/event", () => ({ listen: bridge.listen }));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => true, invoke: bridge.invoke }));
vi.mock("@delinoio/delidev-api-client", async (original) => ({ ...await original<typeof import("@delinoio/delidev-api-client")>(), createDeliDevTransport: (...args: unknown[]) => bridge.createTransport(...args) }));
beforeEach(() => { bridge.invoke.mockReset(); bridge.createTransport.mockReset(); bridge.listen.mockReset().mockResolvedValue(() => {}); });
it("starts from the joined native launch and keeps permission guidance in transport-independent troubleshooting", async () => {
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") return null;
    if (command === "local_server_status") return { state: LocalServerState.Blocked, attempts: 1, retry_ms: 0, failure: "permission-denied" };
    if (command === "launch_connection") throw "permission-denied";
    throw new Error("Unexpected native authority");
  });
  render(<Desktop />);
  await screen.findByRole("button", { name: "Retry" });
  expect(screen.queryByRole("button", { name: "Start or connect" })).toBeNull();
  expect(screen.queryByText(/0700 for private directories/)).toBeTruthy(); // mounted, hidden controls
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "launch_connection")).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Troubleshooting" }));
  const problem = screen.getByText(/0700 for private directories/);
  expect(problem.textContent).toContain("selected device is authorized");
  expect(problem.textContent).toContain("accessible only to you");
  expect(problem.textContent).toContain("Preserve existing data");
  expect(screen.getByRole("dialog", { name: "Connection & diagnostics" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Saved servers" })).toBeTruthy();
  expect(bridge.invoke.mock.calls.some(([command]) => ["connect_local", "inspect_local_registration", "recover_local_registration"].includes(command))).toBe(false);
  expect(bridge.createTransport).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Re-register this desktop" })).toBeNull();
});

function localFixture() {
  const connection = { endpoint: "http://127.0.0.1:46310", server_id: newRequestId(), device_id: newRequestId(), token: "private-local-fixture-token" };
  const getStatus = vi.fn(() => ({ version: "0.1.0", protocolVersion: 1, serverId: connection.server_id }));
  const stop = vi.fn(async () => ({}));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus, stopServer: stop });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
  });
  bridge.createTransport.mockReturnValue(transport);
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") return null;
    if (command === "launch_connection" || command === "retry_launch") return connection;
    if (command === "local_server_status") return { state: LocalServerState.Ready, attempts: 0, retry_ms: 0 };
    if (command === "notification_permission") return { permission: "unavailable", problem: "os-unavailable" };
    if (command === "begin_tray") return "fixture-presentation";
    if (command === "publish_tray" || command === "read_tray_action") return;
    throw new Error("Unexpected native authority");
  });
  return { connection, getStatus, stop };
}
it("enters the verified product automatically under Strict Mode and confines controls to diagnostics", async () => {
  const fixture = localFixture();
  render(<StrictMode><Desktop /></StrictMode>);
  await screen.findByText("Your sessions, in one place");
  expect(fixture.getStatus).toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Start or connect" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Saved servers" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Check desktop registration" })).toBeNull();
  expect(bridge.invoke.mock.calls.some(([command]) => ["connect_local", "retry_launch", "recover_local_registration"].includes(command))).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  expect(screen.getByRole("button", { name: "Check desktop registration" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Saved servers" })).toBeTruthy();
  fireEvent.click(screen.getByText("Local server"));
  fireEvent.click(screen.getByRole("button", { name: "Stop local server" }));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  expect(screen.getByRole("button", { name: "Confirm server stop" })).toBeTruthy();
  expect(fixture.stop).not.toHaveBeenCalled();
});
it("joins pending observations across remounts and submits only an explicit serialized retry", async () => {
  const fixture = localFixture();
  let finish!: (value: typeof fixture.connection) => void;
  const pending = new Promise<typeof fixture.connection>((resolve) => { finish = resolve; });
  const original = bridge.invoke.getMockImplementation()!;
  bridge.invoke.mockImplementation(async (command: string) => command === "launch_connection" ? pending : original(command));
  const first = render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  first.unmount();
  render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  expect(bridge.createTransport).not.toHaveBeenCalled();
  await act(async () => finish(fixture.connection));
  await screen.findByText("Your sessions, in one place");
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "launch_connection")).toHaveLength(2);
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "retry_launch")).toBe(false);
});
for (const failure of ["sidecar-missing", "timed-out", "permission-denied", "incompatible", "service-managed", "credential-unavailable"]) it(`retains ${failure} without replay and verifies explicit Retry`, async () => {
  const fixture = localFixture();
  const original = bridge.invoke.getMockImplementation()!;
  let finish!: (value: typeof fixture.connection) => void;
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "launch_connection") throw failure;
    if (command === "retry_launch") return new Promise<typeof fixture.connection>((resolve) => { finish = resolve; });
    return original(command);
  });
  render(<Desktop />);
  const retry = await screen.findByRole("button", { name: "Retry" });
  expect(screen.getByRole("alert").textContent).toContain("DeliDev could not connect");
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "launch_connection")).toHaveLength(1);
  fireEvent.click(retry); fireEvent.click(retry);
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "retry_launch")).toHaveLength(1);
  await act(async () => finish(fixture.connection));
  await screen.findByText("Your sessions, in one place");
  expect(fixture.getStatus).toHaveBeenCalled();
});
function savedFixture() {
  const profile: SavedConnection = { version: 1, revision: 1, id: newRequestId(), name: "Remote fixture", endpoint: "https://fixture.example.test", server_id: newRequestId(), device_id: newRequestId(), pairing_id: newRequestId(), state: SavedConnectionState.Paired, created_at: "2026-09-25T00:00:00Z" };
  const connection = { endpoint: profile.endpoint, server_id: profile.server_id, device_id: profile.device_id, token: "private-native-fixture-token" };
  const status = vi.fn(() => ({ version: "0.1.0", protocolVersion: 1, serverId: profile.server_id }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
  });
  bridge.createTransport.mockReturnValue(transport);
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") return profile;
    if (command === "connect_saved") return connection;
    if (command === "show_connection_manager") return;
    if (command === "notification_permission") return { permission: "unavailable", problem: "os-unavailable" };
    if (command === "begin_tray") return "fixture-presentation";
    if (command === "publish_tray" || command === "read_tray_action") return;
    throw new Error("Unexpected native authority");
  });
  return { profile, connection, status };
}
it("uses only the native-pinned saved authority and direct product RPCs without local bootstrap", async () => {
  const value = savedFixture();
  render(<Desktop />);
  await screen.findByText("Your sessions, in one place");
  expect(screen.getByText("Remote fixture")).toBeTruthy();
  expect(bridge.createTransport).toHaveBeenCalledWith(expect.objectContaining({ origin: value.profile.endpoint }));
  expect(value.status).toHaveBeenCalled();
  expect(bridge.invoke.mock.calls.every(([command]) => ["connection_context", "connect_saved", "begin_tray", "publish_tray", "read_tray_action", "notification_permission"].includes(command))).toBe(true);
  expect(JSON.stringify(bridge.invoke.mock.calls)).not.toContain(value.connection.token);
  expect(screen.queryByText(value.connection.token)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  fireEvent.click(screen.getByRole("button", { name: "Verify saved connection" }));
  await waitFor(() => expect(bridge.invoke.mock.calls.filter(([command]) => command === "connect_saved")).toHaveLength(2));
  await waitFor(() => expect((screen.getByRole("button", { name: "Verify saved connection" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Show local window" }));
  await waitFor(() => expect(bridge.invoke).toHaveBeenCalledWith("show_connection_manager"));
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "local_server_status")).toBe(false);
});
it("rejects a different native server before creating a renderer transport", async () => {
  const value = savedFixture();
  value.connection.server_id = newRequestId();
  render(<Desktop />);
  await screen.findByText(/original saved connection needs inspection/);
  expect(bridge.createTransport).not.toHaveBeenCalled();
  expect(value.status).not.toHaveBeenCalled();
  expect(screen.queryByText("Your sessions, in one place")).toBeNull();
});

it("registers and inspects this computer through only the saved window's fixed Worker boundary", async () => {
  savedFixture();
  const original = bridge.invoke.getMockImplementation()!;
  const machine = newRequestId();
  let registered = false;
  bridge.invoke.mockImplementation(async (command: string, args?: { action: LocalWorkerAction }) => {
    if (command !== "saved_worker_control") return original(command, args);
    if (args?.action === LocalWorkerAction.Register) registered = true;
    if (!registered) throw "credential-unavailable";
    return { state: LocalWorkerState.NotStarted, machine_id: machine, controller_active: false };
  });
  render(<Desktop />);
  await screen.findByText("Your sessions, in one place");
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Execution Workers" }));
  await screen.findByText(/local Worker could not be inspected/);
  fireEvent.click(screen.getByRole("button", { name: "Register this computer" }));
  await screen.findByText(`Execution machine: ${machine}`);
  await waitFor(() => expect((screen.getByRole("button", { name: "Start local Worker" }) as HTMLButtonElement).disabled).toBe(false));
  const calls = bridge.invoke.mock.calls.filter(([command]) => command === "saved_worker_control");
  expect(calls.filter(([, args]) => args.action === LocalWorkerAction.Register)).toHaveLength(1);
  expect(calls.every(([, args]) => Object.keys(args).every((key) => ["action", "generation"].includes(key)))).toBe(true);
  expect(bridge.invoke.mock.calls.some(([command]) => ["local_worker_control", "local_worker_proof", "connect_local"].includes(command))).toBe(false);
});

it("refreshes a window label without replacing transport or open settings and ignores older notifications", async () => {
  const value = savedFixture();
  let changed!: () => void;
  const unlisten = vi.fn();
  bridge.listen.mockImplementation(async (event: string, callback: () => void) => {
    expect(event).toBe("saved-connection-label"); changed = callback; return unlisten;
  });
  const view = render(<Desktop />);
  await screen.findByText("Your sessions, in one place");
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  await screen.findByRole("dialog", { name: "Settings" });
  const original = bridge.invoke.getMockImplementation()!;
  bridge.invoke.mockImplementation(async (command: string) => command === "connection_context" ? { ...value.profile, revision: 3, name: "Renamed window" } : original(command));
  await act(async () => changed());
  await screen.findByText("Renamed window");
  expect(screen.getByRole("dialog", { name: "Settings" })).toBeTruthy();
  expect(bridge.createTransport).toHaveBeenCalledTimes(1);
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "connect_saved")).toHaveLength(1);
  bridge.invoke.mockImplementation(async (command: string) => command === "connection_context" ? { ...value.profile, revision: 2, name: "Older result" } : original(command));
  await act(async () => changed());
  expect(screen.getByText("Renamed window")).toBeTruthy();
  expect(screen.queryByText("Older result")).toBeNull();
  view.unmount();
  await waitFor(() => expect(unlisten).toHaveBeenCalledOnce());
});
