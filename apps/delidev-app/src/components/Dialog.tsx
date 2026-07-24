import {
  useEffect,
  useRef,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

const focusableSelector =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function Dialog({
  children,
  descriptionId,
  onClose,
  titleId,
}: {
  children: ReactNode;
  descriptionId?: string;
  onClose: () => void;
  titleId: string;
}) {
  const panelRef = useRef<HTMLDivElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    previousFocusRef.current = document.activeElement as HTMLElement | null;
    const background = [...document.body.children].filter(
      (element): element is HTMLElement =>
        element instanceof HTMLElement && element !== backdropRef.current,
    );
    const previousBackgroundState = background.map((element) => ({
      ariaHidden: element.getAttribute("aria-hidden"),
      element,
      inert: element.inert,
    }));
    for (const element of background) {
      element.inert = true;
      element.setAttribute("aria-hidden", "true");
    }
    const firstFocusable =
      panelRef.current?.querySelector<HTMLElement>(focusableSelector);
    (firstFocusable ?? panelRef.current)?.focus();
    return () => {
      for (const state of previousBackgroundState) {
        state.element.inert = state.inert;
        if (state.ariaHidden === null) {
          state.element.removeAttribute("aria-hidden");
        } else {
          state.element.setAttribute("aria-hidden", state.ariaHidden);
        }
      }
      previousFocusRef.current?.focus();
    };
  }, []);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key !== "Tab" || !panelRef.current) {
      return;
    }
    const focusable = [
      ...panelRef.current.querySelectorAll<HTMLElement>(focusableSelector),
    ];
    const first = focusable[0];
    const last = focusable.at(-1);
    if (!first || !last) {
      event.preventDefault();
      panelRef.current.focus();
      return;
    }
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  };

  return createPortal(
    // The backdrop is intentionally pointer-dismissible while the dialog panel
    // owns all keyboard interaction and focus containment.
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions
    <div
      className="dialog-backdrop"
      onMouseDown={onClose}
      ref={backdropRef}
    >
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions */}
      <div
        aria-describedby={descriptionId}
        aria-labelledby={titleId}
        aria-modal="true"
        className="dialog-panel"
        onKeyDown={handleKeyDown}
        onMouseDown={(event) => event.stopPropagation()}
        ref={panelRef}
        role="dialog"
        tabIndex={-1}
      >
        {children}
      </div>
    </div>,
    document.body,
  );
}
