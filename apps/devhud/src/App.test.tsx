// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import * as identityClient from "./identity-client";
import type { IdentitySession } from "./identity-client";
import { messages } from "./localization";
import { LifecycleState, NativeBridgeError, NativeBridgeErrorCode, NotificationPermission, RuntimePlatform, type NativeBridgeEventV1, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { saveGuestSettings } from "./service-boundary";
import { defaultDevHudSettings } from "./settings-contract";

const mobileRuntime: RuntimeSnapshot = {
  bridgeVersion: 1,
  platform: RuntimePlatform.Ios,
  architecture: "arm64",
  osVersion: "16.0",
  lifecycle: LifecycleState.Active,
  capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false },
};
const desktopRuntime: RuntimeSnapshot = { ...mobileRuntime, platform: RuntimePlatform.Desktop, architecture: "x86_64", osVersion: "test" };

function bridgeWith(request: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>): NativeBridgeV1 {
  return {
    request,
    async listen(_listener: (event: NativeBridgeEventV1) => void) { return () => {}; },
  };
}

beforeEach(() => {
  delete window.__TAURI_INTERNALS__;
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
  vi.unstubAllGlobals();
});

describe("native App state", () => {
  it.each([["en", messages.en], ["ko", messages.ko]] as const)("renders the accessible %s GitHub setup surface", async (language, copy) => {
    localStorage.setItem("devhud.shell.preferences.v1", JSON.stringify({ version: 1, theme: "system", language, apiOrigin: "https://devhud.api.delino.io", launchAtLogin: false }));
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "lifecycle.open-external") return { kind: "ok" };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<App bridge={bridgeWith(request)} initialRuntime={mobileRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: copy.settings }));

    expect(screen.getByRole("heading", { name: copy.githubSetupTitle })).toBeTruthy();
    expect(screen.getByText(copy.githubDirectSecurity)).toBeTruthy();
    expect(screen.getByRole("textbox", { name: copy.githubProfileName })).toBeTruthy();
    expect((screen.getByLabelText(copy.githubToken) as HTMLInputElement).type).toBe("password");
    expect(screen.getByRole("combobox", { name: copy.githubTokenKind })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: copy.githubCreateFinePat }));
    fireEvent.click(screen.getByRole("button", { name: copy.githubCreateClassicPat }));
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "lifecycle.open-external", target: "classic-pat", apiOrigin: "https://devhud.api.delino.io" }));
    expect(document.documentElement.lang).toBe(language);
  });

  it("routes Settings PAT links through the packaged desktop opener", async () => {
    const invoke = vi.fn(async () => undefined);
    window.__TAURI_INTERNALS__ = { invoke };
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const bridge = bridgeWith(async (request) => {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      throw new Error(`unexpected operation ${request.operation}`);
    });
    render(<App bridge={bridge} initialRuntime={desktopRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubCreateFinePat }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith("open_external", { target: "fine-grained-pat", apiOrigin: "https://devhud.api.delino.io" }));
  });

  it.each([
    ["en", messages.en],
    ["ko", messages.ko],
  ] as const)("renders the complete %s first-run identity choice accessibly", async (language, copy) => {
    localStorage.removeItem("devhud.shell.onboarding.v1");
    localStorage.setItem("devhud.shell.preferences.v1", JSON.stringify({ version: 1, theme: "system", language, apiOrigin: "https://devhud.api.delino.io", launchAtLogin: false }));
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const bridge = bridgeWith(async (request) => {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      throw new Error(`unexpected operation ${request.operation}`);
    });

    render(<App bridge={bridge} initialRuntime={mobileRuntime} />);

    const input = screen.getByRole("textbox", { name: copy.apiOrigin }) as HTMLInputElement;
    expect(input.value).toBe("https://devhud.api.delino.io");
    expect(input).toBe(document.activeElement);
    expect(screen.getByRole("button", { name: copy.signIn })).toBeTruthy();
    expect(screen.getByRole("button", { name: copy.continueLocally })).toBeTruthy();
    expect(screen.getByText(copy.customApiWarning)).toBeTruthy();
    expect(document.documentElement.lang).toBe(language);
  });

  it("rejects insecure custom APIs and confirms a secure API change before clearing its session", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const transitionOperations: string[] = [];
    let pendingCallback: string | null = "devhud://auth/callback?code=old&state=old";
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "session.configure-origins") {
        if (value.apiOrigin === "https://custom.example") transitionOperations.push("configure-new-origin");
        return { kind: "session-network-policy", changed: false };
      }
      if (value.operation === "auth.take-pending-callback") {
        transitionOperations.push("discard-callback");
        const url = pendingCallback;
        pendingCallback = null;
        return { kind: "auth-callback", url };
      }
      if (value.operation === "secure.purge") { transitionOperations.push("purge-session"); return { kind: "ok" }; }
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={mobileRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    const input = screen.getByRole("textbox", { name: messages.en.apiOrigin });

    fireEvent.change(input, { target: { value: "https://devhud.api.delino.io/" } });
    expect((screen.getByRole("button", { name: messages.en.applyApiOrigin }) as HTMLButtonElement).disabled).toBe(true);
    expect(confirm).not.toHaveBeenCalled();
    expect(request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "secure.purge" }));

    fireEvent.change(input, { target: { value: "http://remote.example" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.applyApiOrigin }));
    expect(screen.getByRole("alert").textContent).toBe(messages.en.invalidApiOrigin);
    expect(confirm).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "https://custom.example" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.applyApiOrigin }));
    await waitFor(() => expect(JSON.parse(localStorage.getItem("devhud.shell.preferences.v1") ?? "null").apiOrigin).toBe("https://custom.example"));
    expect(confirm).toHaveBeenCalledWith(messages.en.apiChangeConfirm);
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "secure.purge", scope: "api-change", profileId: expect.stringMatching(/^origin\./u) }));
    expect(pendingCallback).toBeNull();
    expect(transitionOperations).toEqual(["discard-callback", "purge-session", "configure-new-origin"]);
  });

  it("loads the default content state once", async () => {
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "runtime.snapshot") return { kind: "runtime", snapshot: mobileRuntime };
      if (value.operation === "auth.peek-pending-callback") return { kind: "auth-callback", url: null };
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} />);
    await screen.findByText(messages.en.welcome);

    expect(request.mock.calls.filter(([value]) => value.operation === "runtime.snapshot")).toHaveLength(1);
    expect(request.mock.calls.filter(([value]) => value.operation === "auth.peek-pending-callback")).toHaveLength(1);
  });

  it("keeps a cold-start Deck link queued across an origin-policy reload", async () => {
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    let deckLink: string | null = "018f47a2-7b3c-7def-8abc-1234567890ab";
    let originConfigured = false;
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => false,
      signIn: async () => {},
      handleCallback: async () => {},
      clear: async () => {},
    } as unknown as IdentitySession);
    vi.stubGlobal("location", { reload: vi.fn() });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      projectId: "PROJECT_ID_DEVHUD",
      protocolSchemaVersion: 1,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" },
      logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
    }), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } })));
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "runtime.snapshot") return { kind: "runtime", snapshot: desktopRuntime };
      if (value.operation === "auth.peek-pending-callback") return { kind: "auth-callback", url: null };
      if (value.operation === "deck.peek-pending-link") return { kind: "deck-link", deckId: deckLink };
      if (value.operation === "deck.take-pending-link") {
        const deckId = deckLink;
        deckLink = null;
        return { kind: "deck-link", deckId };
      }
      if (value.operation === "session.configure-origins") {
        if (!value.logtoIssuer && !originConfigured) {
          originConfigured = true;
          return { kind: "session-network-policy", changed: true };
        }
        return { kind: "session-network-policy", changed: false };
      }
      throw new Error(`unexpected operation ${value.operation}`);
    });

    const first = render(<App bridge={bridgeWith(request)} />);
    await waitFor(() => expect(originConfigured).toBe(true));
    expect(deckLink).toBe("018f47a2-7b3c-7def-8abc-1234567890ab");
    expect(request.mock.calls.filter(([value]) => value.operation === "deck.take-pending-link")).toHaveLength(0);
    first.unmount();

    render(<App bridge={bridgeWith(request)} />);
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.take-pending-link" }));
    expect(deckLink).toBeNull();
    expect(request.mock.calls.filter(([value]) => value.operation === "deck.take-pending-link")).toHaveLength(1);
  });

  it("consumes a pending Deck link after Continue locally and origin configuration", async () => {
    localStorage.removeItem("devhud.shell.onboarding.v1");
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    let deckLink: string | null = "018f47a2-7b3c-7def-8abc-1234567890ab";
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (value.operation === "deck.peek-pending-link") return { kind: "deck-link", deckId: deckLink };
      if (value.operation === "deck.take-pending-link") {
        const deckId = deckLink;
        deckLink = null;
        return { kind: "deck-link", deckId };
      }
      throw new Error("unexpected operation");
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={desktopRuntime} />);
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.peek-pending-link" }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.continueLocally }));

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.take-pending-link" }));
    expect(deckLink).toBeNull();
  });

  it("peeks for a callback only after the native listener is installed", async () => {
    const operations: string[] = [];
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      operations.push(value.operation);
      if (value.operation === "runtime.snapshot") return { kind: "runtime", snapshot: mobileRuntime };
      if (value.operation === "auth.peek-pending-callback") return { kind: "auth-callback", url: null };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const bridge: NativeBridgeV1 = {
      request,
      async listen() { operations.push("listener-installed"); return () => {}; },
    };

    render(<App bridge={bridge} />);
    await screen.findByText(messages.en.welcome);

    expect(operations.slice(0, 3)).toEqual(["listener-installed", "runtime.snapshot", "auth.peek-pending-callback"]);
  });

  it("drains a cold-start callback after the identity session becomes ready", async () => {
    let authenticated = false;
    const handleCallback = vi.fn(async () => { authenticated = true; });
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => authenticated,
      signIn: async () => {},
      handleCallback,
      clear: async () => {},
    } as unknown as IdentitySession);
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      projectId: "PROJECT_ID_DEVHUD",
      protocolSchemaVersion: 1,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" },
      logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
    }), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } })));
    const request = vi.fn(async (request: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (request.operation === "runtime.snapshot") return { kind: "runtime", snapshot: mobileRuntime };
      if (request.operation === "auth.peek-pending-callback" || request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: "devhud://auth/callback?code=opaque&state=opaque" };
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      throw new Error(`unexpected operation ${request.operation}`);
    });

    render(<App bridge={bridgeWith(request)} />);

    await waitFor(() => expect(handleCallback).toHaveBeenCalledOnce());
    expect(request.mock.calls.filter(([value]) => value.operation === "auth.peek-pending-callback")).toHaveLength(1);
    expect(request.mock.calls.filter(([value]) => value.operation === "auth.take-pending-callback")).toHaveLength(1);
  });

  it("keeps a cold-start callback queued across an issuer-policy reload", async () => {
    const callbackUrl = "devhud://auth/callback?code=opaque&state=opaque";
    let pendingCallback: string | null = callbackUrl;
    let issuerConfigured = false;
    let authenticated = false;
    const handleCallback = vi.fn(async () => { authenticated = true; });
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => authenticated,
      signIn: async () => {},
      handleCallback,
      clear: async () => {},
    } as unknown as IdentitySession);
    vi.stubGlobal("location", { reload: vi.fn() });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      projectId: "PROJECT_ID_DEVHUD",
      protocolSchemaVersion: 1,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" },
      logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
    }), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } })));
    const request = vi.fn(async (request: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (request.operation === "runtime.snapshot") return { kind: "runtime", snapshot: mobileRuntime };
      if (request.operation === "auth.peek-pending-callback") return { kind: "auth-callback", url: pendingCallback };
      if (request.operation === "auth.take-pending-callback") {
        const url = pendingCallback;
        pendingCallback = null;
        return { kind: "auth-callback", url };
      }
      if (request.operation === "session.configure-origins") {
        if (request.logtoIssuer && !issuerConfigured) {
          issuerConfigured = true;
          return { kind: "session-network-policy", changed: true };
        }
        return { kind: "session-network-policy", changed: false };
      }
      throw new Error(`unexpected operation ${request.operation}`);
    });

    const first = render(<App bridge={bridgeWith(request)} />);
    await waitFor(() => expect(issuerConfigured).toBe(true));
    expect(pendingCallback).toBe(callbackUrl);
    expect(request.mock.calls.filter(([value]) => value.operation === "auth.take-pending-callback")).toHaveLength(0);
    first.unmount();

    render(<App bridge={bridgeWith(request)} />);
    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "auth.take-pending-callback")).toHaveLength(1));
    expect(pendingCallback).toBeNull();
    expect(handleCallback).toHaveBeenCalledWith(callbackUrl);
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

  it("leaves a foreground callback queued until the identity session is ready", async () => {
    let receive!: (event: NativeBridgeEventV1) => void;
    const request = vi.fn(async (): Promise<NativeBridgeResponseV1> => ({ kind: "auth-callback", url: null }));
    const bridge: NativeBridgeV1 = {
      request,
      async listen(listener) { receive = listener; return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={mobileRuntime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));
    receive({ version: 1, kind: "auth-callback", url: "devhud://auth/callback?state=opaque" });

    await waitFor(() => expect(request).not.toHaveBeenCalledWith({ operation: "auth.take-pending-callback" }));
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

  it("requests browser notification permission on desktop when native notifications are unavailable", async () => {
    const requestPermission = vi.fn(async () => "granted" as NotificationPermission);
    vi.stubGlobal("Notification", { permission: "default", requestPermission });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => { throw new Error(`unexpected operation ${value.operation}`); });

    render(<App bridge={bridgeWith(request)} initialRuntime={desktopRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.notificationPermission }));

    await waitFor(() => expect(requestPermission).toHaveBeenCalledOnce());
    expect(await screen.findByText(messages.en.notificationAuthorized)).toBeTruthy();
    expect(request.mock.calls.filter(([value]) => value.operation === "notifications.request-permission")).toHaveLength(0);
  });

  it("preserves an undetermined browser notification permission when its prompt is dismissed", async () => {
    const requestPermission = vi.fn(async () => "default" as NotificationPermission);
    vi.stubGlobal("Notification", { permission: "default", requestPermission });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => { throw new Error(`unexpected operation ${value.operation}`); });

    render(<App bridge={bridgeWith(request)} initialRuntime={desktopRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.notificationPermission }));

    await waitFor(() => expect(requestPermission).toHaveBeenCalledOnce());
    expect(await screen.findByText(messages.en.notificationNotDetermined)).toBeTruthy();
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
    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "notifications.permission")).toHaveLength(1));

    await act(async () => { receive({ version: 1, kind: "lifecycle", state: LifecycleState.Background }); });
    expect(request.mock.calls.filter(([value]) => value.operation === "notifications.permission")).toHaveLength(1);

    permission = NotificationPermission.Denied;
    await act(async () => { receive({ version: 1, kind: "lifecycle", state: LifecycleState.Active }); });
    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "notifications.permission")).toHaveLength(2));

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

  it("polls every configured Deck while Home is selected", async () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const deck = (id: string, repository: string) => ({ id, name: repository, profileRef: profile.id, query: `repo:${repository} is:pr`, builder: null, display: { groupBy: "none" as const, showDrafts: true }, refreshMinutes: 5 as const, notifications: [] });
    saveGuestSettings(localStorage, {
      ...defaultDevHudSettings,
      github: { ...defaultDevHudSettings.github, profiles: [profile] },
      decks: [deck("018f47a2-7b3c-7def-8abc-1234567890ac", "octo/first"), deck("018f47a2-7b3c-7def-8abc-1234567890ad", "octo/second")],
    });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (value.operation === "secure.read") return { kind: "secure-value", value: "github_pat_fixture" };
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={mobileRuntime} />);

    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "secure.read")).toHaveLength(2));
    expect(screen.getByText(messages.en.welcome)).toBeTruthy();
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
