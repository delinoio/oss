// SPDX-License-Identifier: Apache-2.0
import { applyAppearanceColors, appearanceIdentity, defaultPreferences, parsePreferences, selectedColors, type AppearancePreferences } from "./appearance-preferences";
import { AppearanceControls } from "./appearance-controls";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { LocalizedText, copy, useLocale } from "./localization";
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export enum Theme { System = "system", Light = "light", Dark = "dark" }
export enum AppearanceProblem {
  Unavailable = "unavailable", ReadFailed = "read-failed", InvalidDocument = "invalid-document", UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed", OutcomeUnknown = "outcome-unknown", Changed = "changed",
}
enum AppearanceOperation { Reading = "reading", Saving = "saving" }
export interface AppearanceSnapshot { revision: number; theme: Theme; problem: AppearanceProblem | null; preferences?: AppearancePreferences }
export interface AppearanceBridge {
  read: () => Promise<unknown>;
  update: (theme: Theme, revision: number, preferences?: AppearancePreferences) => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}
const defaultSnapshot: AppearanceSnapshot = { revision: 0, theme: Theme.System, problem: AppearanceProblem.Unavailable };
const nativeBridge: AppearanceBridge = {
  read: () => isTauri() ? invoke("read_appearance") : Promise.resolve(defaultSnapshot),
  update: (theme, revision, preferences = defaultPreferences()) => invoke("update_appearance", { theme, preferences, expectedRevision: revision }),
  subscribe: async (changed) => isTauri() ? listen<unknown>("appearance-changed", (event) => changed(event.payload)) : () => {},
};
const problemMessages: Record<AppearanceProblem, string> = {
  get [AppearanceProblem.Unavailable]() { return copy("appearance.appearanceStorageIsUnavailableOpenThe_3e97e6"); },
  get [AppearanceProblem.ReadFailed]() { return copy("appearance.theSavedAppearanceCouldNotBe_845d8f"); },
  get [AppearanceProblem.InvalidDocument]() { return copy("appearance.theSavedAppearanceIsInvalidSystem_84b3f1"); },
  get [AppearanceProblem.UnsupportedVersion]() { return copy("appearance.theSavedAppearanceUsesAnUnsupported_ddd79c"); },
  get [AppearanceProblem.WriteFailed]() { return copy("appearance.theThemeCouldNotBeSaved_fb5c7d"); },
  get [AppearanceProblem.OutcomeUnknown]() { return copy("appearance.theSaveOutcomeIsUnknownReload_ef1864"); },
  get [AppearanceProblem.Changed]() { return copy("appearance.appearanceChangedElsewhereReloadAppearanceBefore_c210e8"); },
};

export function parseAppearance(value: unknown): AppearanceSnapshot {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid appearance snapshot");
  const data = value as Record<string, unknown>;
  if (!["problem,revision,theme", "preferences,problem,revision,theme"].includes(Object.keys(data).sort().join(",")) || !Number.isInteger(data.revision) || Number(data.revision) < 0 || Number(data.revision) > 0xffffffff
    || !Object.values(Theme).includes(data.theme as Theme) || (data.problem !== null && !Object.values(AppearanceProblem).includes(data.problem as AppearanceProblem))) throw new Error("Invalid appearance snapshot");
  if (data.preferences !== undefined) parsePreferences(data.preferences);
  return data as unknown as AppearanceSnapshot;
}

interface AppearanceController {
  snapshot: AppearanceSnapshot;
  operation?: AppearanceOperation;
  select: (theme: Theme) => void;
  reload: () => void;
  update: (preferences: AppearancePreferences, revision: number) => void;
}
const AppearanceContext = createContext<AppearanceController>({ snapshot: defaultSnapshot, select: () => {}, reload: () => {}, update: () => {} });

