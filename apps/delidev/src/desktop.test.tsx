import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { Desktop } from "./desktop";
import { SavedConnectionState, type SavedConnection } from "./saved-connections";
import { LocalWorkerAction, LocalWorkerState } from "./local-worker-controls";
import { LocalServerState } from "./local-server";

const bridge = vi.hoisted(() => ({ invoke: vi.fn(), createTransport: vi.fn(), listen: vi.fn() }));
vi.mock("@tauri-apps/api/event", () => ({ listen: bridge.listen }));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => true, invoke: bridge.invoke }));
vi.mock("@delinoio/delidev-api-client", async (original) => ({ ...await original<typeof import("@delinoio/delidev-api-client")>(), createDeliDevTransport: (...args: unknown[]) => bridge.createTransport(...args) }));
beforeEach(() => { bridge.invoke.mockReset(); bridge.createTransport.mockReset(); bridge.listen.mockReset().mockResolvedValue(() => {}); });
it("observes failed launch and keeps detailed recovery in troubleshooting without automatic repair", async () => {
  bridge.invoke.mockImplementation(async (command: string) => {
    if (command === "connection_context") return null;
    if (command === "local_server_status") return { state: LocalServerState.Blocked, attempts: 1, retry_ms: 60000, failure: "permission-denied" };
    if (command === "launch_local" || command === "retry_local") throw "permission-denied";
    throw new Error("Unexpected native authority");
  });
  render(<Desktop />);
  await screen.findByRole("button", { name: "Retry" });
  expect(screen.queryByRole("button", { name: "Start or connect" })).toBeNull();
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "retry_local")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Troubleshooting" }));
  const problem = await screen.findByText(/Access was denied/);
  expect(problem.textContent).toContain("selected device is authorized");
  expect(problem.textContent).toContain("accessible only to you");
  expect(problem.textContent).toContain("0700 for private directories and 0600 for private files");
  expect(problem.textContent).toContain("Preserve existing data");
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "launch_local")).toHaveLength(1);
  expect(bridge.invoke.mock.calls.every(([command]) => ["connection_context", "local_server_status", "launch_local"].includes(command))).toBe(true);
  expect(bridge.createTransport).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Re-register this desktop" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Continue desktop recovery" })).toBeNull();
});
function savedFixture(stopServer?: (_request: unknown) => Promise<object>) {
  const profile: SavedConnection = { version: 1, revision: 1, id: newRequestId(), name: "Remote fixture", endpoint: "https://fixture.example.test", server_id: newRequestId(), device_id: newRequestId(), pairing_id: newRequestId(), state: SavedConnectionState.Paired, created_at: "2026-09-25T00:00:00Z" };
  const connection = { endpoint: profile.endpoint, server_id: profile.server_id, device_id: profile.device_id, token: "private-native-fixture-token" };
  const status = vi.fn(() => ({ version: "0.1.0", protocolVersion: 1, serverId: profile.server_id }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status, stopServer });
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
  fireEvent.click(screen.getByRole("button", { name: "Connection controls" }));
  fireEvent.click(screen.getByRole("button", { name: "Verify saved connection" }));
  await waitFor(() => expect(bridge.invoke.mock.calls.filter(([command]) => command === "connect_saved")).toHaveLength(2));
  await waitFor(() => expect((screen.getByRole("button", { name: "Verify saved connection" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Show local window" }));
  await waitFor(() => expect(bridge.invoke).toHaveBeenCalledWith("show_connection_manager"));
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "local_server_status" || command === "launch_local")).toBe(false);
}, 15_000);
it("rejects a different native server before creating a renderer transport", async () => {
  const value = savedFixture();
  value.connection.server_id = newRequestId();
  render(<Desktop />);
  await screen.findByText(/DeliDev could not verify this connection/);
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

function localFixture(stopServer?: (_request: unknown) => Promise<object>) {
  const value = savedFixture(stopServer);
  value.connection.endpoint = "http://127.0.0.1:46310";
  const original = bridge.invoke.getMockImplementation()!;
  bridge.invoke.mockImplementation(async (command: string, args?: unknown) => {
    if (command === "connection_context") return null;
    if (command === "local_server_status") return { state: LocalServerState.Ready, attempts: 0, retry_ms: 0 };
    if (command === "launch_local" || command === "retry_local" || command === "connect_local") return value.connection;
    return original(command, args);
  });
  return value;
}
it("enters the verified product automatically and hides routine infrastructure controls", async () => {
  const value = localFixture();
  render(<StrictMode><Desktop /></StrictMode>);
  await screen.findByText("Your sessions, in one place");
  expect(value.status).toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Start or connect" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Saved servers" })).toBeNull();
  expect(screen.queryByText("Server 0.1.0")).toBeNull();
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "retry_local")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
  fireEvent.click(screen.getByRole("button", { name: "Connection controls" }));
  expect(screen.getByRole("button", { name: "Saved servers" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Check desktop registration" })).toBeTruthy();
}, 15_000);
it("keeps a pending observation across remount and serializes explicit Retry", async () => {
  localFixture();
  const original = bridge.invoke.getMockImplementation()!;
  let finish!: (value: unknown) => void;
  const pending = new Promise((resolve) => { finish = resolve; });
  bridge.invoke.mockImplementation(async (command: string, args?: unknown) => command === "launch_local" ? pending : original(command, args));
  const first = render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  first.unmount();
  render(<Desktop />);
  await screen.findByText("Starting DeliDev…");
  await act(async () => finish(null));
  bridge.invoke.mockImplementation(async (command: string, args?: unknown) => command === "launch_local" ? Promise.reject("timed-out") : original(command, args));
  await screen.findByRole("button", { name: "Retry" });
  expect(bridge.invoke.mock.calls.some(([command]) => command === "retry_local" || command === "connect_local")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await screen.findByText("Your sessions, in one place");
  expect(bridge.invoke.mock.calls.filter(([command]) => command === "retry_local")).toHaveLength(1);
});
it("never turns a stopped launch observation into readiness or a restart", async () => {
  localFixture();
  const original = bridge.invoke.getMockImplementation()!;
  bridge.invoke.mockImplementation(async (command: string, args?: unknown) => command === "launch_local" ? Promise.reject("stopped") : original(command, args));
  render(<Desktop />);
  await screen.findByText(/DeliDev is disconnected on this computer/);
  expect((screen.getByRole("button", { name: "Retry" }) as HTMLButtonElement).disabled).toBe(true);
  expect(bridge.createTransport).not.toHaveBeenCalled();
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "retry_local")).toBe(false);
});

it("retains the original Stop and confirmation across diagnostics hiding and Settings disposal", async () => {
  const stop = vi.fn(async (_request: unknown) => ({}));
  stop.mockRejectedValueOnce(new ConnectError("Receipt lost", Code.Unavailable));
  localFixture(stop);
  render(<Desktop />);
  await screen.findByText("Your sessions, in one place");
  const open = () => {
    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    fireEvent.click(screen.getByRole("button", { name: "Connection & diagnostics" }));
    fireEvent.click(screen.getByRole("button", { name: "Connection controls" }));
  };
  const hide = () => {
    fireEvent.click(within(screen.getByRole("dialog", { name: "Connection & diagnostics" })).getByRole("button", { name: "Close Connection & diagnostics" }));
    fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  };
  open();
  fireEvent.click(screen.getByText("Local server"));
  fireEvent.click(screen.getByRole("button", { name: "Stop local server" }));
  hide();
  open();
  expect(screen.getByRole("button", { name: "Confirm server stop" })).toBeTruthy();
  expect(stop).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm server stop" }));
  await screen.findByRole("button", { name: "Retry the same server stop" });
  hide();
  open();
  fireEvent.click(screen.getByRole("button", { name: "Retry the same server stop" }));
  await waitFor(() => expect(stop).toHaveBeenCalledTimes(2));
  expect(stop.mock.calls[1][0]).toEqual(stop.mock.calls[0][0]);
  expect(bridge.invoke.mock.calls.some(([command]) => command === "connect_local" || command === "retry_local")).toBe(false);
}, 15_000);
