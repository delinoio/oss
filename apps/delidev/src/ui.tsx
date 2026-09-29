import { useEffect, useId, useRef, type ReactNode } from "react";
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
export enum ModalLayout { Standard = "standard", FullWindow = "full-window" }

export function Modal({ title, close, children, visible = true, layout = ModalLayout.Standard }: { title: string; close: () => void; children: ReactNode; visible?: boolean; layout?: ModalLayout }) {
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (!visible) return;
    const opener = document.activeElement as HTMLElement | null;
    const dialog = ref.current!;
    dialog.showModal();
    return () => { dialog.close(); if (opener?.isConnected) opener.focus(); };
  }, [visible]);
  return <dialog ref={ref} className={layout === ModalLayout.FullWindow ? "modal-full-window" : undefined} aria-labelledby={id} onCancel={(event) => { event.preventDefault(); close(); }}>
    {layout === ModalLayout.FullWindow
      ? <header className="modal-header-full-window"><h2 id={id}>{title}</h2><button className="modal-full-window-close" onClick={close} aria-label={`Close ${title}`}><span aria-hidden="true">×</span><span>Close</span><kbd aria-hidden="true">Esc</kbd></button></header>
      : <header><h2 id={id}>{title}</h2><button onClick={close} aria-label={`Close ${title}`}>Close</button></header>}
    {children}
  </dialog>;
}
export function More({ available, busy, load }: { available: boolean; busy: boolean; load: () => void }) {
  return available ? <button disabled={busy} onClick={load}>{busy ? "Loading…" : "Load more"}</button> : null;
}
