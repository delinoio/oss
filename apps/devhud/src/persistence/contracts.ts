export const SETTINGS_STORAGE_KEY = "devhud.settings.v1" as const;
export const WIDGET_CONFIGURATION_STORAGE_KEY = "devhud.widget-configuration.v1" as const;
export const PERSISTENCE_SCHEMA_VERSION = 1 as const;

export type PersistenceKey =
  | typeof SETTINGS_STORAGE_KEY
  | typeof WIDGET_CONFIGURATION_STORAGE_KEY;

export enum ThemePreference {
  System = "system",
  Light = "light",
  Dark = "dark",
}

export enum ShortcutModifier {
  Control = "control",
  Alt = "alt",
  Shift = "shift",
  Meta = "meta",
}

export enum ShortcutKey {
  A = "a",
  B = "b",
  C = "c",
  D = "d",
  E = "e",
  F = "f",
  G = "g",
  H = "h",
  I = "i",
  J = "j",
  K = "k",
  L = "l",
  M = "m",
  N = "n",
  O = "o",
  P = "p",
  Q = "q",
  R = "r",
  S = "s",
  T = "t",
  U = "u",
  V = "v",
  W = "w",
  X = "x",
  Y = "y",
  Z = "z",
  Digit0 = "0",
  Digit1 = "1",
  Digit2 = "2",
  Digit3 = "3",
  Digit4 = "4",
  Digit5 = "5",
  Digit6 = "6",
  Digit7 = "7",
  Digit8 = "8",
  Digit9 = "9",
  F1 = "f1",
  F2 = "f2",
  F3 = "f3",
  F4 = "f4",
  F5 = "f5",
  F6 = "f6",
  F7 = "f7",
  F8 = "f8",
  F9 = "f9",
  F10 = "f10",
  F11 = "f11",
  F12 = "f12",
  Space = "space",
  Enter = "enter",
}

export interface StructuredShortcut {
  readonly modifiers: readonly ShortcutModifier[];
  readonly key: ShortcutKey;
}

declare const stableToolId: unique symbol;

/** A persisted tool reference is accepted only after the stable-ID validation below. */
export type StableToolId = string & { readonly [stableToolId]: "StableToolId" };

export enum WidgetSlot {
  Primary = "primary",
  Secondary = "secondary",
  Tertiary = "tertiary",
}

export interface WidgetSlotReference {
  readonly slot: WidgetSlot;
  readonly toolId: StableToolId;
}

export interface DevHudSettings {
  readonly theme: ThemePreference;
  readonly launchAtLogin: boolean;
  readonly shortcut: StructuredShortcut | null;
}

export interface WidgetConfiguration {
  readonly slots: readonly WidgetSlotReference[];
}

export interface SettingsRecord {
  readonly version: typeof PERSISTENCE_SCHEMA_VERSION;
  readonly settings: DevHudSettings;
}

export interface WidgetConfigurationRecord {
  readonly version: typeof PERSISTENCE_SCHEMA_VERSION;
  readonly configuration: WidgetConfiguration;
}

export const defaultSettings: DevHudSettings = Object.freeze({
  theme: ThemePreference.System,
  launchAtLogin: false,
  shortcut: null,
});

export const defaultWidgetConfiguration: WidgetConfiguration = Object.freeze({
  slots: Object.freeze([]),
});

const TOOL_ID = /^[a-z]+(?:-[a-z0-9]+)*$/u;
const modifiers = new Set<string>(Object.values(ShortcutModifier));
const shortcutKeys = new Set<string>(Object.values(ShortcutKey));
const themes = new Set<string>(Object.values(ThemePreference));
const widgetSlots = new Set<string>(Object.values(WidgetSlot));

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const valueKeys = Object.keys(value);
  return valueKeys.length === keys.length && keys.every((key) => key in value);
}

function isShortcut(value: unknown): value is StructuredShortcut {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["modifiers", "key"]) ||
    !Array.isArray(value.modifiers) ||
    typeof value.key !== "string"
  ) {
    return false;
  }
  if (value.modifiers.length === 0 || !shortcutKeys.has(value.key)) {
    return false;
  }
  const uniqueModifiers = new Set(value.modifiers);
  return (
    uniqueModifiers.size === value.modifiers.length &&
    value.modifiers.every(
      (modifier): modifier is ShortcutModifier =>
        typeof modifier === "string" && modifiers.has(modifier),
    )
  );
}

