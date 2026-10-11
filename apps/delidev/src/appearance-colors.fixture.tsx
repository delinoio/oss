// SPDX-License-Identifier: Apache-2.0
// Synthetic bridge for automated browser color/cascade checks; no native IPC.
import { createRoot } from "react-dom/client";
import { AppearanceProvider, Theme, type AppearanceSnapshot } from "./appearance";
import { defaultPreferences, palettes, Palette } from "./appearance-preferences";
import "./themes.css";

const customId = "018f0000-0000-7000-8000-000000000001";
const preferences = defaultPreferences();
preferences.light_palette = Palette.Nord;
preferences.dark_palette = Palette.Dracula;
preferences.custom_themes = [{ version: 1, id: customId, name: "Synthetic custom", light: structuredClone(palettes.default.light), dark: { ...palettes.default.dark, background: "#111111", accent: "#000000" } }];
let snapshot: AppearanceSnapshot = { revision: 1, theme: Theme.Dark, problem: null, preferences };
const listeners = new Set<(value: unknown) => void>();
const root = createRoot(document.getElementById("root")!);
Object.assign(window, { appearanceFixture: {
  select: (theme: Theme, custom: boolean) => {
    snapshot = { ...snapshot, revision: snapshot.revision + 1, theme, preferences: { ...preferences, dark_palette: custom ? customId : Palette.Dracula } };
    listeners.forEach(listener => listener(snapshot));
  },
  dispose: () => root.unmount(),
} });
root.render(<AppearanceProvider bridge={{ read: async () => snapshot, update: async () => snapshot, subscribe: async listener => { listeners.add(listener); return () => { listeners.delete(listener); }; } }}><p>Palette fixture</p></AppearanceProvider>);
