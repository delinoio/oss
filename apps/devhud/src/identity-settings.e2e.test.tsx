// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { create, toBinary } from "@bufbuild/protobuf";
import { PermissionFailureReason, PermissionFailureSchema, SettingsRevisionConflictSchema } from "@delinoio/devhud-api-client";
import { LogtoRequestError } from "@logto/client";
import { useQueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../fixtures/identity-settings-e2e.json";
import { App } from "./App";
import * as identityClient from "./identity-client";
import type { IdentitySession } from "./identity-client";
import { SynchronizedSettingsBoundary } from "./identity-ui";
import { messages } from "./localization";
import { hasGuestSettings, readAuthenticatedSettingsCache, readGuestSettings, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
import { canonicalDevHudSettings, defaultDevHudSettings, parseDevHudSettings } from "./settings-contract";
import { LifecycleState, RuntimePlatform, type NativeBridgeEventV1, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1, type RuntimeSnapshot } from "./native-bridge";
import { clearIdentityForApiChange, DevHudServiceBoundary, useIdentitySettings } from "./service-boundary";

const runtime: RuntimeSnapshot = {
  bridgeVersion: 1,
  platform: RuntimePlatform.Desktop,
  architecture: "x86_64",
  osVersion: "fixture",
  lifecycle: LifecycleState.Active,
  capabilities: { secureSettings: true, notifications: false, storeUpdates: false, widgets: false },
};
const mappingProfile = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", name: "Work", kind: "fine-grained" as const };

function withMappingProfile(urlMappings: typeof defaultDevHudSettings.urlMappings) {
  return { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [mappingProfile] }, urlMappings };
}

function connectResponse(body: unknown): Response {
  const snapshot = body !== null && typeof body === "object" && "snapshot" in body ? (body as { readonly snapshot?: { readonly schemaVersion?: number; readonly canonicalJson?: string } }).snapshot : undefined;
  const normalized = snapshot?.schemaVersion === 1 && typeof snapshot.canonicalJson === "string" && atob(snapshot.canonicalJson).includes("\"schemaVersion\":2")
    ? { ...body as object, snapshot: { ...snapshot, schemaVersion: 2 } }
    : body;
  return new Response(JSON.stringify(normalized), { status: 200, headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" } });
}

function authenticatedBridge(purgeScopes: string[] = [], secureOperations: string[] = [], reconciliations: NativeBridgeRequestV1[] = []): NativeBridgeV1 {
  const accessTokenMap = JSON.stringify({ "@https://api.example/api": { token: "fixture-access-token", scope: "", expiresAt: 4_102_444_800 } });
  const secureSession = JSON.stringify({ idToken: "fixture-id-token", accessToken: accessTokenMap });
  return {
    async request(request) {
      if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
      if (request.operation === "secure.read") { secureOperations.push("read"); return { kind: "secure-value", value: request.setting.kind === "logto-session" ? secureSession : null }; }
      if (request.operation === "secure.purge") { purgeScopes.push(request.scope); return { kind: "ok" }; }
      if (request.operation === "secure.write" || request.operation === "secure.remove") { secureOperations.push(request.operation); return { kind: "ok" }; }
      if (request.operation === "secure.reconcile-github-pats") { reconciliations.push(request); return { kind: "ok" }; }
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
      if (request.operation === "secure.reconcile-github-pats") return { kind: "ok" };
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
    <button type="button" onClick={() => void queryClient.refetchQueries()}>refetch probe queries</button>
    <button type="button" onClick={() => void identity.logout().catch(() => {})}>logout probe identity</button>
    <button type="button" onClick={identity.continueLocally}>continue probe locally</button>
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
    onCallbackConsumed={() => {}}
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
    writeAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io", {
      settings: { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" } },
      revision: 9n,
      cachedAt: "2026-08-17T00:00:00.000Z",
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      throw new Error(`unexpected request ${url}`);
    }));
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };

    renderIdentityProbe(signedOutBridge(), replacement);
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("signed-out"));
    expect(readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io")).toBeNull();
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        replacements += 1;
        return connectResponse({ snapshot: { schemaVersion: 2, revision: "2", canonicalJson: encodedSettings(replacement) } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    const settingsTrigger = screen.getByRole("button", { name: messages.en.settings });
    settingsTrigger.focus();
    fireEvent.click(settingsTrigger);
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

  it("keeps synchronized appearance controls read-only while a replacement is pending", async () => {
    const server = { ...defaultDevHudSettings, appearance: { theme: "dark" as const, language: "en" as const } };
    const firstReplacement = { ...server, appearance: { ...server.appearance, theme: "light" as const } };
    const secondReplacement = { ...firstReplacement, appearance: { ...firstReplacement.appearance, language: "ko" as const } };
    const replaceBodies: Record<string, unknown>[] = [];
    let releaseFirst!: () => void;
    const firstPending = new Promise<void>((resolve) => { releaseFirst = resolve; });
    let activeReplacements = 0;
    let maximumActiveReplacements = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const source = typeof init?.body === "string" ? init.body : new TextDecoder().decode(init?.body as ArrayBufferView<ArrayBuffer>);
        replaceBodies.push(JSON.parse(source) as Record<string, unknown>);
        activeReplacements += 1;
        maximumActiveReplacements = Math.max(maximumActiveReplacements, activeReplacements);
        const replacementNumber = replaceBodies.length;
        if (replacementNumber === 1) await firstPending;
        activeReplacements -= 1;
        const replacement = replacementNumber === 1 ? firstReplacement : secondReplacement;
        return connectResponse({ snapshot: { schemaVersion: 2, revision: String(replacementNumber + 1), canonicalJson: encodedSettings(replacement) } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    const theme = await screen.findByLabelText(messages.en.theme) as HTMLSelectElement;
    const language = screen.getByLabelText(messages.en.language) as HTMLSelectElement;
    await waitFor(() => expect(theme.disabled).toBe(false));
    fireEvent.change(theme, { target: { value: "light" } });

    await waitFor(() => {
      expect(theme.disabled).toBe(true);
      expect(language.disabled).toBe(true);
      expect(replaceBodies).toHaveLength(1);
    });
    releaseFirst();
    await waitFor(() => {
      expect(theme.disabled).toBe(false);
      expect(theme.value).toBe("light");
    });
    fireEvent.change(language, { target: { value: "ko" } });

    await waitFor(() => expect(replaceBodies).toHaveLength(2));
    expect(maximumActiveReplacements).toBe(1);
    expect(replaceBodies.map((body) => body.expectedRevision)).toEqual(["1", "2"]);
    expect(replaceBodies[1]?.canonicalJson).toBe(encodedSettings(secondReplacement));
  });

  it("applies authenticated appearance while Home is the active surface", async () => {
    localStorage.removeItem("devhud.shell.onboarding.v1");
    const server = { ...defaultDevHudSettings, appearance: { theme: "dark" as const, language: "ko" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);

    expect(await screen.findByRole("heading", { name: messages.ko.welcome })).toBeTruthy();
    await waitFor(() => {
      expect(document.documentElement.dataset.theme).toBe("dark");
      expect(document.documentElement.lang).toBe("ko");
    });
  });

  it("reconciles GitHub PATs from the identity boundary while Home is active", async () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const removedProfileId = "018f47a2-7b3c-7def-8abc-1234567890ac";
    const server = parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile], pendingPatRemovals: [removedProfileId] } });
    const cleaned = { ...server, github: { ...server.github, pendingPatRemovals: [] } };
    const reconciliations: NativeBridgeRequestV1[] = [];
    const replacements: Record<string, unknown>[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const source = typeof init?.body === "string" ? init.body : new TextDecoder().decode(init?.body as ArrayBufferView<ArrayBuffer>);
        replacements.push(JSON.parse(source) as Record<string, unknown>);
        return connectResponse({ snapshot: { schemaVersion: 2, revision: "2", canonicalJson: encodedSettings(cleaned) } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge([], [], reconciliations)} initialRuntime={runtime} />);

    expect(await screen.findByRole("heading", { name: messages.en.welcome })).toBeTruthy();
    await waitFor(() => expect(reconciliations).toContainEqual({
      operation: "secure.reconcile-github-pats",
      scopeId: expect.any(String),
      profileIds: [profile.id],
    }));
    await waitFor(() => expect(replacements).toHaveLength(1));
    expect(replacements[0]?.canonicalJson).toBe(encodedSettings(cleaned));
    expect(screen.queryByRole("heading", { name: messages.en.githubSetupTitle })).toBeNull();
  });

  it("reconciles GitHub PATs again after adopting an unchanged server snapshot", async () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const server = parseDevHudSettings(defaultDevHudSettings);
    const proposed = parseDevHudSettings({ ...server, github: { ...server.github, profiles: [profile] } });
    const reconciliations: NativeBridgeRequestV1[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
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

    render(<DevHudServiceBoundary
      apiOrigin="https://devhud.api.delino.io"
      active
      online
      callbackUrl={null}
      platform={RuntimePlatform.Desktop}
      bridge={authenticatedBridge([], [], reconciliations)}
      onCallbackConsumed={() => {}}
      onContinueLocally={() => {}}
      onLoggedOut={() => {}}
    ><IdentityStateProbe replacement={proposed} /><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    await waitFor(() => expect(reconciliations).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "replace probe settings" }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.adoptServer }));

    await waitFor(() => expect(reconciliations).toHaveLength(2));
    expect(reconciliations[1]).toEqual({
      operation: "secure.reconcile-github-pats",
      scopeId: expect.any(String),
      profileIds: [],
    });
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: fixture.serverRevision, canonicalJson: toBase64(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const source = typeof init?.body === "string" ? init.body : new TextDecoder().decode(init?.body as ArrayBufferView<ArrayBuffer>);
        replaceBodies.push(JSON.parse(source) as Record<string, unknown>);
        if (replaceBodies.length === 1) {
          const detail = create(SettingsRevisionConflictSchema, {
            expectedRevision: 3n,
            currentSnapshot: { schemaVersion: 2, revision: 4n, canonicalJson: encoder.encode(canonicalDevHudSettings(server)) },
          });
          const value = btoa(String.fromCharCode(...toBinary(SettingsRevisionConflictSchema, detail)));
          return new Response(JSON.stringify({ code: "aborted", message: "settings revision conflict", details: [{ type: SettingsRevisionConflictSchema.typeName, value }] }), { status: 409, headers: { "Content-Type": "application/json" } });
        }
        return connectResponse({ snapshot: { schemaVersion: 2, revision: "5", canonicalJson: toBase64(local) } });
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

    const settingsTrigger = screen.getByRole("button", { name: messages.en.settings });
    settingsTrigger.focus();
    fireEvent.click(settingsTrigger);
    expect(await screen.findByText(messages.en.importSettingsTitle)).toBeTruthy();
    const importDialog = screen.getByRole("dialog", { name: messages.en.importSettingsTitle });
    const importClose = within(importDialog).getByRole("button", { name: messages.en.close });
    const importReplace = within(importDialog).getByRole("button", { name: messages.en.replaceLocal });
    expect(importClose).toBe(document.activeElement);
    importReplace.focus();
    fireEvent.keyDown(importDialog, { key: "Tab" });
    expect(importClose).toBe(document.activeElement);
    expect(screen.getByText("$.appearance.theme")).toBeTruthy();
    expect(screen.getByText("dark")).toBeTruthy();
    expect(screen.getByText("light")).toBeTruthy();
    expect(replaceBodies).toHaveLength(0);

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: messages.en.importSettingsTitle })).toBeNull();
    await waitFor(() => expect(settingsTrigger).toBe(document.activeElement));
    fireEvent.click(screen.getByRole("button", { name: messages.en.importSettingsTitle }));
    expect(screen.getByRole("dialog", { name: messages.en.importSettingsTitle })).toBeTruthy();
    expect(within(screen.getByRole("dialog", { name: messages.en.importSettingsTitle })).getByRole("button", { name: messages.en.close })).toBe(document.activeElement);
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
    const deleteTrigger = await screen.findByRole("button", { name: messages.en.deleteAccount });
    fireEvent.click(deleteTrigger);
    const firstConfirmation = screen.getByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle });
    const cancel = within(firstConfirmation).getByRole("button", { name: messages.en.cancel });
    const confirmDelete = within(firstConfirmation).getByRole("button", { name: messages.en.deleteAccount });
    expect(cancel).toBe(document.activeElement);
    fireEvent.keyDown(firstConfirmation, { key: "Tab" });
    expect(confirmDelete).toBe(document.activeElement);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle })).toBeNull();
    await waitFor(() => expect(deleteTrigger).toBe(document.activeElement));
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

  it("clears the authenticated cache when offline startup finds no session", async () => {
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
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
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

    expect(await screen.findByRole("button", { name: messages.en.signIn })).toBeTruthy();
    await waitFor(() => expect(readAuthenticatedSettingsCache(localStorage, apiOrigin)).toBeNull());
    expect(fetchMock).not.toHaveBeenCalled();
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: toBase64(defaultDevHudSettings) } });
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

  it("purges secure credentials when pending-deletion Web Storage enumeration fails", async () => {
    const purges: string[] = [];
    vi.spyOn(Storage.prototype, "key").mockImplementation(() => { throw new DOMException("denied", "SecurityError"); });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PENDING" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge(purges)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    expect(await screen.findByText(messages.en.deletionPendingTitle)).toBeTruthy();
    await waitFor(() => expect(purges).toEqual(["account-deletion"]));
    expect(screen.getByRole("button", { name: messages.en.restoreAccount })).toBeTruthy();
  });

  it("runs pending-deletion cleanup when Settings reports the account state", async () => {
    const purges: string[] = [];
    const detail = create(PermissionFailureSchema, { reason: PermissionFailureReason.ACCOUNT_DELETION_PENDING });
    const value = btoa(String.fromCharCode(...toBinary(PermissionFailureSchema, detail)));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return new Response(JSON.stringify({ code: "unavailable", message: "retry" }), { status: 503, headers: { "Content-Type": "application/json" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return new Response(JSON.stringify({ code: "permission_denied", message: "deletion pending", details: [{ type: PermissionFailureSchema.typeName, value }] }), { status: 403, headers: { "Content-Type": "application/json" } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge(purges)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    expect(await screen.findByText(messages.en.deletionPendingTitle)).toBeTruthy();
    await waitFor(() => expect(purges).toContain("account-deletion"));
  });

  it("retries secure cleanup without repeating a successful account deletion", async () => {
    let deletionRequests = 0;
    let cleanupAttempts = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      if (url.endsWith("/devhud.v1.AccountService/DeleteAccount")) {
        deletionRequests += 1;
        return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PENDING" } });
      }
      throw new Error(`unexpected request ${url}`);
    }));
    const accessTokenMap = JSON.stringify({ "@https://api.example/api": { token: "fixture-access-token", scope: "", expiresAt: 4_102_444_800 } });
    const secureSession = JSON.stringify({ idToken: "fixture-id-token", accessToken: accessTokenMap });
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.read") return { kind: "secure-value", value: request.setting.kind === "logto-session" ? secureSession : null };
        if (request.operation === "secure.write" || request.operation === "secure.remove") return { kind: "ok" };
        if (request.operation === "secure.purge" && request.scope === "account-deletion") {
          cleanupAttempts += 1;
          if (cleanupAttempts === 1) throw new Error("secure-store-unavailable");
          return { kind: "ok" };
        }
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.deleteAccount }));
    fireEvent.click(within(screen.getByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle })).getByRole("button", { name: messages.en.deleteAccount }));

    expect(await screen.findByText(messages.en.deletionPendingTitle)).toBeTruthy();
    const cleanupAlert = await screen.findByRole("alert");
    expect(cleanupAlert.textContent).toContain(messages.en.accountActionFailed);
    fireEvent.click(within(cleanupAlert).getByRole("button", { name: messages.en.retry }));

    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(cleanupAttempts).toBe(2);
    expect(deletionRequests).toBe(1);
  });

  it("keeps an established session usable after a background Bootstrap failure", async () => {
    let bootstrapRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) {
        bootstrapRequests += 1;
        if (bootstrapRequests > 1) return new Response(JSON.stringify({ code: "unavailable", message: "retry" }), { status: 503, headers: { "Content-Type": "application/json" } });
        return connectResponse(fixture.bootstrap);
      }
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    renderIdentityProbe(authenticatedBridge());
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("authenticated"));
    fireEvent.click(screen.getByRole("button", { name: "refetch probe queries" }));

    await waitFor(() => expect(bootstrapRequests).toBeGreaterThan(1));
    expect(screen.getByTestId("identity-state").dataset.status).toBe("authenticated");
    expect(screen.getByTestId("identity-state").dataset.error).toBe("");
  });

  it("purges an irreversible account when Web Storage removal fails", async () => {
    const purges: string[] = [];
    localStorage.setItem("devhud.identity.v1.account.fixture", "sensitive");
    const originalRemoveItem = Storage.prototype.removeItem;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, key) {
      if (key.startsWith("devhud.identity.v1.")) throw new DOMException("denied", "SecurityError");
      originalRemoveItem.call(this, key);
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, deletionState: "ACCOUNT_DELETION_STATE_PURGE_CLAIMED" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    render(<App bridge={authenticatedBridge([], secureOperations)} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    await waitFor(() => expect(secureOperations).toContain("secure.remove"));
    expect(screen.getByRole("button", { name: messages.en.signIn })).toBeTruthy();
    await waitFor(() => expect(readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io")).toBeNull());
  });

  it("clears a terminal Logto refresh failure before any service request", async () => {
    let authenticated = true;
    const clear = vi.fn(async () => { authenticated = false; });
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      client: {},
      storage: {},
      getAccessToken: async () => { throw new LogtoRequestError("invalid_grant", "refresh token revoked"); },
      isAuthenticated: async () => authenticated,
      signIn: async () => {},
      handleCallback: async () => {},
      clear,
    } as unknown as IdentitySession);
    writeAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io", { settings: defaultDevHudSettings, revision: 9n, cachedAt: "2026-08-17T00:00:00.000Z" });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      throw new Error(`service request unexpectedly reached ${url}`);
    }));

    renderIdentityProbe(signedOutBridge());

    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("signed-out"));
    expect(clear).toHaveBeenCalledOnce();
    expect(readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io")).toBeNull();
  });

  it("retains the Logto session and retries transient token refresh failures", async () => {
    let failing = true;
    const clear = vi.fn(async () => {});
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      client: {},
      storage: {},
      getAccessToken: async () => {
        if (failing) throw new TypeError("network unavailable");
        return "fixture-access-token";
      },
      isAuthenticated: async () => true,
      signIn: async () => {},
      handleCallback: async () => {},
      clear,
    } as unknown as IdentitySession);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected fixture request ${url}`);
    }));

    renderIdentityProbe(signedOutBridge());
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.accountError).not.toBe(""));
    expect(screen.getByTestId("identity-state").dataset.status).toBe("authenticated");

    failing = false;
    fireEvent.click(screen.getByRole("button", { name: "refetch probe queries" }));

    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.accountError).toBe(""));
    expect(clear).not.toHaveBeenCalled();
  });

  it("keeps guest settings writable when pending Bootstrap fails after Continue locally", async () => {
    let rejectBootstrap!: (reason: unknown) => void;
    const bootstrap = new Promise<Response>((_resolve, reject) => { rejectBootstrap = reject; });
    const fetch = vi.fn(() => bootstrap);
    vi.stubGlobal("fetch", fetch);

    renderIdentityProbe(signedOutBridge());
    await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "continue probe locally" }));
    await act(async () => { rejectBootstrap(new TypeError("network unavailable")); });

    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("guest"));
    expect(screen.getByTestId("identity-state").dataset.readOnly).toBe("false");
    expect(screen.getByTestId("identity-state").dataset.error).toBe("");
  });

  it("rejects an oversized guest settings snapshot before persistence", async () => {
    let rejectBootstrap!: (reason: unknown) => void;
    const bootstrap = new Promise<Response>((_resolve, reject) => { rejectBootstrap = reject; });
    vi.stubGlobal("fetch", vi.fn(() => bootstrap));
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: `https://source.example/${"path".repeat(1_500)}`, repository: { owner: "owner".repeat(1_500), name: "repository".repeat(1_500) }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const oversized = withMappingProfile(Array.from({ length: 100 }, (_, index) => ({ ...mapping, id: `018f47a2-7b3c-7def-8abc-${(123456789000 + index).toString().padStart(12, "0")}` })));

    renderIdentityProbe(signedOutBridge(), oversized);
    await waitFor(() => expect(screen.getByRole("button", { name: "continue probe locally" })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "continue probe locally" }));
    await act(async () => { rejectBootstrap(new TypeError("network unavailable")); });
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("guest"));

    fireEvent.click(screen.getByRole("button", { name: "replace probe settings" }));
    await waitFor(() => expect(hasGuestSettings(localStorage)).toBe(false));
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
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

  it.each(["malformed", "storage-failure"] as const)("recovers a %s secure session only after explicit reset", async (failure) => {
    let broken = true;
    const secureRemovals: NativeBridgeRequestV1[] = [];
    vi.spyOn(identityClient, "createIdentitySession").mockImplementation(async () => ({
      client: {},
      storage: {},
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => {
        if (broken) throw new Error(failure);
        return false;
      },
      signIn: async () => {},
      handleCallback: async () => {},
      clear: async () => {},
    } as unknown as IdentitySession));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      throw new Error(`unexpected request ${url}`);
    }));
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "secure.remove") {
          secureRemovals.push(request);
          broken = false;
          return { kind: "ok" };
        }
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));

    const reset = await screen.findByRole("button", { name: messages.en.resetSignIn });
    expect(screen.getByText(messages.en.resetSignInHint)).toBeTruthy();
    expect(secureRemovals).toEqual([]);
    fireEvent.click(reset);

    await waitFor(() => expect((screen.getByRole("button", { name: messages.en.signIn }) as HTMLButtonElement).disabled).toBe(false));
    expect(secureRemovals).toHaveLength(1);
    expect(secureRemovals[0]).toMatchObject({ operation: "secure.remove", setting: { kind: "logto-session", profileId: expect.stringMatching(/^origin\./u) } });
  });

  it.each(["first-run", "account"] as const)("guards the %s Sign in control with one in-flight authorization", async (surface) => {
    if (surface === "first-run") localStorage.removeItem("devhud.shell.onboarding.v1");
    let releaseSignIn!: () => void;
    const pendingSignIn = new Promise<void>((resolve) => { releaseSignIn = resolve; });
    const signIn = vi.fn(() => pendingSignIn);
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      client: {},
      storage: {},
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => false,
      signIn,
      handleCallback: async () => {},
      clear: async () => {},
    } as unknown as IdentitySession);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      throw new Error(`unexpected request ${url}`);
    }));
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    if (surface === "account") fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    const button = await screen.findByRole("button", { name: messages.en.signIn }) as HTMLButtonElement;
    await waitFor(() => expect(button.disabled).toBe(false));
    fireEvent.click(button);

    expect(signIn).toHaveBeenCalledOnce();
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
    expect(signIn).toHaveBeenCalledOnce();
    act(() => releaseSignIn());
    await waitFor(() => expect(button.disabled).toBe(false));
  });

  it.each([
    ["en", messages.en],
    ["ko", messages.ko],
  ] as const)("recovers the first-run %s identity surface by retrying Bootstrap", async (language, copy) => {
    localStorage.removeItem("devhud.shell.onboarding.v1");
    localStorage.setItem("devhud.shell.preferences.v1", JSON.stringify({ version: 1, theme: "system", language, apiOrigin: "https://devhud.api.delino.io", launchAtLogin: false }));
    let bootstrapRequests = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (!url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) throw new Error(`unexpected request ${url}`);
      bootstrapRequests += 1;
      if (bootstrapRequests === 1) return new Response(JSON.stringify({ code: "unavailable", message: "retry" }), { status: 503, headers: { "Content-Type": "application/json" } });
      return connectResponse(fixture.bootstrap);
    }));

    render(<App bridge={signedOutBridge()} initialRuntime={runtime} />);
    expect((await screen.findByRole("alert")).textContent).toContain(copy.bootstrapFailed);
    fireEvent.click(screen.getByRole("button", { name: copy.retry }));

    await waitFor(() => expect((screen.getByRole("button", { name: copy.signIn }) as HTMLButtonElement).disabled).toBe(false));
    expect(bootstrapRequests).toBe(2);
  });

  it("does not reuse a consumed callback after logout resets identity", async () => {
    const callbackUrl = "devhud://auth/callback?code=opaque&state=opaque";
    let receive!: (event: NativeBridgeEventV1) => void;
    let pendingCallback: string | null = callbackUrl;
    let authenticated = false;
    let callbackTakes = 0;
    const handleCallback = vi.fn(async () => { authenticated = true; });
    const clear = vi.fn(async () => { authenticated = false; });
    vi.spyOn(identityClient, "createIdentitySession").mockResolvedValue({
      client: {},
      storage: {},
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => authenticated,
      signIn: async () => {},
      handleCallback,
      clear,
    } as unknown as IdentitySession);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected request ${url}`);
    }));
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "auth.take-pending-callback") {
          callbackTakes += 1;
          const url = pendingCallback;
          pendingCallback = null;
          return { kind: "auth-callback", url };
        }
        if (request.operation === "secure.purge") return { kind: "ok" };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen(listener) { receive = listener; return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));
    act(() => receive({ version: 1, kind: "auth-callback", url: callbackUrl }));
    await waitFor(() => expect(handleCallback).toHaveBeenCalledWith(callbackUrl));
    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    expect(await screen.findByText("Fixture User")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: messages.en.logout }));

    expect(await screen.findByRole("button", { name: messages.en.signIn })).toBeTruthy();
    expect(screen.queryByText(messages.en.bootstrapFailed)).toBeNull();
    expect(callbackTakes).toBe(1);
  });

  it("retains a callback until a transient Logto exchange failure is retried", async () => {
    const callbackUrl = "devhud://auth/callback?code=opaque&state=opaque";
    let receive!: (event: NativeBridgeEventV1) => void;
    let pendingCallback: string | null = callbackUrl;
    let authenticated = false;
    const handleCallback = vi.fn(async () => {
      if (handleCallback.mock.calls.length === 1) throw new Error("token-exchange-unavailable");
      authenticated = true;
    });
    vi.spyOn(identityClient, "createIdentitySession").mockImplementation(async () => ({
      client: {},
      storage: {},
      getAccessToken: async () => "fixture-access-token",
      isAuthenticated: async () => authenticated,
      signIn: async () => {},
      handleCallback,
      clear: async () => { authenticated = false; },
    } as unknown as IdentitySession));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected request ${url}`);
    }));
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "session.configure-origins") return { kind: "session-network-policy", changed: false };
        if (request.operation === "auth.take-pending-callback") {
          const url = pendingCallback;
          pendingCallback = null;
          return { kind: "auth-callback", url };
        }
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen(listener) { receive = listener; return () => {}; },
    };

    render(<App bridge={bridge} initialRuntime={runtime} />);
    await waitFor(() => expect(receive).toBeTypeOf("function"));
    act(() => receive({ version: 1, kind: "auth-callback", url: callbackUrl }));
    await waitFor(() => expect(handleCallback).toHaveBeenCalledOnce());
    expect(pendingCallback).toBe(callbackUrl);

    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    fireEvent.click(await screen.findByRole("button", { name: messages.en.retry }));

    await waitFor(() => expect(handleCallback).toHaveBeenCalledTimes(2));
    expect(pendingCallback).toBeNull();
    expect(await screen.findByText("Fixture User")).toBeTruthy();
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "7", canonicalJson: encodedSettings(defaultDevHudSettings) } });
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

  it("keeps offline settings read-only while the cached session probe is pending", async () => {
    writeCachedIdentityBootstrap(localStorage, "https://devhud.api.delino.io", {
      issuer: fixture.bootstrap.logtoIssuer,
      audience: fixture.bootstrap.logtoAudience,
      clientId: fixture.bootstrap.logtoClients.desktop,
      redirectUri: "devhud://auth/callback",
    });
    writeAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io", { settings: defaultDevHudSettings, revision: 3n, cachedAt: "2026-08-17T00:00:00.000Z" });

    render(<DevHudServiceBoundary
      apiOrigin="https://devhud.api.delino.io"
      active
      online={false}
      callbackUrl={null}
      platform={RuntimePlatform.Desktop}
      bridge={authenticatedBridge()}
      onCallbackConsumed={() => {}}
      onContinueLocally={() => {}}
      onLoggedOut={() => {}}
    ><IdentityStateProbe /></DevHudServiceBoundary>);

    expect(screen.getByTestId("identity-state").dataset.readOnly).toBe("true");
    await waitFor(() => {
      const state = screen.getByTestId("identity-state");
      expect(state.dataset.status).toBe("authenticated");
      expect(state.dataset.readOnly).toBe("true");
      expect(state.dataset.revision).toBe("3");
    });
  });

  it("discards a pending callback before draining the old identity session and purging the API origin", async () => {
    const operations: string[] = [];
    let pendingCallback: string | null = "devhud://auth/callback?code=old&state=old";
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
        if (request.operation === "auth.take-pending-callback") {
          operations.push("discard-callback");
          const url = pendingCallback;
          pendingCallback = null;
          return { kind: "auth-callback", url };
        }
        if (request.operation === "secure.purge") { operations.push("purge"); return { kind: "ok" }; }
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };

    const clearing = clearIdentityForApiChange(bridge, localStorage, "https://devhud.api.delino.io", sessionRef);
    await waitFor(() => expect(operations).toEqual(["discard-callback", "drain-start"]));
    expect(sessionRef?.current).toBeNull();
    expect(pendingCallback).toBeNull();
    release?.();
    await clearing;

    expect(operations).toEqual(["discard-callback", "drain-start", "late-secure-write-settled", "purge"]);
  });

  it("keeps cached settings read-only, surfaces correlation metadata, and retries GetSettings", async () => {
    const correlationId = "018f47a2-7b3c-7def-8abc-1234567890cd";
    const cached = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    let settingsRequests = 0;
    writeAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io", { settings: cached, revision: 9n, cachedAt: "2026-08-17T00:00:00.000Z" });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) {
        settingsRequests += 1;
        if (settingsRequests === 1) return new Response(JSON.stringify({ code: "unavailable", message: "retry later" }), { status: 503, headers: { "Content-Type": "application/json", "x-devhud-correlation-id": correlationId } });
        return connectResponse({ snapshot: { schemaVersion: 2, revision: "10", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));

    await waitFor(() => {
      const theme = screen.getByLabelText(messages.en.theme) as HTMLSelectElement;
      expect(theme.disabled).toBe(true);
      expect(theme.value).toBe("dark");
    });
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain(correlationId);
    fireEvent.click(within(alert).getByRole("button", { name: messages.en.retry }));

    await waitFor(() => expect((screen.getByLabelText(messages.en.theme) as HTMLSelectElement).disabled).toBe(false));
    expect(settingsRequests).toBe(2);
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
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

  it("tombstones the current origin cache when logout cannot enumerate or remove it", async () => {
    const apiOrigin = "https://devhud.api.delino.io";
    writeAuthenticatedSettingsCache(localStorage, apiOrigin, { settings: defaultDevHudSettings, revision: 7n, cachedAt: "2026-08-17T00:00:00.000Z" });
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
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "7", canonicalJson: encodedSettings(defaultDevHudSettings) } });
      throw new Error(`unexpected request ${url}`);
    }));

    renderIdentityProbe(bridge);
    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("authenticated"));
    const cacheKey = Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)).find((key) => key?.endsWith(".settings"));
    expect(cacheKey).toBeTypeOf("string");
    vi.spyOn(Storage.prototype, "key").mockImplementation(() => { throw new DOMException("denied", "SecurityError"); });
    const originalRemoveItem = Storage.prototype.removeItem;
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (this: Storage, key) {
      if (key === cacheKey) throw new DOMException("denied", "SecurityError");
      originalRemoveItem.call(this, key);
    });

    fireEvent.click(screen.getByRole("button", { name: "logout probe identity" }));

    await waitFor(() => expect(screen.getByTestId("identity-state").dataset.status).toBe("signed-out"));
    expect(localStorage.getItem(cacheKey as string)).not.toBeNull();
    expect(readAuthenticatedSettingsCache(localStorage, apiOrigin)).toBeNull();
  });

  it("rejects an unsupported ReplaceSettings envelope without changing or caching it", async () => {
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) return connectResponse({ snapshot: { schemaVersion: 3, revision: "2", canonicalJson: encodedSettings(replacement) } });
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

  it.each(["missing", "malformed"] as const)("fails closed for a %s replacement snapshot", async (kind) => {
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        return kind === "missing"
          ? connectResponse({})
          : connectResponse({ snapshot: { schemaVersion: 2, revision: "2", canonicalJson: btoa('{ "schemaVersion": 1 }') } });
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
  });

  it("rejects an unsupported conflict envelope before decoding it", async () => {
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const detail = create(SettingsRevisionConflictSchema, {
          expectedRevision: 1n,
          currentSnapshot: { schemaVersion: 3, revision: 2n, canonicalJson: new TextEncoder().encode(canonicalDevHudSettings(server)) },
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

  it("fails closed on malformed canonical bytes in a conflict snapshot", async () => {
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    const replacement = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const detail = create(SettingsRevisionConflictSchema, {
          expectedRevision: 1n,
          currentSnapshot: { schemaVersion: 2, revision: 2n, canonicalJson: new TextEncoder().encode('{ "schemaVersion": 1 }') },
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
    expect(screen.queryByText(messages.en.conflictTitle)).toBeNull();
  });

  it("revalidates a refetched import snapshot before adopting the server", async () => {
    const local = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    let invalidRefetch = false;
    writeGuestSettings(localStorage, local);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: invalidRefetch ? 3 : 2, revision: "1", canonicalJson: encodedSettings(server) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary
      apiOrigin="https://devhud.api.delino.io"
      active
      online
      callbackUrl={null}
      platform={RuntimePlatform.Desktop}
      bridge={authenticatedBridge()}
      onCallbackConsumed={() => {}}
      onContinueLocally={() => {}}
      onLoggedOut={() => {}}
    ><IdentityStateProbe /><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);
    expect(await screen.findByRole("dialog", { name: messages.en.importSettingsTitle })).toBeTruthy();
    invalidRefetch = true;
    fireEvent.click(screen.getByRole("button", { name: "refetch probe queries" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("settings-contract-invalid"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.replaceLocal }));

    expect((screen.getByLabelText(messages.en.theme) as HTMLSelectElement).value).toBe("dark");
    expect(hasGuestSettings(localStorage)).toBe(true);
    expect(readAuthenticatedSettingsCache(localStorage, "https://devhud.api.delino.io")).toBeNull();
  });

  it("resets a dirty mapping draft when import replacement adopts the server snapshot", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://local.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const local = withMappingProfile([mapping]);
    const server = withMappingProfile([{ ...mapping, pattern: "https://server.example/**" }]);
    writeGuestSettings(localStorage, local);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(server) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary
      apiOrigin="https://devhud.api.delino.io"
      active
      online
      callbackUrl={null}
      platform={RuntimePlatform.Desktop}
      bridge={authenticatedBridge()}
      onCallbackConsumed={() => {}}
      onContinueLocally={() => {}}
      onLoggedOut={() => {}}
    ><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    expect(await screen.findByRole("dialog", { name: messages.en.importSettingsTitle })).toBeTruthy();
    const pattern = screen.getByLabelText(messages.en.urlPattern) as HTMLInputElement;
    fireEvent.change(pattern, { target: { value: "https://draft.example/**" } });
    expect(pattern.value).toBe("https://draft.example/**");
    fireEvent.click(screen.getByRole("button", { name: messages.en.replaceLocal }));
    await waitFor(() => expect((screen.getByLabelText(messages.en.urlPattern) as HTMLInputElement).value).toBe("https://server.example/**"));
  });

  it("keeps an intermediate negative mapping priority editable until save", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://local.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const server = withMappingProfile([{ ...mapping, pattern: "https://server.example/**" }]);
    writeGuestSettings(localStorage, withMappingProfile([mapping]));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 1, revision: "1", canonicalJson: encodedSettings(server) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    await screen.findByRole("dialog", { name: messages.en.importSettingsTitle });
    const priority = screen.getByLabelText(messages.en.mappingPriority) as HTMLInputElement;
    fireEvent.change(priority, { target: { value: "" } });
    expect(priority.value).toBe("");
    fireEvent.change(priority, { target: { value: "-1" } });
    expect(priority.value).toBe("-1");
  });

  it("disables mapping edits while a save is pending", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://local.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const server = withMappingProfile([mapping]);
    let resolveReplace!: (response: Response) => void;
    const replacement = new Promise<Response>((resolve) => { resolveReplace = resolve; });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) return replacement;
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    const pattern = await screen.findByLabelText(messages.en.urlPattern) as HTMLInputElement;
    fireEvent.click(screen.getByRole("button", { name: messages.en.saveUrlMappings }));
    await waitFor(() => expect(pattern.matches(":disabled")).toBe(true));
    expect((screen.getByRole("button", { name: messages.en.addUrlMapping }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: messages.en.saveUrlMappings }) as HTMLButtonElement).disabled).toBe(true);

    await act(async () => { resolveReplace(connectResponse({ snapshot: { schemaVersion: 2, revision: "2", canonicalJson: encodedSettings(server) } })); });
    await waitFor(() => expect(pattern.matches(":disabled")).toBe(false));
    expect(screen.getByRole("status").textContent).toBe(messages.en.mappingSaved);
  });

  it("preserves a dirty mapping draft when conflict reapply encounters another revision conflict", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://server.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const initial = withMappingProfile([mapping]);
    const later = withMappingProfile([{ ...mapping, pattern: "https://later.example/**" }]);
    let replaceAttempts = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(initial) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        replaceAttempts += 1;
        const current = replaceAttempts === 1 ? initial : later;
        const detail = create(SettingsRevisionConflictSchema, {
          expectedRevision: BigInt(replaceAttempts),
          currentSnapshot: { schemaVersion: 2, revision: BigInt(replaceAttempts + 1), canonicalJson: new TextEncoder().encode(canonicalDevHudSettings(current)) },
        });
        const value = btoa(String.fromCharCode(...toBinary(SettingsRevisionConflictSchema, detail)));
        return new Response(JSON.stringify({ code: "aborted", message: "settings revision conflict", details: [{ type: SettingsRevisionConflictSchema.typeName, value }] }), { status: 409, headers: { "Content-Type": "application/json" } });
      }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    const pattern = await screen.findByLabelText(messages.en.urlPattern) as HTMLInputElement;
    fireEvent.change(pattern, { target: { value: "https://draft.example/**" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.saveUrlMappings }));
    await screen.findByRole("dialog", { name: messages.en.conflictTitle });
    fireEvent.click(screen.getByRole("button", { name: messages.en.reapplyLocal }));
    await waitFor(() => expect(replaceAttempts).toBe(2));
    await waitFor(() => expect((screen.getByLabelText(messages.en.urlPattern) as HTMLInputElement).value).toBe("https://draft.example/**"));
  });

  it("does not report a mapping validation error when a mapping save has a transport failure", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://server.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const server = withMappingProfile([mapping]);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) throw new TypeError("network unavailable");
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    await screen.findByLabelText(messages.en.urlPattern);
    fireEvent.click(screen.getByRole("button", { name: messages.en.saveUrlMappings }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain(messages.en.settingsActionFailed));
    expect(screen.queryByText(messages.en.mappingInvalid)).toBeNull();
  });

  it("clears the guest import marker when a conflicted upload adopts the server", async () => {
    const local = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "dark" as const } };
    const server = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, theme: "light" as const } };
    writeGuestSettings(localStorage, local);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "3", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) {
        const detail = create(SettingsRevisionConflictSchema, {
          expectedRevision: 3n,
          currentSnapshot: { schemaVersion: 2, revision: 4n, canonicalJson: new TextEncoder().encode(canonicalDevHudSettings(server)) },
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
        return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(defaultDevHudSettings) } });
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

  it("keeps unsaved URL mapping drafts while navigating between app surfaces", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://server.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const server = withMappingProfile([mapping]);
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<App bridge={authenticatedBridge()} initialRuntime={runtime} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));
    const pattern = await screen.findByLabelText(messages.en.urlPattern) as HTMLInputElement;
    const priority = screen.getByLabelText(messages.en.mappingPriority) as HTMLInputElement;
    fireEvent.change(pattern, { target: { value: "https://draft.example/**" } });
    fireEvent.change(priority, { target: { value: "-1" } });

    fireEvent.click(screen.getByRole("button", { name: messages.en.account }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.settings }));

    expect((await screen.findByLabelText(messages.en.urlPattern) as HTMLInputElement).value).toBe("https://draft.example/**");
    expect((screen.getByLabelText(messages.en.mappingPriority) as HTMLInputElement).value).toBe("-1");
  });

  it("keeps an editable mapping draft while the authenticated account hydrates", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://server.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const server = withMappingProfile([mapping]);
    let resolveAccount!: (response: Response) => void;
    const account = new Promise<Response>((resolve) => { resolveAccount = resolve; });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return account;
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    const pattern = await screen.findByLabelText(messages.en.urlPattern) as HTMLInputElement;
    fireEvent.change(pattern, { target: { value: "https://draft.example/**" } });
    await act(async () => { resolveAccount(connectResponse({ account: fixture.account })); });

    await waitFor(() => expect((screen.getByLabelText(messages.en.urlPattern) as HTMLInputElement).value).toBe("https://draft.example/**"));
  });

  it("requires an explicit credential profile for a newly added mapping", async () => {
    const server = withMappingProfile([]);
    let replacements = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: fixture.account });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: "1", canonicalJson: encodedSettings(server) } });
      if (url.endsWith("/devhud.v1.SettingsService/ReplaceSettings")) { replacements += 1; return connectResponse({ snapshot: { schemaVersion: 2, revision: "2", canonicalJson: encodedSettings(server) } }); }
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    await screen.findByRole("button", { name: messages.en.addUrlMapping });
    await waitFor(() => expect((screen.getByRole("button", { name: messages.en.addUrlMapping }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: messages.en.addUrlMapping }));
    const profile = screen.getByLabelText(messages.en.credentialProfile) as HTMLSelectElement;
    expect(profile.value).toBe("");
    fireEvent.click(screen.getByRole("button", { name: messages.en.saveUrlMappings }));
    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.mappingInvalid);
    expect(replacements).toBe(0);
    fireEvent.change(profile, { target: { value: mappingProfile.id } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.saveUrlMappings }));
    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.githubSetupFailed);
    expect(replacements).toBe(0);
  });

  it("clears unsaved URL mapping drafts when the authenticated account changes", async () => {
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://first.example/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: mappingProfile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };
    const first = withMappingProfile([mapping]);
    const second = withMappingProfile([{ ...mapping, pattern: "https://second.example/**" }]);
    let useSecondAccount = false;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/devhud.v1.BootstrapService/GetBootstrap")) return connectResponse(fixture.bootstrap);
      if (url.endsWith("/devhud.v1.AccountService/GetAccount")) return connectResponse({ account: { ...fixture.account, logtoSubject: useSecondAccount ? "second-account" : "first-account" } });
      if (url.endsWith("/devhud.v1.SettingsService/GetSettings")) return connectResponse({ snapshot: { schemaVersion: 2, revision: useSecondAccount ? "2" : "1", canonicalJson: encodedSettings(useSecondAccount ? second : first) } });
      throw new Error(`unexpected request ${url}`);
    }));

    render(<DevHudServiceBoundary apiOrigin="https://devhud.api.delino.io" active online callbackUrl={null} platform={RuntimePlatform.Desktop} bridge={authenticatedBridge()} onCallbackConsumed={() => {}} onContinueLocally={() => {}} onLoggedOut={() => {}}><IdentityStateProbe /><SynchronizedSettingsBoundary copy={messages.en} /></DevHudServiceBoundary>);

    const pattern = await screen.findByLabelText(messages.en.urlPattern) as HTMLInputElement;
    fireEvent.change(pattern, { target: { value: "https://draft.example/**" } });
    useSecondAccount = true;
    fireEvent.click(screen.getByRole("button", { name: "refetch probe queries" }));

    await waitFor(() => expect((screen.getByLabelText(messages.en.urlPattern) as HTMLInputElement).value).toBe("https://second.example/**"));
  });

  it.each([
    ["unsupported schema", 3, encodedSettings(defaultDevHudSettings)],
    ["noncanonical body", 2, btoa('{ "schemaVersion": 2 }')],
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
