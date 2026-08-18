import { useEffect, useEffectEvent, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type Ref } from "react";
import type { Copy } from "./localization";
import { GitHubSettings } from "./github-settings-ui.tsx";
import type { GitHubProvider } from "./github-provider.ts";
import { NativeBridgeError, nativeBridge, type NativeBridgeV1, type NativeShortcutPermission, type NativeShortcutPlatform } from "./native-bridge";
import { useIdentitySettings } from "./service-boundary";
import { browserShell, LanguagePreference, PlatformCapability, normalizeApiOrigin, ThemePreference, type ExternalLinkTarget, type RuntimeCapabilities } from "./shell";
import type { DevHudSettingsV1 } from "./settings-contract";
import type { SettingsDiffEntry } from "./settings-diff";
import { inactiveDesktopShortcutBindings, ShortcutActionId, ShortcutContractError, ShortcutKey, ShortcutModifier, ShortcutValidationCode, availableShortcutActions, parseDesktopShortcutBindings, type ShortcutBinding } from "./shortcuts";

interface ApiEditorProps {
  readonly copy: Copy;
  readonly value: string;
  readonly inputRef?: Ref<HTMLInputElement>;
  readonly autoFocus?: boolean;
  readonly onApply: (value: string) => Promise<void>;
}

export function ApiOriginEditor({ copy, value, inputRef, autoFocus = false, onApply }: ApiEditorProps) {
  const [draft, setDraft] = useState(value);
  const [error, setError] = useState(false);
  useEffect(() => setDraft(value), [value]);
  const apply = async () => {
    const normalized = normalizeApiOrigin(draft);
    if (normalized === null) { setError(true); return; }
    setError(false);
    setDraft(normalized);
    await onApply(normalized);
  };
  return <div className="api-origin-editor">
    <label>{copy.apiOrigin}<input ref={inputRef} autoFocus={autoFocus} value={draft} onChange={(event) => setDraft(event.target.value)} aria-describedby="api-origin-security-warning api-origin-validation" /></label>
    <button type="button" onClick={() => void apply()} disabled={normalizeApiOrigin(draft) === normalizeApiOrigin(value)}>{copy.applyApiOrigin}</button>
    <p id="api-origin-security-warning" className="notice">{copy.customApiWarning}</p>
    <p>{copy.apiOriginHint}</p>
    {error && <p id="api-origin-validation" role="alert">{copy.invalidApiOrigin}</p>}
  </div>;
}

interface IdentityProps {
  readonly copy: Copy;
  readonly apiOrigin: string;
  readonly onApiOrigin: (value: string) => Promise<void>;
  readonly onComplete: () => void;
}

export function FirstRunIdentity({ copy, apiOrigin, onApiOrigin, onComplete }: IdentityProps) {
  const identity = useIdentitySettings();
  const [actionError, setActionError] = useState(false);
  useEffect(() => {
    if (identity.status === "authenticated" || identity.status === "blocked" || identity.status === "deletion-pending") onComplete();
  }, [identity.status, onComplete]);
  return <>
    <p className="eyebrow">{copy.account}</p>
    <h1>{copy.accountTitle}</h1>
    <p>{copy.firstRunSummary}</p>
    <ApiOriginEditor copy={copy} value={apiOrigin} autoFocus onApply={onApiOrigin} />
    <div className="actions">
      <button onClick={() => { setActionError(false); void identity.signIn().catch(() => setActionError(true)); }} disabled={identity.status === "starting" || identity.bootstrap === null || identity.signInPending}>{copy.signIn}</button>
      <button onClick={identity.continueLocally}>{copy.continueLocally}</button>
    </div>
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <section className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><button onClick={identity.retryIdentity}>{copy.retry}</button>{identity.identityResetAvailable && <button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</button>}</div></section>}
    {actionError && <p role="alert">{copy.signInFailed}</p>}
  </>;
}

