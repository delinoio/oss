// SPDX-License-Identifier: Apache-2.0
// Synthetic appearance bridge; no native storage or account authority.
import { createRoot } from "react-dom/client";
import { AppearanceProvider, Theme, type AppearanceBridge, type AppearanceSnapshot } from "./appearance";
import { defaultPreferences, Palette, palettes, type AppearancePreferences } from "./appearance-preferences";
import "./styles.css";
import "./doctor.css";
import "./disclosure.css";
import "./agent-configuration.css";
import "./session.css";
import "./project-list.css";
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
root.render(<AppearanceProvider bridge={bridge}><main id="appearance-probe">
  <aside id="palette-sidebar" className="sidebar"><div><button id="palette-hover" className="sidebar-project-row">Demo</button><button id="palette-selected" aria-pressed="true">Selected</button></div></aside>
  <section className="appearance-preview"><div id="palette-card"><p>Body text</p><small id="palette-muted">Secondary text</small><a id="palette-link" className="diagnostics-symbol" href="#appearance-probe">Example link</a></div></section>
  <section id="palette-conversation" className="usage-page"><div id="palette-conversation-card" className="usage-chart-panel">Synthetic conversation surface</div></section>
  <textarea aria-label="Retained draft" defaultValue="unsent" />
  <section id="palette-focus-consumers" aria-label="Synthetic focus consumers">
    <div id="focus-runner-column" className="settings-runner-column" tabIndex={0}>Runner column</div>
    <details className="settings-runner-worker-details"><summary id="focus-runner-worker">Runner details</summary></details>
    <details className="inbox-execution-metadata"><summary id="focus-inbox-execution">Execution metadata</summary></details>
    <aside className="sidebar"><div className="sidebar-conversation-line"><button id="focus-conversation" className="sidebar-conversation-disclosure disclosure-header">Expand</button><button id="focus-project-more" className="sidebar-project-more">More</button></div></aside>
    <button id="focus-wide" className="sidebar-wide-toggle">Sidebar</button>
    <form className="repository-profile-association"><button id="focus-repository" type="button">Repository profile</button></form>
    <button id="focus-agent" className="agent-configuration">Agent</button>
    <details className="disclosure"><summary id="focus-disclosure" className="disclosure-header">Disclosure</summary></details>
    <div className="session-tab"><button id="focus-session" type="button">Session tab</button></div>
    <details className="project-original-details"><summary id="focus-project">Project details</summary></details>
    <button id="focus-sentinel" type="button">End of focus probes</button>
  </section>
</main></AppearanceProvider>);
const fixture = {
  commit(theme: Theme, custom = false, defaults = false) {
    snapshot = { ...snapshot, revision: snapshot.revision + 1, theme,
      preferences: { ...preferences, light_palette: defaults ? Palette.Default : Palette.Nord,
        dark_palette: defaults ? Palette.Default : custom ? customId : Palette.Dracula } };
    listeners.forEach(changed => changed(snapshot));
  },
  select(light: Palette, dark: Palette, theme: Theme) {
    snapshot = { ...snapshot, revision: snapshot.revision + 1, theme, preferences: { ...preferences, light_palette: light, dark_palette: dark } };
    listeners.forEach(changed => changed(snapshot));
  },
  unmount: () => root.unmount(),
};
Object.assign(window, { appearanceColorsFixture: fixture });
