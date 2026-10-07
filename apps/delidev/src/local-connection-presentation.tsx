// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useContext, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { copy, useLocale } from "./localization";

interface PresentationOwner { target?: HTMLElement; inline: boolean; helpTarget?: HTMLElement; requestInline?: (target: HTMLElement) => void; release?: (target: HTMLElement) => void }
const Presentation = createContext<PresentationOwner>({ inline: true });
export function LocalConnectionPresentationProvider({ target, inline, children, onRequest }: { onRequest?: (target: HTMLElement | undefined) => void; target?: HTMLElement; inline: boolean; children: ReactNode }) {
  const [helpTarget, setHelpTarget] = useState<HTMLElement>();
  const callback = useRef(onRequest); callback.current = onRequest;
  const ownerTarget = useRef<HTMLElement | undefined>(undefined);
  const requestInline = useCallback((slot: HTMLElement) => { ownerTarget.current = slot; setHelpTarget(slot); callback.current?.(slot); }, []);
  const release = useCallback((slot: HTMLElement) => {
    if (ownerTarget.current !== slot) return;
    // Release presentation only; never cancel or replay retained native work.
    ownerTarget.current = undefined;
    setHelpTarget(undefined);
    callback.current?.(undefined);
  }, []);
  return <Presentation.Provider value={{ target, inline, helpTarget, requestInline, release }}>{children}</Presentation.Provider>;
}
// Move only the view. The original controller stays mounted in its authenticated
// mutation scope when diagnostics opens or closes, retaining confirmations.
export function LocalConnectionPresentation({ children, diagnosticsOnly = false }: { children: ReactNode; diagnosticsOnly?: boolean }) {
  const { target, inline, helpTarget } = useContext(Presentation);
  const destination = diagnosticsOnly ? target : helpTarget ?? target;
  return destination ? createPortal(children, destination) : <div hidden={!inline || diagnosticsOnly}>{children}</div>;
}

// The slot belongs to the invoking task, including an existing modal's top layer.
// Opening it neither probes credentials nor repairs the connection.
export function LocalConnectionHelp() {
  useLocale();
  const { requestInline, release } = useContext(Presentation);
  const slot = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => { const original = slot.current; return () => { if (original) release?.(original); }; }, [release]);
  return requestInline ? <><button type="button" onClick={() => { if (slot.current) requestInline(slot.current); }}>{copy("desktop.connectionControls_6f99ea")}</button><div ref={slot} /></> : null;
}
