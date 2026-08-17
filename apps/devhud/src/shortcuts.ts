import { ActionId, PlatformCapability, type RuntimeCapabilities } from "./shell.ts";

export const ShortcutActionId = {
  CommandPalette: "shell.command-palette",
  CaptureDisplay: ActionId.CaptureDisplay,
  CaptureActiveWindow: ActionId.CaptureActiveWindow,
  CaptureAllDisplays: ActionId.CaptureAllDisplays,
  CaptureSelection: ActionId.CaptureSelection,
  CaptureToolbar: ActionId.CaptureToolbar,
} as const;
export type ShortcutActionId = (typeof ShortcutActionId)[keyof typeof ShortcutActionId];

export const ShortcutModifier = { RightPrimary: "right-primary", Shift: "shift", Alt: "alt" } as const;
export type ShortcutModifier = (typeof ShortcutModifier)[keyof typeof ShortcutModifier];
export const ShortcutKey = {
  K: "key-k", Digit1: "digit-1", Digit2: "digit-2", Digit3: "digit-3", Digit4: "digit-4", Digit5: "digit-5",
  Space: "space", Tab: "tab", Q: "key-q", Delete: "delete", Backspace: "backspace",
} as const;
export type ShortcutKey = (typeof ShortcutKey)[keyof typeof ShortcutKey];

export interface ShortcutBinding {
  readonly enabled: boolean;
  readonly modifiers: readonly ShortcutModifier[];
  readonly key: ShortcutKey;
}
export type DesktopShortcutBindings = Readonly<Record<ShortcutActionId, ShortcutBinding>>;

const actions = Object.values(ShortcutActionId);
const keys = Object.values(ShortcutKey);
const modifiers = Object.values(ShortcutModifier);
const bareKeys = new Set<ShortcutKey>([ShortcutKey.Digit1, ShortcutKey.Digit2, ShortcutKey.Digit3, ShortcutKey.Digit4, ShortcutKey.Digit5]);

export const defaultDesktopShortcutBindings: DesktopShortcutBindings = Object.freeze({
  [ShortcutActionId.CommandPalette]: { enabled: true, modifiers: [ShortcutModifier.RightPrimary], key: ShortcutKey.K },
  [ShortcutActionId.CaptureDisplay]: { enabled: true, modifiers: [], key: ShortcutKey.Digit1 },
  [ShortcutActionId.CaptureActiveWindow]: { enabled: true, modifiers: [], key: ShortcutKey.Digit2 },
  [ShortcutActionId.CaptureAllDisplays]: { enabled: true, modifiers: [], key: ShortcutKey.Digit3 },
  [ShortcutActionId.CaptureSelection]: { enabled: true, modifiers: [], key: ShortcutKey.Digit4 },
  [ShortcutActionId.CaptureToolbar]: { enabled: true, modifiers: [], key: ShortcutKey.Digit5 },
});

export class ShortcutContractError extends TypeError {
  readonly code: ShortcutValidationCode;

  constructor(code: ShortcutValidationCode) {
    super(code);
    this.name = "ShortcutContractError";
    this.code = code;
  }
}
export const ShortcutValidationCode = {
  Malformed: "malformed", Conflict: "conflict", Reserved: "reserved", RegistrationFailed: "registration-failed", PermissionDenied: "permission-denied",
} as const;
export type ShortcutValidationCode = (typeof ShortcutValidationCode)[keyof typeof ShortcutValidationCode];

export function parseDesktopShortcutBindings(value: unknown): DesktopShortcutBindings {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  const source = value as Record<string, unknown>;
  // The previous v1 shell persisted an empty map. It has no user-entered chord
  // material, so it safely upgrades to the contracted structured defaults.
  if (Object.keys(source).length === 0) return defaultDesktopShortcutBindings;
  if (Object.keys(source).length !== actions.length || actions.some((action) => !(action in source))) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  const parsed = Object.fromEntries(actions.map((action) => [action, parseBinding(source[action])])) as DesktopShortcutBindings;
  validateBindings(parsed);
  return parsed;
}

export function validateBindings(bindings: DesktopShortcutBindings): void {
  const seen = new Set<string>();
  for (const action of actions) {
    const binding = bindings[action];
    const identifier = chordIdentifier(binding);
    if (!binding.enabled) continue;
    if (isReserved(binding)) throw new ShortcutContractError(ShortcutValidationCode.Reserved);
    if (seen.has(identifier)) throw new ShortcutContractError(ShortcutValidationCode.Conflict);
    seen.add(identifier);
  }
}

export function availableShortcutActions(capabilities: RuntimeCapabilities): readonly ShortcutActionId[] {
  return actions.filter((action) => action === ShortcutActionId.CommandPalette || capabilities.available.has(PlatformCapability.Capture) || !action.startsWith("realqa.capture."));
}

export function chordIdentifier(binding: ShortcutBinding): string {
  return `${[...binding.modifiers].toSorted().join("+")}+${binding.key}`;
}

function parseBinding(value: unknown): ShortcutBinding {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  const binding = value as Record<string, unknown>;
  if (Object.keys(binding).length !== 3 || !("enabled" in binding) || !("modifiers" in binding) || !("key" in binding)) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  if (typeof binding.enabled !== "boolean" || !Array.isArray(binding.modifiers) || typeof binding.key !== "string" || !keys.includes(binding.key as ShortcutKey)) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  if (!binding.modifiers.every((modifier): modifier is ShortcutModifier => typeof modifier === "string" && modifiers.includes(modifier as ShortcutModifier))) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  const parsed = { enabled: binding.enabled, modifiers: [...binding.modifiers] as readonly ShortcutModifier[], key: binding.key as ShortcutKey };
  if (new Set(parsed.modifiers).size !== parsed.modifiers.length || parsed.modifiers.filter((modifier) => modifier === ShortcutModifier.RightPrimary).length > 1) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  if (parsed.enabled && parsed.modifiers.length === 0 && !bareKeys.has(parsed.key)) throw new ShortcutContractError(ShortcutValidationCode.Malformed);
  return parsed;
}

function isReserved(binding: ShortcutBinding): boolean {
  const modifierSet = new Set(binding.modifiers);
  return (modifierSet.has(ShortcutModifier.RightPrimary) && (binding.key === ShortcutKey.Space || binding.key === ShortcutKey.Tab || binding.key === ShortcutKey.Q))
    || (modifierSet.has(ShortcutModifier.RightPrimary) && modifierSet.has(ShortcutModifier.Alt) && (binding.key === ShortcutKey.Delete || binding.key === ShortcutKey.Backspace));
}
