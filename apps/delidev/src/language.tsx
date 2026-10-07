// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { copy, i18n, SupportedLanguage, useLocale, type MessageKey } from "./localization";

export enum LanguagePreference { System = "system", English = "en", Korean = "ko" }
export enum LanguageProblem {
  Unavailable = "unavailable", ReadFailed = "read-failed", InvalidDocument = "invalid-document", UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed", OutcomeUnknown = "outcome-unknown", Changed = "changed",
}
enum LanguageOperation { Reading = "reading", Saving = "saving" }
export interface LanguageSnapshot { revision: number; language: LanguagePreference; resolved_language: SupportedLanguage; widget_problem?: "storage-unavailable" | null; problem: LanguageProblem | null }
export interface LanguageBridge {
  read: () => Promise<unknown>;
  update: (language: LanguagePreference, revision: number) => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}
const defaultSnapshot: LanguageSnapshot = { revision: 0, language: LanguagePreference.System, resolved_language: SupportedLanguage.English, problem: LanguageProblem.Unavailable };
const nativeBridge: LanguageBridge = {
  read: () => isTauri() ? invoke("read_language") : Promise.resolve(defaultSnapshot),
  update: (language, revision) => invoke("update_language", { language, expectedRevision: revision }),
  subscribe: async (changed) => isTauri() ? listen<unknown>("language-changed", (event) => changed(event.payload)) : () => {},
};
const problemMessages: Record<LanguageProblem, MessageKey> = {
  [LanguageProblem.Unavailable]: "language.unavailable",
  [LanguageProblem.ReadFailed]: "language.readFailed",
  [LanguageProblem.InvalidDocument]: "language.invalidDocument",
  [LanguageProblem.UnsupportedVersion]: "language.unsupportedVersion",
  [LanguageProblem.WriteFailed]: "language.writeFailed",
  [LanguageProblem.OutcomeUnknown]: "language.outcomeUnknown",
  [LanguageProblem.Changed]: "language.changed",
};

export function parseLanguage(value: unknown): LanguageSnapshot {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid language snapshot");
  const data = value as Record<string, unknown>;
  if (Object.keys(data).filter(key => key !== "widget_problem").sort().join(",") !== "language,problem,resolved_language,revision" || !Number.isInteger(data.revision) || Number(data.revision) < 0 || Number(data.revision) > 0xffffffff
    || !Object.values(SupportedLanguage).includes(data.resolved_language as SupportedLanguage) || (data.widget_problem !== undefined && data.widget_problem !== null && data.widget_problem !== "storage-unavailable")
    || (data.language !== LanguagePreference.System && data.language !== data.resolved_language)
    || !Object.values(LanguagePreference).includes(data.language as LanguagePreference) || (data.problem !== null && !Object.values(LanguageProblem).includes(data.problem as LanguageProblem))) throw new Error("Invalid language snapshot");
  return data as unknown as LanguageSnapshot;
}

interface LanguageController {
  snapshot: LanguageSnapshot;
  operation?: LanguageOperation;
  select: (language: LanguagePreference) => void;
  reload: () => void;
}
const LanguageContext = createContext<LanguageController>({ snapshot: defaultSnapshot, select: () => {}, reload: () => {} });

