// SPDX-License-Identifier: Apache-2.0
// Production owners with synthetic device snapshots; no native/account acceptance.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { AppearanceProvider, Theme, type AppearanceBridge, type AppearanceSnapshot } from "./appearance";
import { applyAppearanceColors, defaultPreferences, palettes, Palette, type AppearancePreferences } from "./appearance-preferences";
import { TrayStatus, type TrayPanelBridge } from "./tray-status";
import { SupportedLanguage } from "./localization";
import { DateFormatPreference } from "./timestamp-format";
import "./themes.css";
const instance = "018f0000-0000-7000-8000-000000000001";
const custom = { version: 1 as const, id: instance, name: "Synthetic custom", light: { ...palettes.nord.light, background: "#EEF2F6", accent: "#2244AA" }, dark: { ...palettes.dracula.dark, background: "#161B26", text: "#F6F8FC", accent: "#6644BB" } };
const modes = Object.values(Theme);
let snapshot: AppearanceSnapshot = { revision: 1, theme: Theme.Dark, problem: null, preferences: preferences("bundled") };
function preferences(kind: string): AppearancePreferences {
  return { ...defaultPreferences(), light_palette: kind === "default" ? Palette.Default : kind === "custom" ? instance : Palette.Nord, dark_palette: kind === "default" ? Palette.Default : kind === "custom" ? instance : Palette.Dracula, custom_themes: [custom] };
}
const listeners = new Set<(value: unknown) => void>();
const bridge: AppearanceBridge = { read: async () => snapshot, update: async () => snapshot, subscribe: async changed => { listeners.add(changed); return () => { listeners.delete(changed); }; } };
const trayBridge: TrayPanelBridge = { read: async () => ({ instance, windows: [], more: false, recent: null, theme: snapshot.theme, language: SupportedLanguage.English, date_format: DateFormatPreference.System, colors: { light: snapshot.preferences!.light_palette === instance ? custom.light : snapshot.preferences!.light_palette === Palette.Default ? palettes.default.light : palettes.nord.light, dark: snapshot.preferences!.dark_palette === instance ? custom.dark : snapshot.preferences!.dark_palette === Palette.Default ? palettes.default.dark : palettes.dracula.dark } }), subscribe: async changed => { const listener = () => changed(); listeners.add(listener); return () => { listeners.delete(listener); }; }, activate: async () => {}, dismiss: async () => {} };
const unrelated = new CSSStyleSheet(); unrelated.replaceSync(":root { --fixture-unrelated: retained; }"); document.adoptedStyleSheets = [unrelated];
const removePreview = applyAppearanceColors(palettes.solarized.light, '[data-preview="owned"]');
function Fixture() {
  const [mounted, mount] = useState(true);
  const tray = new URLSearchParams(location.search).has("tray");
  const publish = (theme: Theme, kind: string) => { snapshot = { revision: snapshot.revision + 1, theme, problem: null, preferences: preferences(kind) }; listeners.forEach(listener => listener(snapshot)); };
  return <><nav>{modes.flatMap(mode => ["bundled", "custom", "default"].map(kind => <button key={`${mode}-${kind}`} onClick={() => publish(mode, kind)}>{mode}-{kind}</button>))}<button onClick={() => mount(false)}>Dispose owner</button><button onClick={removePreview}>Dispose preview</button></nav><div data-preview="owned">Preview</div>{mounted ? tray ? <TrayStatus instance={instance} bridge={trayBridge} /> : <AppearanceProvider bridge={bridge}><main>Palette fixture</main></AppearanceProvider> : null}</>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
