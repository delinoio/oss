// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { copy, useLocale } from "./localization";

/** Tool entry gestures share the original callbacks; this popup owns only focus. */
export function SessionToolMenu({ children, active }: { children: ReactNode; active: boolean }) {
  useLocale();
  const id = useId(), root = useRef<HTMLDivElement>(null), trigger = useRef<HTMLButtonElement>(null), menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => { if (!active) setOpen(false); }, [active]);
  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const rect = trigger.current?.getBoundingClientRect(), popup = menu.current;
      if (!rect || !popup) return;
      const below = Math.max(40, window.innerHeight - rect.bottom - 14), above = Math.max(40, rect.top - 14);
      const flipped = below < 200 && above > below;
      popup.classList.toggle("is-above", flipped);
      popup.style.maxHeight = `${flipped ? above : below}px`;
    };
    place();
    menu.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  return <div className="session-tool-popup" ref={root} onKeyDown={event => {
    if (!open || event.nativeEvent.isComposing) return;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setOpen(false); trigger.current?.focus(); return; }
    const entries = [...(menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    const current = entries.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1 : event.key === "ArrowDown" ? (current + 1) % entries.length : event.key === "ArrowUp" ? (current + entries.length - 1) % entries.length : undefined;
    if (next !== undefined) { event.preventDefault(); event.stopPropagation(); entries[next]?.focus(); }
    if (event.key === "Tab") setOpen(false);
  }}>
    <button type="button" ref={trigger} aria-haspopup="menu" aria-expanded={open} aria-controls={id} onClick={() => setOpen(value => !value)}>{copy("session.openTool")}</button>
    <div ref={menu} id={id} className="session-tool-menu" role="menu" aria-label={copy("session.openTool")} hidden={!open} onClick={event => {
      if (!(event.target instanceof Element) || !event.target.closest('button:not(:disabled)')) return;
      setOpen(false);
    }}>{children}</div>
  </div>;
}