export function AppearanceProvider({ children, bridge = nativeBridge }: { children: ReactNode; bridge?: AppearanceBridge }) {
  useLocale();
  const [snapshot, setSnapshot] = useState(defaultSnapshot);
  const current = useRef(defaultSnapshot);
  const [operation, setOperation] = useState<AppearanceOperation | undefined>(AppearanceOperation.Reading);
  const running = useRef(false);
  const generation = useRef(0);
  const subscription = useRef<(() => void) | undefined>(undefined);
  const [systemDark, setSystemDark] = useState(() => window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false);
  const accept = (value: unknown) => {
    const next = parseAppearance(value);
    if (next.revision < current.current.revision) return;
    if (next.revision === current.current.revision && (next.theme !== current.current.theme || appearanceIdentity(next.preferences) !== appearanceIdentity(current.current.preferences))) throw new Error("Contradictory appearance revision");
    current.current = next;
    setSnapshot(next);
  };
  const failed = (problem: AppearanceProblem) => {
    const next = { ...current.current, problem };
    current.current = next;
    setSnapshot(next);
  };
  useEffect(() => {
    const media = window.matchMedia?.("(prefers-color-scheme: dark)");
    if (!media) return;
    const changed = () => setSystemDark(media.matches);
    changed();
    media.addEventListener("change", changed);
    return () => media.removeEventListener("change", changed);
  }, []);
  const preferences = snapshot.preferences ?? defaultPreferences();
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = snapshot.theme === Theme.System ? (systemDark ? Theme.Dark : Theme.Light) : snapshot.theme;
    const root = document.documentElement;
    root.dataset.composerLayout=preferences.composer_layout; root.dataset.composerSize=String(preferences.composer_size); root.dataset.conversationSize=String(preferences.conversation_size); root.dataset.density=preferences.density;
    root.dataset.animation=String(preferences.animation); root.dataset.colorAssistance=String(preferences.color_assistance); root.dataset.sessionAccent=String(preferences.session_accent); root.dataset.statusPresentation=preferences.status; root.dataset.showTokens=String(preferences.show_tokens); root.dataset.showTime=String(preferences.show_time); root.dataset.imageSize=preferences.image_size;
    // A constructed stylesheet changes only validated application color tokens.
    // It does not inject style text or relax the production CSP.
    return applyAppearanceColors(selectedColors(preferences, root.dataset.theme===Theme.Dark));
  }, [snapshot.theme, snapshot.preferences, systemDark]);
  const subscribe = async (owned: number) => {
    const remove = await bridge.subscribe((value) => {
      if (generation.current !== owned) return;
      try { accept(value); } catch { failed(AppearanceProblem.OutcomeUnknown); }
    });
    if (generation.current !== owned) { remove(); return false; }
    subscription.current = remove;
    return true;
  };
  useEffect(() => {
    const owned = ++generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(AppearanceOperation.Reading);
    void (async () => {
      try {
        // Listen before reading so no committed change is lost in the gap.
        if (!await subscribe(owned)) return;
        const value = await bridge.read();
        if (generation.current === owned) accept(value);
      } catch { if (generation.current === owned && current.current.revision <= before) failed(AppearanceProblem.ReadFailed); }
      finally { if (generation.current === owned) { running.current = false; setOperation(undefined); } }
    })();
    return () => { generation.current++; subscription.current?.(); subscription.current = undefined; };
  }, [bridge]);
  const run = async (theme?: Theme, preferences?: AppearancePreferences, revision?: number) => {
    if (running.current || (theme !== undefined && (current.current.problem || revision !== undefined && revision !== current.current.revision))) return;
    const owned = generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(theme === undefined ? AppearanceOperation.Reading : AppearanceOperation.Saving);
    try {
      if (!subscription.current && !await subscribe(owned)) return;
      const value = theme === undefined ? await bridge.read() : await bridge.update(theme, current.current.revision, preferences ?? current.current.preferences ?? defaultPreferences());
      if (generation.current === owned) accept(value);
    } catch {
      if (generation.current === owned && current.current.revision <= before) failed(theme === undefined ? AppearanceProblem.ReadFailed : AppearanceProblem.OutcomeUnknown);
    } finally {
      if (generation.current === owned) { running.current = false; setOperation(undefined); }
    }
  };
  useEffect(() => {
    const inspect = () => {
      // Recover a missed event when a retained window becomes visible. Errors
      // and uncertain saves still require the explicit Reload action.
      if (document.visibilityState === "visible" && !current.current.problem) void run();
    };
    window.addEventListener("focus", inspect);
    document.addEventListener("visibilitychange", inspect);
    return () => { window.removeEventListener("focus", inspect); document.removeEventListener("visibilitychange", inspect); };
  }, [bridge]);
  return <AppearanceContext.Provider value={{ snapshot, operation, select: (theme) => { void run(theme); }, reload: () => { void run(); }, update: (preferences, revision) => { void run(current.current.theme, preferences, revision); } }}>
    {children}
    {!operation && snapshot.problem ? <p className="appearance-notice" role="alert"><LocalizedText id="appearance.appearance_463630" components={{ s0: <>{problemMessages[snapshot.problem]}</> }} /></p> : null}
  </AppearanceContext.Provider>;
}

export function AppearanceSettings() {
  useLocale();
  const { snapshot, operation, select, reload } = useContext(AppearanceContext);
  return <section data-settings-search-target="theme" className="appearance-settings" aria-label={copy("appearance.deviceAppearance_880cb9")}>
    <AppearanceControls modeControls={<div className="appearance-choices">{[Theme.System, Theme.Light, Theme.Dark].map((theme) => <label className="appearance-choice" key={theme}>
        <input type="radio" name="device-theme" value={theme} checked={snapshot.theme === theme} onChange={() => select(theme)} />
        <span className="appearance-miniature" data-preview={theme} aria-hidden="true" /><span className="appearance-choice-label">{theme === Theme.System ? copy("appearance.system_6725e7") : theme === Theme.Light ? copy("appearance.light_dbcd5e") : copy("appearance.dark_60acc5")}</span>
      </label>)}</div>} />
    <p id="appearance-scope">{copy("appearance.systemFollowsThisComputerSAppearance_edf3db")}</p>
    <p role="status" aria-live="polite">{operation === AppearanceOperation.Reading ? copy("appearance.readingAppearance_04736c") : operation === AppearanceOperation.Saving ? copy("appearance.savingTheme_26a81f") : snapshot.problem ? copy("appearance.appearanceIsNotSavedInspectThe_76382e") : copy("appearance.themeSaved_5d20dd")}</p>
    {snapshot.problem ? <><p role="alert">{problemMessages[snapshot.problem]}</p><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={Boolean(operation)} onClick={reload}>{copy("appearance.reloadAppearance_c3f2b4")}</SettingsActionButton></> : null}
  </section>;
}

export function useAppearance() { return useContext(AppearanceContext); }
export function useAppearancePreferences() { return useAppearance().snapshot.preferences ?? defaultPreferences(); }
