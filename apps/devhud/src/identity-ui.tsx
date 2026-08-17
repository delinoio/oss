import { useEffect, useEffectEvent, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type Ref } from "react";
import type { Copy } from "./localization";
import { useIdentitySettings } from "./service-boundary";
import { LanguagePreference, normalizeApiOrigin, ThemePreference } from "./shell";
import type { DevHudSettingsV1 } from "./settings-contract";
import type { SettingsDiffEntry } from "./settings-diff";
import { NativeBridgeError, nativeBridge, type NativeBridgeV1, type NativeShortcutPermission, type NativeShortcutPlatform } from "./native-bridge";
import { ShortcutActionId, ShortcutKey, ShortcutModifier, ShortcutValidationCode, parseDesktopShortcutBindings, type ShortcutBinding } from "./shortcuts";

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
    {identity.status === "error" && <section className="notice" role="alert"><p>{copy.bootstrapFailed}</p>{identity.identityResetAvailable && <p>{copy.resetSignInHint}</p>}<div className="actions"><button onClick={identity.retryIdentity}>{copy.retry}</button>{identity.identityResetAvailable && <button onClick={() => void identity.resetIdentity().catch(() => {})}>{copy.resetSignIn}</button>}</div></section>}
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

export function SynchronizedSettingsBoundary({ copy, bridge = nativeBridge, showNativeShortcuts = false }: { readonly copy: Copy; readonly bridge?: NativeBridgeV1; readonly showNativeShortcuts?: boolean }) {
  const identity = useIdentitySettings();
  const [actionError, setActionError] = useState(false);
  const invoke = (action: () => Promise<void>) => { setActionError(false); void action().catch(() => setActionError(true)); };
  const replaceAppearance = (appearance: Partial<DevHudSettingsV1["appearance"]>) => invoke(() => identity.replaceSettings({
    ...identity.settings,
    appearance: { ...identity.settings.appearance, ...appearance },
  }));
  return <>
    <label>{copy.theme}<select value={identity.settings.appearance.theme} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ theme: event.target.value as DevHudSettingsV1["appearance"]["theme"] })}>{Object.values(ThemePreference).map((value) => <option key={value} value={value}>{copy[value]}</option>)}</select></label>
    <label>{copy.language}<select value={identity.settings.appearance.language} disabled={identity.readOnly} onChange={(event) => replaceAppearance({ language: event.target.value as DevHudSettingsV1["appearance"]["language"] })}><option value={LanguagePreference.System}>{copy.system}</option><option value={LanguagePreference.English}>{copy.english}</option><option value={LanguagePreference.Korean}>{copy.korean}</option></select></label>
    {showNativeShortcuts && <ShortcutSettings copy={copy} bridge={bridge} disabled={identity.readOnly} bindings={identity.settings.shortcuts.desktop} onPersist={async (desktop) => identity.replaceSettings({ ...identity.settings, shortcuts: { ...identity.settings.shortcuts, desktop } })} />}
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

function ShortcutSettings({ copy, bridge, disabled, bindings, onPersist }: { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly disabled: boolean; readonly bindings: DevHudSettingsV1["shortcuts"]["desktop"]; readonly onPersist: (bindings: DevHudSettingsV1["shortcuts"]["desktop"]) => Promise<void> }) {
  const [status, setStatus] = useState<{ platform: NativeShortcutPlatform; permission: NativeShortcutPermission; error: ShortcutValidationCode | null } | null>(null);
  const commit = async (action: ShortcutActionId, update: Partial<ShortcutBinding>) => {
    const previous = bindings;
    const candidate = { ...bindings, [action]: { ...bindings[action], ...update } };
    try {
      const structured = parseDesktopShortcutBindings(candidate);
      const result = await bridge.request({ operation: "shortcuts.apply", bindings: structured });
      if (result.kind !== "shortcut-status" || result.error !== null) { if (result.kind === "shortcut-status") setStatus(result); return; }
      try { await onPersist(structured); setStatus(result); }
      catch { await bridge.request({ operation: "shortcuts.apply", bindings: previous }).catch(() => {}); setStatus({ platform: result.platform, permission: result.permission, error: ShortcutValidationCode.RegistrationFailed }); }
    } catch (error) {
      setStatus({ platform: status?.platform ?? "unsupported", permission: error instanceof NativeBridgeError ? "denied" : status?.permission ?? "unsupported", error: error instanceof NativeBridgeError ? ShortcutValidationCode.PermissionDenied : ShortcutValidationCode.Malformed });
    }
  };
  const requestPermission = () => void bridge.request({ operation: "shortcuts.request-permission" }).then((response) => { if (response.kind === "shortcut-status") setStatus(response); });
  const errorCopy = status?.error === ShortcutValidationCode.Conflict ? copy.shortcutConflict : status?.error === ShortcutValidationCode.Reserved ? copy.shortcutReserved : status?.error === ShortcutValidationCode.PermissionDenied ? copy.shortcutPermissionDenied : status?.error === ShortcutValidationCode.RegistrationFailed ? copy.shortcutRegistrationFailed : status?.error === ShortcutValidationCode.Malformed ? copy.shortcutMalformed : null;
  return <section className="native-setting" aria-label={copy.keyboardShortcuts}>
    <h3>{copy.keyboardShortcuts}</h3>
    {Object.values(ShortcutActionId).map((action) => {
      const binding = bindings[action];
      return <fieldset key={action} disabled={disabled}><legend>{copy[shortcutLabels[action]]}</legend>
        <label className="check"><input type="checkbox" checked={binding.enabled} onChange={(event) => void commit(action, { enabled: event.target.checked })} />{copy.shortcutEnabled}</label>
        <span>{copy.shortcutModifier}</span>{([ShortcutModifier.RightPrimary, ShortcutModifier.Shift, ShortcutModifier.Alt] as const).map((modifier) => <label className="check" key={modifier}><input type="checkbox" checked={binding.modifiers.includes(modifier)} onChange={(event) => void commit(action, { modifiers: event.target.checked ? [...binding.modifiers, modifier] : binding.modifiers.filter((current) => current !== modifier) })} />{modifier === ShortcutModifier.RightPrimary ? copy.shortcutRightPrimary : modifier === ShortcutModifier.Shift ? copy.shortcutShift : copy.shortcutAlt}</label>)}
        <label>{copy.shortcutKey}<select value={binding.key} onChange={(event) => void commit(action, { key: event.target.value as ShortcutKey })}>{Object.values(ShortcutKey).map((key) => <option key={key} value={key}>{key}</option>)}</select></label>
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
