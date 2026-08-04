export const SETTINGS_STORAGE_KEY = "devhud.settings.v1" as const;
/** Device-only registrations. Feature definitions deliberately never live here. */
export const SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY = "devhud.shortcut-effective-state.v2" as const;
export const WIDGET_CONFIGURATION_STORAGE_KEY = "devhud.widget-configuration.v1" as const;
export const PERSISTENCE_SCHEMA_VERSION = 1 as const;

export type PersistenceKey =
  | typeof SETTINGS_STORAGE_KEY
  | typeof SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY
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

export const MAX_DECK_SHORTCUT_DEFINITIONS = 20;
export const MAX_REALQA_SHORTCUT_DEFINITIONS = 20;

declare const opaqueShortcutDefinitionId: unique symbol;
/** Opaque server/client definition IDs are never included in diagnostics. */
export type ShortcutDefinitionId = string & {
  readonly [opaqueShortcutDefinitionId]: "ShortcutDefinitionId";
};

export type ShortcutOwner =
  | { readonly feature: "devhud" }
  | { readonly feature: "deck"; readonly accountId: string; readonly definitionId: ShortcutDefinitionId }
  | { readonly feature: "realqa"; readonly definitionId: ShortcutDefinitionId };

export interface ShortcutDefinition {
  readonly owner: ShortcutOwner;
  readonly shortcut: StructuredShortcut;
}

/**
 * This is local effective state only. Deck definitions synchronize through its
 * feature API and RealQA definitions remain device scoped; neither is copied
 * into the base shell's persistence boundary.
 */
export interface ShortcutEffectiveState {
  readonly version: 2;
  readonly genericShortcut: StructuredShortcut | null;
  readonly inactive: readonly ShortcutOwner[];
}

export enum DeckWidgetFamily {
  AppleSmall = "apple-small",
  AppleMedium = "apple-medium",
  AppleLarge = "apple-large",
  AndroidCompact = "android-compact",
  AndroidWide = "android-wide",
  AndroidList = "android-list",
}

export enum DeckWidgetPrivacy {
  CountsOnly = "counts-only",
  RepositoryAndTitles = "repository-and-titles",
}

export enum DeckWidgetFreshness {
  Fresh = "fresh",
  Stale = "stale",
  Offline = "offline",
  Disconnected = "disconnected",
  NeverRefreshed = "never-refreshed",
}

export interface DeckWidgetPullRequest {
  readonly repositoryOwner: string;
  readonly repositoryName: string;
  readonly number: number;
  readonly title: string;
}

/** The only Deck PR data allowed to persist offline. */
export interface DeckWidgetSnapshot {
  readonly matchingCount: number;
  readonly pullRequests: readonly DeckWidgetPullRequest[];
  readonly freshness: DeckWidgetFreshness;
  readonly offline: boolean;
  readonly generatedAt: string;
}

export interface DeckWidgetInstance {
  readonly widgetId: string;
  readonly viewId: string;
  readonly family: DeckWidgetFamily;
  readonly privacy: DeckWidgetPrivacy;
  readonly snapshot: DeckWidgetSnapshot;
}

export interface DevHudSettings {
  readonly theme: ThemePreference;
  readonly launchAtLogin: boolean;
  readonly shortcut: StructuredShortcut | null;
}

export interface WidgetConfiguration {
  /** Opaque account binding; native storage additionally binds encryption to it. */
  readonly accountId: string;
  readonly widgets: readonly DeckWidgetInstance[];
}

export interface SettingsRecord {
  readonly version: typeof PERSISTENCE_SCHEMA_VERSION;
  readonly settings: DevHudSettings;
}

export interface WidgetConfigurationRecord {
  readonly version: typeof PERSISTENCE_SCHEMA_VERSION;
  readonly configuration: WidgetConfiguration;
}

export interface ShortcutEffectiveStateRecord {
  readonly version: 2;
  readonly state: ShortcutEffectiveState;
}

export const defaultSettings: DevHudSettings = Object.freeze({
  theme: ThemePreference.System,
  launchAtLogin: false,
  shortcut: null,
});

