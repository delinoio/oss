import type { CopyKey, SupportedLanguage } from "./localization";

export const MiniAppId = { Realqa: "realqa", Deck: "deck" } as const;
export type MiniAppId = (typeof MiniAppId)[keyof typeof MiniAppId];
export const ActionId = { CaptureDisplay: "realqa.capture.display", CaptureActiveWindow: "realqa.capture.active-window", CaptureAllDisplays: "realqa.capture.all-displays", CaptureSelection: "realqa.capture.selection", CaptureToolbar: "realqa.capture.toolbar", Home: "navigation.home", Realqa: "navigation.realqa", Deck: "navigation.deck", Settings: "navigation.settings", Account: "navigation.account", Diagnostics: "navigation.diagnostics", Theme: "settings.theme", Language: "settings.language", LaunchAtLogin: "settings.launch-at-login" } as const;
export type ActionId = (typeof ActionId)[keyof typeof ActionId];
export const SurfaceId = { Home: "home", Realqa: "realqa", Deck: "deck", Settings: "settings", Account: "account", Diagnostics: "diagnostics" } as const;
export type SurfaceId = (typeof SurfaceId)[keyof typeof SurfaceId];
export const ThemePreference = { System: "system", Light: "light", Dark: "dark" } as const;
export type ThemePreference = (typeof ThemePreference)[keyof typeof ThemePreference];
export const LanguagePreference = { System: "system", English: "en", Korean: "ko" } as const;
export type LanguagePreference = (typeof LanguagePreference)[keyof typeof LanguagePreference];
export const ExternalLinkTarget = { Authentication: "authentication", Pat: "pat", Issue: "issue" } as const;
export type ExternalLinkTarget = (typeof ExternalLinkTarget)[keyof typeof ExternalLinkTarget];
export const PlatformCapability = { Desktop: "desktop", Capture: "capture", Tray: "tray", LaunchAtLogin: "launch-at-login" } as const;
export type PlatformCapability = (typeof PlatformCapability)[keyof typeof PlatformCapability];

export interface RuntimeCapabilities { readonly available: ReadonlySet<PlatformCapability>; }
export const desktopCapabilities: RuntimeCapabilities = { available: new Set([PlatformCapability.Desktop, PlatformCapability.Tray]) };
export interface RegisteredAction { id: ActionId; title: CopyKey; required: readonly PlatformCapability[]; surface?: SurfaceId; }
export const actionRegistry: readonly RegisteredAction[] = [
  { id: ActionId.CaptureDisplay, title: "captureDisplay", required: [PlatformCapability.Capture], surface: SurfaceId.Realqa },
  { id: ActionId.CaptureActiveWindow, title: "captureWindow", required: [PlatformCapability.Capture], surface: SurfaceId.Realqa },
  { id: ActionId.CaptureAllDisplays, title: "captureAll", required: [PlatformCapability.Capture], surface: SurfaceId.Realqa },
  { id: ActionId.CaptureSelection, title: "captureSelection", required: [PlatformCapability.Capture], surface: SurfaceId.Realqa },
  { id: ActionId.CaptureToolbar, title: "captureToolbar", required: [PlatformCapability.Capture], surface: SurfaceId.Realqa },
  { id: ActionId.Home, title: "navHome", required: [], surface: SurfaceId.Home }, { id: ActionId.Realqa, title: "navRealqa", required: [], surface: SurfaceId.Realqa },
  { id: ActionId.Deck, title: "navDeck", required: [], surface: SurfaceId.Deck }, { id: ActionId.Settings, title: "navSettings", required: [], surface: SurfaceId.Settings },
  { id: ActionId.Account, title: "navAccount", required: [], surface: SurfaceId.Account }, { id: ActionId.Diagnostics, title: "navDiagnostics", required: [], surface: SurfaceId.Diagnostics },
  { id: ActionId.Theme, title: "settingTheme", required: [], surface: SurfaceId.Settings }, { id: ActionId.Language, title: "settingLanguage", required: [], surface: SurfaceId.Settings },
  { id: ActionId.LaunchAtLogin, title: "settingLaunch", required: [PlatformCapability.LaunchAtLogin], surface: SurfaceId.Settings },
];
export const miniAppRegistry = [{ id: MiniAppId.Realqa, surface: SurfaceId.Realqa, required: [PlatformCapability.Desktop] }, { id: MiniAppId.Deck, surface: SurfaceId.Deck, required: [] }] as const;
export function availableActions(capabilities: RuntimeCapabilities) { return actionRegistry.filter(({ required }) => required.every((item) => capabilities.available.has(item))); }

