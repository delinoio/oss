import { useEffect, useRef, type ReactNode } from "react";

export function Dialog({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    returnFocus.current = document.activeElement as HTMLElement | null;
    const node = dialog.current;
    node?.showModal();
    (node?.querySelector<HTMLElement>("[data-autofocus]") ?? node)?.focus();
    return () => returnFocus.current?.focus();
  }, []);

  return (
    <dialog
      aria-labelledby="dialog-title"
      className="dialog"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      ref={dialog}
      tabIndex={-1}
    >
      <section>
        <h2 id="dialog-title">{title}</h2>
        {children}
      </section>
    </dialog>
  );
}
