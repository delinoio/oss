import { useLayoutEffect, useId, useRef, type ReactNode, type RefObject, type ComponentPropsWithRef } from "react";
import { clientFailure, type ClientFailure } from "@delinoio/delidev-api-client";

export function Problem({ error }: { error: unknown }) {
  if (!error) return null;
  const failure = clientFailure(error);
  return <Failure failure={failure} />;
}
export function Failure({ failure }: { failure?: ClientFailure }) {
  if (!failure) return null;
  return <div role="alert" className="problem"><strong>{failure.message}</strong><p>{failure.guidance}</p>{failure.correlationId ? <small>Reference: {failure.correlationId}</small> : null}</div>;
}
// Shared native surface; presentation owners control opening and focus lifetime.
export function DialogSurface(props: ComponentPropsWithRef<"dialog">) {
  return <dialog {...props} onCancel={event => { event.preventDefault(); props.onCancel?.(event); }} />;
}
export function Modal({ title, close, children, visible = true, className, initialFocus, trapFocus = false }: { title: string; close: () => void; children: ReactNode; visible?: boolean; className?: string; initialFocus?: RefObject<HTMLElement | null>; trapFocus?: boolean }) {
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    if (!visible) return;
    const opener = document.activeElement as HTMLElement | null;
    const dialog = ref.current!;
    dialog.showModal();
    initialFocus?.current?.focus();
    return () => { dialog.close(); if (opener?.isConnected) opener.focus(); };
  }, [visible, initialFocus]);
  return <DialogSurface ref={ref} className={className} aria-labelledby={id} onCancel={(event) => { event.preventDefault(); close(); }} onKeyDown={event => {
    if (!trapFocus || event.key !== "Tab") return;
    // Some desktop browser hosts include their chrome in the native modal's
    // Tab cycle. This opt-in boundary keeps repository actions in the dialog.
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>("button,input,select,textarea,a[href],[tabindex]")].filter(node => node.tabIndex >= 0 && !node.matches(":disabled") && node.getClientRects().length > 0 && !node.closest("[hidden],[inert]"));
    const first = controls[0], last = controls.at(-1);
    if (!first || !last) return;
    const focus = document.activeElement;
    if (!event.currentTarget.contains(focus) || (event.shiftKey ? focus === first : focus === last)) { event.preventDefault(); (event.shiftKey ? last : first).focus(); }
  }}>
    <header><h2 id={id}>{title}</h2><button onClick={close} aria-label={`Close ${title}`}>Close</button></header>
    {children}
  </DialogSurface>;
}
export function More({ available, busy, load }: { available: boolean; busy: boolean; load: () => void }) {
  return available ? <button disabled={busy} onClick={load}>{busy ? "Loading…" : "Load more"}</button> : null;
}
