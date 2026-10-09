// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
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
export function LocalConnectionPresentation({ children, diagnosticsOnly = false, destination: explicitDestination }: { children: ReactNode; diagnosticsOnly?: boolean; destination?: HTMLElement }) {
  const { target, inline, helpTarget } = useContext(Presentation);
  const [host] = useState(() => document.createElement("div"));
  const fallback = useRef<HTMLDivElement>(null);
  const destination = explicitDestination ?? (diagnosticsOnly ? target : helpTarget ?? target);
  const hidden = !inline || diagnosticsOnly;
  useLayoutEffect(() => {
    const ownedFocus = host.contains(document.activeElement) ? document.activeElement as HTMLElement : undefined;
    const outlet = destination?.isConnected ? destination : fallback.current;
    if (outlet && host.parentElement !== outlet) outlet.append(host);
    if (ownedFocus && outlet && !outlet.closest("[hidden], [inert], dialog:not([open])")) ownedFocus.focus({ preventScroll: true });
    else if (ownedFocus && document.activeElement === ownedFocus) ownedFocus.blur();
  }, [destination, hidden, host]);
  useLayoutEffect(() => () => host.remove(), [host]);
  // The portal identity never changes. Moving its owned host retains every
  // original child controller, including updater drafts and native uncertainty.
  return <><div ref={fallback} hidden={hidden} />{createPortal(children, host)}</>;

}

// The slot belongs to the invoking task, including an existing modal's top layer.
// Opening it neither probes credentials nor repairs the connection.
export function LocalConnectionHelp({ active = true }: { active?: boolean }) {
  useLocale();
  const { requestInline, release } = useContext(Presentation);
  const slot = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => { const original = slot.current; return () => { if (original) release?.(original); }; }, [release, active]);
  return active && requestInline ? <><SettingsActionButton icon={SettingsActionIcon.Connect} type="button" onClick={() => { if (slot.current) requestInline(slot.current); }}>{copy("desktop.connectionControls_6f99ea")}</SettingsActionButton><div ref={slot} /></> : null;
}
