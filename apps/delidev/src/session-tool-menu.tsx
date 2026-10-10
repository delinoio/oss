// SPDX-License-Identifier: Apache-2.0
import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal, flushSync } from "react-dom";
import { copy, useLocale } from "./localization";
import { SessionIcon, SessionIconKind } from "./session-presentation";
import { DialogSurface } from "./ui";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput, availableShortcutTarget, shortcutModalVisible } from "./shortcuts";
import { useShortcutPreferences } from "./shortcut-preference-controller";
import { openToolShortcutSuppressed } from "./shortcut-preferences";
import { Surface } from "./surface";

/** Presentation only: retain mounted original tool and ancillary action owners. */
export function SessionToolMenu({ children, ancillary, active }: { children: ReactNode; ancillary?: ReactNode; active: boolean }) {
  useLocale();
  const { snapshot } = useShortcutPreferences();
  const suppressed = openToolShortcutSuppressed(snapshot.overrides);
  const id = useId(), trigger = useRef<HTMLButtonElement>(null), dialog = useRef<HTMLDialogElement>(null), rows = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const activeOwner = useRef(active), actionDismissal = useRef(false), restore = useRef<HTMLElement | null>(null);
  activeOwner.current = active;
  const show = () => { if (active && !open && availableShortcutTarget(trigger.current) && !shortcutModalVisible()) { actionDismissal.current = false; setOpen(true); } };
  const shortcuts = useShortcuts([{ id: ShortcutId.OpenTool, scope: Surface.Sessions, label: "session.openTool", bindings: [{ key: "t", primary: true }], input: ShortcutInput.Allow, active, enabled: active, run: show }]);
  useLayoutEffect(() => {
    if (!active) setOpen(false);
    if (!open || !active) {
      const target = restore.current;
      restore.current = null;
      if (active && !actionDismissal.current && availableShortcutTarget(target)) target?.focus({ preventScroll: true });
      return;
    }
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const node = dialog.current!;
    node.showModal();
    rows.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    return () => {
      node.close();
      if (actionDismissal.current || !activeOwner.current) return;
      // Restore in the next layout setup after React restores commit selection;
      // closed dialogs keep original action controllers mounted.
      restore.current = availableShortcutTarget(opener) ? opener : trigger.current;
    };
  }, [open, active]);
  return <div className="session-tool-popup">
    <button type="button" ref={trigger} disabled={!active} aria-label={copy("session.openTool")} title={copy("session.openTool")} aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenTool) || undefined} aria-description={suppressed ? copy("shortcuts.customToolPriority") : undefined} aria-haspopup="dialog" aria-expanded={open && active} aria-controls={id} onClick={() => { trigger.current?.focus(); show(); }}><SessionIcon kind={SessionIconKind.Tools}/></button>
    {createPortal(<DialogSurface ref={dialog} id={id} className="session-tool-dialog" aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event => { event.stopPropagation(); setOpen(false); }} onClick={event => { event.stopPropagation(); if (event.target === event.currentTarget) setOpen(false); }} onKeyDown={event => {
      event.stopPropagation();
      if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) { if (event.key === "Enter" || event.key === " ") event.preventDefault(); return; }
      if (event.key === "Escape") { event.preventDefault(); setOpen(false); return; }
      const enabled = [...(dialog.current?.querySelectorAll<HTMLElement>('button,input,select,textarea,a[href],summary,[tabindex]') ?? [])].filter(node => node.tabIndex >= 0 && availableShortcutTarget(node));
      const tools = [...(rows.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
      const current = tools.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? tools.length - 1 : event.key === "ArrowDown" ? (current + 1) % tools.length : event.key === "ArrowUp" ? (current < 0 ? tools.length - 1 : (current + tools.length - 1) % tools.length) : undefined;
      if (next !== undefined) { event.preventDefault(); tools[next]?.focus(); }
      if (event.key === "Tab" && enabled.length) {
        const index = enabled.indexOf(document.activeElement as HTMLButtonElement);
        if (event.shiftKey && index <= 0 || !event.shiftKey && (index < 0 || index === enabled.length - 1)) { event.preventDefault(); enabled[event.shiftKey ? enabled.length - 1 : 0]?.focus(); }
      }
    }}>
      <header><h2 id={`${id}-title`}>{copy("session.openTool")}</h2><button type="button" aria-label={copy("ui.close_7d9eb7")} onClick={() => setOpen(false)}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button></header>
      <div className="session-tool-dialog-actions" onClickCapture={event => {
        const target = event.target instanceof Element ? event.target.closest<HTMLButtonElement>('button') : null;
        if (!target || !event.currentTarget.contains(target)) return;
        if (target.disabled || !activeOwner.current || !dialog.current?.open) { event.preventDefault(); event.stopPropagation(); return; }
        // Close the native modal before its already-collected original callback.
        // Keep children mounted so reads/controller identity are not tied to opening.
        actionDismissal.current = true; flushSync(() => setOpen(false));
      }}>
        <div ref={rows} className="session-tool-dialog-rows">{children}</div>
        {ancillary ? <div className="session-tool-dialog-ancillary">{ancillary}</div> : null}
      </div>
    </DialogSurface>, document.body)}
  </div>;
}
