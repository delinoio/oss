// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SynchronizedSettingsBoundary, type SettingsSectionContributions } from "./identity-ui";
import { messages } from "./localization";
import type { NativeBridgeRequestV1, NativeBridgeV1 } from "./native-bridge";
import type { IdentitySettingsValue } from "./service-boundary";
import { SettingsSectionId, availableSettingsSections } from "./settings-sections";
import { defaultDevHudSettings, parseDevHudSettings } from "./settings-contract";
import { ShortcutSettings } from "./shortcut-settings-ui";

let identity: IdentitySettingsValue;
vi.mock("./service-boundary", () => ({ useIdentitySettings: () => identity }));

function identityWith(overrides: Partial<IdentitySettingsValue> = {}): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings: defaultDevHudSettings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: defaultDevHudSettings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, deckAccessSuspended: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("test"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true),
    signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), replaceSettingsAt: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(async () => true), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
    ...overrides,
  };
}

const bridge: NativeBridgeV1 = { async request() { throw new Error("unexpected request"); }, async listen() { return () => {}; } };
function StatefulSection({ title }: { readonly title: string }) {
  return <section><h3 tabIndex={-1}>{title}</h3><label>{title} draft<input aria-label={`${title} draft`} /></label></section>;
}
function ShortcutProbe() {
  return <label>{messages.en.settingsShortcutsTitle} draft<input aria-label={`${messages.en.settingsShortcutsTitle} draft`} /></label>;
}
const contributions: SettingsSectionContributions = {
  Shortcuts: ShortcutProbe,
  LocalAgents: () => <StatefulSection title={messages.en.localAgentsTitle} />,
  Updates: () => <StatefulSection title={messages.en.settingsUpdatesTitle} />,
};
const Chrome = () => <StatefulSection title={messages.en.settingsChromeTitle} />;
const sectionButton = (title: string) => screen.getByRole("button", { name: new RegExp(title, "u") });
const frame = () => act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));

