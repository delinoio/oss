import { useEffect, useRef, useState, type KeyboardEvent } from "react";

import {
  ShortcutKey,
  ShortcutModifier,
  type StructuredShortcut,
} from "../persistence/contracts";
import type {
  AutostartOutcome,
  DesktopBridge,
  ShortcutFailure,
} from "../runtime/desktop";
import { ThemePreference, useApplication } from "./state";

type CapturedShortcut =
  | { readonly kind: "candidate"; readonly shortcut: StructuredShortcut }
  | { readonly kind: "cancelled" }
  | { readonly kind: "ignored" }
  | { readonly kind: "invalid" };

const keyForCode: Readonly<Record<string, ShortcutKey>> = Object.freeze({
  ...Object.fromEntries(
    "ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("").map((letter) => [
      `Key${letter}`,
      letter.toLowerCase() as ShortcutKey,
    ]),
  ),
  ...Object.fromEntries(
    "0123456789".split("").map((digit) => [
      `Digit${digit}`,
      digit as ShortcutKey,
    ]),
  ),
  F1: ShortcutKey.F1,
  F2: ShortcutKey.F2,
  F3: ShortcutKey.F3,
  F4: ShortcutKey.F4,
  F5: ShortcutKey.F5,
  F6: ShortcutKey.F6,
  F7: ShortcutKey.F7,
  F8: ShortcutKey.F8,
  F9: ShortcutKey.F9,
  F10: ShortcutKey.F10,
  F11: ShortcutKey.F11,
  F12: ShortcutKey.F12,
  Space: ShortcutKey.Space,
  Enter: ShortcutKey.Enter,
});

const modifierCodes = new Set([
  "AltLeft",
  "AltRight",
  "ControlLeft",
  "ControlRight",
  "MetaLeft",
  "MetaRight",
  "ShiftLeft",
  "ShiftRight",
]);

export function shortcutFromKeyboardInput(input: {
  readonly code: string;
  readonly ctrlKey: boolean;
  readonly altKey: boolean;
  readonly shiftKey: boolean;
  readonly metaKey: boolean;
}): CapturedShortcut {
  if (input.code === "Escape") return { kind: "cancelled" };
  if (modifierCodes.has(input.code)) return { kind: "ignored" };
  const key = keyForCode[input.code];
  if (key === undefined) return { kind: "invalid" };
  const modifiers = [
    input.ctrlKey ? ShortcutModifier.Control : null,
    input.altKey ? ShortcutModifier.Alt : null,
    input.shiftKey ? ShortcutModifier.Shift : null,
    input.metaKey ? ShortcutModifier.Meta : null,
  ].filter((modifier): modifier is ShortcutModifier => modifier !== null);
  if (modifiers.length === 0) return { kind: "invalid" };
  return { kind: "candidate", shortcut: { modifiers, key } };
}

function shortcutLabel(shortcut: StructuredShortcut | null): string {
  if (shortcut === null) return "Not configured";
  const labels: Record<ShortcutModifier, string> = {
    [ShortcutModifier.Control]: "Ctrl",
    [ShortcutModifier.Alt]: "Alt",
    [ShortcutModifier.Shift]: "Shift",
    [ShortcutModifier.Meta]: "Meta",
  };
  return [...shortcut.modifiers.map((modifier) => labels[modifier]), shortcut.key.toUpperCase()].join(
    " + ",
  );
}

const shortcutFailureMessage: Record<ShortcutFailure, string> = {
  malformed: "That shortcut is malformed. The previous shortcut is still active.",
  conflict: "That shortcut is already in use. The previous shortcut is still active.",
  "permission-denied":
    "DevHud does not have permission to register that shortcut. The previous shortcut is still active.",
  "registration-failed":
    "DevHud could not register that shortcut. The previous shortcut is still active.",
  "unsupported-display":
    "Global shortcuts require X11 or XWayland on Linux. The previous shortcut is unchanged.",
  "storage-failed":
    "DevHud could not save that shortcut. The previous shortcut is still active.",
};

const shortcutStartupFailureMessage: Record<ShortcutFailure, string> = {
  malformed:
    "The saved shortcut is malformed and could not be restored. Record another shortcut.",
  conflict:
    "The saved shortcut is already in use and could not be restored. Record another shortcut.",
  "permission-denied":
    "DevHud does not have permission to restore the saved shortcut. Record another shortcut after granting permission.",
  "registration-failed":
    "DevHud could not restore the saved shortcut. Record another shortcut.",
  "unsupported-display":
    "The saved shortcut could not be restored because global shortcuts require X11 or XWayland on Linux.",
  "storage-failed":
    "DevHud could not restore the saved shortcut because local settings are unavailable.",
};

