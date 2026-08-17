// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { create, toBinary } from "@bufbuild/protobuf";
import { SettingsRevisionConflictSchema } from "@delinoio/devhud-api-client";
import { useQueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../fixtures/identity-settings-e2e.json";
import { App } from "./App";
import { messages } from "./localization";
import { hasGuestSettings, readAuthenticatedSettingsCache, readGuestSettings, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
import { canonicalDevHudSettings, defaultDevHudSettings } from "./settings-contract";
import { LifecycleState, RuntimePlatform, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { clearIdentityForApiChange, DevHudServiceBoundary, useIdentitySettings } from "./service-boundary";

const runtime: RuntimeSnapshot = {
  bridgeVersion: 1,
  platform: RuntimePlatform.Desktop,
  architecture: "x86_64",
  osVersion: "fixture",
  lifecycle: LifecycleState.Active,
  capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false },
};

function connectResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } });
}

function authenticatedBridge(purgeScopes: string[] = [], secureOperations: string[] = []): NativeBridgeV1 {
  const accessTokenMap = JSON.stringify({ "@https://api.example/api": { token: "fixture-access-token", scope: "", expiresAt: 4_102_444_800 } });
  const secureSession = JSON.stringify({ idToken: "fixture-id-token", accessToken: accessTokenMap });
  return {
    async request(request) {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (request.operation === "secure.read") { secureOperations.push("read"); return { kind: "secure-value", value: request.setting.kind === "logto-session" ? secureSession : null }; }
      if (request.operation === "secure.purge") { purgeScopes.push(request.scope); return { kind: "ok" }; }
      if (request.operation === "secure.write" || request.operation === "secure.remove") { secureOperations.push(request.operation); return { kind: "ok" }; }
      if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
      throw new Error(`unexpected bridge operation ${request.operation}`);
    },
    async listen() { return () => {}; },
  };
}

function signedOutBridge(): NativeBridgeV1 {
  return {
    async request(request) {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (request.operation === "secure.read") return { kind: "secure-value", value: null };
      if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
      throw new Error(`unexpected bridge operation ${request.operation}`);
    },
    async listen() { return () => {}; },
  };
}

function encodedSettings(value: unknown): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(canonicalDevHudSettings(value))));
}

function IdentityStateProbe({ replacement = defaultDevHudSettings }: { readonly replacement?: typeof defaultDevHudSettings }) {
  const identity = useIdentitySettings();
  const queryClient = useQueryClient();
  return <>
    <output
      data-testid="identity-state"
      data-status={identity.status}
      data-read-only={String(identity.readOnly)}
      data-revision={identity.revision.toString()}
      data-theme={identity.settings.appearance.theme}
      data-error={identity.error ?? ""}
      data-account-error={identity.accountError?.code ?? ""}
      data-account-correlation={identity.accountError?.correlationId ?? ""}
      data-correlation={identity.settingsError?.correlationId ?? ""}
      data-query-data-count={queryClient.getQueryCache().getAll().filter((query) => query.state.data !== undefined).length}
    />
    <button type="button" onClick={() => void identity.replaceSettings(replacement).catch(() => {})}>replace probe settings</button>
    <button type="button" onClick={() => void identity.logout().catch(() => {})}>logout probe identity</button>
  </>;
}