describe("Settings section composition", () => {
  beforeEach(() => { identity = identityWith(); });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("defines the exact enum-style order and capability predicates", () => {
    const full = availableSettingsSections({ mobile: false, shortcuts: true, nativeMessaging: true, localAgents: true, notifications: true, updates: true });
    expect(full.map(({ id }) => id)).toEqual(Object.values(SettingsSectionId));
    const mobile = availableSettingsSections({ mobile: true, shortcuts: true, nativeMessaging: true, localAgents: true, notifications: true, updates: true });
    expect(mobile.map(({ id }) => id)).toEqual([SettingsSectionId.Appearance, SettingsSectionId.UrlMappings, SettingsSectionId.GitHubCredentials, SettingsSectionId.CloudflareR2, SettingsSectionId.Notifications, SettingsSectionId.Updates]);
    expect(availableSettingsSections({ mobile: false, shortcuts: false, nativeMessaging: false, localAgents: false, notifications: false, updates: false }).map(({ id }) => id)).toEqual([SettingsSectionId.Appearance, SettingsSectionId.UrlMappings, SettingsSectionId.GitHubCredentials, SettingsSectionId.CloudflareR2]);
  });

  it("renders nine desktop TOC entries, one active panel, and preserves mounted section state", async () => {
    render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} sectionContributions={contributions} NativeMessagingSettings={Chrome} localAgentsAvailable notification={{ permission: messages.en.notificationAuthorized, failed: false, onRequest: vi.fn() }} />);
    const toc = screen.getByRole("navigation", { name: messages.en.settingsSections });
    expect(within(toc).getAllByRole("button")).toHaveLength(9);
    expect(document.querySelectorAll("[data-settings-section]")).toHaveLength(9);
    expect(document.querySelectorAll("[data-settings-section]:not([hidden])")).toHaveLength(1);

    fireEvent.click(sectionButton(messages.en.settingsShortcutsTitle));
    await frame();
    const heading = screen.getByRole("heading", { name: messages.en.settingsShortcutsTitle });
    expect(document.activeElement).toBe(heading);
    const draft = screen.getByLabelText(`${messages.en.settingsShortcutsTitle} draft`) as HTMLInputElement;
    fireEvent.change(draft, { target: { value: "staged" } });
    fireEvent.click(sectionButton(messages.en.settingsAppearanceTitle));
    fireEvent.click(sectionButton(messages.en.settingsShortcutsTitle));
    expect((screen.getByLabelText(`${messages.en.settingsShortcutsTitle} draft`) as HTMLInputElement).value).toBe("staged");
  });

  it("preserves real section drafts and secure inputs while switching", () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", pattern: "https://example.com/**", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: profile.id, priority: 0, chromeOrigin: null, updatedAt: "2026-08-18T00:00:00.000Z" };
    identity = identityWith({ settings: parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, urlMappings: [mapping] }) });
    render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} sectionContributions={contributions} localAgentsAvailable />);

    fireEvent.click(sectionButton(messages.en.urlMappingsTitle));
    fireEvent.change(screen.getByLabelText(messages.en.urlPattern), { target: { value: "https://example.com/issues/**" } });
    fireEvent.change(screen.getByLabelText(messages.en.mappingPriority), { target: { value: "17" } });
    fireEvent.click(sectionButton(messages.en.githubSetupTitle));
    fireEvent.change(screen.getByLabelText(messages.en.githubSetProfileToken), { target: { value: "replacement-pat" } });
    fireEvent.click(sectionButton(messages.en.r2SettingsTitle));
    fireEvent.change(screen.getByLabelText(messages.en.r2Bucket), { target: { value: "screenshots" } });
    fireEvent.change(screen.getByLabelText(messages.en.r2SecretAccessKey), { target: { value: "device-secret" } });
    fireEvent.click(sectionButton(messages.en.localAgentsTitle));
    fireEvent.change(screen.getByLabelText(`${messages.en.localAgentsTitle} draft`), { target: { value: "repository prompt" } });

    fireEvent.click(sectionButton(messages.en.urlMappingsTitle));
    expect((screen.getByLabelText(messages.en.urlPattern) as HTMLInputElement).value).toBe("https://example.com/issues/**");
    expect((screen.getByLabelText(messages.en.mappingPriority) as HTMLInputElement).value).toBe("17");
    fireEvent.click(sectionButton(messages.en.githubSetupTitle));
    expect((screen.getByLabelText(messages.en.githubSetProfileToken) as HTMLInputElement).value).toBe("replacement-pat");
    fireEvent.click(sectionButton(messages.en.r2SettingsTitle));
    expect((screen.getByLabelText(messages.en.r2Bucket) as HTMLInputElement).value).toBe("screenshots");
    expect((screen.getByLabelText(messages.en.r2SecretAccessKey) as HTMLInputElement).value).toBe("device-secret");
    fireEvent.click(sectionButton(messages.en.localAgentsTitle));
    expect((screen.getByLabelText(`${messages.en.localAgentsTitle} draft`) as HTMLInputElement).value).toBe("repository prompt");
  });

  it("keeps an in-flight shortcut stage mounted across section switches", async () => {
    let finishStage: ((response: Awaited<ReturnType<NativeBridgeV1["request"]>>) => void) | undefined;
    const requests: NativeBridgeRequestV1[] = [];
    const shortcutBridge: NativeBridgeV1 = {
      async request(request) {
        requests.push(request);
        if (request.operation === "shortcuts.status") return { kind: "shortcut-status", platform: "macos", permission: "available", bindings: defaultDevHudSettings.shortcuts.desktop, error: null };
        if (request.operation === "shortcuts.stage") return new Promise((resolve) => { finishStage = resolve; });
        if (request.operation === "shortcuts.commit") return { kind: "shortcut-status", platform: "macos", permission: "available", bindings: request.bindings, error: null };
        throw new Error(`unexpected request ${request.operation}`);
      },
      async listen() { return () => {}; },
    };
    render(<SynchronizedSettingsBoundary copy={messages.en} bridge={shortcutBridge} sectionContributions={{ ...contributions, Shortcuts: ShortcutSettings }} />);
    fireEvent.click(sectionButton(messages.en.settingsShortcutsTitle));
    const paletteGroup = screen.getByRole("group", { name: messages.en.openPalette });
    const enabled = within(paletteGroup).getByRole("checkbox", { name: messages.en.shortcutEnabled }) as HTMLInputElement;
    fireEvent.click(enabled);
    await waitFor(() => expect(requests.some(({ operation }) => operation === "shortcuts.stage")).toBe(true));

    fireEvent.click(sectionButton(messages.en.settingsAppearanceTitle));
    expect(enabled.matches(":disabled")).toBe(true);
    fireEvent.click(sectionButton(messages.en.settingsShortcutsTitle));
    expect((within(screen.getByRole("group", { name: messages.en.openPalette })).getByRole("checkbox", { name: messages.en.shortcutEnabled }) as HTMLInputElement).matches(":disabled")).toBe(true);

    const stage = requests.find((request) => request.operation === "shortcuts.stage");
    if (!stage || stage.operation !== "shortcuts.stage" || !finishStage) throw new Error("shortcut stage was not retained");
    finishStage({ kind: "shortcut-status", platform: "macos", permission: "available", bindings: stage.bindings, error: null });
    await waitFor(() => expect(requests.some(({ operation }) => operation === "shortcuts.commit")).toBe(true));
  });

  it.each([390, 320])("renders exactly six mobile rows at %ipx and restores focus to the detail opener", async (width) => {
    Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: width });
    render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} mobile sectionContributions={contributions} NativeMessagingSettings={Chrome} localAgentsAvailable notification={{ permission: messages.en.notificationAuthorized, failed: false, onRequest: vi.fn() }} storeUpdates={{ configured: true, failed: false, onOpen: vi.fn() }} />);
    const index = screen.getByRole("region", { name: messages.en.settingsSections });
    const rows = within(index).getAllByRole("button");
    expect(rows).toHaveLength(6);
    expect(document.querySelector("[data-settings-section='shortcuts']")).toBeNull();
    expect(document.querySelector("[data-settings-section='chrome-extension']")).toBeNull();
    expect(document.querySelector("[data-settings-section='local-agents']")).toBeNull();
    const opener = rows[2]!;
    fireEvent.click(opener);
    await frame();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: messages.en.githubSetupTitle }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.backToSettings }));
    await frame();
    expect(document.activeElement).toBe(sectionButton(messages.en.githubSetupTitle));
  });

  it("falls back to Appearance when a selected capability disappears", async () => {
    const view = render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} sectionContributions={contributions} localAgentsAvailable />);
    fireEvent.click(sectionButton(messages.en.localAgentsTitle));
    await frame();
    view.rerender(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} sectionContributions={contributions} localAgentsAvailable={false} />);
    await frame();
    expect(document.querySelector("[data-settings-section='local-agents']")).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: messages.en.settingsAppearanceTitle }));
  });

  it.each([
    ["guest", messages.en.guestSettingsLocal],
    ["signed-out", messages.en.guestSettingsLocal],
    ["blocked", messages.en.blockedLocalHint],
    ["deletion-pending", messages.en.deletionPendingSummary],
  ] as const)("keeps the %s synchronization summary at Settings level", (status, expected) => {
    identity = identityWith({ status });
    render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} />);
    expect(within(screen.getByRole("region", { name: messages.en.synchronizedSettings })).getByText(expected)).toBeTruthy();
  });

  it("distinguishes authenticated revision and offline read-only summaries", () => {
    identity = identityWith({ status: "authenticated", revision: 7n });
    const view = render(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} />);
    expect(screen.getByText(`${messages.en.settingsRevision}: 7`)).toBeTruthy();
    identity = identityWith({ status: "authenticated", revision: 7n, offline: true, readOnly: true });
    view.rerender(<SynchronizedSettingsBoundary copy={messages.en} bridge={bridge} />);
    expect(screen.getByText(messages.en.offlineSettingsReadOnly)).toBeTruthy();
  });
});
