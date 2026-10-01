// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export enum Theme { System = "system", Light = "light", Dark = "dark" }
export enum AppearanceProblem {
  Unavailable = "unavailable", ReadFailed = "read-failed", InvalidDocument = "invalid-document", UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed", OutcomeUnknown = "outcome-unknown", Changed = "changed",
}
enum AppearanceOperation { Reading = "reading", Saving = "saving" }
export interface AppearanceSnapshot { revision: number; theme: Theme; problem: AppearanceProblem | null }
export interface AppearanceBridge {
  read: () => Promise<unknown>;
  update: (theme: Theme, revision: number) => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}
const defaultSnapshot: AppearanceSnapshot = { revision: 0, theme: Theme.System, problem: AppearanceProblem.Unavailable };
const nativeBridge: AppearanceBridge = {
  read: () => isTauri() ? invoke("read_appearance") : Promise.resolve(defaultSnapshot),
  update: (theme, revision) => invoke("update_appearance", { theme, expectedRevision: revision }),
  subscribe: async (changed) => isTauri() ? listen<unknown>("appearance-changed", (event) => changed(event.payload)) : () => {},
};
const problemMessages: Record<AppearanceProblem, string> = {
  [AppearanceProblem.Unavailable]: "Appearance storage is unavailable. Open the desktop app and check this computer’s configuration access.",
  [AppearanceProblem.ReadFailed]: "The saved appearance could not be read. The last known theme remains active. Check configuration access, then reload appearance.",
  [AppearanceProblem.InvalidDocument]: "The saved appearance is invalid. System is the fallback. The original file is preserved; repair or remove it before reloading appearance.",
  [AppearanceProblem.UnsupportedVersion]: "The saved appearance uses an unsupported version. System is the fallback. The original file is preserved; use a compatible app or repair it before reloading appearance.",
  [AppearanceProblem.WriteFailed]: "The theme could not be saved. The last committed selection remains active. Check configuration access, then reload appearance before trying again.",
  [AppearanceProblem.OutcomeUnknown]: "The save outcome is unknown. Reload appearance to inspect the committed selection before trying again.",
  [AppearanceProblem.Changed]: "Appearance changed elsewhere. Reload appearance before choosing again.",
};

export function parseAppearance(value: unknown): AppearanceSnapshot {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid appearance snapshot");
  const data = value as Record<string, unknown>;
  if (Object.keys(data).sort().join(",") !== "problem,revision,theme" || !Number.isInteger(data.revision) || Number(data.revision) < 0 || Number(data.revision) > 0xffffffff
    || !Object.values(Theme).includes(data.theme as Theme) || (data.problem !== null && !Object.values(AppearanceProblem).includes(data.problem as AppearanceProblem))) throw new Error("Invalid appearance snapshot");
  return data as unknown as AppearanceSnapshot;
}

interface AppearanceController {
  snapshot: AppearanceSnapshot;
  operation?: AppearanceOperation;
  select: (theme: Theme) => void;
  reload: () => void;
}
const AppearanceContext = createContext<AppearanceController>({ snapshot: defaultSnapshot, select: () => {}, reload: () => {} });

export function AppearanceProvider({ children, bridge = nativeBridge }: { children: ReactNode; bridge?: AppearanceBridge }) {
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
    if (next.revision === current.current.revision && next.theme !== current.current.theme) throw new Error("Contradictory appearance revision");
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
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = snapshot.theme === Theme.System ? (systemDark ? Theme.Dark : Theme.Light) : snapshot.theme;
  }, [snapshot.theme, systemDark]);
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
  const run = async (theme?: Theme) => {
    if (running.current || (theme !== undefined && current.current.problem)) return;
    const owned = generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(theme === undefined ? AppearanceOperation.Reading : AppearanceOperation.Saving);
    try {
      if (!subscription.current && !await subscribe(owned)) return;
      const value = theme === undefined ? await bridge.read() : await bridge.update(theme, current.current.revision);
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
  return <AppearanceContext.Provider value={{ snapshot, operation, select: (theme) => { void run(theme); }, reload: () => { void run(); } }}>
    {children}
    {!operation && snapshot.problem ? <p className="appearance-notice" role="alert">Appearance: {problemMessages[snapshot.problem]}</p> : null}
  </AppearanceContext.Provider>;
}

export function AppearanceSettings() {
  const { snapshot, operation, select, reload } = useContext(AppearanceContext);
  return <section className="appearance-settings" aria-label="Device appearance">
    <fieldset disabled={Boolean(operation || snapshot.problem)} aria-describedby="appearance-scope">
      <legend>Theme</legend>
      {[Theme.System, Theme.Light, Theme.Dark].map((theme) => <label className="appearance-choice" key={theme}>
        <input type="radio" name="device-theme" value={theme} checked={snapshot.theme === theme} onChange={() => select(theme)} />
        <span>{theme === Theme.System ? "System" : theme === Theme.Light ? "Light" : "Dark"}</span>
      </label>)}
    </fieldset>
    <p id="appearance-scope">System follows this computer’s appearance. Theme changes save automatically and apply to every DeliDev window.</p>
    <p role="status" aria-live="polite">{operation === AppearanceOperation.Reading ? "Reading appearance…" : operation === AppearanceOperation.Saving ? "Saving theme…" : snapshot.problem ? "Appearance is not saved. Inspect the saved selection before trying again." : "Saved on this computer."}</p>
    {snapshot.problem ? <><p role="alert">{problemMessages[snapshot.problem]}</p><button type="button" disabled={Boolean(operation)} onClick={reload}>Reload appearance</button></> : null}
  </section>;
}
