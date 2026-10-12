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
import {
  availableShortcutTarget,
  sessionShortcutBindings,
  ShortcutId,
  ShortcutInput,
} from "./shortcuts";
import { Surface } from "./surface";

/** Tool entry gestures share the original callbacks; this dialog owns only focus. */
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
    menu = useRef<HTMLDivElement>(null),
    opener = useRef<HTMLElement | null>(null);
  const [open, setOpen] = useState(false);
  const openDialog = (
    origin = document.activeElement as HTMLElement | null,
  ) => {
    if (!active || dialog.current?.open) return;
    opener.current = origin;
    setOpen(true);
  };
  const dismiss = (restore = true) => {
    // Close synchronously before the original row callback can open its destination.
    dialog.current?.close();
    setOpen(false);
    if (!restore) return;
    const origin = opener.current;
    const target =
      availableShortcutTarget(origin) &&
      (origin!.tabIndex >= 0 ||
        origin!.hasAttribute("tabindex") ||
        origin!.isContentEditable)
        ? origin
        : trigger.current;
    if (availableShortcutTarget(target)) target?.focus({ preventScroll: true });
  };
  const shortcuts = useShortcuts([
    {
      id: ShortcutId.OpenTool,
      scope: Surface.Sessions,
      label: "session.openTool",
      bindings: sessionShortcutBindings[ShortcutId.OpenTool],
      input: ShortcutInput.Allow,
      active,
      run: () => openDialog(),
    },
  ]);
  useLayoutEffect(() => {
    const node = dialog.current;
    if (!active || !open || !node) {
      node?.close();
      if (!active && open) setOpen(false);
      return;
    }
    node.showModal();
    (
      menu.current?.querySelector<HTMLButtonElement>("button:not(:disabled)") ??
      node.querySelector<HTMLButtonElement>("header button")
    )?.focus();
    // Departure and disposal retire presentation without restoring a hidden opener.
    return () => node.close();
  }, [open, active]);
  return (
    <div className="session-tool-popup">
      <button
        type="button"
        ref={trigger}
        aria-label={copy("session.openTool")}
        title={copy("session.openTool")}
        aria-keyshortcuts={shortcuts.aria(ShortcutId.OpenTool) || undefined}
        aria-haspopup="dialog"
        aria-expanded={open && active}
        aria-controls={id}
        onClick={() => openDialog(trigger.current)}
      >
        <SessionIcon kind={SessionIconKind.Tools} />
      </button>
      {createPortal(
        <DialogSurface
          ref={dialog}
          id={id}
          className="session-tool-dialog"
          aria-labelledby={`${id}-title`}
          onCancel={(event) => {
            event.stopPropagation();
            dismiss();
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
              dismiss();
          }}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing || event.keyCode === 229) return;
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              dismiss();
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
            if (event.key === "Tab") {
              event.preventDefault();
              event.stopPropagation();
              const controls = [
                ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
                  "button:not(:disabled)",
                ),
              ];
              const index = controls.indexOf(
                document.activeElement as HTMLButtonElement,
              );
              const target =
                index < 0
                  ? event.shiftKey
                    ? controls.length - 1
                    : 0
                  : (index + (event.shiftKey ? controls.length - 1 : 1)) %
                    controls.length;
              controls[target]?.focus();
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
              onClick={() => dismiss()}
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
              if (
                !(event.target instanceof Element) ||
                !event.target.closest("button:not(:disabled)")
              )
                return;
              if (!active || !dialog.current?.open) {
                event.preventDefault();
                event.stopPropagation();
                return;
              }
              dismiss(false);
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
