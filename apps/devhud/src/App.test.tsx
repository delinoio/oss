// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import { messages } from "./localization";
import { LifecycleState, NativeBridgeError, NativeBridgeErrorCode, NotificationPermission, RuntimePlatform, type NativeBridgeEventV1, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";

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

  it("consumes a queued callback after foreground event delivery", async () => {
    let receive!: (event: NativeBridgeEventV1) => void;
    const request = vi.fn(async (): Promise<NativeBridgeResponseV1> => ({ kind: "auth-callback", url: null }));
    const bridge: NativeBridgeV1 = {
      request,
      async listen(listener) { receive = listener; return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={mobileRuntime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));
    receive({ version: 1, kind: "auth-callback", url: "devhud://auth/callback?state=opaque" });

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "auth.take-pending-callback" }));
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

  it("refreshes notification permission when the app returns to the active lifecycle", async () => {
    let receive!: (event: NativeBridgeEventV1) => void;
    let permission: NotificationPermission = NotificationPermission.Authorized;
    const runtime = { ...mobileRuntime, capabilities: { ...mobileRuntime.capabilities, notifications: true } };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "notifications.permission") return { kind: "notification-permission", permission };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const bridge: NativeBridgeV1 = {
      request,
      async listen(listener) { receive = listener; return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));
    await waitFor(() => expect(request).toHaveBeenCalledTimes(1));

    await act(async () => { receive({ version: 1, kind: "lifecycle", state: LifecycleState.Background }); });
    expect(request).toHaveBeenCalledTimes(1);

    permission = NotificationPermission.Denied;
    await act(async () => { receive({ version: 1, kind: "lifecycle", state: LifecycleState.Active }); });
    await waitFor(() => expect(request).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    expect(await screen.findByText(messages.en.notificationDenied)).toBeTruthy();
  });

  it("surfaces expected notification permission failures inline and clears them after retry", async () => {
    let requestAttempts = 0;
    const runtime = { ...mobileRuntime, capabilities: { ...mobileRuntime.capabilities, notifications: true } };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "notifications.permission") return { kind: "notification-permission", permission: NotificationPermission.Authorized };
      if (value.operation === "notifications.request-permission") {
        requestAttempts += 1;
        if (requestAttempts === 1) throw new NativeBridgeError(NativeBridgeErrorCode.PlatformFailure);
        return { kind: "notification-permission", permission: NotificationPermission.Denied };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    expect(await screen.findByText(messages.en.notificationAuthorized)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.notificationPermission }));
    expect((await screen.findByRole("alert")).textContent).toBe(messages.en.notificationPermissionFailed);
    expect(screen.getByText(messages.en.notificationAuthorized)).toBeTruthy();
    expect(screen.queryByText(messages.en.errorTitle)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: messages.en.notificationPermission }));
    expect(await screen.findByText(messages.en.notificationDenied)).toBeTruthy();
    await waitFor(() => expect(screen.queryByText(messages.en.notificationPermissionFailed)).toBeNull());
  });

  it("updates the Deck state when connectivity changes", () => {
    let online = true;
    vi.spyOn(window.navigator, "onLine", "get").mockImplementation(() => online);

    render(<App bridge={bridgeWith(async () => { throw new Error("unexpected request"); })} initialRuntime={mobileRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.deck }));
    expect(screen.getByText(messages.en.emptyTitle)).toBeTruthy();

    online = false;
    fireEvent(window, new Event("offline"));
    expect(screen.getByText(messages.en.offlineTitle)).toBeTruthy();

    online = true;
    fireEvent(window, new Event("online"));
    expect(screen.getByText(messages.en.emptyTitle)).toBeTruthy();
  });

  it("surfaces expected native update errors inline and clears them after retry", async () => {
    let openAttempts = 0;
    const runtime = { ...mobileRuntime, capabilities: { ...mobileRuntime.capabilities, storeUpdates: true } };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "updates.status") return { kind: "update-status", store: "play-store", installedVersion: "1", configured: true };
      if (value.operation === "updates.open-store") {
        openAttempts += 1;
        if (openAttempts === 1) throw new NativeBridgeError(NativeBridgeErrorCode.NotConfigured);
        return { kind: "ok" };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.updatePolicy }));

    expect((await screen.findByRole("alert")).textContent).toBe(messages.en.storeOpenFailed);
    expect(screen.queryByText(messages.en.errorTitle)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: messages.en.updatePolicy }));
    await waitFor(() => expect(screen.queryByText(messages.en.storeOpenFailed)).toBeNull());
    expect(openAttempts).toBe(2);
  });
});
