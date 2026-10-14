import {
  useEffect,
  useId,
  useRef,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
} from "react";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

export interface ModalProps {
  title: string;
  onClose: () => void;
  children: ReactNode;
  /** Optional footer, usually the dialog actions. */
  footer?: ReactNode;
  wide?: boolean;
  /**
   * Element that receives focus when the dialog opens, for dialogs whose most
   * important control is not the first focusable one.
   */
  initialFocus?: RefObject<HTMLElement | null>;
}

/**
 * Accessible modal dialog. Mount it only while open: on mount focus moves into
 * the dialog, Tab is trapped, Escape closes it, and on unmount focus returns to
 * the element that was focused before it opened.
 */
export function Modal({
  title,
  onClose,
  children,
  footer,
  wide,
  initialFocus,
}: ModalProps) {
  const titleId = useId();
  const ref = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const previous =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const dialog = ref.current;
    const first = dialog?.querySelector<HTMLElement>(FOCUSABLE);
    (initialFocus?.current ?? first ?? dialog)?.focus();
    return () => {
      if (previous && previous.isConnected) previous.focus();
    };
  }, []);

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.stopPropagation();
      onCloseRef.current();
      return;
    }
    if (event.key !== "Tab" || !ref.current) return;
    const items = Array.from(
      ref.current.querySelectorAll<HTMLElement>(FOCUSABLE),
    );
    if (items.length === 0) {
      event.preventDefault();
      ref.current.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || active === ref.current)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onCloseRef.current();
      }}
    >
      <div
        ref={ref}
        className={wide ? "modal modal-wide" : "modal"}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        onKeyDown={onKeyDown}
      >
        <div className="modal-header">
          <h2 id={titleId}>{title}</h2>
          <button
            type="button"
            className="button-ghost"
            aria-label="Close dialog"
            onClick={() => onCloseRef.current()}
          >
            ✕
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer ? <div className="modal-footer">{footer}</div> : null}
      </div>
    </div>
  );
}
