import { useLayoutEffect, useId, useRef, type ReactNode, type RefObject } from "react";
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
export function Modal({ title, close, children, visible = true, className, initialFocus }: { title: string; close: () => void; children: ReactNode; visible?: boolean; className?: string; initialFocus?: RefObject<HTMLElement | null> }) {
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
  return <dialog ref={ref} className={className} aria-labelledby={id} onCancel={(event) => { event.preventDefault(); close(); }}>
    <header><h2 id={id}>{title}</h2><button onClick={close} aria-label={`Close ${title}`}>Close</button></header>
    {children}
  </dialog>;
}
export function More({ available, busy, load }: { available: boolean; busy: boolean; load: () => void }) {
  return available ? <button disabled={busy} onClick={load}>{busy ? "Loading…" : "Load more"}</button> : null;
}
