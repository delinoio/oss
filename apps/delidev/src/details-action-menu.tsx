// SPDX-License-Identifier: Apache-2.0
import { useCallback, useRef, useState, type ReactNode } from "react";
import {
  AnchoredPopupAlign,
  AnchoredPopupWidth,
  useAnchoredPopup,
  useOutsidePointerDismiss,
} from "./anchored-popup";

/**
 * Row or toolbar action menu built on native details/summary semantics whose
 * actions are an anchored top-layer popup (`useAnchoredPopup`), so opening it
 * never grows its row, wraps a toolbar or moves any following content.
 *
 * The open state is owned here, not by the native toggle, so the popup mounts
 * in the same commit as the summary click. While closed no action is rendered.
 * Escape closes and returns focus to the summary; focus leaving the menu, an
 * outside pointer press or choosing an enabled action dismisses it; opening,
 * closing and positioning perform no business mutation. The consumer keeps
 * every action's own disabled, pending and uncertain guards.
 *
 * `overlay={false}` is for hosts without a persistent owner to overlay (the
 * legacy fallback dialog): the actions stay listed in flow and the details
 * element stays open.
 */
export function DetailsActionMenu({
  overlay = true,
  className,
  popupClassName,
  label,
  children,
}: {
  overlay?: boolean;
  className: string;
  popupClassName: string;
  label: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const owner = useRef<HTMLDetailsElement>(null),
    summary = useRef<HTMLElement>(null),
    popup = useRef<HTMLDivElement>(null);
  const popupOpen = overlay && open;
  const close = useCallback(() => setOpen(false), []);
  useAnchoredPopup({
    open: popupOpen,
    anchor: summary,
    popup,
    onInvalid: close,
    width: AnchoredPopupWidth.Content,
    align: AnchoredPopupAlign.End,
  });
  useOutsidePointerDismiss(popupOpen, [owner], close);
  return (
    <details
      ref={owner}
      className={className}
      open={overlay ? open : true}
      onBlur={(event) => {
        if (
          overlay &&
          !event.currentTarget.contains(event.relatedTarget as Node | null)
        )
          close();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          close();
          summary.current?.focus();
        }
      }}
    >
      <summary
        ref={summary}
        aria-label={label}
        onClick={(event) => {
          if (!overlay) return;
          // The native toggle would arrive a task later than the popup needs.
          event.preventDefault();
          setOpen((value) => !value);
        }}
      >
        ⋯
      </summary>
      {popupOpen ? (
        <div
          ref={popup}
          popover="manual"
          className={`anchored-popup ${popupClassName}`}
          onClick={(event) => {
            // Any enabled action hands off to its own task or dialog.
            const action = (event.target as Element).closest("button");
            if (action && !action.disabled) close();
          }}
        >
          {children}
        </div>
      ) : overlay ? null : (
        <div>{children}</div>
      )}
    </details>
  );
}
