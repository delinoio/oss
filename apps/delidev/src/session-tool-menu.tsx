// SPDX-License-Identifier: Apache-2.0
import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal, flushSync } from "react-dom";
import { copy, useLocale } from "./localization";
import { SessionIcon, SessionIconKind } from "./session-presentation";
import { DialogSurface } from "./ui";
import { availableShortcutTarget, ShortcutId, ShortcutInput, shortcutModalVisible } from "./shortcuts";
import { useShortcuts } from "./shortcut-provider";
import { defaultShortcutSuppressed } from "./shortcut-preferences";
import { useShortcutPreferences } from "./shortcut-preference-controller";
import { Surface } from "./surface";

/** Tool entry gestures share original callbacks; this modal owns only presentation. */
export function SessionToolMenu({ children, active }: { children: ReactNode; active: boolean }) {
  useLocale();
  const id = useId(), trigger = useRef<HTMLButtonElement>(null), dialog = useRef<HTMLDialogElement>(null), menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const activeOwner = useRef(active);
  activeOwner.current = active;
  const selecting = useRef(false), opener = useRef<HTMLElement | null>(null);
  const { snapshot } = useShortcutPreferences();
  const suppressed = defaultShortcutSuppressed(ShortcutId.OpenTool, snapshot.overrides);
  const show = () => {
    if (!active || open || shortcutModalVisible()) return;
    selecting.current = false;
    const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    opener.current = availableShortcutTarget(focused) && (focused!.tabIndex >= 0 || focused!.hasAttribute("tabindex") || focused!.isContentEditable) ? focused : trigger.current;
    setOpen(true);
  };
  const shortcuts = useShortcuts([{ id: ShortcutId.OpenTool, scope: Surface.Sessions, label: "session.openTool", bindings: [{ key: "t", primary: true }], input: ShortcutInput.Allow, active, run: show }]);
  useLayoutEffect(() => { if (!active) setOpen(false); }, [active]);
  useLayoutEffect(() => {
    if (!open || !active) return;
    const node = dialog.current!;
    node.showModal();
    menu.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    return () => {
      node.close();
      if (selecting.current || !activeOwner.current || !availableShortcutTarget(trigger.current)) return;
      const target = availableShortcutTarget(opener.current) ? opener.current : trigger.current;
      if (availableShortcutTarget(target)) target?.focus({ preventScroll: true });
    };
  }, [open, active]);
  return <div className="session-tool-popup">
    <button type="button" ref={trigger} aria-label={copy("session.openTool")} title={suppressed ? `${copy("session.openTool")} — ${copy("shortcuts.customBindingPriority")}` : copy("session.openTool")} aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenTool)} aria-describedby={suppressed ? `${id}-priority` : undefined} aria-haspopup="dialog" aria-expanded={open && active} aria-controls={id} onClick={show}><SessionIcon kind={SessionIconKind.Tools}/></button>
    {suppressed ? <span id={`${id}-priority`} className="session-tool-priority">{copy("shortcuts.customBindingPriority")}</span> : null}
    {createPortal(<DialogSurface ref={dialog} id={id} className="session-tool-dialog" aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event => { event.stopPropagation(); setOpen(false); }} onClick={event => {
      if (event.target !== event.currentTarget) return;
      const rect = event.currentTarget.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) setOpen(false);
    }} onClickCapture={event => {
      if (!(event.target instanceof Element)) return;
      const button = event.target.closest('button');
      if (!button || !menu.current?.contains(button)) return;
      if (!active || !open || button.disabled || selecting.current) { event.preventDefault(); event.stopPropagation(); return; }
      // Release the native inert boundary before any original action callback.
      // Keep children mounted so their resource/controller owners survive closing.
      selecting.current = true;
      flushSync(() => setOpen(false));
    }} onKeyDown={event => {
      if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setOpen(false); return; }
      const entries = [...(menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
      const current = entries.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1 : event.key === "ArrowDown" ? (current + 1) % entries.length : event.key === "ArrowUp" ? (current + entries.length - 1) % entries.length : undefined;
      if (next !== undefined) { event.preventDefault(); event.stopPropagation(); entries[next]?.focus(); }
      if (event.key === "Tab") {
        const controls = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')].filter(node => !node.closest('[hidden],[inert]'));
        const position = controls.indexOf(document.activeElement as HTMLButtonElement);
        event.preventDefault(); event.stopPropagation();
        controls[(position + (event.shiftKey ? controls.length - 1 : 1)) % controls.length]?.focus();
      }
    }}>
      <header><h2 id={`${id}-title`}>{copy("session.openTool")}</h2><button type="button" aria-label={copy("ui.close_0fbe2a", { v0: copy("session.openTool") })} onClick={() => setOpen(false)}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"/></svg></button></header>
      <div ref={menu} className="session-tool-menu" role="menu" aria-label={copy("session.openTool")}>{children}</div>
    </DialogSurface>, document.body)}
  </div>;
}
