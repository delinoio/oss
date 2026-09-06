import { useEffect, useState, type Ref } from "react";

import type { Copy } from "./localization";
import { NativeBridgeError, nativeBridge, type NativeBridgeV1, type NativeShortcutPermission, type NativeShortcutPlatform } from "./native-bridge";
import { useIdentitySettings } from "./service-boundary";
import type { RuntimeCapabilities } from "./shell";
import {
  inactiveDesktopShortcutBindings,
  ShortcutActionId,
  ShortcutContractError,
  ShortcutKey,
  ShortcutModifier,
  ShortcutValidationCode,
  availableShortcutActions,
  parseDesktopShortcutBindings,
  type ShortcutBinding,
} from "./shortcuts";
import { SearchIcon } from "./ui-icons";

function nativeShortcutsAreActive(status: { readonly error: ShortcutValidationCode | null; readonly permission: NativeShortcutPermission }) {
  return status.error === null && status.permission === "available";
}

export function SynchronizedShortcutBoundary({ bridge = nativeBridge }: { readonly bridge?: NativeBridgeV1 }) {
  const identity = useIdentitySettings();
  const bindings = identity.settings.shortcuts.desktop;
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | undefined;
    void bridge.listen((event) => {
      if (!active || event.version !== 1 || event.kind !== "shortcut-status") return;
      identity.setActiveShortcutBindings(nativeShortcutsAreActive(event) && identity.shortcutHydrationReady ? event.bindings : inactiveDesktopShortcutBindings);
    }).then((value) => {
      if (!active) { value(); return; }
      unlisten = value;
    }).catch(() => {});
    return () => { active = false; unlisten?.(); };
  }, [bridge, identity.setActiveShortcutBindings, identity.shortcutHydrationReady]);
  useEffect(() => {
    let active = true;
    if (!identity.shortcutHydrationReady) {
      void bridge.request({ operation: "shortcuts.suspend" }).then((response) => {
        if (active && response.kind === "shortcut-status") identity.setActiveShortcutBindings(inactiveDesktopShortcutBindings);
      }).catch(() => {});
      return () => { active = false; };
    }
    void bridge.request({ operation: "shortcuts.apply", bindings }).then(async (response) => {
      if (!active || response.kind !== "shortcut-status") return;
      if (nativeShortcutsAreActive(response)) {
        identity.setActiveShortcutBindings(response.bindings);
        return;
      }
      if (response.error !== ShortcutValidationCode.Reserved) {
        identity.setActiveShortcutBindings(inactiveDesktopShortcutBindings);
        return;
      }
      identity.setActiveShortcutBindings(response.bindings);
      const fallback = await bridge.request({ operation: "shortcuts.apply", bindings: response.bindings });
      if (active && fallback.kind === "shortcut-status" && nativeShortcutsAreActive(fallback)) identity.setActiveShortcutBindings(fallback.bindings);
    }).catch(() => {});
    return () => { active = false; };
  }, [bindings, bridge, identity.setActiveShortcutBindings, identity.shortcutHydrationReady]);
  return null;
}

const shortcutKeyLabels: Record<ShortcutKey, keyof Copy> = {
  [ShortcutKey.K]: "shortcutKeyK", [ShortcutKey.Digit1]: "shortcutDigit1", [ShortcutKey.Digit2]: "shortcutDigit2", [ShortcutKey.Digit3]: "shortcutDigit3", [ShortcutKey.Digit4]: "shortcutDigit4", [ShortcutKey.Digit5]: "shortcutDigit5", [ShortcutKey.Space]: "shortcutSpace", [ShortcutKey.Tab]: "shortcutTab", [ShortcutKey.Q]: "shortcutKeyQ", [ShortcutKey.Delete]: "shortcutDelete", [ShortcutKey.Backspace]: "shortcutBackspace",
};

const shortcutLabels: Record<ShortcutActionId, keyof Copy> = {
  [ShortcutActionId.CommandPalette]: "openPalette",
  [ShortcutActionId.CaptureDisplay]: "captureDisplay",
  [ShortcutActionId.CaptureActiveWindow]: "captureWindow",
  [ShortcutActionId.CaptureAllDisplays]: "captureAll",
  [ShortcutActionId.CaptureSelection]: "captureSelection",
  [ShortcutActionId.CaptureToolbar]: "captureToolbar",
};

