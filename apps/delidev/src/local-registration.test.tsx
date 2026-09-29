import { useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { LocalRegistrationRecovery, RegistrationState } from "./local-registration";

const native = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke: native.invoke }));
beforeEach(() => native.invoke.mockReset());
function fixture(state = RegistrationState.Revoked) {
  const status = { state, server_id: newRequestId(), device_id: newRequestId(), revision: "9007199254740993", ...(state === RegistrationState.Recovering ? { request_id: newRequestId() } : {}) };
  const connection = { endpoint: "http://127.0.0.1:46310", server_id: status.server_id, device_id: newRequestId(), token: "private-fixture-token" };
  native.invoke.mockImplementation(async (command: string) => command === "inspect_local_registration" ? status : connection);
  const recovered = vi.fn(async () => {});
  function View() { const [busy, setBusy] = useState(false); return <LocalRegistrationRecovery busy={busy} setBusy={setBusy} recovered={recovered} />; }
  return { status, connection, recovered, View };
}
it("requires inspection and explicit confirmation and never displays credentials", async () => {
  const f = fixture(); render(<f.View />);
  expect(native.invoke).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Check desktop registration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Re-register this desktop" }));
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/unsent drafts/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Confirm desktop re-registration" }));
  await waitFor(() => expect(f.recovered).toHaveBeenCalledWith(f.connection));
  expect(native.invoke.mock.calls[1]).toEqual(["recover_local_registration", { deviceId: f.status.device_id, revision: f.status.revision, requestId: expect.any(String) }]);
  expect(screen.queryByText(f.connection.token)).toBeNull();
});
it("retains an uncertain request across dialog dismissal and repeated clicks", async () => {
  const f = fixture();
  let reject!: (reason: unknown) => void;
  native.invoke.mockImplementationOnce(async () => f.status).mockImplementationOnce(() => new Promise((_resolve, no) => { reject = no; })).mockResolvedValueOnce(f.connection);
  render(<f.View />);
  fireEvent.click(screen.getByRole("button", { name: "Check desktop registration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Re-register this desktop" }));
  const confirm = screen.getByRole("button", { name: "Confirm desktop re-registration" });
  fireEvent.click(confirm); fireEvent.click(confirm);
  expect(native.invoke).toHaveBeenCalledTimes(2);
  await act(async () => reject("timed-out"));
  fireEvent.click(screen.getByRole("button", { name: "Close Re-register this desktop" }));
  fireEvent.click(screen.getByRole("button", { name: "Continue desktop recovery" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry original desktop recovery" }));
  await waitFor(() => expect(f.recovered).toHaveBeenCalledOnce());
  expect(native.invoke.mock.calls[1]).toEqual(native.invoke.mock.calls[2]);
});
it("uses the original durable request after restart", async () => {
  const f = fixture(RegistrationState.Recovering); render(<f.View />);
  fireEvent.click(screen.getByRole("button", { name: "Check desktop registration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Continue desktop recovery" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry original desktop recovery" }));
  await waitFor(() => expect(f.recovered).toHaveBeenCalledOnce());
  expect(native.invoke.mock.calls[1][1].requestId).toBe(f.status.request_id);
});
it("does not offer replacement for an authorized device or malformed evidence", async () => {
  const f = fixture(RegistrationState.Authorized); render(<f.View />);
  fireEvent.click(screen.getByRole("button", { name: "Check desktop registration" }));
  await screen.findByText("This desktop registration is authorized.");
  expect(screen.queryByRole("button", { name: "Re-register this desktop" })).toBeNull();
  native.invoke.mockResolvedValueOnce({ ...f.status, state: RegistrationState.Revoked, revision: 9007199254740992 });
  fireEvent.click(screen.getByRole("button", { name: "Check desktop registration" }));
  await screen.findByRole("alert");
  expect(screen.queryByRole("button", { name: "Re-register this desktop" })).toBeNull();
  expect(f.recovered).not.toHaveBeenCalled();
});
it("rejects a foreign server before replacing the active transport", async () => {
  const f = fixture(); native.invoke.mockImplementation(async (command: string) => command === "inspect_local_registration" ? f.status : { ...f.connection, server_id: newRequestId() });
  render(<f.View />);
  fireEvent.click(screen.getByRole("button", { name: "Check desktop registration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Re-register this desktop" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm desktop re-registration" }));
  await screen.findByRole("alert");
  expect(f.recovered).not.toHaveBeenCalled();
});