interface AccountIdentityProps {
  readonly copy: Copy;
  readonly apiOrigin: string;
  readonly inputRef: Ref<HTMLInputElement>;
  readonly onApiOrigin: (value: string) => Promise<void>;
}

export function AccountIdentity({ copy, apiOrigin, inputRef, onApiOrigin }: AccountIdentityProps) {
  const identity = useIdentitySettings();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [actionError, setActionError] = useState(false);
  const deleteTrigger = useRef<HTMLButtonElement>(null);
  const deleteDialog = useRef<HTMLElement>(null);
  const cancelDelete = useRef<HTMLButtonElement>(null);
  const invoke = (action: () => Promise<void>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  const closeDeleteConfirmation = () => {
    setConfirmDelete(false);
    requestAnimationFrame(() => deleteTrigger.current?.focus());
  };
  useEffect(() => {
    if (!confirmDelete) return;
    cancelDelete.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") closeDeleteConfirmation(); };
    addEventListener("keydown", closeOnEscape);
    return () => removeEventListener("keydown", closeOnEscape);
  }, [confirmDelete]);
  return <>
    <p className="eyebrow">{copy.account}</p>
    <h2>{copy.accountTitle}</h2>
    <p>{copy.accountSummary}</p>
    <ApiOriginEditor copy={copy} value={apiOrigin} inputRef={inputRef} onApply={onApiOrigin} />
    {identity.status === "starting" && <p role="status">{copy.fetchingBootstrap}</p>}
    {identity.status === "error" && <section className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><button onClick={identity.retryIdentity}>{copy.retry}</button><button onClick={identity.continueLocally}>{copy.continueLocally}</button>{identity.identityResetAvailable && <button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</button>}</div></section>}
    {(identity.status === "signed-out" || identity.status === "guest") && <button onClick={() => invoke(identity.signIn)} disabled={identity.bootstrap === null || identity.signInPending}>{copy.signIn}</button>}
    {identity.status === "authenticated" && identity.accountError && <section className="notice" role="alert"><p>{copy.accountLoadFailed}</p><code>{`account-connect-${identity.accountError.code}`}</code>{identity.accountError.correlationId && <> {copy.correlationId}: <code>{identity.accountError.correlationId}</code></>}<button onClick={() => void identity.retryAccount()}>{copy.retry}</button></section>}
    {identity.status === "authenticated" && !identity.accountError && identity.account === null && <p role="status">{copy.loadingAccount}</p>}
    {identity.status === "authenticated" && !identity.accountError && identity.account !== null && <section className="account-session" aria-label={copy.signedInSession}>
      <p>{identity.account.displayName || identity.account.email || copy.signedIn}</p>
      <div className="actions"><button onClick={() => invoke(identity.logout)}>{copy.logout}</button><button ref={deleteTrigger} className="danger" onClick={() => setConfirmDelete(true)}>{copy.deleteAccount}</button></div>
    </section>}
    {identity.status === "blocked" && <section className="notice" role="status"><h3>{copy.blockedTitle}</h3><p>{copy.blockedSummary}</p><p>{copy.blockedLocalHint}</p><button onClick={() => invoke(identity.logout)}>{copy.logout}</button></section>}
    {identity.status === "deletion-pending" && <section className="notice" role="status"><h3>{copy.deletionPendingTitle}</h3><p>{copy.deletionPendingSummary}</p>{identity.account?.recoverableUntil && <p>{copy.recoverableUntil}: {new Date(Number(identity.account.recoverableUntil.seconds) * 1000).toLocaleString()}</p>}<div className="actions"><button onClick={() => invoke(identity.restoreAccount)}>{copy.restoreAccount}</button><button onClick={() => invoke(identity.logout)}>{copy.logout}</button></div></section>}
    {identity.status === "deletion-pending" && identity.deletionCleanupFailed && <section className="notice" role="alert"><p>{copy.accountActionFailed}</p><button onClick={() => void identity.retryDeletionCleanup()}>{copy.retry}</button></section>}
    {confirmDelete && identity.status === "authenticated" && !identity.accountError && identity.account !== null && <section ref={deleteDialog} className="confirmation" role="alertdialog" aria-modal="true" aria-labelledby="delete-account-title" onKeyDown={(event) => trapDialogFocus(event, deleteDialog.current)}><h3 id="delete-account-title">{copy.deleteAccountConfirmTitle}</h3><p>{copy.deleteAccountConfirmSummary}</p><div className="actions"><button className="danger" onClick={() => { closeDeleteConfirmation(); invoke(identity.deleteAccount); }}>{copy.deleteAccount}</button><button ref={cancelDelete} onClick={closeDeleteConfirmation}>{copy.cancel}</button></div></section>}
    {actionError && <p role="alert">{copy.accountActionFailed}</p>}
  </>;
}

export function SynchronizedAppearanceBoundary({ onAppearance }: { readonly onAppearance: (appearance: DevHudSettingsV1["appearance"]) => void }) {
  const identity = useIdentitySettings();
  const applyAppearance = useEffectEvent(onAppearance);
  useEffect(() => {
    applyAppearance(identity.settings.appearance);
  }, [identity.settings.appearance.language, identity.settings.appearance.theme]);
  return null;
}

export function SynchronizedShortcutBoundary({ bridge = nativeBridge }: { readonly bridge?: NativeBridgeV1 }) {
  const identity = useIdentitySettings();
  const bindings = identity.settings.shortcuts.desktop;
  useEffect(() => {
    let active = true;
    void bridge.listen((event) => {
      if (!active || event.version !== 1 || event.kind !== "shortcut-status") return;
      identity.setActiveShortcutBindings(event.error === null && identity.shortcutHydrationReady ? event.bindings : inactiveDesktopShortcutBindings);
    }).catch(() => {
      // Status requests in Settings remain the fallback for hosts without events.
    });
    return () => { active = false; };
  }, [bridge, identity.setActiveShortcutBindings, identity.shortcutHydrationReady]);
  useEffect(() => {
    let active = true;
    if (!identity.shortcutHydrationReady) {
      void bridge.request({ operation: "shortcuts.suspend" }).then((response) => {
        if (active && response.kind === "shortcut-status") identity.setActiveShortcutBindings(inactiveDesktopShortcutBindings);
      }).catch(() => {
        // A pre-bridge host cannot suspend matching. Its status remains visible
        // in Settings while the renderer waits for authenticated hydration.
      });
      return () => { active = false; };
    }
    void bridge.request({ operation: "shortcuts.apply", bindings }).then(async (response) => {
      if (!active || response.kind !== "shortcut-status") return;
      if (response.error === null) {
        identity.setActiveShortcutBindings(response.bindings);
        return;
      }
      if (response.error !== ShortcutValidationCode.Reserved) {
        identity.setActiveShortcutBindings(inactiveDesktopShortcutBindings);
        return;
      }
      // Synchronized bindings can be valid on their source desktop platform but
      // reserved here. Keep the native backend's last known platform-valid map
      // active without overwriting the shared settings snapshot.
      identity.setActiveShortcutBindings(response.bindings);
      const fallback = await bridge.request({ operation: "shortcuts.apply", bindings: response.bindings });
      if (active && fallback.kind === "shortcut-status" && fallback.error === null) identity.setActiveShortcutBindings(fallback.bindings);
    }).catch(() => {
      // Shortcut status remains available in Settings; startup hydration must
      // not turn the shell into an error state when native access is pending.
    });
    return () => { active = false; };
  }, [bindings, bridge, identity.setActiveShortcutBindings, identity.shortcutHydrationReady]);
  return null;
}

export function SynchronizedSettingsBoundary({ copy, bridge = nativeBridge, githubProvider, onOpenExternal = (target) => browserShell.openExternal(target, ""), showNativeShortcuts = false, shortcutCapabilities = { available: new Set<PlatformCapability>() } }: { readonly copy: Copy; readonly bridge?: NativeBridgeV1; readonly githubProvider?: GitHubProvider; readonly onOpenExternal?: (target: ExternalLinkTarget) => Promise<void>; readonly showNativeShortcuts?: boolean; readonly shortcutCapabilities?: RuntimeCapabilities }) {
  const identity = useIdentitySettings();
  const [actionError, setActionError] = useState(false);
  const invoke = (action: () => Promise<unknown>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  const replaceAppearance = (appearance: Partial<DevHudSettingsV1["appearance"]>) => invoke(() => identity.replaceSettings((current) => ({
    ...current,
    appearance: { ...current.appearance, ...appearance },
  })));
  return <>
    <label>{copy.theme}<select value={identity.settings.appearance.theme} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ theme: event.target.value as DevHudSettingsV1["appearance"]["theme"] })}>{Object.values(ThemePreference).map((value) => <option key={value} value={value}>{copy[value]}</option>)}</select></label>
    <label>{copy.language}<select value={identity.settings.appearance.language} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ language: event.target.value as DevHudSettingsV1["appearance"]["language"] })}><option value={LanguagePreference.System}>{copy.system}</option><option value={LanguagePreference.English}>{copy.english}</option><option value={LanguagePreference.Korean}>{copy.korean}</option></select></label>
    {showNativeShortcuts && <ShortcutSettings copy={copy} bridge={bridge} disabled={identity.readOnly} capabilities={shortcutCapabilities} bindings={identity.settings.shortcuts.desktop} onActiveBindings={identity.setActiveShortcutBindings} onPersist={(desktop) => identity.replaceSettings((current) => ({ ...current, shortcuts: { ...current.shortcuts, desktop } }))} />}
    {(identity.status === "guest" || identity.status === "signed-out" || identity.status === "starting") && <p className="notice">{copy.guestSettingsLocal}</p>}
    {identity.status === "blocked" && <p className="notice">{copy.blockedLocalHint}</p>}
    {identity.status === "deletion-pending" && <p className="notice">{copy.deletionPendingSummary}</p>}
    {identity.status === "authenticated" && <section className="synchronized-settings" aria-label={copy.synchronizedSettings}>
      <h3>{copy.synchronizedSettings}</h3>
      {identity.offline && <p className="notice" role="status">{copy.offlineSettingsReadOnly}</p>}
      {!identity.offline && <p>{copy.settingsRevision}: {identity.revision.toString()}</p>}
    {identity.importDiff && <SnapshotChoice key="import" choiceId="import" copy={copy} entries={identity.importDiff} title={copy.importSettingsTitle} summary={copy.importSettingsSummary} primary={copy.uploadLocal} secondary={copy.replaceLocal} onPrimary={() => invoke(identity.uploadLocal)} onSecondary={identity.replaceLocal} />}
    {identity.conflict && <SnapshotChoice key="conflict" choiceId="conflict" copy={copy} entries={identity.conflict.diff} title={copy.conflictTitle} summary={copy.conflictSummary} primary={copy.reapplyLocal} secondary={copy.adoptServer} onPrimary={() => invoke(identity.reapplyConflictLocal)} onSecondary={identity.adoptConflictServer} />}
    {(actionError || identity.error?.startsWith("settings-") || identity.settingsError) && <section className="notice" role="alert"><p>{copy.settingsActionFailed}{identity.error?.startsWith("settings-") && <> <code>{identity.error}</code></>}{identity.settingsError && <> <code>{`settings-connect-${identity.settingsError.code}`}</code>{identity.settingsError.correlationId && <> {copy.correlationId}: <code>{identity.settingsError.correlationId}</code></>}</>}</p><button onClick={() => invoke(identity.retrySettings)}>{copy.retry}</button></section>}
    </section>}
    <GitHubSettings copy={copy} bridge={bridge} provider={githubProvider} openExternal={onOpenExternal} />
  </>;
}

