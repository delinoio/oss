import {
  useEffect,
  useRef,
  type KeyboardEvent,
  type ReactNode,
} from "react";

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
  const previousFocusRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    previousFocusRef.current = document.activeElement as HTMLElement | null;
    const firstFocusable =
      panelRef.current?.querySelector<HTMLElement>(focusableSelector);
    firstFocusable?.focus();
    return () => previousFocusRef.current?.focus();
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
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  };

  return (
    // The backdrop is intentionally pointer-dismissible while the dialog panel
    // owns all keyboard interaction and focus containment.
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions
    <div className="dialog-backdrop" onMouseDown={onClose}>
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
      >
        {children}
      </div>
    </div>
  );
}
