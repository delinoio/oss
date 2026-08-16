// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import { messages } from "./localization";
import { LifecycleState, NotificationPermission, RuntimePlatform, type NativeBridgeEventV1, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";

const mobileRuntime: RuntimeSnapshot = {
  bridgeVersion: 1,
  platform: RuntimePlatform.Ios,
  architecture: "arm64",
  osVersion: "16.0",
  lifecycle: LifecycleState.Active,
  capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false },
};

function bridgeWith(request: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>): NativeBridgeV1 {
  return {
    request,
    async listen(_listener: (event: NativeBridgeEventV1) => void) { return () => {}; },
  };
}

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("devhud.shell.onboarding.v1", "complete");
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })),
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("native App state", () => {
  it("loads the default content state once", async () => {
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "runtime.snapshot") return { kind: "runtime", snapshot: mobileRuntime };
      if (value.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} />);
    await screen.findByText(messages.en.welcome);

    expect(request.mock.calls.filter(([value]) => value.operation === "runtime.snapshot")).toHaveLength(1);
    expect(request.mock.calls.filter(([value]) => value.operation === "auth.take-pending-callback")).toHaveLength(1);
  });

  it("unsubscribes when a listener resolves after cleanup", async () => {
    let resolveListen!: (unsubscribe: () => void) => void;
    const unsubscribe = vi.fn();
    const bridge: NativeBridgeV1 = {
      async request() { throw new Error("unexpected request"); },
      listen: vi.fn(() => new Promise<() => void>((resolve) => { resolveListen = resolve; })),
    };

    const { unmount } = render(<App bridge={bridge} initialRuntime={mobileRuntime} />);
    unmount();
    resolveListen(unsubscribe);

    await waitFor(() => expect(unsubscribe).toHaveBeenCalledOnce());
  });

  it("reads and localizes notification permission and diagnostic labels", async () => {
    localStorage.setItem("devhud.shell.preferences.v1", JSON.stringify({
      version: 1,
      theme: "system",
      language: "ko",
      apiOrigin: "https://devhud.api.delino.io/",
      launchAtLogin: false,
    }));
    const runtime = { ...mobileRuntime, capabilities: { ...mobileRuntime.capabilities, notifications: true } };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "notifications.permission") return { kind: "notification-permission", permission: NotificationPermission.Authorized };
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.ko.settings }));
    expect(await screen.findByText(messages.ko.notificationAuthorized)).toBeTruthy();
    expect(screen.queryByText(NotificationPermission.Authorized)).toBeNull();
    expect(request.mock.calls.filter(([value]) => value.operation === "notifications.permission")).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: messages.ko.diagnostics }));
    expect(screen.getByText(messages.ko.diagnosticPlatform)).toBeTruthy();
    expect(screen.getByText(messages.ko.diagnosticArchitecture)).toBeTruthy();
    expect(screen.getByText(messages.ko.diagnosticBridge)).toBeTruthy();
  });
});