const shortcutLabels: Record<ShortcutActionId, keyof Copy> = {
  [ShortcutActionId.CommandPalette]: "openPalette",
  [ShortcutActionId.CaptureDisplay]: "captureDisplay",
  [ShortcutActionId.CaptureActiveWindow]: "captureWindow",
  [ShortcutActionId.CaptureAllDisplays]: "captureAll",
  [ShortcutActionId.CaptureSelection]: "captureSelection",
  [ShortcutActionId.CaptureToolbar]: "captureToolbar",
};

export function ShortcutPaletteTrigger({ copy, isMac, onOpen, triggerRef }: { readonly copy: Copy; readonly isMac: boolean; readonly onOpen: () => void; readonly triggerRef: Ref<HTMLButtonElement> }) {
  const identity = useIdentitySettings();
  const binding = identity.activeShortcutBindings[ShortcutActionId.CommandPalette];
  const modifiers = binding.modifiers.map((modifier) => modifier === ShortcutModifier.RightPrimary ? isMac ? copy.rightCommandK.replace(/ K$/u, "") : copy.rightControlK.replace(/ K$/u, "") : modifier === ShortcutModifier.Shift ? copy.shortcutShift : copy.shortcutAlt);
  const label = binding.enabled ? [...modifiers, copy[shortcutKeyLabels[binding.key]]].join(" + ") : copy.shortcutNone;
  return <button ref={triggerRef} className="palette-trigger" onClick={onOpen} aria-label={copy.openPalette}>{label}</button>;
}

