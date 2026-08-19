// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AccountIdentity, NativeMessagingSettings, ShortcutPaletteTrigger, SynchronizedSettingsBoundary, SynchronizedShortcutBoundary } from "./identity-ui";
import { messages } from "./localization";
import type { NativeBridgeV1 } from "./native-bridge";
import type { IdentitySettingsValue } from "./service-boundary";
import { defaultDevHudSettings } from "./settings-contract";
import { inactiveDesktopShortcutBindings, ShortcutActionId, ShortcutKey, ShortcutModifier } from "./shortcuts";

let identity: IdentitySettingsValue;

const nativeMessagingMock = vi.hoisted(() => ({
  status: vi.fn(),
  beginPairing: vi.fn(),
  unpair: vi.fn(),
  configure: vi.fn(),
}));

vi.mock("./service-boundary", () => ({ useIdentitySettings: () => identity }));
vi.mock("./native-messaging", () => ({ nativeMessaging: nativeMessagingMock }));

function identityWith(overrides: Partial<IdentitySettingsValue> = {}): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings: defaultDevHudSettings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: defaultDevHudSettings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("test"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true),
    signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), replaceSettingsAt: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(async () => true), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
    ...overrides,
  };
}

beforeEach(() => {
  identity = identityWith();
  nativeMessagingMock.status.mockReset().mockResolvedValue({ paired: false });
  nativeMessagingMock.beginPairing.mockReset().mockResolvedValue({ paired: false, pairingNonce: "pair-code", expiresInSeconds: 120 });
  nativeMessagingMock.unpair.mockReset().mockResolvedValue({ paired: false });
  nativeMessagingMock.configure.mockReset().mockResolvedValue(undefined);
});
afterEach(() => { vi.useRealTimers(); cleanup(); });

