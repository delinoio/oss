// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { DiagnosticArchitecture, DiagnosticComponent, DiagnosticPlatform, DiagnosticSeverity } from "@delinoio/devhud-api-client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import { DiagnosticsCorrelationsKey, DiagnosticsStorageKey } from "./diagnostics";
import * as identityClient from "./identity-client";
import type { IdentitySession } from "./identity-client";
import { messages } from "./localization";
import { LifecycleState, NativeBridgeError, NativeBridgeErrorCode, NotificationPermission, RuntimePlatform, type DesktopUpdaterStatus, type NativeBridgeEventV1, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { desktopNativeMessagingIntegration } from "./native-messaging-ui";
import { saveGuestSettings } from "./service-boundary";
import { defaultDevHudSettings } from "./settings-contract";

const mobileRuntime: RuntimeSnapshot = {
  bridgeVersion: 1,
  platform: RuntimePlatform.Ios,
  operatingSystem: "ios",
  architecture: "arm64",
  osVersion: "16.0",
  appVersion: "0.1.0",
  buildId: "test",
  tauriRevision: "4af26a3f7f8b692d62cca549bbacd93f5ce90b41",
  cefRevision: "",
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
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 1024 });
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
  it("publishes Native Messaging configuration before Settings is opened", async () => {
    const invoke = vi.fn(async () => undefined);
    window.__TAURI_INTERNALS__ = { invoke };
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const bridge = bridgeWith(async (request) => {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      throw new Error(`unexpected operation ${request.operation}`);
    });

    render(<App bridge={bridge} initialRuntime={desktopRuntime} nativeMessaging={desktopNativeMessagingIntegration} />);

    expect(screen.getByRole("heading", { name: messages.en.welcome })).toBeTruthy();
    await waitFor(() => expect(invoke).toHaveBeenCalledWith("native_messaging_replace_configuration", {
      configuration: { origins: [], language: "en" },
      scopeId: expect.stringMatching(/^[0-9a-f-]{36}$/u),
    }, undefined));
  });

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
      protocolSchemaVersion: 2,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      publicAssetBaseUrl: "https://images.example/devhud",
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

  it("keeps a Deck link queued through successful first-run bootstrap", async () => {
    localStorage.removeItem("devhud.shell.onboarding.v1");
    window.__TAURI_INTERNALS__ = { invoke: vi.fn() };
    let deckLink: string | null = "018f47a2-7b3c-7def-8abc-1234567890ab";
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => false,
      signIn: async () => {},
      handleCallback: async () => {},
      clear: async () => {},
    } as unknown as IdentitySession);
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      projectId: "PROJECT_ID_DEVHUD", protocolSchemaVersion: 2, apiVersion: "0.1.0-dev", logtoIssuer: "https://identity.example/oidc", logtoAudience: "https://api.example/api", publicAssetBaseUrl: "https://images.example/devhud",
      logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" }, logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
    }), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } })));
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "deck.peek-pending-link") return { kind: "deck-link", deckId: deckLink };
      if (value.operation === "deck.take-pending-link") { const deckId = deckLink; deckLink = null; return { kind: "deck-link", deckId }; }
      if (value.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={desktopRuntime} />);
    await screen.findByRole("button", { name: messages.en.continueLocally });
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.peek-pending-link" }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(request.mock.calls.filter(([value]) => value.operation === "deck.take-pending-link")).toHaveLength(0);

    fireEvent.click(screen.getByRole("button", { name: messages.en.continueLocally }));
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.take-pending-link" }));
    expect(deckLink).toBeNull();
  });

  it("prunes expired diagnostics during startup without opening Diagnostics", async () => {
    const correlationId = "0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400";
    const occurredAt = "2000-01-01T00:00:00.000Z";
    localStorage.setItem(DiagnosticsStorageKey, JSON.stringify([{
      schemaVersion: 1,
      correlationId,
      occurredAt,
      durationMilliseconds: 0,
      component: DiagnosticComponent.APP,
      severity: DiagnosticSeverity.ERROR,
      outcome: "failed",
      errorCode: "APP_FAILURE",
      summary: "A classified application failure was captured.",
      stackFrames: [],
      relatedCorrelationIds: [],
      build: {
        appVersion: mobileRuntime.appVersion,
        buildId: mobileRuntime.buildId,
        platform: DiagnosticPlatform.IOS,
        architecture: DiagnosticArchitecture.ARM64,
        osVersion: mobileRuntime.osVersion,
        tauriRevision: mobileRuntime.tauriRevision,
        cefRevision: "",
      },
    }]));
    localStorage.setItem(DiagnosticsCorrelationsKey, JSON.stringify([{
      source: "connect-response",
      correlationId,
      operation: "diagnostics",
      occurredAt,
      durationMilliseconds: 1,
    }]));
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const bridge = bridgeWith(async (request) => {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      throw new Error(`unexpected operation ${request.operation}`);
    });

    render(<App bridge={bridge} initialRuntime={mobileRuntime} />);

    await waitFor(() => expect(localStorage.getItem(DiagnosticsStorageKey)).toBeNull());
    expect(localStorage.getItem(DiagnosticsCorrelationsKey)).toBeNull();
    expect(screen.queryByText(messages.en.diagnosticsNoEvents)).toBeNull();
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
      protocolSchemaVersion: 2,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      publicAssetBaseUrl: "https://images.example/devhud",
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
      protocolSchemaVersion: 2,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      publicAssetBaseUrl: "https://images.example/devhud",
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

  it("dispatches a registered desktop capture shortcut into the RealQA selection mode", async () => {
    const runtime: RuntimeSnapshot = { ...desktopRuntime, capabilities: { ...desktopRuntime.capabilities, capture: true } };
    let receive: ((event: NativeBridgeEventV1) => void) | undefined;
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "windows", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const bridge: NativeBridgeV1 = {
      request,
      async listen(listener) { receive = listener; return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.openPalette }));
    expect(screen.getByRole("dialog", { name: messages.en.commandPalette })).toBeTruthy();
    await act(async () => receive?.({ version: 1, kind: "shortcut-triggered", action: "realqa.capture.selection" }));

    expect(await screen.findByRole("dialog", { name: messages.en.captureSelection })).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: messages.en.commandPalette })).toBeNull();
    expect(request).toHaveBeenCalledWith({ operation: "capture.status" });

    fireEvent.click(screen.getByRole("button", { name: messages.en.home }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqa }));
    await screen.findByRole("heading", { name: messages.en.realqaDrafts });
    expect(screen.queryByRole("dialog", { name: messages.en.captureSelection })).toBeNull();
  });

  it("suppresses global shortcuts while an updater approval is open", async () => {
    const updaterStatus: DesktopUpdaterStatus = {
      kind: "available",
      installedVersion: "0.1.0",
      target: "linux-x86_64",
      packageKind: "linux-appimage",
      candidate: { version: "0.2.0", releaseNotes: { en: "Signed notes", ko: "서명된 노트" } },
      diagnostic: null,
    };
    const listeners: Array<(event: NativeBridgeEventV1) => void> = [];
    const bridge: NativeBridgeV1 = {
      async request(value) {
        if (value.operation === "updates.status") return { kind: "desktop-update-status", status: updaterStatus };
        throw new Error(`unexpected operation ${value.operation}`);
      },
      async listen(listener) {
        listeners.push(listener);
        return () => {};
      },
    };

    render(<App bridge={bridge} initialRuntime={desktopRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(await screen.findByRole("button", { name: "Approve download" }));
    expect(screen.getByRole("dialog", { name: "Download this signed update?" })).toBeTruthy();

    await act(async () => {
      for (const listener of listeners) listener({ version: 1, kind: "shortcut-triggered", action: "shell.command-palette" });
    });
    expect(screen.queryByRole("dialog", { name: messages.en.commandPalette })).toBeNull();
    expect(screen.getByRole("dialog", { name: "Download this signed update?" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    await act(async () => {
      for (const listener of listeners) listener({ version: 1, kind: "shortcut-triggered", action: "shell.command-palette" });
    });
    expect(await screen.findByRole("dialog", { name: messages.en.commandPalette })).toBeTruthy();
  });

  it("defers pending Deck links until an updater approval closes", async () => {
    const updaterStatus: DesktopUpdaterStatus = {
      kind: "available",
      installedVersion: "0.1.0",
      target: "linux-x86_64",
      packageKind: "linux-appimage",
      candidate: { version: "0.2.0", releaseNotes: { en: "Signed notes", ko: "서명된 노트" } },
      diagnostic: null,
    };
    const deckId = "018f47a2-7b3c-7def-8abc-1234567890ab";
    const listeners: Array<(event: NativeBridgeEventV1) => void> = [];
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      getAccessToken: async () => null,
      isAuthenticated: async () => false,
      signIn: async () => {},
      handleCallback: async () => {},
      clear: async () => {},
    } as unknown as IdentitySession);
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      projectId: "PROJECT_ID_DEVHUD",
      protocolSchemaVersion: 2,
      apiVersion: "0.1.0-dev",
      logtoIssuer: "https://identity.example/oidc",
      logtoAudience: "https://api.example/api",
      publicAssetBaseUrl: "https://images.example/devhud",
      logtoClients: { desktop: "desktop-client", ios: "ios-client", android: "android-client", admin: "admin-client" },
      logtoRedirects: { native: "devhud://auth/callback", admin: "https://admin.example/callback" },
    }), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } })));
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (value.operation === "updates.status") return { kind: "desktop-update-status", status: updaterStatus };
      if (value.operation === "deck.peek-pending-link") return { kind: "deck-link", deckId };
      if (value.operation === "deck.take-pending-link") return { kind: "deck-link", deckId };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const bridge: NativeBridgeV1 = {
      request,
      async listen(listener) {
        listeners.push(listener);
        return () => {};
      },
    };

    render(<App bridge={bridge} initialRuntime={desktopRuntime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(await screen.findByRole("button", { name: "Approve download" }));
    expect(screen.getByRole("dialog", { name: "Download this signed update?" })).toBeTruthy();
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "session.configure-origins", apiOrigin: "https://devhud.api.delino.io" }));

    await act(async () => {
      for (const listener of listeners) listener({ version: 1, kind: "deck-link", deckId });
    });
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.peek-pending-link" }));
    expect(request.mock.calls.filter(([value]) => value.operation === "deck.take-pending-link")).toHaveLength(0);
    expect(screen.getByRole("dialog", { name: "Download this signed update?" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Go back" }));

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "deck.take-pending-link" }));
    await waitFor(() => expect(screen.getByRole("button", { name: messages.en.deck }).getAttribute("aria-current")).toBe("page"));
  });

  it("keeps the palette modal while a capture completes and preserves its confirmation across navigation", async () => {
    const runtime: RuntimeSnapshot = { ...desktopRuntime, capabilities: { ...desktopRuntime.capabilities, capture: true } };
    let resolveCapture: ((response: NativeBridgeResponseV1) => void) | undefined;
    const capturedDraft = {
      id: "019b0000-0000-7000-8000-000000000001",
      revision: 1,
      createdAt: 1_700_000_000,
      updatedAt: 1_700_000_000,
      expiresAt: 1_702_592_000,
      hasBrowserContext: false,
      imageCount: 1,
      images: [{ id: "019b0000-0000-7000-8000-000000000002", width: 800, height: 600, previewUrl: "realqa://asset/draft/image/source/1", crop: null, layers: [] }],
      canUndo: false,
      canRedo: false,
    };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "windows", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      if (value.operation === "capture.start") return new Promise((resolve) => { resolveCapture = resolve; });
      throw new Error(`unexpected operation ${value.operation}`);
    });

    render(<App bridge={bridgeWith(request)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.realqa }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.captureDisplay }));
    await waitFor(() => expect(request.mock.calls.some(([value]) => value.operation === "capture.start")).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: messages.en.openPalette }));
    const palette = screen.getByRole("dialog", { name: messages.en.commandPalette });
    expect(palette).toBeTruthy();

    await act(async () => { resolveCapture?.({ kind: "capture-draft", draft: capturedDraft }); });
    expect(screen.queryByRole("complementary", { name: messages.en.floatingPreview })).toBeNull();
    fireEvent.click(within(palette).getByRole("button", { name: messages.en.close }));
    expect(await screen.findByRole("complementary", { name: messages.en.floatingPreview })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: messages.en.home }));
    expect(screen.getByRole("complementary", { name: messages.en.floatingPreview })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: messages.en.floatingPreviewOpen }));
    expect(await screen.findByRole("heading", { name: messages.en.editorTitle })).toBeTruthy();
  });
});