const shortcutKeyLabels: Record<ShortcutKey, keyof Copy> = {
  [ShortcutKey.K]: "shortcutKeyK",
  [ShortcutKey.Digit1]: "shortcutDigit1",
  [ShortcutKey.Digit2]: "shortcutDigit2",
  [ShortcutKey.Digit3]: "shortcutDigit3",
  [ShortcutKey.Digit4]: "shortcutDigit4",
  [ShortcutKey.Digit5]: "shortcutDigit5",
  [ShortcutKey.Space]: "shortcutSpace",
  [ShortcutKey.Tab]: "shortcutTab",
  [ShortcutKey.Q]: "shortcutKeyQ",
  [ShortcutKey.Delete]: "shortcutDelete",
  [ShortcutKey.Backspace]: "shortcutBackspace",
};

function ShortcutSettings({ copy, bridge, disabled, capabilities, bindings, onActiveBindings, onPersist }: { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly disabled: boolean; readonly capabilities: RuntimeCapabilities; readonly bindings: DevHudSettingsV1["shortcuts"]["desktop"]; readonly onActiveBindings: (bindings: DevHudSettingsV1["shortcuts"]["desktop"]) => void; readonly onPersist: (bindings: DevHudSettingsV1["shortcuts"]["desktop"]) => Promise<boolean> }) {
  const [status, setStatus] = useState<{ platform: NativeShortcutPlatform; permission: NativeShortcutPermission; error: ShortcutValidationCode | null } | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    let active = true;
    void bridge.request({ operation: "shortcuts.status" }).then((response) => {
      if (active && response.kind === "shortcut-status") setStatus(response);
    }).catch(() => {
      // A settings refresh must not turn an otherwise usable shell into a
      // shortcut error state; explicit user edits still surface their result.
    });
    return () => { active = false; };
  }, [bindings, bridge]);
  useEffect(() => {
    let active = true;
    void bridge.listen((event) => {
      if (active && event.version === 1 && event.kind === "shortcut-status") setStatus(event);
    }).catch(() => {});
    return () => { active = false; };
  }, [bridge]);
  const commit = async (action: ShortcutActionId, update: Partial<ShortcutBinding>) => {
    if (saving) return;
    const candidate = { ...bindings, [action]: { ...bindings[action], ...update } };
    setSaving(true);
    try {
      const structured = parseDesktopShortcutBindings(candidate);
      const result = await bridge.request({ operation: "shortcuts.stage", bindings: structured });
      if (result.kind !== "shortcut-status" || result.error !== null) { if (result.kind === "shortcut-status") setStatus(result); return; }
      try {
        if (await onPersist(structured)) {
          const committed = await bridge.request({ operation: "shortcuts.commit", bindings: structured });
          if (committed.kind === "shortcut-status") setStatus(committed);
          return;
        }
        const rollback = await bridge.request({ operation: "shortcuts.rollback" });
        if (rollback.kind === "shortcut-status") setStatus(rollback);
      } catch {
        await bridge.request({ operation: "shortcuts.rollback" }).catch(() => {});
        setStatus({ platform: result.platform, permission: result.permission, error: ShortcutValidationCode.RegistrationFailed });
      }
    } catch (error) {
      setStatus((current) => ({ platform: current?.platform ?? "unsupported", permission: error instanceof NativeBridgeError ? "denied" : current?.permission ?? "unsupported", error: error instanceof ShortcutContractError ? error.code : error instanceof NativeBridgeError ? ShortcutValidationCode.PermissionDenied : ShortcutValidationCode.Malformed }));
    } finally {
      setSaving(false);
    }
  };
  const requestPermission = async () => {
    try {
      const permission = await bridge.request({ operation: "shortcuts.request-permission" });
      if (permission.kind !== "shortcut-status") return;
      if (permission.permission !== "available") { setStatus(permission); return; }
      const result = await bridge.request({ operation: "shortcuts.apply", bindings });
      if (result.kind !== "shortcut-status") return;
      setStatus(result);
      if (result.error === null) {
        onActiveBindings(result.bindings);
        return;
      }
      if (result.error !== ShortcutValidationCode.Reserved) {
        onActiveBindings(inactiveDesktopShortcutBindings);
        return;
      }
      // A synchronized binding can be reserved on this platform. Restore the
      // platform-valid native map instead of overwriting the shared snapshot.
      onActiveBindings(result.bindings);
      const fallback = await bridge.request({ operation: "shortcuts.apply", bindings: result.bindings });
      if (fallback.kind === "shortcut-status") {
        setStatus(fallback);
        if (fallback.error === null) onActiveBindings(fallback.bindings);
      }
    } catch (error) {
      setStatus((current) => ({ platform: current?.platform ?? "unsupported", permission: error instanceof NativeBridgeError ? "denied" : current?.permission ?? "unsupported", error: error instanceof NativeBridgeError ? ShortcutValidationCode.PermissionDenied : ShortcutValidationCode.RegistrationFailed }));
    }
  };
  const errorCopy = status?.error === ShortcutValidationCode.Conflict ? copy.shortcutConflict : status?.error === ShortcutValidationCode.Reserved ? copy.shortcutReserved : status?.error === ShortcutValidationCode.PermissionDenied ? copy.shortcutPermissionDenied : status?.error === ShortcutValidationCode.RegistrationFailed ? copy.shortcutRegistrationFailed : status?.error === ShortcutValidationCode.Malformed ? copy.shortcutMalformed : null;
  const availableActions = availableShortcutActions(capabilities);
  return <section className="native-setting" aria-label={copy.keyboardShortcuts}>
    <h3>{copy.keyboardShortcuts}</h3>
    {Object.values(ShortcutActionId).map((action) => {
      const binding = bindings[action];
      const unavailable = !availableActions.includes(action);
      return <fieldset key={action} disabled={disabled || saving || unavailable}><legend>{copy[shortcutLabels[action]]}</legend>
        {unavailable && <p className="notice">{copy.unavailable}</p>}
        <label className="check"><input type="checkbox" checked={binding.enabled} onChange={(event) => void commit(action, { enabled: event.target.checked })} />{copy.shortcutEnabled}</label>
        <span>{copy.shortcutModifier}</span>{([ShortcutModifier.RightPrimary, ShortcutModifier.Shift, ShortcutModifier.Alt] as const).map((modifier) => <label className="check" key={modifier}><input type="checkbox" checked={binding.modifiers.includes(modifier)} onChange={(event) => void commit(action, { modifiers: event.target.checked ? [...binding.modifiers, modifier] : binding.modifiers.filter((current) => current !== modifier) })} />{modifier === ShortcutModifier.RightPrimary ? copy.shortcutRightPrimary : modifier === ShortcutModifier.Shift ? copy.shortcutShift : copy.shortcutAlt}</label>)}
        <label>{copy.shortcutKey}<select value={binding.key} onChange={(event) => void commit(action, { key: event.target.value as ShortcutKey })}>{Object.values(ShortcutKey).map((key) => <option key={key} value={key}>{copy[shortcutKeyLabels[key]]}</option>)}</select></label>
      </fieldset>;
    })}
    {status?.platform === "macos" && <p className="notice">{copy.shortcutMacAccessibility} {copy.shortcutMacInputMonitoring}</p>}
    {status?.platform === "x11" && <p className="notice">{copy.shortcutLinuxGuidance}</p>}
    {(status?.permission === "denied" || status?.permission === "not-determined") && <button type="button" onClick={requestPermission}>{copy.shortcutRequestPermission}</button>}
    {status?.permission === "unsupported" && <p className="notice">{copy.shortcutUnsupported}</p>}
    {errorCopy && <p className="native-setting-error" role="alert">{errorCopy}</p>}
  </section>;
}

