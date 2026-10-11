// SPDX-License-Identifier: Apache-2.0
import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { copy, useLocale } from "./localization";
import { SessionIcon, SessionIconKind } from "./session-presentation";
import { DialogSurface } from "./ui";
import { useShortcuts } from "./shortcut-provider";
import { useShortcutPreferences } from "./shortcut-preference-controller";
import { suppressedShortcutReason } from "./shortcut-preferences";
import {
  availableShortcutTarget,
  ShortcutId,
  ShortcutInput,
  shortcutModalVisible,
} from "./shortcuts";
import { Surface } from "./surface";

/** Both entry gestures share one modal; original tool controllers stay mounted. */
export function SessionToolMenu({
  children,
  active,
}: {
  children: ReactNode;
  active: boolean;
}) {
  useLocale();
  const id = useId(),
    trigger = useRef<HTMLButtonElement>(null),
    dialog = useRef<HTMLDialogElement>(null),
    menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLElement | null>(null),
    mounted = useRef(false),
    actionDismissal = useRef(false),
    currentActive = useRef(active);
  currentActive.current = active;
  const { snapshot } = useShortcutPreferences();
  const suppression = suppressedShortcutReason(
    ShortcutId.OpenTool,
    snapshot.overrides,
  );
  const show = (source?: HTMLElement) => {
    if (!active || open || shortcutModalVisible()) return;
    actionDismissal.current = false;
    opener.current =
      source ??
      (document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null);
    setOpen(true);
  };
  const shortcuts = useShortcuts([
    {
      id: ShortcutId.OpenTool,
      scope: Surface.Sessions,
      label: "session.openTool",
      bindings: [{ key: "t", primary: true }],
      input: ShortcutInput.Allow,
      active,
      run: () => show(),
    },
  ]);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    if (!active) setOpen(false);
  }, [active]);
  useLayoutEffect(() => {
    if (!open || !active) return;
    const node = dialog.current!;
    node.showModal();
    menu.current
      ?.querySelector<HTMLButtonElement>("button:not(:disabled)")
      ?.focus();
    return () => {
      node.close();
      if (actionDismissal.current || !currentActive.current || !mounted.current)
        return;
      if (availableShortcutTarget(opener.current))
        opener.current?.focus({ preventScroll: true });
    };
  }, [open, active]);
  const close = () => setOpen(false);
  return (
    <div className="session-tool-popup">
      <button
        type="button"
        ref={trigger}
        aria-label={copy("session.openTool")}
        title={copy("session.openTool")}
        aria-haspopup="dialog"
        aria-expanded={open && active}
        aria-controls={id}
        aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenTool)}
        aria-describedby={suppression ? `${id}-priority` : undefined}
        onClick={(event) => show(event.currentTarget)}
      >
        <SessionIcon kind={SessionIconKind.Tools} />
      </button>
      {suppression ? (
        <span id={`${id}-priority`} className="session-tool-priority">
          {copy(suppression)}
        </span>
      ) : null}
      {createPortal(
        <DialogSurface
          ref={dialog}
          id={id}
          className="session-tool-dialog"
          hidden={!open || !active}
          aria-modal="true"
          aria-labelledby={`${id}-title`}
          onCancel={(event) => {
            event.stopPropagation();
            close();
          }}
          onClick={(event) => {
            if (event.target !== event.currentTarget) return;
            const rect = event.currentTarget.getBoundingClientRect();
            if (
              event.clientX < rect.left ||
              event.clientX > rect.right ||
              event.clientY < rect.top ||
              event.clientY > rect.bottom
            )
              close();
          }}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing || event.keyCode === 229) return;
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              close();
              return;
            }
            if (event.key === "Tab") {
              const controls = [
                ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
                  "button:not(:disabled)",
                ),
              ];
              const index = controls.indexOf(
                document.activeElement as HTMLButtonElement,
              );
              event.preventDefault();
              event.stopPropagation();
              controls[
                index < 0
                  ? event.shiftKey
                    ? controls.length - 1
                    : 0
                  : (index + (event.shiftKey ? controls.length - 1 : 1)) %
                    controls.length
              ]?.focus();
              return;
            }
            const entries = [
              ...(menu.current?.querySelectorAll<HTMLButtonElement>(
                "button:not(:disabled)",
              ) ?? []),
            ];
            const current = entries.indexOf(
              document.activeElement as HTMLButtonElement,
            );
            const next =
              event.key === "Home"
                ? 0
                : event.key === "End"
                  ? entries.length - 1
                  : event.key === "ArrowDown"
                    ? (current + 1) % entries.length
                    : event.key === "ArrowUp"
                      ? current < 0
                        ? entries.length - 1
                        : (current + entries.length - 1) % entries.length
                      : undefined;
            if (next !== undefined) {
              event.preventDefault();
              event.stopPropagation();
              entries[next]?.focus();
            }
          }}
        >
          <header>
            <h2 id={`${id}-title`}>{copy("session.openTool")}</h2>
            <button
              type="button"
              aria-label={copy("ui.close_0fbe2a", {
                v0: copy("session.openTool"),
              })}
              onClick={close}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="m6 6 12 12M18 6 6 18" />
              </svg>
            </button>
          </header>
          <div
            ref={menu}
            className="session-tool-menu"
            role="menu"
            aria-label={copy("session.openTool")}
            onClickCapture={(event) => {
              const button =
                event.target instanceof Element
                  ? event.target.closest("button")
                  : null;
              if (!button || !event.currentTarget.contains(button)) return;
              if (!active || !dialog.current?.open || button.disabled) {
                event.preventDefault();
                event.stopPropagation();
                return;
              }
              // Close the native modal before the original callback. Keep its
              // children mounted and never restore the opener over destination focus.
              actionDismissal.current = true;
              dialog.current.close();
              setOpen(false);
            }}
          >
            {children}
          </div>
        </DialogSurface>,
        document.body,
      )}
    </div>
  );
}
