// SPDX-License-Identifier: Apache-2.0
import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { copy, useLocale } from "./localization";
import { SessionIcon, SessionIconKind } from "./session-presentation";
import { useShortcuts } from "./shortcut-provider";
import { availableShortcutTarget, ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { DialogSurface } from "./ui";

/** Tool entry gestures share original callbacks. This modal owns only presentation and focus. */
export function SessionToolMenu({ children, active }: { children: ReactNode; active: boolean }) {
  useLocale();
  const id = useId(), trigger = useRef<HTMLButtonElement>(null), dialog = useRef<HTMLDialogElement>(null), menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const ownerActive = useRef(active), actionDismissal = useRef(false);
  ownerActive.current = active;
  const shortcuts = useShortcuts([
    { id: ShortcutId.SessionOpenTool, scope: Surface.Sessions, label: "session.openTool", bindings: [{ key: "t", primary: true }], input: ShortcutInput.Allow, active, run: () => setOpen(true) },
  ]);
  useLayoutEffect(() => { if (!active) setOpen(false); }, [active]);
  useLayoutEffect(() => {
    if (!open || !active) return;
    const node = dialog.current!;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : trigger.current;
    actionDismissal.current = false;
    node.showModal();
    (menu.current?.querySelector<HTMLButtonElement>('button:not(:disabled)') ?? node.querySelector<HTMLButtonElement>('button'))?.focus();
    return () => {
      node.close();
      if (actionDismissal.current || !ownerActive.current) return;
      const usableOpener = availableShortcutTarget(opener) && (opener!.tabIndex >= 0 || opener!.hasAttribute("tabindex") || opener!.isContentEditable);
      const target = usableOpener ? opener : trigger.current;
      if (availableShortcutTarget(target)) target?.focus({ preventScroll: true });
    };
  }, [open, active]);
  return <div className="session-tool-popup">
    <button type="button" ref={trigger} aria-label={copy("session.openTool")} title={copy("session.openTool")} aria-haspopup="dialog" aria-expanded={open && active} aria-controls={id} aria-keyshortcuts={shortcuts.aria(ShortcutId.SessionOpenTool) || undefined} onClick={() => { if (active) setOpen(true); }}><SessionIcon kind={SessionIconKind.Tools}/></button>
    <DialogSurface ref={dialog} id={id} className="session-tool-dialog" aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event => { event.stopPropagation(); setOpen(false); }} onClick={event => {
      if (event.target !== event.currentTarget) return;
      const rect = event.currentTarget.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) setOpen(false);
    }} onKeyDown={event => {
      if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setOpen(false); return; }
      const entries = [...(menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
      const current = entries.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1 : event.key === "ArrowDown" ? (current + 1) % entries.length : event.key === "ArrowUp" ? (current + entries.length - 1) % entries.length : undefined;
      if (next !== undefined && entries.length) { event.preventDefault(); event.stopPropagation(); entries[next]?.focus(); }
      if (event.key === "Tab") {
        const controls = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')].filter(availableShortcutTarget);
        const index = controls.indexOf(document.activeElement as HTMLButtonElement);
        event.preventDefault();
        controls[(index + (event.shiftKey ? controls.length - 1 : 1)) % controls.length]?.focus();
      }
    }}>
      <header><h2 id={`${id}-title`}>{copy("session.openTool")}</h2><button type="button" aria-label={copy("ui.close_0fbe2a", { v0: copy("session.openTool") })} onClick={() => setOpen(false)}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button></header>
      <div ref={menu} className="session-tool-menu" role="menu" aria-label={copy("session.openTool")} onClickCapture={event => {
        if (!(event.target instanceof Element) || !event.target.closest('button:not(:disabled)')) return;
        // Release the modal before the original row callback; never restore over destination focus.
        actionDismissal.current = true;
        flushSync(() => setOpen(false));
      }}>{children}</div>
    </DialogSurface>
  </div>;
}
