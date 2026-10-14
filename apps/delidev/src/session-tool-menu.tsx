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

const enabledControls = (root: ParentNode | null | undefined) => [
  ...(root?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []),
];

/**
 * Hover-only reason for a disabled tool row. The pointer target is a
 * noninteractive wrapper because disabled buttons do not receive pointer
 * events in every engine; the button receives the same reason through
 * aria-describedby. The tooltip is positioned inside the row so the dialog's
 * scrolling and narrow or zoomed viewports never clip it, and it is never
 * shown from keyboard focus because the disabled button is not focusable.
 */
export function SessionToolHint({
  reason,
  children,
}: {
  reason: string;
  children: (describedBy: string) => ReactNode;
}) {
  const id = useId();
  const [hovered, setHovered] = useState(false);
  return (
    <div
      role="presentation"
      className="session-tool-hint"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      {children(id)}
      <span
        id={id}
        role="tooltip"
        className="session-tool-tooltip"
        data-visible={hovered ? "true" : "false"}
      >
        {reason}
      </span>
    </div>
  );
}

/**
 * Compact tool dialog shared by the Open tool button and the OpenTool shortcut.
 * Rows keep their original callbacks; this dialog only owns presentation, focus
 * and the shortcut registration. Opening performs no tool action.
 */
export function SessionToolMenu({
  children,
  active: surfaceActive,
  ready = true,
  recoveryFocus,
}: {
  children: ReactNode;
  /** Whether the Session is the selected, visible Sessions surface. */
  active: boolean;
  /**
   * Whether the connection is ready. Losing readiness retires the presentation
   * like Session departure; regaining it never reopens the dialog.
   */
  ready?: boolean;
  /**
   * Safe recovery control that receives focus when readiness is lost while the
   * Session stays selected. A modal close natively restores focus to the
   * opener, which is unavailable during recovery.
   */
  recoveryFocus?: () => HTMLElement | null | undefined;
}) {
  useLocale();
  const active = surfaceActive && ready;
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
    // Close synchronously so a selected row opens its destination afterwards.
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
      if (!active && open) {
        setOpen(false);
        // The previous cleanup's modal close made the browser restore focus to
        // the opener, which is unavailable while readiness is lost. Hand focus
        // to a recovery control; Session departure never refocuses hidden content.
        if (surfaceActive && !ready) {
          const target = recoveryFocus?.();
          if (availableShortcutTarget(target ?? null))
            target?.focus({ preventScroll: true });
          else if (
            document.activeElement === trigger.current ||
            document.activeElement === opener.current
          )
            (document.activeElement as HTMLElement).blur();
        }
      }
      return;
    }
    node.showModal();
    (
      enabledControls(menu.current)[0] ??
      node.querySelector<HTMLButtonElement>("header button")
    )?.focus();
    // Departure and disposal retire the presentation without restoring a hidden opener.
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
            const rows = enabledControls(menu.current);
            const current = rows.indexOf(
              document.activeElement as HTMLButtonElement,
            );
            const next =
              event.key === "Home"
                ? 0
                : event.key === "End"
                  ? rows.length - 1
                  : event.key === "ArrowDown"
                    ? (current + 1) % rows.length
                    : event.key === "ArrowUp"
                      ? current < 0
                        ? rows.length - 1
                        : (current + rows.length - 1) % rows.length
                      : undefined;
            if (next !== undefined) {
              event.preventDefault();
              event.stopPropagation();
              rows[next]?.focus();
            }
            if (event.key === "Tab") {
              // Contain Tab within the close button and the enabled rows.
              event.preventDefault();
              event.stopPropagation();
              const controls = enabledControls(event.currentTarget);
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
              // Close first; the row's original callback then owns destination focus.
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
