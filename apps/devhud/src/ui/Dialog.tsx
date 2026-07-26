import {
  useEffect,
  useLayoutEffect,
  useRef,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";

const focusableSelector = [
  "button:not(:disabled)",
  "[href]",
  "input:not(:disabled)",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  "[tabindex]:not([tabindex='-1'])",
].join(", ");

export function Dialog({
  title,
  descriptionId,
  onClose,
  children,
}: {
  title: string;
  descriptionId?: string;
  onClose(): void;
  children: ReactNode;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocus = useRef<HTMLElement | null>(
    document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );

  useEffect(() => {
    const focusBeforeDialog = previousFocus.current;
    return () => {
      focusBeforeDialog?.focus();
    };
  }, []);

  useLayoutEffect(() => {
    const dialog = dialogRef.current;
    if (dialog === null) return;
    const firstControl = dialog.querySelector<HTMLElement>(focusableSelector);
    if (firstControl === null || !dialog.contains(document.activeElement)) {
      (firstControl ?? dialog).focus();
    }
  });

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const dialog = dialogRef.current;
    if (dialog === null) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const controls = [
      ...dialog.querySelectorAll<HTMLElement>(focusableSelector),
    ];
    const first = controls[0];
    const last = controls.at(-1);
    if (first === undefined) {
      event.preventDefault();
      event.stopPropagation();
      dialog.focus();
      return;
    }
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      event.stopPropagation();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      event.stopPropagation();
      first?.focus();
    }
  };

  return (
    <div className="dialog-backdrop" role="presentation">
      {/* The modal dialog owns keyboard focus and handles its Tab/Escape contract. */}
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions */}
      <div
        ref={dialogRef}
        aria-describedby={descriptionId}
        aria-label={title}
        aria-modal="true"
        className="dialog"
        data-devhud-modal="true"
        onKeyDown={onKeyDown}
        role="dialog"
        tabIndex={-1}
      >
        {children}
      </div>
    </div>
  );
}
