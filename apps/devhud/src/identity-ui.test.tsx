// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

import { AccountIdentity, ShortcutPaletteTrigger, SynchronizedSettingsBoundary, SynchronizedShortcutBoundary } from "./identity-ui";
import { messages } from "./localization";
import { localAgentPromptRepositories } from "./local-agent-settings-ui";
import { NativeMessagingSettings, SynchronizedNativeMessagingBoundary } from "./native-messaging-ui";
import { LocalAgentKind, LocalAgentMode, type NativeBridgeV1 } from "./native-bridge";
import type { IdentitySettingsValue } from "./service-boundary";
import { defaultDevHudSettings, parseDevHudSettings } from "./settings-contract";
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
    status: "guest", bootstrap: null, account: null, settings: defaultDevHudSettings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: defaultDevHudSettings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, deckAccessSuspended: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("test"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true),
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
  const accountProps = (overrides: Partial<ComponentProps<typeof AccountIdentity>> = {}) => ({
    copy: messages.en,
    apiOrigin: "https://devhud.api.delino.io",
    inputRef: { current: null },
    onApiOrigin: vi.fn(async () => undefined),
    onModalConfirmationOpenChange: vi.fn(),
    mobile: false,
    onOpenExternal: vi.fn(),
    externalMessage: null,
    externalMessageText: "",
    externalMessageIsError: false,
    apiChangeError: null,
    ...overrides,
  });

  it.each(["starting", "signed-out", "guest", "authenticated", "blocked", "deletion-pending", "error"] as const)("keeps the Account hierarchy and API origin available for %s", (status) => {
    identity = identityWith({ status, account: status === "authenticated" ? ({ displayName: "Fixture User", email: "fixture@example.com" } as never) : null });
    render(<AccountIdentity {...accountProps()} />);

    const sections = Array.from(document.querySelectorAll<HTMLElement>(".account-section")).map((section) => section.getAttribute("aria-label"));
    expect(sections.slice(0, 4)).toEqual([messages.en.session, messages.en.apiOrigin, messages.en.security, messages.en.externalTools]);
    expect(screen.getByRole("textbox", { name: messages.en.apiOrigin })).toBeTruthy();
    expect(Array.from(document.querySelectorAll(".account-section .state-panel h2"))).toHaveLength(0);
    expect(Array.from(document.querySelectorAll(".account-section .state-panel h4")).length).toBe(status === "authenticated" ? 0 : 1);
    expect(Boolean(screen.queryByLabelText(messages.en.dangerZone))).toBe(status === "authenticated");
  });

  it("confirms origin changes and keeps external targets platform-scoped", async () => {
    const onApiOrigin = vi.fn(async () => undefined);
    const onOpenExternal = vi.fn();
    const onModalConfirmationOpenChange = vi.fn();
    render(<AccountIdentity {...accountProps({ onApiOrigin, onOpenExternal, onModalConfirmationOpenChange })} />);
    const origin = screen.getByRole("textbox", { name: messages.en.apiOrigin });
    fireEvent.change(origin, { target: { value: "https://custom.example" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.applyApiOrigin }));
    const confirmation = await screen.findByRole("dialog", { name: messages.en.apiChangeConfirmTitle });
    await waitFor(() => expect(onModalConfirmationOpenChange).toHaveBeenCalledWith(true));
    expect(onApiOrigin).not.toHaveBeenCalled();
    fireEvent.click(within(confirmation).getByRole("button", { name: messages.en.cancel }));
    await waitFor(() => expect(onModalConfirmationOpenChange).toHaveBeenLastCalledWith(false));
    expect(onApiOrigin).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: messages.en.applyApiOrigin }));
    fireEvent.click(within(await screen.findByRole("dialog", { name: messages.en.apiChangeConfirmTitle })).getByRole("button", { name: messages.en.applyApiOrigin }));
    await waitFor(() => expect(onApiOrigin).toHaveBeenCalledWith("https://custom.example"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubCreateFinePat }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubCreateClassicPat }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.issue }));
    expect(onOpenExternal).toHaveBeenCalledTimes(3);

    cleanup();
    render(<AccountIdentity {...accountProps({ mobile: true, onOpenExternal })} />);
    expect(screen.queryByRole("button", { name: messages.en.issue })).toBeNull();
    expect(screen.getByRole("button", { name: messages.en.githubCreateFinePat })).toBeTruthy();
  });

  it("keeps the custom-origin warning and API-change failures beside the editor", () => {
    render(<AccountIdentity {...accountProps({ apiChangeError: messages.en.externalFailed })} />);
    const input = screen.getByRole("textbox", { name: messages.en.apiOrigin });
    expect(input.getAttribute("aria-describedby")).toContain("api-origin-security-warning");
    expect(document.getElementById("api-origin-security-warning")?.textContent).toBe(messages.en.customApiWarning);
    expect(screen.getByRole("alert").textContent).toBe(messages.en.externalFailed);
  });

  it("uses the shared alert dialog for Delete focus containment and restoration", async () => {
    identity = identityWith({ status: "authenticated", account: { displayName: "Fixture User", email: "fixture@example.com" } as never });
    render(<AccountIdentity {...accountProps()} />);
    const trigger = screen.getByRole("button", { name: messages.en.deleteAccount });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle });
    const cancel = within(dialog).getByRole("button", { name: messages.en.cancel });
    const confirm = within(dialog).getByRole("button", { name: messages.en.deleteAccount });
    await waitFor(() => expect(document.activeElement).toBe(cancel));
    fireEvent.keyDown(cancel, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(confirm);
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it("keeps Account confirmation triggers mutually exclusive", async () => {
    identity = identityWith({ status: "authenticated", account: { displayName: "Fixture User", email: "fixture@example.com" } as never });
    render(<AccountIdentity {...accountProps()} />);
    const apply = screen.getByRole("button", { name: messages.en.applyApiOrigin }) as HTMLButtonElement;
    const deleteTrigger = screen.getByRole("button", { name: messages.en.deleteAccount }) as HTMLButtonElement;
    fireEvent.change(screen.getByRole("textbox", { name: messages.en.apiOrigin }), { target: { value: "https://custom.example" } });
    fireEvent.click(apply);
    const apiConfirmation = await screen.findByRole("dialog", { name: messages.en.apiChangeConfirmTitle });
    await waitFor(() => expect(deleteTrigger.disabled).toBe(true));
    fireEvent.click(deleteTrigger);
    expect(screen.queryByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle })).toBeNull();
    fireEvent.click(within(apiConfirmation).getByRole("button", { name: messages.en.cancel }));
    await waitFor(() => expect(deleteTrigger.disabled).toBe(false));

    fireEvent.click(deleteTrigger);
    const deleteConfirmation = await screen.findByRole("alertdialog", { name: messages.en.deleteAccountConfirmTitle });
    await waitFor(() => expect(apply.disabled).toBe(true));
    fireEvent.click(apply);
    expect(screen.queryByRole("dialog", { name: messages.en.apiChangeConfirmTitle })).toBeNull();
    fireEvent.click(within(deleteConfirmation).getByRole("button", { name: messages.en.cancel }));
    await waitFor(() => expect(apply.disabled).toBe(false));
  });

  it("retries a failed Native Messaging configuration publication", async () => {
    vi.useFakeTimers();
    nativeMessagingMock.configure.mockRejectedValueOnce(new Error("temporary bridge failure")).mockResolvedValue(undefined);
    render(<SynchronizedNativeMessagingBoundary />);
    await act(async () => {});

    expect(nativeMessagingMock.configure).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(249); });
    expect(nativeMessagingMock.configure).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(nativeMessagingMock.configure).toHaveBeenCalledTimes(2);
  });

  it("cancels a stale Native Messaging retry when settings change", async () => {
    vi.useFakeTimers();
    nativeMessagingMock.configure.mockRejectedValueOnce(new Error("temporary bridge failure")).mockResolvedValue(undefined);
    const view = render(<SynchronizedNativeMessagingBoundary />);
    await act(async () => {});
    const updatedSettings = { ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, language: "ko" as const } };
    identity = identityWith({ settings: updatedSettings });

    view.rerender(<SynchronizedNativeMessagingBoundary />);
    await act(async () => {});

    expect(nativeMessagingMock.configure).toHaveBeenCalledTimes(2);
    expect(nativeMessagingMock.configure).toHaveBeenLastCalledWith(updatedSettings, expect.any(String));
    await act(async () => { await vi.advanceTimersByTimeAsync(250); });
    expect(nativeMessagingMock.configure).toHaveBeenCalledTimes(2);
  });

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

  it("clears a transient pairing status failure after polling recovers", async () => {
    vi.useFakeTimers();
    nativeMessagingMock.status
      .mockResolvedValueOnce({ paired: false })
      .mockRejectedValueOnce(new Error("temporary status failure"))
      .mockResolvedValueOnce({ paired: true });
    render(<NativeMessagingSettings copy={messages.en} />);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: messages.en.nativeMessagingPair }));
    await act(async () => {});

    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
    expect(screen.getByRole("status").textContent).toBe(messages.en.nativeMessagingFailed);

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

    render(<AccountIdentity copy={messages.en} apiOrigin="https://devhud.api.delino.io" inputRef={{ current: null }} onApiOrigin={vi.fn(async () => undefined)} onModalConfirmationOpenChange={vi.fn()} mobile={false} onOpenExternal={vi.fn()} externalMessage={null} externalMessageText="" externalMessageIsError={false} apiChangeError={null} />);

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

  it("keeps local-agent prompt edits local until explicitly saved and includes the issue tracker", async () => {
    const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
    const secondaryProfile = { id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Secondary", kind: "classic" as const };
    const secondaryAgent = { id: "codex-secondary", enabled: true, kind: LocalAgentKind.Codex, mode: LocalAgentMode.Direct, repositoryPrompts: [], profileRef: secondaryProfile.id };
    const settings = parseDevHudSettings({
      ...defaultDevHudSettings,
      github: {
        ...defaultDevHudSettings.github,
        profiles: [profile, secondaryProfile],
        repositories: [{ owner: "octo", name: "application", profileRef: profile.id }],
        issueTracker: { owner: "octo", repository: "issues", labels: [], profileRef: profile.id },
      },
      agents: [
        { id: "codex", enabled: false, kind: LocalAgentKind.Codex, mode: LocalAgentMode.Draft, repositoryPrompts: [], profileRef: profile.id },
        secondaryAgent,
      ],
    });
    const replaceSettings = vi.fn<IdentitySettingsValue["replaceSettings"]>(async () => true);
    identity = identityWith({ settings, replaceSettings });

    render(<SynchronizedSettingsBoundary copy={messages.en} showLocalAgents />);
    const codex = screen.getByRole("group", { name: /Codex 0\.147\.0/u });
    const trackerPrompt = within(codex).getByLabelText(`${messages.en.localAgentRepositoryPrompt}: octo/issues`);
    fireEvent.change(trackerPrompt, { target: { value: "Use the issue template." } });
    expect(replaceSettings).not.toHaveBeenCalled();

    const editor = trackerPrompt.closest(".native-setting");
    if (!(editor instanceof HTMLElement)) throw new Error("prompt editor missing");
    fireEvent.click(within(editor).getByRole("button", { name: messages.en.localAgentSaveRepositoryPrompt }));

    await waitFor(() => expect(replaceSettings).toHaveBeenCalledOnce());
    const update = replaceSettings.mock.calls[0]?.[0];
    const updated = typeof update === "function" ? update(settings) : update;
    expect(updated?.agents[0]?.repositoryPrompts).toEqual([{ repository: { owner: "octo", name: "issues" }, body: "Use the issue template." }]);
    expect(updated?.agents).toHaveLength(2);
    expect(updated?.agents[1]).toEqual(secondaryAgent);
  });

  it("deduplicates issue trackers already present in local-agent prompt repositories", () => {
    const settings = {
      ...defaultDevHudSettings,
      github: {
        ...defaultDevHudSettings.github,
        repositories: [{ owner: "octo", name: "issues", profileRef: null }],
        issueTracker: { owner: "OCTO", repository: "ISSUES", labels: [], profileRef: null },
      },
    };
    expect(localAgentPromptRepositories(settings)).toEqual([{ owner: "octo", name: "issues" }]);
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