describe("responsive application shell", () => {
  const unavailableBridge = () => bridgeWith(async (request) => {
    throw new Error(`unexpected operation ${request.operation}`);
  });

  it("renders the six-surface desktop sidebar and only the four approved Home tools", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 1440 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={desktopRuntime} />);

    const shell = document.querySelector<HTMLElement>("[data-shell-layout]");
    expect(shell?.dataset.shellLayout).toBe("sidebar");
    const navigation = screen.getByRole("navigation", { name: messages.en.mobileNavigation });
    expect(within(navigation).getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual([
      messages.en.home, messages.en.realqa, messages.en.deck, messages.en.settings, messages.en.account, messages.en.diagnostics,
    ]);
    expect(within(navigation).getByRole("button", { name: messages.en.home }).getAttribute("aria-current")).toBe("page");
    const tools = document.querySelector(".tool-grid");
    expect(tools).toBeTruthy();
    expect(within(tools as HTMLElement).getAllByRole("button")).toHaveLength(4);
    expect((tools as HTMLElement).textContent).toContain(messages.en.realqaTitle);
    expect((tools as HTMLElement).textContent).toContain(messages.en.deckTitle);
    expect((tools as HTMLElement).textContent).toContain(messages.en.settingsTitle);
    expect((tools as HTMLElement).textContent).toContain(messages.en.diagnosticsTitle);
    expect((tools as HTMLElement).textContent).not.toContain(messages.en.accountTitle);
  });

  it("uses availability-neutral RealQA copy on a capture-capable narrow desktop", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 700 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    const runtime: RuntimeSnapshot = { ...desktopRuntime, capabilities: { ...desktopRuntime.capabilities, capture: true } };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "windows", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    render(<App bridge={bridgeWith(request)} initialRuntime={runtime} />);

    expect(within(document.querySelector(".tool-grid") as HTMLElement).getByText(messages.en.realqaSummary)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: messages.en.more }));
    expect(within(screen.getByRole("dialog", { name: messages.en.more })).getByText(messages.en.realqaSummary)).toBeTruthy();
  });

  it.each([1023, 701])("renders the named tooltip rail at %ipx", (width) => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: width });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={desktopRuntime} />);

    expect(document.querySelector<HTMLElement>("[data-shell-layout]")?.dataset.shellLayout).toBe("rail");
    const navigation = screen.getByRole("navigation", { name: messages.en.mobileNavigation });
    const destinations = within(navigation).getAllByRole("button");
    expect(destinations).toHaveLength(6);
    for (const destination of destinations) {
      expect(destination.getAttribute("aria-label")).toBeTruthy();
      expect(destination.getAttribute("aria-describedby")).toMatch(/^navigation-tooltip-/u);
    }
    expect(screen.getAllByRole("tooltip")).toHaveLength(7);
  });

  it.each([700, 390, 320])("renders exactly five mobile navigation items at %ipx", (width) => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: width });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={mobileRuntime} />);

    expect(document.querySelector<HTMLElement>("[data-shell-layout]")?.dataset.shellLayout).toBe("mobile");
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByRole("heading", { level: 1, name: messages.en.appName })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: messages.en.welcome })).toBeTruthy();
    const navigation = screen.getByRole("navigation", { name: messages.en.mobileNavigation });
    expect(within(navigation).getAllByRole("button").map((button) => button.textContent)).toEqual([
      messages.en.home, messages.en.deck, messages.en.settings, messages.en.account, messages.en.more,
    ]);
    expect(screen.queryByRole("button", { name: messages.en.realqa })).toBeNull();
    expect(screen.queryByRole("button", { name: messages.en.diagnostics })).toBeNull();
  });

  it("closes More before shortcut-driven palette and capture actions", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 700 });
    const runtime: RuntimeSnapshot = { ...desktopRuntime, capabilities: { ...desktopRuntime.capabilities, capture: true } };
    let receive: ((event: NativeBridgeEventV1) => void) | undefined;
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "windows", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    const bridge: NativeBridgeV1 = {
      request,
      async listen(listener) { receive = listener; return () => {}; },
    };
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={bridge} initialRuntime={runtime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));

    const more = screen.getByRole("button", { name: messages.en.more });
    fireEvent.click(more);
    expect(screen.getByRole("dialog", { name: messages.en.more })).toBeTruthy();
    await act(async () => receive?.({ version: 1, kind: "shortcut-triggered", action: "shell.command-palette" }));
    expect(screen.queryByRole("dialog", { name: messages.en.more })).toBeNull();
    const palette = screen.getByRole("dialog", { name: messages.en.commandPalette });
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("textbox", { name: messages.en.searchCommands })));

    fireEvent.click(within(palette).getByRole("button", { name: messages.en.close }));
    fireEvent.click(more);
    expect(screen.getByRole("dialog", { name: messages.en.more })).toBeTruthy();
    await act(async () => receive?.({ version: 1, kind: "shortcut-triggered", action: "realqa.capture.selection" }));
    expect(screen.queryByRole("dialog", { name: messages.en.more })).toBeNull();
    expect(await screen.findByRole("dialog", { name: messages.en.captureSelection })).toBeTruthy();
  });

  it("restores palette focus to the mounted trigger after crossing the mobile breakpoint", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 700 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={desktopRuntime} />);

    const mobileTrigger = screen.getByRole("button", { name: messages.en.openPalette });
    fireEvent.click(mobileTrigger);
    const palette = screen.getByRole("dialog", { name: messages.en.commandPalette });
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("textbox", { name: messages.en.searchCommands })));

    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 701 });
    fireEvent(window, new Event("resize"));
    await waitFor(() => expect(document.querySelector<HTMLElement>("[data-shell-layout]")?.dataset.shellLayout).toBe("rail"));
    const railTrigger = screen.getByRole("button", { name: messages.en.openPalette });
    expect(railTrigger).not.toBe(mobileTrigger);
    fireEvent.keyDown(palette, { key: "Escape" });
    await waitFor(() => expect(document.activeElement).toBe(railTrigger));
  });

  it("moves More focus to the selected desktop destination after crossing the mobile breakpoint", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 700 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={desktopRuntime} />);

    const mobileNavigation = screen.getByRole("navigation", { name: messages.en.mobileNavigation });
    const more = within(mobileNavigation).getByRole("button", { name: messages.en.more });
    fireEvent.click(more);
    fireEvent.click(within(screen.getByRole("dialog", { name: messages.en.more })).getByRole("button", { name: /Diagnostics/u }));
    await waitFor(() => expect(document.activeElement).toBe(more));

    fireEvent.click(more);
    const sheet = screen.getByRole("dialog", { name: messages.en.more });
    await waitFor(() => expect(sheet.contains(document.activeElement)).toBe(true));

    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 701 });
    fireEvent(window, new Event("resize"));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: messages.en.more })).toBeNull());

    const desktopNavigation = screen.getByRole("navigation", { name: messages.en.mobileNavigation });
    const selectedDestination = within(desktopNavigation).getByRole("button", { name: messages.en.diagnostics });
    await waitFor(() => expect(document.activeElement).toBe(selectedDestination));
  });

  it("manages More focus, retains selection, and groups mobile RealQA with Diagnostics", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 390 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={mobileRuntime} />);
    const navigation = screen.getByRole("navigation", { name: messages.en.mobileNavigation });
    const home = within(navigation).getByRole("button", { name: messages.en.home });
    const more = within(navigation).getByRole("button", { name: messages.en.more });
    expect(home.getAttribute("aria-current")).toBe("page");

    fireEvent.click(more);
    const sheet = screen.getByRole("dialog", { name: messages.en.more });
    expect(home.getAttribute("aria-current")).toBe("page");
    const realqa = within(sheet).getByRole("button", { name: /RealQA/u });
    expect(realqa.textContent).toContain(messages.en.desktopOnly);
    await waitFor(() => expect(document.activeElement).toBe(realqa));
    fireEvent.click(realqa);
    expect(screen.getByRole("heading", { name: messages.en.realqaMobileTitle })).toBeTruthy();
    expect(more.getAttribute("aria-current")).toBe("page");

    fireEvent.click(more);
    fireEvent.keyDown(screen.getByRole("dialog", { name: messages.en.more }), { key: "Escape" });
    await waitFor(() => expect(document.activeElement).toBe(more));
    expect(screen.getByRole("heading", { name: messages.en.realqaMobileTitle })).toBeTruthy();

    fireEvent.click(more);
    fireEvent.click(within(screen.getByRole("dialog", { name: messages.en.more })).getByRole("button", { name: /Diagnostics/u }));
    expect(screen.getByRole("heading", { name: messages.en.diagnosticsTitle })).toBeTruthy();
    expect(more.getAttribute("aria-current")).toBe("page");
  });

  it("moves localized skip-link focus to the main content", () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={unavailableBridge()} initialRuntime={desktopRuntime} />);
    fireEvent.click(screen.getByRole("link", { name: messages.en.skipToContent }));
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });

  it("changes shell layouts without remounting the desktop RealQA controller", async () => {
    const runtime: RuntimeSnapshot = { ...desktopRuntime, capabilities: { ...desktopRuntime.capabilities, capture: true } };
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "capture.status") return { kind: "capture-status", available: true, platform: "windows", shadowRemovalSupported: false, topology: [] };
      if (value.operation === "capture.list-drafts") return { kind: "capture-drafts", drafts: [], unreadableDraftIds: [] };
      throw new Error(`unexpected operation ${value.operation}`);
    });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    render(<App bridge={bridgeWith(request)} initialRuntime={runtime} />);
    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "capture.status")).toHaveLength(1));

    for (const width of [1023, 700, 1440]) {
      Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: width });
      fireEvent(window, new Event("resize"));
    }
    await waitFor(() => expect(document.querySelector<HTMLElement>("[data-shell-layout]")?.dataset.shellLayout).toBe("sidebar"));
    expect(request.mock.calls.filter(([value]) => value.operation === "capture.status")).toHaveLength(1);
    expect(request.mock.calls.filter(([value]) => value.operation === "capture.list-drafts")).toHaveLength(1);
  });
});