export function SettingsPanel({
  bridge,
  firstRun = false,
  onClose,
  onFirstRunCompleted,
  showDesktopControls = true,
  startupAutostartOutcome,
  startupShortcutFailure,
}: {
  readonly bridge: DesktopBridge | null;
  readonly firstRun?: boolean;
  readonly onClose: () => void;
  readonly onFirstRunCompleted?: () => void;
  readonly showDesktopControls?: boolean;
  readonly startupAutostartOutcome?: AutostartOutcome | null;
  readonly startupShortcutFailure?: ShortcutFailure | null;
}) {
  const {
    adoptNativeLaunchAtLogin,
    adoptNativeShortcut,
    persistenceReady,
    setLaunchAtLogin,
    setShortcut,
    setTheme,
    settings,
  } = useApplication();
  const captureRef = useRef<HTMLButtonElement>(null);
  const [capturing, setCapturing] = useState(false);
  const [shortcutRestoredInSession, setShortcutRestoredInSession] =
    useState(false);
  const [shortcutStatus, setShortcutStatus] = useState<{
    readonly message: string;
    readonly error: boolean;
  } | null>(null);
  const [autostartStatus, setAutostartStatus] = useState<{
    readonly message: string;
    readonly error: boolean;
  } | null>(null);
  const displayedShortcutStatus =
    shortcutStatus ??
    (startupShortcutFailure === undefined || startupShortcutFailure === null
      ? null
      : {
          message: shortcutStartupFailureMessage[startupShortcutFailure],
          error: true,
        });
  const startupAutostartStatus =
    startupAutostartOutcome?.status === "unchanged"
      ? {
          message:
            startupAutostartOutcome.reason === "permission-denied"
              ? "DevHud could not restore launch at login because permission was denied. The actual system setting is shown."
              : startupAutostartOutcome.reason === "storage-failed"
                ? "DevHud left launch at login unchanged because local settings are unavailable or invalid. The actual system setting is shown."
              : "DevHud could not restore launch at login. The actual system setting is shown.",
          error: true,
        }
      : null;
  const displayedAutostartStatus =
    autostartStatus ?? startupAutostartStatus;
  const displayedLaunchAtLogin =
    autostartStatus === null &&
    startupAutostartOutcome?.status === "unchanged"
      ? startupAutostartOutcome.enabled
      : settings.launchAtLogin;

  useEffect(() => {
    if (firstRun && persistenceReady) captureRef.current?.focus();
  }, [firstRun, persistenceReady]);

  const beginCapture = () => {
    setCapturing(true);
    setShortcutStatus({
      message: "Press a shortcut with at least one modifier, or press Escape to cancel.",
      error: false,
    });
    queueMicrotask(() => captureRef.current?.focus());
  };

  const onShortcutKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (!capturing) return;
    event.preventDefault();
    event.stopPropagation();
    const captured = shortcutFromKeyboardInput(event);
    if (captured.kind === "ignored") return;
    if (captured.kind === "cancelled") {
      setCapturing(false);
      setShortcutStatus({
        message: "Shortcut capture cancelled. The previous shortcut is still active.",
        error: false,
      });
      return;
    }
    if (captured.kind === "invalid") {
      setShortcutStatus({
        message: "Use a supported letter, number, function key, Space, or Enter with a modifier.",
        error: true,
      });
      return;
    }

    setCapturing(false);
    void (bridge?.replaceGlobalShortcut(captured.shortcut) ??
      Promise.resolve({
        status: "replaced" as const,
        shortcut: captured.shortcut,
      })).then((outcome) => {
      if (outcome.status === "replaced") {
        if (bridge === null) setShortcut(outcome.shortcut);
        else adoptNativeShortcut(outcome.shortcut);
        setShortcutRestoredInSession(true);
        setShortcutStatus({ message: "Shortcut updated.", error: false });
      } else if (outcome.status === "unchanged") {
        if (outcome.shortcut !== undefined) {
          adoptNativeShortcut(outcome.shortcut);
          setShortcutRestoredInSession(true);
        }
        setShortcutStatus({
          message:
            outcome.shortcut === undefined
              ? shortcutFailureMessage[outcome.reason]
              : "DevHud could not save that shortcut or restore the previous binding. The effective shortcut is shown.",
          error: true,
        });
      } else {
        setShortcutStatus({
          message: "Shortcut capture cancelled. The previous shortcut is still active.",
          error: false,
        });
      }
    });
  };

  const changeAutostart = (enabled: boolean) => {
    setAutostartStatus(null);
    void (bridge?.setLaunchAtLogin(enabled) ??
      Promise.resolve({ status: "applied" as const, enabled })).then((outcome) => {
      if (outcome.status === "applied") {
        if (bridge === null) setLaunchAtLogin(outcome.enabled);
        else adoptNativeLaunchAtLogin(outcome.enabled);
        setAutostartStatus({
          message: outcome.enabled
            ? "DevHud will launch at login."
            : "Launch at login is disabled.",
          error: false,
        });
      } else {
        adoptNativeLaunchAtLogin(outcome.enabled);
        setAutostartStatus({
          message: outcome.reason === "permission-denied"
            ? "Permission was denied. The previous launch-at-login setting was kept."
            : "DevHud could not change launch at login. The previous setting was kept.",
          error: true,
        });
      }
    });
  };

  const changeTheme = (theme: ThemePreference) => {
    void setTheme(theme).then((saved) => {
      if (saved) bridge?.publishTheme(theme);
    });
  };

  const completeFirstRun = () => {
    void (bridge?.completeFirstRun() ??
      Promise.resolve({ status: "completed" as const })).then((outcome) => {
      if (outcome.status === "completed") {
        onFirstRunCompleted?.();
        onClose();
      } else {
        setShortcutStatus({
          message: "DevHud could not save first-run settings. Try again from the tray.",
          error: true,
        });
      }
    });
  };

  return (
    <>
      <div className="dialog-heading">
        <div>
          <p className="eyebrow">{firstRun ? "Welcome" : "Settings"}</p>
          <h1>{firstRun ? "Set up DevHud" : "DevHud settings"}</h1>
        </div>
        <button
          aria-label="Close settings"
          className="icon-button"
          onClick={onClose}
          type="button"
        >
          ×
        </button>
      </div>
      {firstRun ? (
        <p className="muted">
          Choose a global shortcut, or skip for now. DevHud remains available from
          the tray either way.
        </p>
      ) : null}

      {showDesktopControls ? (
        <>
          <section className="settings-section" aria-labelledby="shortcut-heading">
            <h2 id="shortcut-heading">Global shortcut</h2>
            <p className="setting-value">
              Current shortcut:{" "}
              <kbd>
                {shortcutLabel(
                  startupShortcutFailure !== undefined &&
                    startupShortcutFailure !== null &&
                    !shortcutRestoredInSession
                    ? null
                    : settings.shortcut,
                )}
              </kbd>
            </p>
            <div className="button-row">
              <button
                aria-pressed={capturing}
                className="secondary-button"
                disabled={!persistenceReady}
                onClick={beginCapture}
                onKeyDown={onShortcutKeyDown}
                ref={captureRef}
                type="button"
              >
                {capturing ? "Press shortcut…" : "Record shortcut"}
              </button>
              {capturing ? (
                <button
                  className="text-button"
                  onClick={() => {
                    setCapturing(false);
                    setShortcutStatus({
                      message:
                        "Shortcut capture cancelled. The previous shortcut is still active.",
                      error: false,
                    });
                  }}
                  type="button"
                >
                  Cancel
                </button>
              ) : null}
            </div>
            {displayedShortcutStatus ? (
              <p role={displayedShortcutStatus.error ? "alert" : "status"}>
                {displayedShortcutStatus.message}
              </p>
            ) : null}
          </section>

          <section className="settings-section" aria-labelledby="startup-heading">
            <h2 id="startup-heading">Startup</h2>
            <label className="check-field">
              <input
                checked={displayedLaunchAtLogin}
                disabled={!persistenceReady}
                onChange={(event) => changeAutostart(event.target.checked)}
                type="checkbox"
              />
              Launch DevHud at login
            </label>
            <p className="muted">
              Disabled by default. DevHud starts in the tray when enabled.
            </p>
            {displayedAutostartStatus ? (
              <p role={displayedAutostartStatus.error ? "alert" : "status"}>
                {displayedAutostartStatus.message}
              </p>
            ) : null}
          </section>
        </>
      ) : null}

      <section className="settings-section" aria-labelledby="appearance-heading">
        <h2 id="appearance-heading">Appearance</h2>
        <label className="field" htmlFor="theme-preference">
          Theme preference
          <select
            disabled={!persistenceReady}
            id="theme-preference"
            onChange={(event) =>
              changeTheme(event.target.value as ThemePreference)
            }
            value={settings.theme}
          >
            <option value={ThemePreference.System}>System</option>
            <option value={ThemePreference.Light}>Light</option>
            <option value={ThemePreference.Dark}>Dark</option>
          </select>
        </label>
      </section>

      {!persistenceReady ? (
        <p className="muted" role="status">
          Loading local settings…
        </p>
      ) : null}
      <p className="muted">
        Settings stay on this device. No account or cloud sync is available.
      </p>
      <div className="button-row settings-actions">
        {firstRun ? (
          <button className="text-button" onClick={completeFirstRun} type="button">
            Skip for now
          </button>
        ) : null}
        <button
          className="primary-button"
          onClick={firstRun ? completeFirstRun : onClose}
          type="button"
        >
          Done
        </button>
      </div>
    </>
  );
}