export const defaultWidgetConfiguration: WidgetConfiguration = Object.freeze({
  accountId: "",
  widgets: Object.freeze([]),
});

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const modifiers = new Set<string>(Object.values(ShortcutModifier));
const shortcutKeys = new Set<string>(Object.values(ShortcutKey));
const themes = new Set<string>(Object.values(ThemePreference));
const widgetFamilies = new Set<string>(Object.values(DeckWidgetFamily));
const widgetPrivacyValues = new Set<string>(Object.values(DeckWidgetPrivacy));
const widgetFreshnessValues = new Set<string>(Object.values(DeckWidgetFreshness));

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const valueKeys = Object.keys(value);
  return valueKeys.length === keys.length && keys.every((key) => key in value);
}

export function isStructuredShortcut(
  value: unknown,
): value is StructuredShortcut {
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

function isShortcutOwner(value: unknown): value is ShortcutOwner {
  if (!isRecord(value) || typeof value.feature !== "string") return false;
  if (value.feature === "devhud") return hasExactKeys(value, ["feature"]);
  if (value.feature === "deck") {
    return hasExactKeys(value, ["feature", "accountId", "definitionId"])
      && typeof value.accountId === "string" && value.accountId.length > 0
      && typeof value.definitionId === "string" && value.definitionId.length > 0;
  }
  return value.feature === "realqa"
    && hasExactKeys(value, ["feature", "definitionId"])
    && typeof value.definitionId === "string" && value.definitionId.length > 0;
}

function isShortcutEffectiveState(value: unknown): value is ShortcutEffectiveState {
  return isRecord(value)
    && hasExactKeys(value, ["version", "genericShortcut", "inactive"])
    && value.version === 2
    && (value.genericShortcut === null || isStructuredShortcut(value.genericShortcut))
    && Array.isArray(value.inactive)
    && value.inactive.every(isShortcutOwner);
}

function isSettings(value: unknown): value is DevHudSettings {
  return (
    isRecord(value) &&
    hasExactKeys(value, ["theme", "launchAtLogin", "shortcut"]) &&
    typeof value.theme === "string" &&
    themes.has(value.theme) &&
    typeof value.launchAtLogin === "boolean" &&
    (value.shortcut === null || isStructuredShortcut(value.shortcut))
  );
}

function isWidgetConfiguration(value: unknown): value is WidgetConfiguration {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["accountId", "widgets"]) ||
    typeof value.accountId !== "string" ||
    !Array.isArray(value.widgets) ||
    value.widgets.length > 20
  ) {
    return false;
  }
  if (value.widgets.length > 0 && !UUID.test(value.accountId)) return false;

  const seenWidgetIds = new Set<string>();
  return value.widgets.every((widget) => {
    if (
      !isRecord(widget) ||
      !hasExactKeys(widget, ["widgetId", "viewId", "family", "privacy", "snapshot"]) ||
      typeof widget.widgetId !== "string" ||
      typeof widget.viewId !== "string" ||
      typeof widget.family !== "string" ||
      typeof widget.privacy !== "string" ||
      !UUID.test(widget.widgetId) ||
      !UUID.test(widget.viewId) ||
      !widgetFamilies.has(widget.family) ||
      !widgetPrivacyValues.has(widget.privacy) ||
      seenWidgetIds.has(widget.widgetId) ||
      !isRecord(widget.snapshot) ||
      !hasExactKeys(widget.snapshot, [
        "matchingCount",
        "pullRequests",
        "freshness",
        "offline",
        "generatedAt",
      ]) ||
      typeof widget.snapshot.matchingCount !== "number" ||
      !Number.isSafeInteger(widget.snapshot.matchingCount) ||
      widget.snapshot.matchingCount < 0 ||
      !Array.isArray(widget.snapshot.pullRequests) ||
      widget.snapshot.pullRequests.length > 10 ||
      typeof widget.snapshot.freshness !== "string" ||
      !widgetFreshnessValues.has(widget.snapshot.freshness) ||
      typeof widget.snapshot.offline !== "boolean" ||
      typeof widget.snapshot.generatedAt !== "string" ||
      Number.isNaN(Date.parse(widget.snapshot.generatedAt))
    ) {
      return false;
    }
    seenWidgetIds.add(widget.widgetId);
    if (
      widget.privacy === DeckWidgetPrivacy.CountsOnly &&
      widget.snapshot.pullRequests.length !== 0
    ) return false;
    return widget.snapshot.pullRequests.every((pullRequest) =>
      isRecord(pullRequest) &&
      hasExactKeys(pullRequest, ["repositoryOwner", "repositoryName", "number", "title"]) &&
      typeof pullRequest.repositoryOwner === "string" &&
      pullRequest.repositoryOwner.length > 0 &&
      typeof pullRequest.repositoryName === "string" &&
      pullRequest.repositoryName.length > 0 &&
      typeof pullRequest.number === "number" &&
      Number.isSafeInteger(pullRequest.number) &&
      pullRequest.number > 0 &&
      typeof pullRequest.title === "string" &&
      pullRequest.title.length > 0
    );
  });
}