function renderIdentityProbe(bridge: NativeBridgeV1, replacement = defaultDevHudSettings) {
  return render(<DevHudServiceBoundary
    apiOrigin="https://devhud.api.delino.io"
    active
    online
    callbackUrl={null}
    platform={RuntimePlatform.Desktop}
    bridge={bridge}
    onContinueLocally={() => {}}
    onLoggedOut={() => {}}
  ><IdentityStateProbe replacement={replacement} /></DevHudServiceBoundary>);
}

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("devhud.shell.onboarding.v1", "complete");
  Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
  Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })) });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("generated Connect identity/settings fixture", () => {
  it("stores signed-out appearance edits in the guest snapshot", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      throw new Error(`unexpected request ${url}`);
    }));
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };

    renderIdentityProbe(signedOutBridge(), replacement);
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("signed-out"));
    fireEvent.click(screen.getByRole("button", { name: "replace probe settings" }));

    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.theme).toBe("dark"));
    expect(hasGuestSettings(localStorage)).toBe(true);
    expect(readGuestSettings(localStorage).appearance.theme).toBe("dark");
  });

  it("applies fetched appearance and persists control edits through ReplaceSettings", async () => {
    const server = { ...defaultDevHudSettings, appearance: { theme: "dark" as const, language: "en" as const } };
    const replacement = { ...server, appearance: { ...server.appearance, theme: "light" as const } };
    let replacements = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        replacements += 1;
        return connectResponse({ snapshot: { schemaVersion: 1, revision: "2", canonicalJson: encodedSettings(replacement) } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    const theme = await screen.findByLabelText(messages.en.theme) as HTMLSelectElement;
    await waitFor(() => {
      expect(theme.value).toBe("dark");
      expect(theme.disabled).toBe(false);
      expect(document.documentElement.dataset.theme).toBe("dark");
    });
    fireEvent.change(theme, { target: { value: "light" } });

    await waitFor(() => expect(document.documentElement.dataset.theme).toBe("light"));
    expect(replacements).toBe(1);
  });

  it("requires explicit guest upload and preserves only the recovery session through deletion", async () => {
    const local = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    writeGuestSettings(localStorage, local);
    const encoder = new TextEncoder();
    const toBase64 = (value: unknown) => btoa(String.fromCharCode(...encoder.encode(canonicalDevHudSettings(value))));
    const replaceBodies: Record<string, unknown>[] = [];
    const recoveryUntil = new Date(Date.now() + (30 * 24 * 60 * 60 * 1000)).toISOString();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: fixture.serverRevision, canonicalJson: toBase64(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const source = typeof init?.body === "string" ? init.body : new TextDecoder().decode(init?.body as ArrayBufferView<ArrayBuffer>);
        replaceBodies.push(JSON.parse(source) as Record<string, unknown>);
        if (replaceBodies.length === 1) {
          const detail = create(SettingsRevisionConflictSchema, {
            expectedRevision: 3n,
            currentSnapshot: { schemaVersion: 1, revision: 4n, canonicalJson: encoder.encode(canonicalDevHudSettings(server)) },
          });
          const value = btoa(String.fromCharCode(...toBinary(SettingsRevisionConflictSchema, detail)));
          return new Response(JSON.stringify({ code: "aborted", message: "settings revision conflict", details: [{ type: SettingsRevisionConflictSchema.typeName, value }] }), { status: 409, headers: { "Content-Type": "application/json" } });
        }
        return connectResponse({ snapshot: { schemaVersion: 1, revision: "5", canonicalJson: toBase64(local) } });
      }
      if (url.endsWith("/devhud.v1.AccountService/DeleteAccount")) return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PENDING", recoverableUntil: recoveryUntil } });
      if (url.endsWith("/devhud.v1.AccountService/RestoreAccount")) return connectResponse({ account: fixture.account });
      throw new Error(`unexpected fixture request ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    const accessTokenMap = JSON.stringify({ "@https://api.example/api": { token: "fixture-access-token", scope: "", expiresAt: 4_102_444_800 } });
    const secureSession = JSON.stringify({ idToken: "fixture-id-token", accessToken: accessTokenMap });
    const purgeScopes: string[] = [];
    const bridge: NativeBridgeV1 = {
      async request(request: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: request.setting.kind === "logto-session" ? secureSession : null };
        if (request.operation === "secure.purge") { purgeScopes.push(request.scope); return { kind: "ok" }; }
        if (request.operation === "secure.write" || request.operation === "secure.remove") return { kind: "ok" };
        if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    expect(await screen.findByText("Fixture User")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    expect(await screen.findByText(messages.en.importSettingsTitle)).toBeTruthy();
    expect(screen.getByText("$.appearance.theme")).toBeTruthy();
    expect(screen.getByText("dark")).toBeTruthy();
    expect(screen.getByText("light")).toBeTruthy();
    expect(replaceBodies).toHaveLength(0);

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: messages.en.importSettingsTitle })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: messages.en.importSettingsTitle }));
    expect(screen.getByRole("dialog", { name: messages.en.importSettingsTitle })).toBeTruthy();
    expect(screen.getByText("$.appearance.theme")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.uploadLocal }));
    expect(await screen.findByText(messages.en.conflictTitle)).toBeTruthy();
    expect(replaceBodies[0]?.expectedRevision).toBe(fixture.serverRevision);
    expect(screen.getByText("$.appearance.theme")).toBeTruthy();
    expect(screen.queryByText(messages.en.importSettingsTitle)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: messages.en.reapplyLocal }));
    await waitFor(() => expect(replaceBodies).toHaveLength(2));
    expect(replaceBodies[1]?.expectedRevision).toBe("4");
    await waitFor(() => expect(screen.getByLabelText(messages.en.synchronizedSettings).textContent).toContain(`${messages.en.settingsRevision}: 5`));
    expect(screen.queryByText(messages.en.importSettingsTitle)).toBeNull();
    expect(screen.queryByText(messages.en.conflictTitle)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.deleteAccount }));
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: messages.en.deleteAccount }));
    const confirmation = screen.getByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle });
    fireEvent.click(within(confirmation).getByRole("button", { name: messages.en.deleteAccount }));
    expect(await screen.findByText(messages.en.deletionPendingTitle)).toBeTruthy();
    expect(purgeScopes).toEqual(["account-deletion"]);
    expect(screen.getByRole("button", { name: messages.en.restoreAccount })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: messages.en.restoreAccount }));
    expect(await screen.findByText("Fixture User")).toBeTruthy();
    expect(purgeScopes).toEqual(["account-deletion"]);

    fireEvent.click(screen.getByRole("button", { name: messages.en.logout }));
    await waitFor(() => expect(purgeScopes).toEqual(["account-deletion", "logout"]));
    expect(await screen.findByRole("button", { name: messages.en.signIn })).toBeTruthy();
  });

  it("loads an authenticated cache read-only while offline and keeps direct local surfaces available", async () => {
    Object.defineProperty(navigator, "onLine", { configurable: true, value: false });
    const apiOrigin = "https://devhud.api.delino.io";
    writeCachedIdentityBootstrap(localStorage, apiOrigin, {
      issuer: "https://identity.example",
      audience: "https://api.example",
      clientId: "desktop-client",
      redirectUri: "devhud://auth/callback",
    });
    writeAuthenticatedSettingsCache(localStorage, apiOrigin, {
      settings: { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" } },
      revision: 9n,
      cachedAt: "2026-08-17T00:00:00.000Z",
    });
    const accessTokenMap = JSON.stringify({ "@https://api.example": { token: "offline-token", scope: "", expiresAt: 4_102_444_800 } });
    const secureSession = JSON.stringify({ idToken: "offline-id-token", accessToken: accessTokenMap });
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: request.setting.kind === "logto-session" ? secureSession : null };
        if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    expect(await screen.findByText(messages.en.offlineSettingsReadOnly)).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: messages.en.home }));
    expect(await screen.findByRole("heading", { name: messages.en.welcome })).toBeTruthy();
  });

  it("blocks authenticated service actions without disabling local navigation", async () => {
    const encoder = new TextEncoder();
    const toBase64 = (value: unknown) => btoa(String.fromCharCode(...encoder.encode(canonicalDevHudSettings(value))));
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, administrativeBlockState: "ADMINISTRATIVE_BLOCK_STATE_BLOCKED" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: toBase64(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    const accessTokenMap = JSON.stringify({ "@https://api.example/api": { token: "blocked-token", scope: "", expiresAt: 4_102_444_800 } });
    const secureSession = JSON.stringify({ idToken: "blocked-id-token", accessToken: accessTokenMap });
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: request.setting.kind === "logto-session" ? secureSession : null };
        if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
        if (request.operation === "secure.purge" || request.operation === "secure.remove") return { kind: "ok" };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    expect(await screen.findByText(messages.en.blockedTitle)).toBeTruthy();
    expect(screen.getByText(messages.en.blockedLocalHint)).toBeTruthy();
    expect(screen.queryByRole("button", { name: messages.en.deleteAccount })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: messages.en.home }));
    expect(await screen.findByRole("heading", { name: messages.en.welcome })).toBeTruthy();
  });

  it("retries local credential cleanup when a pending deletion is observed", async () => {
    const purges: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PENDING" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge(purges)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    expect(await screen.findByText(messages.en.deletionPendingTitle)).toBeTruthy();
    await waitFor(() => expect(purges).toEqual(["account-deletion"]));
    expect(screen.getByRole("button", { name: messages.en.restoreAccount })).toBeTruthy();
  });

  it("clears an irreversible purge-claimed account without exposing restore", async () => {
    const purges: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PURGE_CLAIMED" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge(purges)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    await waitFor(() => expect(purges).toEqual(["logout"]));
    expect(screen.getByRole("button", { name: messages.en.signIn })).toBeTruthy();
    expect(screen.queryByRole("button", { name: messages.en.restoreAccount })).toBeNull();
  });

  it("clears an invalid session when GetAccount returns unauthenticated", async () => {
    const secureOperations: string[] = [];
    writeAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io", { settings: { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" } }, revision: 9n, cachedAt: "2026-08-17T00:00:00.000Z" });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return new Response(JSON.stringify({ code: "unauthenticated", message: "invalid credentials" }), { status: 401, headers: { "Content-Type": "application/json" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge([], secureOperations)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    await waitFor(() => expect(secureOperations).toContain("secure.remove"));
    expect(screen.getByRole("button", { name: messages.en.signIn })).toBeTruthy();
    await waitFor(() => expect(readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io")).toBeNull());
  });

  it("surfaces typed GetAccount failures without exposing destructive actions and retries them", async () => {
    const correlationId = "018f47a2-7b3c-7def-8abc-1234567890ef";
    let accountRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) {
        accountRequests += 1;
        if (accountRequests === 1) return new Response(JSON.stringify({ code: "unavailable", message: "retry later" }), { status: 503, headers: { "Content-Type": "application/json", "x-devhud-correlation-id": correlationId } });
        return connectResponse({ account: fixture.account });
      }
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en.accountLoadFailed);
    expect(alert.textContent).toContain("account-connect-14");
    expect(alert.textContent).toContain(correlationId);
    expect(screen.queryByRole("button", { name: messages.en.deleteAccount })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: messages.en.retry }));

    expect(await screen.findByText("Fixture User")).toBeTruthy();
    expect(accountRequests).toBe(2);
  });

  it.each([
    ["en", messages.en],
    ["ko", messages.ko],
  ] as const)("recovers the post-onboarding %s identity surface by retrying Bootstrap", async (language, copy) => {
    localStorage.setItem("devhud.shell.preferences.v1", JSON.stringify({ version: 1, theme: "system", language, apiOrigin: "https://devhud.api.delino.io", launchAtLogin: false }));
    let bootstrapRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (!url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) throw new Error(`unexpected request ${url}`);
      bootstrapRequests += 1;
      if (bootstrapRequests === 1) return new Response(JSON.stringify({ code: "unavailable", message: "retry" }), { status: 503, headers: { "Content-Type": "application/json" } });
      return connectResponse(fixture.bootstrap);
    }));
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: null };
        if (request.operation === "auth.take-pending-callback") return { kind: "auth-callback", url: null };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: copy.account }));
    expect((await screen.findByRole("alert")).textContent).toContain(copy.bootstrapFailed);
    fireEvent.click(screen.getByRole("button", { name: copy.retry }));
    expect(await screen.findByRole("button", { name: copy.signIn })).toBeTruthy();
    expect(bootstrapRequests).toBeGreaterThanOrEqual(2);
  });

  it("preserves the correlation ID from a failed Settings replacement", async () => {
    const correlationId = "018f47a2-7b3c-7def-8abc-1234567890ab";
    writeGuestSettings(localStorage, { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" } });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) return new Response(JSON.stringify({ code: "unavailable", message: "retry later" }), { status: 503, headers: { "Content-Type": "application/json", "x-devhud-correlation-id": correlationId } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.uploadLocal }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(messages.en.settingsActionFailed);
    expect(alert.textContent).toContain(messages.en.correlationId);
    expect(alert.textContent).toContain(correlationId);
  });

  it("keeps online identity bootstrap usable when Web Storage rejects the cache write", async () => {
    const originalSetItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) {
      if (key.endsWith(".bootstrap")) throw new DOMException("quota exceeded", "QuotaExceededError");
      originalSetItem.call(this, key, value);
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      throw new Error(`unexpected request ${url}`);
    }));
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: null };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    await waitFor(() => expect((screen.getByRole("button", { name: messages.en.signIn }) as HTMLButtonElement).disabled).toBe(false));
  });

  it("keeps authenticated settings usable when Web Storage rejects cache writes", async () => {
    const originalSetItem = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key, value) {
      if (key.endsWith(".settings")) throw new DOMException("quota exceeded", "QuotaExceededError");
      originalSetItem.call(this, key, value);
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "7", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected request ${url}`);
    }));

    renderIdentityProbe(authenticatedBridge());

    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.status).toBe("authenticated");
      expect(state.dataset.readOnly).toBe("false");
      expect(state.dataset.revision).toBe("7");
      expect(state.dataset.error).toBe("");
    });
  });

  it("drains the old identity session before the final API-origin purge", async () => {
    const operations: string[] = [];
    let release: (() => void) | undefined;
    const draining = new Promise<void>((resolve) => { release = resolve; });
    const sessionRef = {
      current: {
        clear: async () => {
          operations.push("drain-start");
          await draining;
          operations.push("late-secure-write-settled");
        },
      },
    } as unknown as Parameters<typeof clearIdentityForApiChange>[3];
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "secure.purge") { operations.push("purge"); return { kind: "ok" }; }
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    const clearing = clearIdentityForApiChange(bridge, localStorage, "https://devhud.api.delino.io", sessionRef);
    await waitFor(() => expect(operations).toEqual(["drain-start"]));
    expect(sessionRef?.current).toBeNull();
    release?.();
    await clearing;

    expect(operations).toEqual(["drain-start", "late-secure-write-settled", "purge"]);
  });

  it("keeps the last cached settings read-only and surfaces GetSettings correlation metadata", async () => {
    const correlationId = "018f47a2-7b3c-7def-8abc-1234567890cd";
    const cached = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    writeAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io", { settings: cached, revision: 9n, cachedAt: "2026-08-17T00:00:00.000Z" });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return new Response(JSON.stringify({ code: "unavailable", message: "retry later" }), { status: 503, headers: { "Content-Type": "application/json", "x-devhud-correlation-id": correlationId } });
      throw new Error(`unexpected request ${url}`);
    }));

    renderIdentityProbe(authenticatedBridge());

    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.status).toBe("authenticated");
      expect(state.dataset.readOnly).toBe("true");
      expect(state.dataset.revision).toBe("9");
      expect(state.dataset.theme).toBe("dark");
      expect(state.dataset.correlation).toBe(correlationId);
    });
  });

  it("removes identity-scoped React Query data on logout", async () => {
    const accessTokenMap = JSON.stringify({ "@https://api.example/api": { token: "fixture-access-token", scope: "", expiresAt: 4_102_444_800 } });
    const secureSession = JSON.stringify({ idToken: "fixture-id-token", accessToken: accessTokenMap });
    let authenticated = true;
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: authenticated && request.setting.kind === "logto-session" ? secureSession : null };
        if (request.operation === "secure.purge" || request.operation === "secure.remove") { authenticated = false; return { kind: "ok" }; }
        if (request.operation === "secure.write") return { kind: "ok" };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected request ${url}`);
    }));

    renderIdentityProbe(bridge);
    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.status).toBe("authenticated");
      expect(state.dataset.queryDataCount).toBe("3");
    });
    fireEvent.click(screen.getByRole("button", { name: "logout probe identity" }));

    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.status).toBe("signed-out");
      expect(state.dataset.queryDataCount).toBe("1");
    });
  });

  it("rejects an unsupported ReplaceSettings envelope without changing or caching it", async () => {
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "2", canonicalJson: encodedSettings(replacement) } });
      throw new Error(`unexpected request ${url}`);
    }));

    renderIdentityProbe(authenticatedBridge(), replacement);
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.readOnly).toBe("false"));
    fireEvent.click(screen.getByRole("button", { name: "replace probe settings" }));

    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.readOnly).toBe("true");
      expect(state.dataset.revision).toBe("1");
      expect(state.dataset.theme).toBe("light");
      expect(state.dataset.error).toBe("settings-contract-invalid");
    });
    const persisted = readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io");
    expect(persisted?.revision).toBe(1n);
    expect(persisted?.settings.appearance.theme).toBe("light");
  });

  it("rejects an unsupported conflict envelope before decoding it", async () => {
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const detail = create(SettingsRevisionConflictSchema, {
          expectedRevision: 1n,
          currentSnapshot: { schemaVersion: 2, revision: 2n, canonicalJson: new TextEncoder().encode(canonicalDevHudSettings(server)) },
        });
        const value = btoa(String.fromCharCode(...toBinary(SettingsRevisionConflictSchema, detail)));
        return new Response(JSON.stringify({ code: "aborted", message: "settings revision conflict", details: [{ type: SettingsRevisionConflictSchema.typeName, value }] }), { status: 409, headers: { "Content-Type": "application/json" } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    renderIdentityProbe(authenticatedBridge(), replacement);
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.readOnly).toBe("false"));
    fireEvent.click(screen.getByRole("button", { name: "replace probe settings" }));

    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.readOnly).toBe("true");
      expect(state.dataset.revision).toBe("1");
      expect(state.dataset.theme).toBe("light");
      expect(state.dataset.error).toBe("settings-contract-invalid");
    });
    expect(readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io")?.revision).toBe(1n);
  });

  it("clears the guest import marker when a conflicted upload adopts the server", async () => {
    const local = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    writeGuestSettings(localStorage, local);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "3", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const detail = create(SettingsRevisionConflictSchema, {
          expectedRevision: 3n,
          currentSnapshot: { schemaVersion: 1, revision: 4n, canonicalJson: new TextEncoder().encode(canonicalDevHudSettings(server)) },
        });
        const value = btoa(String.fromCharCode(...toBinary(SettingsRevisionConflictSchema, detail)));
        return new Response(JSON.stringify({ code: "aborted", message: "settings revision conflict", details: [{ type: SettingsRevisionConflictSchema.typeName, value }] }), { status: 409, headers: { "Content-Type": "application/json" } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.uploadLocal }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.adoptServer }));

    expect(hasGuestSettings(localStorage)).toBe(false);
    expect(screen.queryByText(messages.en.conflictTitle)).toBeNull();
  });

  it("restores a deletion-pending blocked account without refetching forbidden settings", async () => {
    let settingsRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PENDING", administrativeBlockState: "ADMINISTRATIVE_BLOCK_STATE_BLOCKED" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) {
        settingsRequests += 1;
        if (settingsRequests > 1) return new Response(JSON.stringify({ code: "permission_denied", message: "blocked" }), { status: 403, headers: { "Content-Type": "application/json" } });
        return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      }
      if (url.endsWith("/devhud.v1.AccountService/RestoreAccount")) return connectResponse({ account: { ...fixture.account, administrativeBlockState: "ADMINISTRATIVE_BLOCK_STATE_BLOCKED" } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.restoreAccount }));

    expect(await screen.findByText(messages.en.blockedTitle)).toBeTruthy();
    expect(screen.queryByText(messages.en.accountActionFailed)).toBeNull();
    expect(settingsRequests).toBeLessThanOrEqual(1);
  });

  it.each([
    ["unsupported schema", 2, encodedSettings(defaultDevHudSettings)],
    ["noncanonical body", 1, btoa('{ "schemaVersion": 1 }')],
  ])("keeps malformed server settings recoverable for %s", async (_name, schemaVersion, canonicalJson) => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion, revision: "1", canonicalJson } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));

    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.settingsActionFailed);
    expect(screen.getByRole("heading", { name: messages.en.settingsTitle })).toBeTruthy();
  });
});
