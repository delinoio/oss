// SPDX-License-Identifier: Apache-2.0
// Synthetic appearance bridge; no native storage or account authority.
import { createRoot } from "react-dom/client";
import { AppearanceProvider, Theme, type AppearanceBridge, type AppearanceSnapshot } from "./appearance";
import { defaultPreferences, Palette, palettes, type AppearancePreferences } from "./appearance-preferences";
import "./themes.css";
import "./appearance-colors.fixture.css";

const customId = "01900000-0000-7000-8000-000000000001";
const preferences: AppearancePreferences = {
  ...defaultPreferences(), light_palette: Palette.Nord, dark_palette: Palette.Dracula,
  custom_themes: [{ version: 1, id: customId, name: "Synthetic custom", light: { ...palettes.default.light },
    dark: { ...palettes.default.dark, background: "#201B2D", text: "#E8EDF6", accent: "#6B21A8" } }],
};
let snapshot: AppearanceSnapshot = { revision: 1, theme: Theme.Dark, problem: null, preferences };
const listeners = new Set<(value: unknown) => void>();
const bridge: AppearanceBridge = {
  read: async () => snapshot,
  subscribe: async changed => { listeners.add(changed); return () => { listeners.delete(changed); }; },
  update: async () => { throw new Error("Fixture must not write appearance"); },
};
const root = createRoot(document.getElementById("root")!);
root.render(<AppearanceProvider bridge={bridge}><main id="appearance-probe"><textarea aria-label="Retained draft" defaultValue="unsent" /></main></AppearanceProvider>);
const fixture = {
  commit(theme: Theme, custom = false, defaults = false) {
    snapshot = { ...snapshot, revision: snapshot.revision + 1, theme,
      preferences: { ...preferences, light_palette: defaults ? Palette.Default : Palette.Nord,
        dark_palette: defaults ? Palette.Default : custom ? customId : Palette.Dracula } };
    listeners.forEach(changed => changed(snapshot));
  },
  unmount: () => root.unmount(),
};
Object.assign(window, { appearanceColorsFixture: fixture });
