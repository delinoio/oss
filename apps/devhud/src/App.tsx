import { useEffect, useRef, useState } from "react";

import { loadRuntimeInfo, tauriRuntimeBridge, type RuntimeInfo } from "./runtime/startup";
import { Dialog } from "./ui/Dialog";
import { ApplicationProvider, MobileScreen, ThemePreference, useApplication } from "./ui/state";

type RuntimeState =
  | { status: "loading" }
  | { status: "failed"; message: string }
  | { status: "ready"; runtimeInfo: RuntimeInfo };

function Wordmark() {
  return <div aria-label="DevHud" className="wordmark"><span aria-hidden="true">DH</span><strong>DevHud</strong></div>;
}

function SettingsDialog() {
  const { closeSettings, setTheme, theme } = useApplication();
  return (
    <Dialog title="DevHud settings" onClose={closeSettings}>
      <div className="dialog-heading"><div><p className="eyebrow">Settings</p><h2>Appearance</h2></div><button aria-label="Close settings" className="icon-button" onClick={closeSettings} type="button">×</button></div>
      <label className="field" htmlFor="theme-preference">Theme preference
        <select id="theme-preference" onChange={(event) => setTheme(event.target.value as ThemePreference)} value={theme}>
          <option value={ThemePreference.System}>System</option><option value={ThemePreference.Light}>Light</option><option value={ThemePreference.Dark}>Dark</option>
        </select>
      </label>
      <p className="muted">Settings stay on this device. No account or cloud sync is available.</p>
    </Dialog>
  );
}

function EmptyTools({ compact = false }: { compact?: boolean }) {
  const { openSettings } = useApplication();
  return <section className={compact ? "empty-state compact" : "empty-state"} aria-labelledby="tools-empty-title">
    <p className="eyebrow">Local foundation</p><h2 id="tools-empty-title">No tools yet</h2>
    <p>No tools are available in this foundation preview.</p>
    <button className="primary-button" onClick={openSettings} type="button">Settings</button>
  </section>;
}

function DesktopHud({ runtime }: { runtime: RuntimeState }) {
  const searchRef = useRef<HTMLInputElement>(null);
  const { openSettings } = useApplication();
  useEffect(() => { searchRef.current?.focus(); }, []);
  return <main className="desktop-shell">
    <header className="app-header"><Wordmark /><button className="text-button" onClick={openSettings} type="button">Settings</button></header>
    <section className="hud-panel" aria-labelledby="hud-title">
      <h1 id="hud-title">Developer tools, kept local.</h1>
      <label className="search-label" htmlFor="tool-search">Search tools</label>
      <input ref={searchRef} id="tool-search" placeholder="Search available tools" type="search" />
      {runtime.status === "loading" ? <p className="runtime-status" role="status">Starting DevHud…</p> : null}
      {runtime.status === "failed" ? <p className="runtime-status error" role="alert">{runtime.message}</p> : null}
      <EmptyTools />
    </section>
  </main>;
}

const mobileScreenLabels: Record<MobileScreen, string> = { [MobileScreen.Home]: "Home", [MobileScreen.Widgets]: "Widgets", [MobileScreen.Settings]: "Settings", [MobileScreen.Diagnostics]: "Diagnostics" };

function MobileContent({ runtime }: { runtime: RuntimeState }) {
  const { mobileScreen, openSettings } = useApplication();
  if (mobileScreen === MobileScreen.Home) return <EmptyTools compact />;
  if (mobileScreen === MobileScreen.Widgets) return <section className="empty-state compact" aria-labelledby="widgets-title"><p className="eyebrow">Widgets</p><h1 id="widgets-title">No widgets available</h1><p>Visible widgets are not part of this foundation preview.</p></section>;
  if (mobileScreen === MobileScreen.Settings) return <section className="empty-state compact" aria-labelledby="settings-title"><p className="eyebrow">Settings</p><h1 id="settings-title">Choose your appearance</h1><p>Use your device preference, a light theme, or a dark theme.</p><button className="primary-button" onClick={openSettings} type="button">Open settings</button></section>;
  return <section className="empty-state compact" aria-labelledby="diagnostics-title"><p className="eyebrow">Diagnostics</p><h1 id="diagnostics-title">Diagnostics are unavailable</h1>{runtime.status === "loading" ? <p role="status">Loading local diagnostics…</p> : null}{runtime.status === "failed" ? <p role="alert">{runtime.message}</p> : null}{runtime.status === "ready" ? <p>Local diagnostics are not exposed in this foundation preview.</p> : null}</section>;
}

function MobileShell({ runtime }: { runtime: RuntimeState }) {
  const { mobileScreen, setMobileScreen } = useApplication();
  return <main className="mobile-shell"><header className="app-header"><Wordmark /></header><MobileContent runtime={runtime} /><nav aria-label="Mobile navigation" className="mobile-nav">{Object.values(MobileScreen).map((screen) => <button aria-current={mobileScreen === screen ? "page" : undefined} key={screen} onClick={() => setMobileScreen(screen)} type="button">{mobileScreenLabels[screen]}</button>)}</nav></main>;
}

function ApplicationSurface({ platform }: { platform: "desktop" | "mobile" }) {
  const [runtime, setRuntime] = useState<RuntimeState>({ status: "loading" });
  useEffect(() => { let active = true; void loadRuntimeInfo(tauriRuntimeBridge).then((runtimeInfo) => { if (active) setRuntime({ status: "ready", runtimeInfo }); }, () => { if (active) setRuntime({ status: "failed", message: "DevHud could not initialize its local runtime." }); }); return () => { active = false; }; }, []);
  const { settingsOpen } = useApplication();
  return <>{platform === "desktop" ? <DesktopHud runtime={runtime} /> : <MobileShell runtime={runtime} />}{settingsOpen ? <SettingsDialog /> : null}</>;
}

export function App({ platform = "desktop" }: { platform?: "desktop" | "mobile" }) {
  return <ApplicationProvider><ApplicationSurface platform={platform} /></ApplicationProvider>;
}
