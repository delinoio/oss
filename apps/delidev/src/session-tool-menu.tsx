// SPDX-License-Identifier: Apache-2.0
import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { copy, useLocale } from "./localization";
import { SessionIcon, SessionIconKind } from "./session-presentation";
import { DialogSurface } from "./ui";
import { Surface } from "./surface";
import { useShortcuts } from "./shortcut-provider";
import { availableShortcutTarget, ShortcutId, ShortcutInput, shortcutModalVisible } from "./shortcuts";

/** Original tool callbacks retain resource authority; this dialog owns presentation only. */
export function SessionToolMenu({ children, active, owner }: { children: ReactNode; active: boolean; owner?: string }) {
  useLocale();
  const id = useId(), trigger = useRef<HTMLButtonElement>(null), opener = useRef<HTMLElement | null>(null);
  const [open, setOpen] = useState(false), actionDismissal = useRef(false), alive = useRef(true);
  const eligible = useRef(active); eligible.current = active;
  useLayoutEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useLayoutEffect(() => { actionDismissal.current = true; setOpen(false); }, [active, owner]);
  const show = (source: HTMLElement | null) => {
    if (!active || !availableShortcutTarget(trigger.current) || open || shortcutModalVisible()) return;
    opener.current = source; actionDismissal.current = false; setOpen(true);
  };
  const shortcuts = useShortcuts([{ id: ShortcutId.OpenTool, scope: Surface.Sessions, label: "session.openTool", bindings: [{ key: "t", primary: true }], input: ShortcutInput.Allow, active, run: () => show(document.activeElement instanceof HTMLElement ? document.activeElement : null) }]);
  return <div className="session-tool-popup">
    <button type="button" ref={trigger} aria-label={copy("session.openTool")} title={copy("session.openTool")} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined} aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenTool) || undefined} onClick={() => show(trigger.current)}><SessionIcon kind={SessionIconKind.Tools}/></button>
    {open ? <ToolDialog id={id} close={() => setOpen(false)} selected={() => { actionDismissal.current = true; setOpen(false); }} restore={() => {
      if (!alive.current || !eligible.current || actionDismissal.current) return;
      const original = opener.current;
      const target = availableShortcutTarget(original) && (original!.tabIndex >= 0 || original!.hasAttribute("tabindex") || original!.isContentEditable) ? original : availableShortcutTarget(trigger.current) ? trigger.current : document.querySelector<HTMLElement>("#main");
      if (availableShortcutTarget(target)) target?.focus({ preventScroll: true });
    }}>{children}</ToolDialog> : null}
  </div>;
}
function ToolDialog({ id, children, close, selected, restore }: { id: string; children: ReactNode; close: () => void; selected: () => void; restore: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null), rows = useRef<HTMLDivElement>(null), closeButton = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    const node = dialog.current!; node.showModal();
    (rows.current?.querySelector<HTMLButtonElement>('button:not(:disabled)') ?? closeButton.current)?.focus();
    return () => { node.close(); restore(); };
  }, []);
  return createPortal(<DialogSurface ref={dialog} id={id} className="session-tool-dialog" aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event => { event.stopPropagation(); close(); }} onClick={event => { if (event.target === event.currentTarget) close(); }} onKeyDown={event => {
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); return; }
    const entries = [...(rows.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    const at = entries.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1 : event.key === "ArrowDown" ? (at + 1) % entries.length : event.key === "ArrowUp" ? (at + entries.length - 1) % entries.length : undefined;
    if (next !== undefined) { event.preventDefault(); event.stopPropagation(); entries[next]?.focus(); }
    if (event.key === "Tab") {
      const controls = [closeButton.current, ...entries].filter((node): node is HTMLButtonElement => Boolean(node));
      const current = controls.indexOf(document.activeElement as HTMLButtonElement);
      event.preventDefault(); event.stopPropagation(); controls[(current + (event.shiftKey ? controls.length - 1 : 1)) % controls.length]?.focus();
    }
  }}>
    <header><h2 id={`${id}-title`}>{copy("session.openTool")}</h2><button ref={closeButton} type="button" aria-label={copy("ui.close_0fbe2a", { v0: copy("session.openTool") })} onClick={close}>{copy("ui.close_7d9eb7")}</button></header>
    <div ref={rows} role="menu" aria-label={copy("session.openTool")} onClickCapture={event => {
      if (event.target instanceof Element && event.target.closest('button:not(:disabled)')) {
        // Close the native modal before the original React row callback, while
        // retaining its dispatch target until the current event has completed.
        dialog.current?.close(); selected();
      }
    }}>{children}</div>
  </DialogSurface>, document.body);
}