function SnapshotChoice({ choiceId, copy, entries, title, summary, primary, secondary, onPrimary, onSecondary }: { readonly choiceId: string; readonly copy: Copy; readonly entries: readonly SettingsDiffEntry[]; readonly title: string; readonly summary: string; readonly primary: string; readonly secondary: string; readonly onPrimary: () => void; readonly onSecondary: () => void }) {
  const [open, setOpen] = useState(true);
  const dialog = useRef<HTMLElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef<HTMLElement | null>(null);
  const close = () => {
    setOpen(false);
    requestAnimationFrame(() => restoreFocus.current?.focus());
  };
  const choose = (action: () => void) => {
    action();
    requestAnimationFrame(() => restoreFocus.current?.focus());
  };
  useEffect(() => {
    if (!open) return;
    restoreFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButton.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    addEventListener("keydown", closeOnEscape);
    return () => removeEventListener("keydown", closeOnEscape);
  }, [open]);
  if (!open) return <section className="notice"><p>{summary}</p><button onClick={() => setOpen(true)}>{title}</button></section>;
  const titleId = `snapshot-choice-${choiceId}-title`;
  return <section ref={dialog} className="snapshot-choice" role="dialog" aria-modal="true" aria-labelledby={titleId} onKeyDown={(event) => trapDialogFocus(event, dialog.current)}>
    <button ref={closeButton} type="button" onClick={close}>{copy.close}</button>
    <h4 id={titleId}>{title}</h4><p>{summary}</p>
    <table><caption>{copy.completeSnapshotDiff}</caption><thead><tr><th scope="col">{copy.settingPath}</th><th scope="col">{copy.localValue}</th><th scope="col">{copy.serverValue}</th></tr></thead><tbody>{entries.length === 0 ? <tr><td colSpan={3}>{copy.noDifferences}</td></tr> : entries.map((entry) => <tr key={`${entry.path}:${entry.kind}`}><th scope="row">{entry.path}</th><td><code>{printValue(entry.local)}</code></td><td><code>{printValue(entry.server)}</code></td></tr>)}</tbody></table>
    <div className="actions"><button onClick={() => choose(onPrimary)}>{primary}</button><button onClick={() => choose(onSecondary)}>{secondary}</button></div>
  </section>;
}

function trapDialogFocus(event: ReactKeyboardEvent<HTMLElement>, dialog: HTMLElement | null): void {
  if (event.key !== "Tab" || dialog === null) return;
  const focusable = dialog.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), [href]");
  if (focusable.length === 0) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function printValue(value: unknown): string {
  if (value === undefined) return "—";
  return typeof value === "string" ? value : JSON.stringify(value);
}