const preferenceKey = "devhud.shell.preferences.v1";
const onboardingKey = "devhud.shell.onboarding.v1";
export interface Preferences { version: 1; theme: ThemePreference; language: LanguagePreference; apiOrigin: string; launchAtLogin: boolean; }
export const defaultPreferences: Preferences = { version: 1, theme: ThemePreference.System, language: LanguagePreference.System, apiOrigin: "https://devhud.api.delino.io", launchAtLogin: false };
type LocalStorage = Pick<Storage, "getItem" | "setItem">;
const sessionStorage = new Map<string, string>();
const inMemoryStorage: LocalStorage = {
  getItem: (key) => sessionStorage.get(key) ?? null,
  setItem: (key, value) => { sessionStorage.set(key, value); },
};
export function getLocalStorage(): LocalStorage {
  try {
    return window.localStorage;
  } catch {
    return inMemoryStorage;
  }
}
function isEnumValue<T extends Record<string, string>>(values: T, value: unknown): value is T[keyof T] { return typeof value === "string" && Object.values(values).includes(value); }
export function isValidApiOrigin(value: unknown): value is string { if (typeof value !== "string" || value !== value.trim()) return false; try { const url = new URL(value); const octets = url.hostname.split("."); const loopback = url.hostname === "localhost" || url.hostname === "[::1]" || url.hostname === "::1" || (octets.length === 4 && octets[0] === "127" && octets.every((octet) => /^\d+$/u.test(octet) && Number(octet) <= 255)); return !url.username && !url.password && !url.search && !url.hash && url.pathname === "/" && (url.protocol === "https:" || (url.protocol === "http:" && loopback)); } catch { return false; } }
export function readPreferences(storage: Pick<Storage, "getItem">): Preferences { try { const stored: unknown = JSON.parse(storage.getItem(preferenceKey) ?? "null"); if (!stored || typeof stored !== "object" || Array.isArray(stored)) return defaultPreferences; const value = stored as Record<string, unknown>; if (value.version !== 1) return defaultPreferences; return { version: 1, theme: isEnumValue(ThemePreference, value.theme) ? value.theme : defaultPreferences.theme, language: isEnumValue(LanguagePreference, value.language) ? value.language : defaultPreferences.language, apiOrigin: isValidApiOrigin(value.apiOrigin) ? value.apiOrigin : defaultPreferences.apiOrigin, launchAtLogin: typeof value.launchAtLogin === "boolean" ? value.launchAtLogin : defaultPreferences.launchAtLogin }; } catch { return defaultPreferences; } }
export function writePreferences(storage: Pick<Storage, "setItem">, preferences: Preferences) { try { storage.setItem(preferenceKey, JSON.stringify(preferences)); } catch {} }
export function hasCompletedOnboarding(storage: Pick<Storage, "getItem">) { try { return storage.getItem(onboardingKey) === "complete"; } catch { return false; } }
export function completeOnboarding(storage: Pick<Storage, "setItem">) { try { storage.setItem(onboardingKey, "complete"); } catch {} }
export function resolveLanguage(preference: LanguagePreference, languages: readonly string[]): SupportedLanguage { if (preference !== LanguagePreference.System) return preference; for (const locale of languages) { const language = locale.trim().toLowerCase().split(/[-_]/u, 1)[0]; if (language === "en" || language === "ko") return language; } return "en"; }
export function resolveTheme(preference: ThemePreference, dark: boolean) { return preference === ThemePreference.System ? (dark ? ThemePreference.Dark : ThemePreference.Light) : preference; }
export function synchronizeDocumentPreferences(documentElement: Pick<HTMLElement, "lang" | "dataset">, preferences: Preferences, systemDark: boolean, languages: readonly string[]) {
  const language = resolveLanguage(preferences.language, languages);
  const theme = resolveTheme(preferences.theme, systemDark);
  documentElement.lang = language;
  documentElement.dataset.theme = theme;
  return { language, theme };
}

export interface NativeShell { setLaunchAtLogin(enabled: boolean): Promise<void>; openExternal(target: ExternalLinkTarget, apiOrigin: string): Promise<void>; quit(): Promise<void>; }
interface TauriInternals { invoke(command: string, args?: Record<string, unknown>): Promise<unknown>; }
declare global { interface Window { __TAURI_INTERNALS__?: TauriInternals; } }
function invoke(command: string, args?: Record<string, unknown>) { return window.__TAURI_INTERNALS__?.invoke(command, args); }
export function setTrayLanguage(language: SupportedLanguage) { return invoke("set_tray_language", { language }) ?? Promise.resolve(); }
export function markFrontendReady() { return invoke("frontend_ready"); }
export const browserShell: NativeShell = { async setLaunchAtLogin() {}, async openExternal(target, apiOrigin) { if (target === ExternalLinkTarget.Authentication && !isValidApiOrigin(apiOrigin)) throw new Error("invalid API origin"); const native = invoke("open_external", { target, apiOrigin }); if (native) { await native; return; } const path = target === ExternalLinkTarget.Authentication ? apiOrigin : target === ExternalLinkTarget.Pat ? "https://github.com/settings/personal-access-tokens/new" : "https://github.com/delinoio/oss/issues/new"; window.open(path, "_blank", "noopener,noreferrer"); }, async quit() {} };