describe("identity UI", () => {
  it("refreshes pairing status until the extension completes pairing", async () => {
    vi.useFakeTimers();
    render(<NativeMessagingSettings copy={messages.en} />);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: messages.en.nativeMessagingPair }));
    await act(async () => {});
    expect(screen.getByText("pair-code")).toBeTruthy();
    nativeMessagingMock.status.mockResolvedValue({ paired: true });

    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });

    expect(screen.getByRole("status").textContent).toBe(messages.en.nativeMessagingPaired);
    expect(screen.queryByText("pair-code")).toBeNull();
  });

  it("expires the displayed pairing code and stops polling", async () => {
    vi.useFakeTimers();
    render(<NativeMessagingSettings copy={messages.en} />);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: messages.en.nativeMessagingPair }));
    await act(async () => {});
    expect(screen.getByText("pair-code")).toBeTruthy();

    await act(async () => { await vi.advanceTimersByTimeAsync(120_000); });

    expect(screen.queryByText("pair-code")).toBeNull();
    const statusCallsAfterExpiry = nativeMessagingMock.status.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
    expect(nativeMessagingMock.status).toHaveBeenCalledTimes(statusCallsAfterExpiry);
  });

  it("keeps local continuation available after Bootstrap fails", () => {
    const continueLocally = vi.fn();
    identity = identityWith({ status: "error", continueLocally });

    render(<AccountIdentity copy={messages.en} apiOrigin="https://devhud.api.delino.io" inputRef={{ current: null }} onApiOrigin={vi.fn(async () => undefined)} />);

    fireEvent.click(screen.getByRole("button", { name: messages.en.continueLocally }));
    expect(continueLocally).toHaveBeenCalledOnce();
  });

  it("renders the active command-palette binding instead of the default", () => {
    identity = identityWith({ activeShortcutBindings: {
      ...defaultDevHudSettings.shortcuts.desktop,
      [ShortcutActionId.CommandPalette]: { enabled: true, modifiers: [ShortcutModifier.Shift], key: ShortcutKey.Q },
    } });

    render(<ShortcutPaletteTrigger copy={messages.en} isMac={false} onOpen={vi.fn()} triggerRef={{ current: null }} />);

    expect(screen.getByRole("button", { name: messages.en.openPalette }).textContent).toBe(`${messages.en.shortcutShift} + ${messages.en.shortcutKeyQ}`);
  });

  it("suspends shortcuts while identity hydration is unavailable", async () => {
    const operations: string[] = [];
    const bridge: NativeBridgeV1 = {
      async request(request) {
        operations.push(request.operation);
        if (request.operation === "shortcuts.suspend") return { kind: "shortcut-status", platform: "unsupported", permission: "unsupported", bindings: inactiveDesktopShortcutBindings, error: null };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };
    identity = identityWith({ status: "signed-out", shortcutHydrationReady: false });

    render(<SynchronizedShortcutBoundary bridge={bridge} />);

    await waitFor(() => expect(operations).toContain("shortcuts.suspend"));
    expect(operations).not.toContain("shortcuts.apply");
    expect(identity.setActiveShortcutBindings).toHaveBeenCalledWith(inactiveDesktopShortcutBindings);
  });

  it("does not offer an unchanged URL-mapping draft for saving", () => {
    render(<SynchronizedSettingsBoundary copy={messages.en} />);

    expect((screen.getByRole("button", { name: messages.en.saveUrlMappings }) as HTMLButtonElement).disabled).toBe(true);
    expect(identity.replaceSettingsAt).not.toHaveBeenCalled();
  });

  it("clears URL-mapping dirty state when an edit returns to its baseline", () => {
    const profileId = "018f47a2-7b3c-7def-8abc-1234567890ab";
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://example.com/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: profileId, priority: 0, chromeOrigin: null, updatedAt: "2026-08-18T00:00:00.000Z" };
    identity = identityWith({ settings: { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [{ id: profileId, name: "Work", kind: "fine-grained" as const }] }, urlMappings: [mapping] } });

    render(<SynchronizedSettingsBoundary copy={messages.en} />);
    const pattern = screen.getByLabelText(messages.en.urlPattern);
    fireEvent.change(pattern, { target: { value: "https://example.com/issues/**" } });
    expect((screen.getByRole("button", { name: messages.en.saveUrlMappings }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.change(pattern, { target: { value: mapping.pattern } });

    expect((screen.getByRole("button", { name: messages.en.saveUrlMappings }) as HTMLButtonElement).disabled).toBe(true);
    expect(identity.replaceSettingsAt).not.toHaveBeenCalled();
  });

  it("reports a rebase failure after repository validation", async () => {
    const profileId = "018f47a2-7b3c-7def-8abc-1234567890ab";
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://example.com/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: profileId, priority: 0, chromeOrigin: null, updatedAt: "2026-08-18T00:00:00.000Z" };
    const settings = { ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [{ id: profileId, name: "Work", kind: "fine-grained" as const }] }, urlMappings: [mapping] };
    const replaceSettingsAt = vi.fn(async (update: Parameters<IdentitySettingsValue["replaceSettingsAt"]>[0]) => {
      if (typeof update === "function") update({ ...settings, github: { ...settings.github, profiles: [] } });
      return false;
    });
    identity = identityWith({ settings, replaceSettingsAt });
    const bridge: NativeBridgeV1 = {
      async request(request) {
        if (request.operation === "secure.read") return { kind: "secure-value", value: "fixture-token" };
        throw new Error(`unexpected bridge operation ${request.operation}`);
      },
      async listen() { return () => {}; },
    };
    const githubProvider = { id: "github.com", validateRepository: vi.fn(async () => {}) } as unknown as import("./github-provider").GitHubProvider;

    render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} githubProvider={githubProvider} />);
    fireEvent.change(screen.getByLabelText(messages.en.repositoryName), { target: { value: "reviewed" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.saveUrlMappings }));

    expect((await screen.findByRole("alert")).textContent).toContain(messages.en.githubSetupFailed);
    expect(replaceSettingsAt).toHaveBeenCalledOnce();
  });
});
