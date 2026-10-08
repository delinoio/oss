// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { copy, i18n, SupportedLanguage, useLocale, type MessageKey } from "./localization";
import "./language.css";

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
const languageLabels: Record<LanguagePreference, MessageKey> = {
  [LanguagePreference.System]: "language.system",
  [LanguagePreference.English]: "language.english",
  [LanguagePreference.Korean]: "language.korean",
};
// Sort by stable English names, independently of the current UI language.
const languageChoices = [
  { preference: LanguagePreference.English, englishName: "English", nativeName: "English" },
  { preference: LanguagePreference.Korean, englishName: "Korean", nativeName: "한국어" },
].sort((left, right) => left.englishName.localeCompare(right.englishName, "en"));

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
  const input = useRef<HTMLInputElement>(null);
  const picker = useRef<HTMLDivElement>(null);
  const [search, setSearch] = useState<{ revision: number; query: string; active?: LanguagePreference }>();
  const disabled = Boolean(operation || snapshot.problem);
  const opened = !disabled && search?.revision === snapshot.revision ? search : undefined;
  const popup = Boolean(opened);
  const matchingChoices = (value: string) => {
    const query = value.trim().toLowerCase();
    return [LanguagePreference.System, ...languageChoices.map(choice => choice.preference)].filter(preference => {
      if (preference === LanguagePreference.System) return copy(languageLabels[preference]).toLowerCase().includes(query);
      const choice = languageChoices.find(candidate => candidate.preference === preference)!;
      return choice.englishName.toLowerCase().includes(query) || choice.nativeName.toLowerCase().includes(query);
    });
  };
  const choices = matchingChoices(opened?.query ?? "");
  const active = opened?.active && choices.includes(opened.active) ? opened.active : undefined;
  const currentLabel = copy(languageLabels[snapshot.language]);
  const inputValue = opened?.query ?? currentLabel;
  const listId = `${id}-list`;
  const available = () => !disabled && input.current !== null && !input.current.matches(":disabled");
  const close = () => setSearch(undefined);
  const open = () => { if (available()) setSearch({ revision: snapshot.revision, query: "" }); };
  const pick = (preference: LanguagePreference) => {
    if (!available() || !popup || !choices.includes(preference)) return;
    input.current?.focus();
    close();
    if (preference !== snapshot.language) select(preference);
  };
  useEffect(() => {
    // A read/save lock or newer native observation discards only this search.
    if (disabled || (search && search.revision !== snapshot.revision)) close();
  }, [disabled, snapshot.revision]);
  useEffect(() => {
    if (!popup) return;
    const dismiss = (event: PointerEvent) => { if (!picker.current?.contains(event.target as Node)) close(); };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [popup]);
  return <section data-settings-search-target="language" className="language-settings" aria-labelledby={`${id}-title`}>
    <label htmlFor={`${id}-input`}><span id={`${id}-title`}>{copy("language.title")}</span></label>
    <div ref={picker} className="language-picker" onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close(); }}>
      <div className="language-control">
        <svg className="language-search-icon" aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></svg>
        {/* A pending save locks editing without making the focused input lose
            browser focus. Reads and recovery failures still disable it. */}
        <input ref={input} id={`${id}-input`} role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-disabled={disabled} aria-controls={popup ? listId : undefined} aria-activedescendant={active ? `${listId}-${active}` : undefined} aria-describedby={`${id}-scope ${id}-help`} value={inputValue} placeholder={copy("language.searchPlaceholder")} autoComplete="off" spellCheck={false} disabled={disabled && operation !== LanguageOperation.Saving} readOnly={operation === LanguageOperation.Saving}
          onFocus={open} onClick={() => { if (!popup) open(); }}
          onChange={event => {
            if (!available()) return;
            const next = event.currentTarget.value;
            setSearch({ revision: snapshot.revision, query: next, active: matchingChoices(next)[0] });
          }}
          onKeyDown={event => {
            if (event.nativeEvent.isComposing || event.keyCode === 229 || !available()) return;
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              const index = active ? choices.indexOf(active) : -1;
              const next = event.key === "ArrowDown" ? Math.min(index + 1, choices.length - 1) : index < 0 ? choices.length - 1 : Math.max(0, index - 1);
              setSearch({ revision: snapshot.revision, query: opened?.query ?? "", active: choices[next] });
            } else if (event.key === "Enter" && popup) {
              event.preventDefault();
              if (active) pick(active);
            } else if (event.key === "Escape" && popup) {
              event.preventDefault(); event.stopPropagation(); close();
            } else if (event.key === "Tab") close();
          }} />
        <button type="button" className="language-toggle" tabIndex={-1} disabled={disabled} aria-label={copy(popup ? "language.hideOptions" : "language.showOptions")} aria-expanded={popup} aria-haspopup="listbox" aria-controls={popup ? listId : undefined}
          onPointerDown={event => event.preventDefault()} onClick={() => { if (!available()) return; input.current?.focus(); if (popup) close(); else open(); }}><svg aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6"><path d="m6 9 6 6 6-6" /></svg></button>
      </div>
      {popup ? <>
        <ul id={listId} className="language-options" role="listbox" aria-labelledby={`${id}-title`}>
          {choices.map(preference => <li id={`${listId}-${preference}`} key={preference} role="option" aria-selected={active ? active === preference : snapshot.language === preference} aria-describedby={snapshot.language === preference ? `${id}-current` : undefined}
            onPointerDown={event => event.preventDefault()} onClick={() => pick(preference)}>
            <span>{copy(languageLabels[preference])}</span>
            {snapshot.language === preference ? <svg className="language-check" aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="m5 12 4 4 10-10" /></svg> : null}
          </li>)}
        </ul>
        <span id={`${id}-current`} className="language-sr-only">{copy("language.current")}</span>
        {choices.length === 0 ? <p className="language-empty" role="status">{copy("language.noResults")}</p> : null}
      </> : null}
    </div>
    <p id={`${id}-help`} className="language-sr-only">{copy("language.searchHelp")}</p>
    <p id={`${id}-scope`}>{copy("language.scope")}</p>
    <p role="status" aria-live="polite">{copy(operation === LanguageOperation.Reading ? "language.reading" : operation === LanguageOperation.Saving ? "language.saving" : snapshot.problem ? "language.notSaved" : "language.saved")}</p>
    {snapshot.problem ? <><p role="alert">{copy(problemMessages[snapshot.problem])}</p><button type="button" disabled={Boolean(operation)} onClick={reload}>{copy("language.reload")}</button></> : null}
    {snapshot.widget_problem ? <><p role="alert">{copy("language.widgetFailed")}</p>{!snapshot.problem ? <button type="button" disabled={Boolean(operation)} onClick={reload}>{copy("language.reload")}</button> : null}</> : null}
  </section>;
}