export function LanguageProvider({ children, bridge = nativeBridge }: { children: ReactNode; bridge?: LanguageBridge }) {
  useLocale();
  const [snapshot, setSnapshot] = useState(defaultSnapshot);
  const current = useRef(defaultSnapshot);
  const [operation, setOperation] = useState<LanguageOperation | undefined>(LanguageOperation.Reading);
  const running = useRef(false);
  const generation = useRef(0);
  const subscription = useRef<(() => void) | undefined>(undefined);
  const accept = (value: unknown) => {
    const next = parseLanguage(value);
    if (next.revision < current.current.revision) return;
    if (next.revision === current.current.revision && (next.language !== current.current.language || next.resolved_language !== current.current.resolved_language)) throw new Error("Contradictory language revision");
    current.current = next;
    void i18n.changeLanguage(next.resolved_language);
    setSnapshot(next);
  };
  const failed = (problem: LanguageProblem) => {
    const next = { ...current.current, problem };
    current.current = next;
    setSnapshot(next);
  };
  useLayoutEffect(() => {
    document.documentElement.lang = snapshot.resolved_language;
  }, [snapshot.resolved_language]);
  const subscribe = async (owned: number) => {
    const remove = await bridge.subscribe((value) => {
      if (generation.current !== owned) return;
      try { accept(value); } catch { failed(LanguageProblem.OutcomeUnknown); }
    });
    if (generation.current !== owned) { remove(); return false; }
    subscription.current = remove;
    return true;
  };
  useEffect(() => {
    const owned = ++generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(LanguageOperation.Reading);
    void (async () => {
      try {
        // Listen before reading so no committed change is lost in the gap.
        if (!await subscribe(owned)) return;
        const value = await bridge.read();
        if (generation.current === owned) accept(value);
      } catch { if (generation.current === owned && current.current.revision <= before) failed(LanguageProblem.ReadFailed); }
      finally { if (generation.current === owned) { running.current = false; setOperation(undefined); } }
    })();
    return () => { generation.current++; subscription.current?.(); subscription.current = undefined; };
  }, [bridge]);
  const run = async (language?: LanguagePreference) => {
    if (running.current || (language !== undefined && current.current.problem)) return;
    const owned = generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(language === undefined ? LanguageOperation.Reading : LanguageOperation.Saving);
    try {
      if (!subscription.current && !await subscribe(owned)) return;
      const value = language === undefined ? await bridge.read() : await bridge.update(language, current.current.revision);
      if (generation.current === owned) accept(value);
    } catch {
      if (generation.current === owned && current.current.revision <= before) failed(language === undefined ? LanguageProblem.ReadFailed : LanguageProblem.OutcomeUnknown);
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
  return <LanguageContext.Provider value={{ snapshot, operation, select: (language) => { void run(language); }, reload: () => { void run(); } }}>
    {children}
    {!operation && snapshot.problem ? <p className="language-notice" role="alert">{copy("language.title")}: {copy(problemMessages[snapshot.problem])}</p> : null}
  </LanguageContext.Provider>;
}

export function LanguageSettings() {
  useLocale();
  const id = useId();
  const { snapshot, operation, select, reload } = useContext(LanguageContext);
  return <section className="language-settings" aria-labelledby={`${id}-title`}>
    <label htmlFor={`${id}-select`}><span id={`${id}-title`}>{copy("language.title")}</span></label>
    <select id={`${id}-select`} aria-describedby={`${id}-scope`} value={snapshot.language} disabled={Boolean(operation || snapshot.problem)} onChange={event => select(event.currentTarget.value as LanguagePreference)}>
      <option value={LanguagePreference.System}>{copy("language.system")}</option>
      <option value={LanguagePreference.English}>{copy("language.english")}</option>
      <option value={LanguagePreference.Korean}>{copy("language.korean")}</option>
    </select>
    <p id={`${id}-scope`}>{copy("language.scope")}</p>
    <p role="status" aria-live="polite">{copy(operation === LanguageOperation.Reading ? "language.reading" : operation === LanguageOperation.Saving ? "language.saving" : snapshot.problem ? "language.notSaved" : "language.saved")}</p>
    {snapshot.problem ? <><p role="alert">{copy(problemMessages[snapshot.problem])}</p><button type="button" disabled={Boolean(operation)} onClick={reload}>{copy("language.reload")}</button></> : null}
    {snapshot.widget_problem ? <><p role="alert">{copy("language.widgetFailed")}</p>{!snapshot.problem ? <button type="button" disabled={Boolean(operation)} onClick={reload}>{copy("language.reload")}</button> : null}</> : null}
  </section>;
}
