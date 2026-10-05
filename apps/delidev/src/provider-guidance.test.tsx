// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ProviderGuidance } from "./provider-guidance";
const native = vi.hoisted(() => ({ invoke: vi.fn(), isTauri: vi.fn(() => true) }));
vi.mock("@tauri-apps/api/core", () => native);

it("sends only the selected closed identity/action, never the supplied help URL, once per explicit click", async () => {
  let settle!: () => void;
  native.invoke.mockImplementation(() => new Promise<void>(resolve => { settle = resolve; }));
  render(<ProviderGuidance preset="gemini" documentation="https://untrusted.invalid/docs" keyCreation="https://untrusted.invalid/key?secret" />);
  const button = screen.getByRole("button", { name: "Open official key creation" });
  fireEvent.click(button); fireEvent.click(button);
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(native.invoke).toHaveBeenCalledWith("open_provider_guidance", { preset: "gemini", action: "api-keys" });
  await act(async () => { settle(); });
  expect((await screen.findByRole("status")).textContent).toContain("Sent to your default browser");
});
it("retains an unconfirmed opening without automatic retry and clears it on provider selection", async () => {
  native.invoke.mockReset(); native.invoke.mockRejectedValue(new Error("fixture error body"));
  const view = render(<ProviderGuidance preset="moonshot-cn" documentation="https://docs.invalid" keyCreation="https://keys.invalid" />);
  fireEvent.click(screen.getByRole("button", { name: "Open provider documentation" }));
  await waitFor(() => expect(screen.getByRole("status").textContent).toContain("not confirmed"));
  expect(screen.queryByText("fixture error body")).toBeNull();expect(native.invoke).toHaveBeenCalledTimes(1);
  view.rerender(<ProviderGuidance preset="minimax" documentation="https://docs.invalid" />);
  expect(screen.queryByRole("status")).toBeNull();expect(screen.queryByRole("button", { name: "Open official key creation" })).toBeNull();
});
