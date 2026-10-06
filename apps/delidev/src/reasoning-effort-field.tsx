// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { text } from "./documents";
import { copy, useLocale } from "./localization";
import "./reasoning-effort-field.css";

enum ReasoningEffort { None = "none", Minimal = "minimal", Low = "low", Medium = "medium", High = "high", XHigh = "xhigh", Max = "max", Ultra = "ultra", Persistent = "persistent" }

// Harness-level hints do not establish support for a selected model or change
// the server's validation of an explicitly entered value.
export const codexEffortSuggestions: readonly string[] = Object.values(ReasoningEffort);
export const claudeEffortSuggestions: readonly string[] = [ReasoningEffort.Low, ReasoningEffort.Medium, ReasoningEffort.High, ReasoningEffort.XHigh, ReasoningEffort.Max];
const emptySuggestions: readonly string[] = [];

export function ReasoningEffortField({ label, value, change, suggestions = emptySuggestions, disabled = false }: { label: string; value: unknown; change: (value: string) => void; suggestions?: readonly string[]; disabled?: boolean }) {
  useLocale();
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const [open, setOpen] = useState(false);
  const [navigation, setNavigation] = useState<{ value: string; suggestions: readonly string[]; index: number }>();
  const current = text(value);
  const filtered = suggestions.filter(candidate => candidate.startsWith(current.trim().toLowerCase()));
  const choices = ["", ...filtered];
  const popup = open && !disabled;
  const highlight = navigation?.value === current && navigation.suggestions === suggestions ? navigation.index : -1;
  const activeIndex = popup && highlight >= 0 && highlight < choices.length ? highlight : -1;
  const listId = `${id}-list`;
  const available = () => input.current !== null && !input.current.matches(":disabled");
  const close = () => { setOpen(false); setNavigation(undefined); };
  const pick = (index: number) => {
    if (!available() || index < 0 || index >= choices.length) return;
    change(choices[index]!);
    input.current?.focus();
    close();
  };

  // The owning form may disable an ancestor fieldset during a save. Custom
  // listbox rows need the same lock as native controls, including after commit.
  useEffect(() => { if (open && !available()) close(); });
  useEffect(() => { if (activeIndex >= 0) list.current?.children[activeIndex]?.scrollIntoView?.({ block: "nearest" }); }, [activeIndex]);

  return <div className="reasoning-effort-field" onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close(); }}>
    <label htmlFor={`${id}-input`}>{label}</label>
    <div className="reasoning-effort-control">
      <input ref={input} id={`${id}-input`} role="combobox" aria-autocomplete="list" aria-expanded={popup} aria-controls={popup ? listId : undefined} aria-activedescendant={activeIndex >= 0 ? `${listId}-${activeIndex}` : undefined} aria-describedby={`${id}-help`} value={current} placeholder={copy("reasoning-effort.nativeDefault")} maxLength={256} autoComplete="off" spellCheck={false} disabled={disabled}
        onFocus={() => { if (available()) setOpen(true); }} onClick={() => { if (available()) setOpen(true); }}
        onChange={event => { change(event.target.value); setNavigation(undefined); setOpen(true); }}
        onKeyDown={event => {
          if (event.nativeEvent.isComposing || event.keyCode === 229 || !available()) return;
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault(); setOpen(true);
            const index = event.key === "ArrowDown" ? (activeIndex + 1) % choices.length : activeIndex <= 0 ? choices.length - 1 : activeIndex - 1;
            setNavigation({ value: current, suggestions, index });
          } else if (event.key === "Enter" && popup) {
            event.preventDefault();
            if (activeIndex >= 0) pick(activeIndex); else close();
          } else if (event.key === "Escape" && popup) {
            event.preventDefault(); event.stopPropagation(); close();
          } else if (event.key === "Tab") close();
        }} />
      <button type="button" className="reasoning-effort-toggle" disabled={disabled} aria-label={`${copy(popup ? "reasoning-effort.hide" : "reasoning-effort.show")} ${label} ${copy("reasoning-effort.suggestions")}`} aria-expanded={popup} aria-haspopup="listbox" aria-controls={popup ? listId : undefined} onMouseDown={event => event.preventDefault()} onClick={() => { if (!available()) return; input.current?.focus(); setNavigation(undefined); setOpen(!popup); }}><svg aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6"><path d="m6 9 6 6 6-6" /></svg></button>
    </div>
    {popup ? <>
      <ul ref={list} id={listId} className="reasoning-effort-suggestions" role="listbox" aria-label={`${label} ${copy("reasoning-effort.suggestions")}`}>
        {choices.map((candidate, index) => <li id={`${listId}-${index}`} key={candidate} role="option" aria-selected={activeIndex >= 0 ? activeIndex === index : current === candidate} onMouseDown={event => event.preventDefault()} onClick={() => pick(index)}>{candidate || copy("reasoning-effort.useNativeDefault")}</li>)}
      </ul>
      {filtered.length === 0 ? <p className="reasoning-effort-status" role="status">{suggestions.length ? copy("reasoning-effort.noMatchingSuggestions") : copy("reasoning-effort.noEffortSuggestions")}</p> : null}
    </> : null}
    <p id={`${id}-help`} className="reasoning-effort-help">{copy("reasoning-effort.chooseSuggestionOrType")}</p>
  </div>;
}