export function ShortcutPaletteTrigger({ copy, isMac, onOpen, triggerRef, compact = false }: { readonly copy: Copy; readonly isMac: boolean; readonly onOpen: () => void; readonly triggerRef: Ref<HTMLButtonElement>; readonly compact?: boolean }) {
  const { activeShortcutBindings } = useIdentitySettings();
  const binding = activeShortcutBindings[ShortcutActionId.CommandPalette];
  const modifiers = binding.modifiers.map((modifier) => modifier === ShortcutModifier.RightPrimary ? isMac ? copy.rightCommandK.replace(/ K$/u, "") : copy.rightControlK.replace(/ K$/u, "") : modifier === ShortcutModifier.Shift ? copy.shortcutShift : copy.shortcutAlt);
  const label = binding.enabled ? [...modifiers, copy[shortcutKeyLabels[binding.key]]].join(" + ") : copy.shortcutNone;
  return <button ref={triggerRef} className="palette-trigger" onClick={onOpen} aria-label={copy.openPalette} aria-describedby={compact ? "palette-trigger-tooltip" : undefined}>{compact ? <><SearchIcon /><span id="palette-trigger-tooltip" className="nav-tooltip" role="tooltip">{copy.openPalette}</span></> : label}</button>;
}

export function ShortcutSettings({ copy, bridge, capabilities }: { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly capabilities: RuntimeCapabilities }) {
  const identity = useIdentitySettings();
  const bindings = identity.settings.shortcuts.desktop;
  const disabled = !identity.shortcutHydrationReady;
  const [status, setStatus] = useState<{ platform: NativeShortcutPlatform; permission: NativeShortcutPermission; error: ShortcutValidationCode | null } | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    let active = true;
    void bridge.request({ operation: "shortcuts.status" }).then((response) => {
      if (active && response.kind === "shortcut-status") setStatus(response);
    }).catch(() => {});
    return () => { active = false; };
  }, [bindings, bridge]);
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | undefined;
    void bridge.listen((event) => {
      if (active && event.version === 1 && event.kind === "shortcut-status") setStatus(event);
    }).then((value) => {
      if (!active) { value(); return; }
      unlisten = value;
    }).catch(() => {});
    return () => { active = false; unlisten?.(); };
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
        if (await identity.replaceSettings((current) => ({ ...current, shortcuts: { ...current.shortcuts, desktop: structured } }))) {
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
      if (nativeShortcutsAreActive(result)) { identity.setActiveShortcutBindings(result.bindings); return; }
      if (result.error !== ShortcutValidationCode.Reserved) { identity.setActiveShortcutBindings(inactiveDesktopShortcutBindings); return; }
      identity.setActiveShortcutBindings(result.bindings);
      const fallback = await bridge.request({ operation: "shortcuts.apply", bindings: result.bindings });
      if (fallback.kind === "shortcut-status") {
        setStatus(fallback);
        if (nativeShortcutsAreActive(fallback)) identity.setActiveShortcutBindings(fallback.bindings);
      }
    } catch (error) {
      setStatus((current) => ({ platform: current?.platform ?? "unsupported", permission: error instanceof NativeBridgeError ? "denied" : current?.permission ?? "unsupported", error: error instanceof NativeBridgeError ? ShortcutValidationCode.PermissionDenied : ShortcutValidationCode.RegistrationFailed }));
    }
  };
  const errorCopy = status?.error === ShortcutValidationCode.Conflict ? copy.shortcutConflict : status?.error === ShortcutValidationCode.Reserved ? copy.shortcutReserved : status?.error === ShortcutValidationCode.PermissionDenied ? copy.shortcutPermissionDenied : status?.error === ShortcutValidationCode.RegistrationFailed ? copy.shortcutRegistrationFailed : status?.error === ShortcutValidationCode.Malformed ? copy.shortcutMalformed : null;
  const availableActions = availableShortcutActions(capabilities);
  return <div className="native-setting" aria-label={copy.keyboardShortcuts}>
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
  </div>;
}
