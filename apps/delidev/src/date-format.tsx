// SPDX-License-Identifier: Apache-2.0
import { DateFormatPreference, formatTimestampLabel, TimestampMode } from "./timestamp-format";
import "./date-format.css";
import { copy, useLocale } from "./localization";
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export { DateFormatPreference } from "./timestamp-format";
export enum DateFormatProblem {
  Unavailable = "unavailable", ReadFailed = "read-failed", InvalidDocument = "invalid-document", UnsupportedVersion = "unsupported-version",
  WriteFailed = "write-failed", OutcomeUnknown = "outcome-unknown", Changed = "changed",
}
enum DateFormatOperation { Reading = "reading", Saving = "saving" }
export interface DateFormatSnapshot { revision: number; date_format: DateFormatPreference; problem: DateFormatProblem | null }
export interface DateFormatBridge {
  read: () => Promise<unknown>;
  update: (date_format: DateFormatPreference, revision: number) => Promise<unknown>;
  subscribe: (changed: (snapshot: unknown) => void) => Promise<() => void>;
}
const defaultSnapshot: DateFormatSnapshot = { revision: 0, date_format: DateFormatPreference.System, problem: DateFormatProblem.Unavailable };
const nativeBridge: DateFormatBridge = {
  read: () => isTauri() ? invoke("read_date_format") : Promise.resolve(defaultSnapshot),
  update: (date_format, revision) => invoke("update_date_format", { dateFormat: date_format, expectedRevision: revision }),
  subscribe: async (changed) => isTauri() ? listen<unknown>("date-format-changed", (event) => changed(event.payload)) : () => {},
};
const problemMessages: Record<DateFormatProblem, string> = {
  get [DateFormatProblem.Unavailable]() { return copy("date-format.unavailable"); },
  get [DateFormatProblem.ReadFailed]() { return copy("date-format.readFailed"); },
  get [DateFormatProblem.InvalidDocument]() { return copy("date-format.invalidDocument"); },
  get [DateFormatProblem.UnsupportedVersion]() { return copy("date-format.unsupportedVersion"); },
  get [DateFormatProblem.WriteFailed]() { return copy("date-format.writeFailed"); },
  get [DateFormatProblem.OutcomeUnknown]() { return copy("date-format.outcomeUnknown"); },
  get [DateFormatProblem.Changed]() { return copy("date-format.changed"); },
};

export function parseDateFormat(value: unknown): DateFormatSnapshot {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid date-format snapshot");
  const data = value as Record<string, unknown>;
  if (Object.keys(data).sort().join(",") !== "date_format,problem,revision" || !Number.isInteger(data.revision) || Number(data.revision) < 0 || Number(data.revision) > 0xffffffff
    || !Object.values(DateFormatPreference).includes(data.date_format as DateFormatPreference) || (data.problem !== null && !Object.values(DateFormatProblem).includes(data.problem as DateFormatProblem))) throw new Error("Invalid date-format snapshot");
  return data as unknown as DateFormatSnapshot;
}

interface DateFormatController {
  snapshot: DateFormatSnapshot;
  operation?: DateFormatOperation;
  select: (date_format: DateFormatPreference) => void;
  reload: () => void;
}
const DateFormatContext = createContext<DateFormatController>({ snapshot: defaultSnapshot, select: () => {}, reload: () => {} });