function isLegacyWidgetConfiguration(value: unknown): boolean {
  if (!isRecord(value) || !hasExactKeys(value, ["slots"]) || !Array.isArray(value.slots)) {
    return false;
  }
  const legacySlots = new Set(["primary", "secondary", "tertiary"]);
  const legacyToolId = /^[a-z]+(?:-[a-z0-9]+)*$/u;
  const seenSlots = new Set<string>();
  return value.slots.every((reference) => {
    if (
      !isRecord(reference) ||
      !hasExactKeys(reference, ["slot", "toolId"]) ||
      typeof reference.slot !== "string" ||
      !legacySlots.has(reference.slot) ||
      seenSlots.has(reference.slot) ||
      typeof reference.toolId !== "string" ||
      !legacyToolId.test(reference.toolId)
    ) {
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

export function decodeFailureFromKind(kind: DecodeFailureKind): DecodeFailure {
  return {
    kind,
    guidance: kind === "future-version" ? futureVersionGuidance : resetGuidance,
  };
}

function parseRecord(raw: string): DecodeResult<Record<string, unknown>> {
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value)) {
      return { ok: false, failure: decodeFailureFromKind("corrupt") };
    }
    if (typeof value.version !== "number" || !Number.isSafeInteger(value.version)) {
      return { ok: false, failure: decodeFailureFromKind("incompatible") };
    }
    if (value.version > PERSISTENCE_SCHEMA_VERSION) {
      return { ok: false, failure: decodeFailureFromKind("future-version") };
    }
    if (value.version !== PERSISTENCE_SCHEMA_VERSION) {
      return { ok: false, failure: decodeFailureFromKind("incompatible") };
    }
    return { ok: true, value };
  } catch {
    return { ok: false, failure: decodeFailureFromKind("corrupt") };
  }
}

export function decodeSettings(raw: string): DecodeResult<DevHudSettings> {
  const parsed = parseRecord(raw);
  if (!parsed.ok) return parsed;
  if (!hasExactKeys(parsed.value, ["version", "settings"]) || !isSettings(parsed.value.settings)) {
    return { ok: false, failure: decodeFailureFromKind("incompatible") };
  }
  return { ok: true, value: parsed.value.settings };
}

export function decodeWidgetConfiguration(raw: string): DecodeResult<WidgetConfiguration> {
  const parsed = parseRecord(raw);
  if (!parsed.ok) return parsed;
  if (!hasExactKeys(parsed.value, ["version", "configuration"])) {
    return { ok: false, failure: decodeFailureFromKind("incompatible") };
  }
  if (isLegacyWidgetConfiguration(parsed.value.configuration)) {
    return { ok: true, value: defaultWidgetConfiguration };
  }
  if (!isWidgetConfiguration(parsed.value.configuration)) {
    return { ok: false, failure: decodeFailureFromKind("incompatible") };
  }
  return { ok: true, value: parsed.value.configuration };
}

export function decodeShortcutEffectiveState(raw: string): DecodeResult<ShortcutEffectiveState> {
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value) || !hasExactKeys(value, ["version", "state"])) {
      return { ok: false, failure: decodeFailureFromKind("incompatible") };
    }
    if (typeof value.version !== "number") {
      return { ok: false, failure: decodeFailureFromKind("incompatible") };
    }
    if (value.version > 2) return { ok: false, failure: decodeFailureFromKind("future-version") };
    if (value.version !== 2 || !isShortcutEffectiveState(value.state)) {
      return { ok: false, failure: decodeFailureFromKind("incompatible") };
    }
    return { ok: true, value: value.state };
  } catch {
    return { ok: false, failure: decodeFailureFromKind("corrupt") };
  }
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

export function encodeShortcutEffectiveState(state: ShortcutEffectiveState): string {
  if (!isShortcutEffectiveState(state)) {
    throw new Error("DevHud shortcut effective state does not satisfy the local persistence contract.");
  }
  const record: ShortcutEffectiveStateRecord = { version: 2, state };
  return JSON.stringify(record);
}