export function parseStableToolId(value: unknown): StableToolId | null {
  return typeof value === "string" && TOOL_ID.test(value)
    ? (value as StableToolId)
    : null;
}

function isSettings(value: unknown): value is DevHudSettings {
  return (
    isRecord(value) &&
    hasExactKeys(value, ["theme", "launchAtLogin", "shortcut"]) &&
    typeof value.theme === "string" &&
    themes.has(value.theme) &&
    typeof value.launchAtLogin === "boolean" &&
    (value.shortcut === null || isShortcut(value.shortcut))
  );
}

function isWidgetConfiguration(value: unknown): value is WidgetConfiguration {
  if (!isRecord(value) || !hasExactKeys(value, ["slots"]) || !Array.isArray(value.slots)) {
    return false;
  }

  const seenSlots = new Set<string>();
  return value.slots.every((reference) => {
    if (
      !isRecord(reference) ||
      !hasExactKeys(reference, ["slot", "toolId"]) ||
      typeof reference.slot !== "string"
    ) {
      return false;
    }
    const toolId = parseStableToolId(reference.toolId);
    if (toolId === null || !widgetSlots.has(reference.slot) || seenSlots.has(reference.slot)) {
      return false;
    }
    seenSlots.add(reference.slot);
    return true;
  });
}

export type DecodeFailureKind = "corrupt" | "incompatible" | "future-version";

export interface DecodeFailure {
  readonly kind: DecodeFailureKind;
  readonly guidance: string;
}

export type DecodeResult<T> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly failure: DecodeFailure };

const resetGuidance =
  "Local DevHud data could not be read. Use the separately confirmed Reset DevHud flow to clear it.";
const futureVersionGuidance =
  "This DevHud data was created by a newer version. Update DevHud before changing it.";

function parseRecord(raw: string): DecodeResult<Record<string, unknown>> {
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value)) {
      return { ok: false, failure: { kind: "corrupt", guidance: resetGuidance } };
    }
    if (typeof value.version !== "number" || !Number.isSafeInteger(value.version)) {
      return { ok: false, failure: { kind: "incompatible", guidance: resetGuidance } };
    }
    if (value.version > PERSISTENCE_SCHEMA_VERSION) {
      return { ok: false, failure: { kind: "future-version", guidance: futureVersionGuidance } };
    }
    if (value.version !== PERSISTENCE_SCHEMA_VERSION) {
      return { ok: false, failure: { kind: "incompatible", guidance: resetGuidance } };
    }
    return { ok: true, value };
  } catch {
    return { ok: false, failure: { kind: "corrupt", guidance: resetGuidance } };
  }
}

export function decodeSettings(raw: string): DecodeResult<DevHudSettings> {
  const parsed = parseRecord(raw);
  if (!parsed.ok) return parsed;
  if (!hasExactKeys(parsed.value, ["version", "settings"]) || !isSettings(parsed.value.settings)) {
    return { ok: false, failure: { kind: "incompatible", guidance: resetGuidance } };
  }
  return { ok: true, value: parsed.value.settings };
}

export function decodeWidgetConfiguration(raw: string): DecodeResult<WidgetConfiguration> {
  const parsed = parseRecord(raw);
  if (!parsed.ok) return parsed;
  if (
    !hasExactKeys(parsed.value, ["version", "configuration"]) ||
    !isWidgetConfiguration(parsed.value.configuration)
  ) {
    return { ok: false, failure: { kind: "incompatible", guidance: resetGuidance } };
  }
  return { ok: true, value: parsed.value.configuration };
}

export function encodeSettings(settings: DevHudSettings): string {
  if (!isSettings(settings)) {
    throw new Error("DevHud settings do not satisfy the local persistence contract.");
  }
  const record: SettingsRecord = { version: PERSISTENCE_SCHEMA_VERSION, settings };
  return JSON.stringify(record);
}

export function encodeWidgetConfiguration(configuration: WidgetConfiguration): string {
  if (!isWidgetConfiguration(configuration)) {
    throw new Error("DevHud widget configuration does not satisfy the local persistence contract.");
  }
  const record: WidgetConfigurationRecord = {
    version: PERSISTENCE_SCHEMA_VERSION,
    configuration,
  };
  return JSON.stringify(record);
}
