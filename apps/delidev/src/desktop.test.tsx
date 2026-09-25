import { createRouterTransport } from "@connectrpc/connect";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { Desktop } from "./desktop";
import { SavedConnectionState, type SavedConnection } from "./saved-connections";

const bridge = vi.hoisted(() => ({ invoke: vi.fn(), createTransport: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => true, invoke: bridge.invoke }));
vi.mock("@delinoio/delidev-api-client", async (original) => ({ ...await original<typeof import("@delinoio/delidev-api-client")>(), createDeliDevTransport: (...args: unknown[]) => bridge.createTransport(...args) }));
beforeEach(() => { bridge.invoke.mockReset(); bridge.createTransport.mockReset(); });
function savedFixture() {
  const profile: SavedConnection = { version: 1, id: newRequestId(), name: "Remote fixture", endpoint: "https://fixture.example.test", server_id: newRequestId(), device_id: newRequestId(), pairing_id: newRequestId(), state: SavedConnectionState.Paired, created_at: "2026-09-25T00:00:00Z" };
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
  expect(bridge.invoke.mock.calls.every(([command]) => ["connection_context", "connect_saved"].includes(command))).toBe(true);
  expect(screen.queryByText(value.connection.token)).toBeNull();
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