export function DateFormatProvider({ children, bridge = nativeBridge }: { children: ReactNode; bridge?: DateFormatBridge }) {
  useLocale();
  const [snapshot, setSnapshot] = useState(defaultSnapshot);
  const current = useRef(defaultSnapshot);
  const [operation, setOperation] = useState<DateFormatOperation | undefined>(DateFormatOperation.Reading);
  const running = useRef(false);
  const generation = useRef(0);
  const subscription = useRef<(() => void) | undefined>(undefined);
  const accept = (value: unknown) => {
    const next = parseDateFormat(value);
    if (next.revision < current.current.revision) return;
    if (next.revision === current.current.revision && next.date_format !== current.current.date_format) throw new Error("Contradictory date-format revision");
    current.current = next;
    setSnapshot(next);
  };
  const failed = (problem: DateFormatProblem) => {
    const next = { ...current.current, problem };
    current.current = next;
    setSnapshot(next);
  };
  const subscribe = async (owned: number) => {
    const remove = await bridge.subscribe((value) => {
      // A later delivery does not prove an uncertain write. Recovery remains
      // explicit even when another window publishes a healthy snapshot.
      if (generation.current !== owned || (current.current.problem && current.current.problem !== DateFormatProblem.Unavailable)) return;
      try { accept(value); } catch { failed(DateFormatProblem.OutcomeUnknown); }
    });
    if (generation.current !== owned) { remove(); return false; }
    subscription.current = remove;
    return true;
  };
  useEffect(() => {
    const owned = ++generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(DateFormatOperation.Reading);
    void (async () => {
      try {
        // Listen before reading so no committed change is lost in the gap.
        if (!await subscribe(owned)) return;
        const value = await bridge.read();
        if (generation.current === owned) accept(value);
      } catch { if (generation.current === owned && current.current.revision <= before) failed(DateFormatProblem.ReadFailed); }
      finally { if (generation.current === owned) { running.current = false; setOperation(undefined); } }
    })();
    return () => { generation.current++; subscription.current?.(); subscription.current = undefined; };
  }, [bridge]);
  const run = async (date_format?: DateFormatPreference) => {
    if (running.current || (date_format !== undefined && current.current.problem)) return;
    const owned = generation.current;
    const before = current.current.revision;
    running.current = true;
    setOperation(date_format === undefined ? DateFormatOperation.Reading : DateFormatOperation.Saving);
    try {
      if (!subscription.current && !await subscribe(owned)) return;
      const value = date_format === undefined ? await bridge.read() : await bridge.update(date_format, current.current.revision);
      if (generation.current === owned) accept(value);
    } catch {
      if (generation.current === owned && current.current.revision <= before) failed(date_format === undefined ? DateFormatProblem.ReadFailed : DateFormatProblem.OutcomeUnknown);
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
  return <DateFormatContext.Provider value={{ snapshot, operation, select: (date_format) => { void run(date_format); }, reload: () => { void run(); } }}>
    {children}
    {!operation && snapshot.problem ? <p className="date-format-notice" role="alert">{copy("date-format.title")}: {problemMessages[snapshot.problem]}</p> : null}
  </DateFormatContext.Provider>;
}

export function useDateFormat(): DateFormatPreference {
  return useContext(DateFormatContext).snapshot.date_format;
}

export function DateFormatSettings() {
  useLocale();
  const { snapshot, operation, select, reload } = useContext(DateFormatContext);
  const choices = [DateFormatPreference.System, DateFormatPreference.Ymd, DateFormatPreference.Mdy, DateFormatPreference.Dmy];
  return <section className="date-format-settings" aria-label={copy("date-format.title")}>
    {/* Native disabling during autosave blurs the selected radio in Chromium.
        Keep it focusable; the controller rejects saves while busy or uncertain. */}
    <fieldset disabled={operation === DateFormatOperation.Reading} aria-disabled={Boolean(operation || snapshot.problem)} aria-busy={operation === DateFormatOperation.Saving}>
      <legend>{copy("date-format.title")}</legend>
      <div className="date-format-choices">{choices.map(value => <label key={value}>
        <input type="radio" name="device-date-format" value={value} checked={snapshot.date_format === value} onChange={() => select(value)} />
        <span>{value === DateFormatPreference.System ? copy("date-format.system") : value === DateFormatPreference.Ymd ? "YYYY-MM-DD" : value === DateFormatPreference.Mdy ? "MM/DD/YYYY" : "DD/MM/YYYY"}</span>
        <small>{formatTimestampLabel("2026-10-08T12:34:56Z", { preference: value, mode: TimestampMode.Absolute })}</small>
      </label>)}</div>
    </fieldset>
    <p>{copy("date-format.scope")}</p>
    <p role="status" aria-live="polite">{copy(operation === DateFormatOperation.Reading ? "date-format.reading" : operation === DateFormatOperation.Saving ? "date-format.saving" : snapshot.problem ? "date-format.notSaved" : "date-format.saved")}</p>
    {snapshot.problem ? <><p role="alert">{problemMessages[snapshot.problem]}</p><button type="button" disabled={Boolean(operation)} onClick={reload}>{copy("date-format.reload")}</button></> : null}
  </section>;
}
