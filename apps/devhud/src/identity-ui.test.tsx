// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AccountIdentity, ShortcutPaletteTrigger } from "./identity-ui";
import { messages } from "./localization";
import type { IdentitySettingsValue } from "./service-boundary";
import { defaultDevHudSettings } from "./settings-contract";
import { ShortcutActionId, ShortcutKey, ShortcutModifier } from "./shortcuts";

let identity: IdentitySettingsValue;

vi.mock("./service-boundary", () => ({ useIdentitySettings: () => identity }));

function identityWith(overrides: Partial<IdentitySettingsValue> = {}): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings: defaultDevHudSettings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: defaultDevHudSettings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("test"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true),
    signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), replaceSettingsAt: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(async () => true), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
    ...overrides,
  };
}

beforeEach(() => { identity = identityWith(); });
afterEach(cleanup);

describe("identity UI", () => {
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
});
