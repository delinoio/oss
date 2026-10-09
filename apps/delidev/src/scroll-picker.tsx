// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ScrollContinuation, type ScrollContinuationQuery } from "./scroll-continuation";
import "./scroll-picker.css";
import { pickerOverlayGeometry, pickerSurfaceBounds, pickerSurfaceOwner } from "./picker-overlay";

export interface ScrollPickerOption { id: string; label: string; disabled?: boolean; decoration?: ReactNode }

/** Choices are display projections. The domain owns exact-ID selection,
 * authoritative reads and any resulting configuration change. */
export function ScrollPicker({ options, label, value, change, query, active, disabled = false, required = false, autoFocus = false, placeholder = "", selectedLabel, selectedDecoration, markRequired = false }: {
  options: ScrollPickerOption[]; label: string; value: string; change: (id: string) => void;
  query: ScrollContinuationQuery; active: boolean; disabled?: boolean; required?: boolean;
  autoFocus?: boolean; placeholder?: string; selectedLabel?: string; selectedDecoration?: ReactNode; markRequired?: boolean;
}) {
  const display = (label: string, decoration?: ReactNode) => decoration === undefined ? label : <span className="scroll-picker-content"><span className="scroll-picker-decoration" aria-hidden="true">{decoration}</span><span>{label}</span></span>;
  const id = useId(), trigger = useRef<HTMLButtonElement>(null), root = useRef<HTMLDivElement>(null), container = useRef<HTMLDivElement>(null);
  const focusedInitially = useRef(false), showing = useRef(false), usable = useRef(false);
  const [open, setOpen] = useState(false), [highlight, setHighlight] = useState("");
  const [scrollRoot, setScrollRoot] = useState<HTMLDivElement | null>(null);
  const attachRoot = useCallback((node: HTMLDivElement | null) => { root.current = node; setScrollRoot(node); }, []);
  const enabled = options.filter(option => !option.disabled);
  const selected = options.find(option => option.id === value);
  const available = active && !disabled;
  usable.current = available; showing.current = open;
  const close = (restore = false) => { showing.current = false; setOpen(false); if (restore) trigger.current?.focus({ preventScroll: true }); };
  const choose = (option: ScrollPickerOption) => {
    if (!usable.current || !showing.current || option.disabled || !trigger.current?.isConnected || trigger.current.closest("[inert], [hidden]") || trigger.current.closest("dialog")?.open === false) return;
    close(true); change(option.id);
  };
  useLayoutEffect(() => { if (autoFocus && available && !focusedInitially.current) { focusedInitially.current = true; trigger.current?.focus(); } }, [autoFocus, available]);
  useEffect(() => { if (!available) setOpen(false); }, [available]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!container.current?.contains(event.target as Node)) close(); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  useLayoutEffect(() => {
    if (!open || !available || !root.current || !trigger.current) return;
    const popup = root.current, opener = trigger.current;
    const position = () => {
      const dialog = opener.closest("dialog");
      if (!opener.isConnected || !usable.current || opener.closest("[inert], [hidden]") || opener.matches(":disabled") || dialog && !dialog.open) { close(); return false; }
      const bounds = pickerSurfaceBounds(opener);
      // Top-layer coordinates remain in CSS layout units under effective zoom.
      // Measure the popup scale instead of assuming ancestor zoom or device DPI.
      // offsetWidth rounds fractional CSS widths and can cause observer oscillation.
      const cssWidth = Number.parseFloat(getComputedStyle(popup).width);
      const scale = cssWidth > 0 ? popup.getBoundingClientRect().width / cssWidth : 1;
      const factor = Number.isFinite(scale) && scale > 0 ? scale : 1;
      const geometry = pickerOverlayGeometry(opener.getBoundingClientRect(), bounds, (popup.scrollHeight + 2) * factor);
      Object.assign(popup.style, { left: `${geometry.left / factor}px`, top: `${geometry.top / factor}px`, width: `${geometry.width / factor}px`, maxHeight: `${geometry.maxHeight / factor}px` });
      return true;
    };
    if (!position()) return;
    popup.showPopover?.();
    position();
    const lifetime = new MutationObserver(position);
    for (let owner: HTMLElement | null = opener; owner; owner = owner.parentElement) lifetime.observe(owner, { attributes: true, attributeFilter: ["open", "inert", "hidden", "disabled"] });
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(position);
    observer?.observe(popup); observer?.observe(opener);
    const surface = pickerSurfaceOwner(opener);
    if (surface) observer?.observe(surface);
    window.addEventListener("resize", position);
    document.addEventListener("scroll", position, true);
    window.visualViewport?.addEventListener("resize", position);
    window.visualViewport?.addEventListener("scroll", position);
    return () => {
      observer?.disconnect(); lifetime.disconnect(); popup.hidePopover?.();
      window.removeEventListener("resize", position);
      document.removeEventListener("scroll", position, true);
      window.visualViewport?.removeEventListener("resize", position);
      window.visualViewport?.removeEventListener("scroll", position);
    };
  }, [open, available]);
  useLayoutEffect(() => {
    if (open && highlight) [...root.current?.querySelectorAll<HTMLElement>("[data-picker-id]") ?? []].find(node => node.dataset.pickerId === highlight)?.scrollIntoView?.({ block: "nearest" });
  }, [open, highlight]);
  const reveal = () => { if (available) { showing.current = true; setOpen(true); setHighlight(enabled.find(option => option.id === value)?.id ?? enabled[0]?.id ?? ""); } };
  return <div ref={container} className="scroll-picker" onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close(); }} onKeyDown={event => {
    if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229 || event.repeat || event.getModifierState("AltGraph")) return;
    if (event.key === "Escape" && open) { event.preventDefault(); event.stopPropagation(); close(true); return; }
    if (event.key === "Tab") return;
    if (!available) return;
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault(); showing.current = true; setOpen(true);
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
    <button ref={trigger} type="button" role="combobox" aria-labelledby={`${id}-label`} aria-expanded={open} aria-controls={`${id}-choices`} aria-haspopup="listbox" aria-required={required || undefined} aria-activedescendant={open && highlight ? `${id}-option-${options.findIndex(option => option.id === highlight)}` : undefined} disabled={!available} data-value={value} onClick={() => open ? close() : reveal()}>{display(selected?.label ?? selectedLabel ?? placeholder, selectedDecoration === undefined ? selected?.decoration : selectedDecoration)}</button>
    {required ? <input hidden aria-hidden="true" tabIndex={-1} required value={value} onChange={() => {}} disabled={!available} onInvalid={event => { event.preventDefault(); trigger.current?.focus(); reveal(); }} /> : null}
    {open ? <div ref={attachRoot} popover="manual" className="scroll-picker-popup" id={`${id}-choices`} role="listbox" aria-labelledby={`${id}-label`}>
      {options.map((option, index) => <button key={option.id} id={`${id}-option-${index}`} data-picker-id={option.id} type="button" role="option" aria-selected={value === option.id} disabled={option.disabled} tabIndex={-1} data-highlighted={highlight === option.id} onMouseDown={event => event.preventDefault()} onPointerMove={() => { if (!option.disabled) setHighlight(option.id); }} onClick={() => choose(option)}>{display(option.label, option.decoration)}</button>)}
      <ScrollContinuation query={query} label={label} root={root} active={available && open && Boolean(scrollRoot)} />
    </div> : null}
  </div>;
}
