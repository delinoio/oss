import { useEffect, useId, useRef, type ReactNode, type ComponentPropsWithRef } from "react";
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
export function Modal({ title, close, children, visible = true }: { title: string; close: () => void; children: ReactNode; visible?: boolean }) {
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (!visible) return;
    const opener = document.activeElement as HTMLElement | null;
    const dialog = ref.current!;
    dialog.showModal();
    return () => { dialog.close(); if (opener?.isConnected) opener.focus(); };
  }, [visible]);
  return <DialogSurface ref={ref} aria-labelledby={id} onCancel={(event) => { event.preventDefault(); close(); }}>
    <header><h2 id={id}>{title}</h2><button onClick={close} aria-label={`Close ${title}`}>Close</button></header>
    {children}
  </DialogSurface>;
}
export function More({ available, busy, load }: { available: boolean; busy: boolean; load: () => void }) {
  return available ? <button disabled={busy} onClick={load}>{busy ? "Loading…" : "Load more"}</button> : null;
}
