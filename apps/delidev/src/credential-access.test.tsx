// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { CredentialAccessAction, CredentialAccessGate, CredentialAccessIssue, CredentialAccessState } from "./credential-access";
import { i18n } from "./localization";
import type { NativeConnection } from "./local-registration";

const native = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke: native.invoke }));
beforeEach(async () => { native.invoke.mockReset(); await i18n.changeLanguage("en"); });
function fixture() {
  const connection: NativeConnection = { endpoint: "http://127.0.0.1:46310", server_id: newRequestId(), device_id: newRequestId(), token: "never-send-token", runtime_generation: newRequestId(), keychain_access_required: true };
  const diagnostics = vi.fn();
  const props = { connection, diagnostics, children: <main id="main"><h1>Product</h1></main> };
  return { ...props, props };
}
it("shows automatic access before mounting account queries and observes the original native attempt", async () => {
  const f = fixture(), attempt = newRequestId();
  let done = false;
  native.invoke.mockImplementation(async () => ({ attempt_id: attempt, state: done ? CredentialAccessState.Succeeded : CredentialAccessState.Checking }));
  render(<StrictMode><CredentialAccessGate {...f.props} /></StrictMode>);
  expect(screen.getByRole("heading", { name: "Checking Keychain access" })).toBeTruthy();
  expect(screen.queryByText("Product")).toBeNull();
  await waitFor(() => expect(native.invoke).toHaveBeenCalled());
  done = true;
  const heading = await screen.findByRole("heading", { name: "Product" });
  // The heading mounts before the passive focus handoff; wait for that handoff.
  await waitFor(() => expect(document.activeElement).toBe(heading));
  expect(native.invoke.mock.calls.every(([command, args]) => command === "desktop_credential_access" && args.action === CredentialAccessAction.Observe && args.server === f.connection.server_id && args.generation === f.connection.runtime_generation)).toBe(true);
  expect(JSON.stringify(native.invoke.mock.calls)).not.toContain(f.connection.token);
});
it("requires explicit retry after denial and never mounts disconnected account presentation", async () => {
  const f = fixture(), failedAttempt = newRequestId();
  native.invoke.mockImplementation(async (_command, { action }) => ({ attempt_id: action === CredentialAccessAction.Retry ? newRequestId() : failedAttempt, state: action === CredentialAccessAction.Retry ? CredentialAccessState.Succeeded : CredentialAccessState.Failed, ...(action === CredentialAccessAction.Retry ? {} : { issue: CredentialAccessIssue.ConfirmationRequired }) }));
  render(<CredentialAccessGate {...f.props} />);
  await screen.findByRole("alert");
  expect(screen.queryByText("Product")).toBeNull();
  await new Promise(resolve => setTimeout(resolve, 550));
  expect(native.invoke).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByText("Product");
  expect(native.invoke.mock.calls[1][1].action).toBe(CredentialAccessAction.Retry);
  expect(native.invoke.mock.calls[1][1].expectedAttemptId).toBe(failedAttempt);
});
it("continues after native skip without publishing late approval", async () => {
  const f = fixture();
  let finish!: (value: object) => void;
  native.invoke.mockImplementation((_command, { action }) => action === CredentialAccessAction.Skip ? Promise.resolve({ attempt_id: newRequestId(), state: CredentialAccessState.Skipped }) : new Promise(resolve => { finish = resolve; }));
  render(<CredentialAccessGate {...f.props} />);
  fireEvent.click(screen.getByRole("button", { name: "Continue without checking" }));
  await screen.findByText("Product");
  expect(screen.getByRole("status").textContent).toContain("checking was skipped");
  await act(async () => finish({ attempt_id: newRequestId(), state: CredentialAccessState.Failed, issue: CredentialAccessIssue.Unavailable }));
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByText("Product")).toBeTruthy();
});
it("observes unknown results with the same attempt instead of asking native to retry authentication", async () => {
  const f = fixture();
  native.invoke.mockRejectedValueOnce("raw-native-path-and-secret").mockResolvedValue({ attempt_id: newRequestId(), state: CredentialAccessState.Succeeded });
  render(<CredentialAccessGate {...f.props} />);
  await screen.findByRole("alert");
  expect(screen.queryByText("raw-native-path-and-secret")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByText("Product");
  expect(native.invoke.mock.calls.every(([, args]) => args.action === CredentialAccessAction.Observe)).toBe(true);
});
it("changes locale without repeating native access and keeps diagnostics available", async () => {
  const f = fixture();
  native.invoke.mockResolvedValue({ attempt_id: newRequestId(), state: CredentialAccessState.Failed, issue: CredentialAccessIssue.ExecutableChanged });
  render(<CredentialAccessGate {...f.props} />);
  await screen.findByRole("alert");
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.getByRole("heading", { name: "키체인 접근을 확인하지 못했습니다" })).toBeTruthy();
  expect(native.invoke).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "연결" }));
  expect(f.diagnostics).toHaveBeenCalledOnce();
});
it("leaves other platforms and saved remote authority outside startup access", () => {
  const f = fixture(); f.connection.keychain_access_required = false;
  render(<CredentialAccessGate {...f.props} />);
  expect(screen.getByText("Product")).toBeTruthy();
  expect(native.invoke).not.toHaveBeenCalled();
});
it("keeps a new execution generation behind confirmation instead of reusing old success", async () => {
  const f = fixture();
  native.invoke.mockResolvedValueOnce({ attempt_id: newRequestId(), state: CredentialAccessState.Succeeded });
  const view = render(<CredentialAccessGate {...f.props} />);
  await screen.findByText("Product");
  native.invoke.mockImplementation(() => new Promise(() => {}));
  view.rerender(<CredentialAccessGate {...f.props} connection={{ ...f.connection, runtime_generation: newRequestId() }} />);
  expect(screen.queryByText("Product")).toBeNull();
  expect(screen.getByRole("status")).toBeTruthy();
});
it("clearly presents skipped access for a saved remote connection without invoking native controls", () => {
  const f = fixture();
  f.connection.keychain_access_required = false;
  f.connection.keychain_access_skipped = true;
  render(<CredentialAccessGate {...f.props} />);
  expect(screen.getByText("Product")).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain("checking was skipped");
  expect(native.invoke).not.toHaveBeenCalled();
});

it("offers original registration presentation locally on denial without initiating inspection or retry", async () => {
  const f = fixture(), registration = vi.fn(), attempt = newRequestId();
  native.invoke.mockResolvedValue({ attempt_id: attempt, state: CredentialAccessState.Failed, issue: CredentialAccessIssue.PermissionDenied });
  render(<CredentialAccessGate {...f.props} registration={registration} />);
  const button = await screen.findByRole("button", { name: "Connection controls" });
  expect(registration).not.toHaveBeenCalled();
  fireEvent.click(button);
  expect(registration).toHaveBeenCalledOnce();
  expect(native.invoke.mock.calls.every(([command, args]) => command === "desktop_credential_access" && args.action === CredentialAccessAction.Observe)).toBe(true);
});
