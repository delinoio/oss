// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { ScrollContinuation, type ScrollContinuationQuery } from "./scroll-continuation";
import "./scroll-picker.css";

export interface ScrollPickerOption { id: string; label: string; disabled?: boolean }

/** Choices are display projections. The domain owns exact-ID selection,
 * authoritative reads and any resulting configuration change. */
export function ScrollPicker({ options, label, value, change, query, active, disabled = false, required = false, autoFocus = false, placeholder = "", selectedLabel, markRequired = false }: {
  options: ScrollPickerOption[]; label: string; value: string; change: (id: string) => void;
  query: ScrollContinuationQuery; active: boolean; disabled?: boolean; required?: boolean;
  autoFocus?: boolean; placeholder?: string; selectedLabel?: string; markRequired?: boolean;
}) {
  const id = useId(), trigger = useRef<HTMLButtonElement>(null), root = useRef<HTMLDivElement>(null), container = useRef<HTMLDivElement>(null);
  const focusedInitially = useRef(false);
  const [open, setOpen] = useState(false), [highlight, setHighlight] = useState("");
  const enabled = options.filter(option => !option.disabled);
  const selected = options.find(option => option.id === value);
  const available = active && !disabled;
  const close = (restore = false) => { setOpen(false); if (restore) trigger.current?.focus(); };
  const choose = (option: ScrollPickerOption) => {
    if (!available || option.disabled) return;
    close(true); change(option.id);
  };
  useLayoutEffect(() => { if (autoFocus && available && !focusedInitially.current) { focusedInitially.current = true; trigger.current?.focus(); } }, [autoFocus, available]);
  useEffect(() => { if (!available) setOpen(false); }, [available]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!container.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  useLayoutEffect(() => {
    if (open && highlight) [...root.current?.querySelectorAll<HTMLElement>("[data-picker-id]") ?? []].find(node => node.dataset.pickerId === highlight)?.scrollIntoView?.({ block: "nearest" });
  }, [open, highlight]);
  const reveal = () => { if (available) { setOpen(true); setHighlight(enabled.find(option => option.id === value)?.id ?? enabled[0]?.id ?? ""); } };
  return <div ref={container} className="scroll-picker" onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close(); }} onKeyDown={event => {
    if (event.key === "Escape" && open) { event.preventDefault(); event.stopPropagation(); close(true); return; }
    if (event.key === "Tab") return;
    if (!available) return;
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault(); setOpen(true);
      const index = enabled.findIndex(option => option.id === highlight);
      const next = event.key === "Home" ? 0 : event.key === "End" ? enabled.length - 1 : event.key === "ArrowDown" ? Math.min(index + 1, enabled.length - 1) : index < 0 ? enabled.length - 1 : Math.max(index - 1, 0);
      setHighlight(enabled[next]?.id ?? "");
    } else if ((event.key === "Enter" || event.key === " ") && event.target === trigger.current) {
      event.preventDefault();
      const option = options.find(option => option.id === highlight);
      if (open && option) choose(option); else reveal();
    }
  }}>
    <span id={`${id}-label`}>{label}{markRequired ? <span className="agent-required" aria-hidden="true"> *</span> : null}</span>
    <button ref={trigger} type="button" role="combobox" aria-labelledby={`${id}-label`} aria-expanded={open} aria-controls={`${id}-choices`} aria-haspopup="listbox" aria-required={required || undefined} aria-activedescendant={open && highlight ? `${id}-option-${options.findIndex(option => option.id === highlight)}` : undefined} disabled={!available} data-value={value} onClick={() => open ? close() : reveal()}>{selected?.label ?? selectedLabel ?? placeholder}</button>
    {required ? <input hidden aria-hidden="true" tabIndex={-1} required value={value} onChange={() => {}} disabled={!available} onInvalid={event => { event.preventDefault(); trigger.current?.focus(); reveal(); }} /> : null}
    {open ? <div ref={root} className="scroll-picker-popup" id={`${id}-choices`} role="listbox" aria-labelledby={`${id}-label`}>
      {options.map((option, index) => <button key={option.id} id={`${id}-option-${index}`} data-picker-id={option.id} type="button" role="option" aria-selected={value === option.id} disabled={option.disabled} tabIndex={-1} data-highlighted={highlight === option.id} onMouseDown={event => event.preventDefault()} onPointerMove={() => { if (!option.disabled) setHighlight(option.id); }} onClick={() => choose(option)}>{option.label}</button>)}
      <ScrollContinuation query={query} label={label} root={root} active={available && open} />
    </div> : null}
  </div>;
}
