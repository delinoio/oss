// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { copy, useLocale } from "./localization";
import "./connections-page.css";

export interface ConnectionPageSlots { current: HTMLElement; saved?: HTMLElement; advanced: HTMLElement }
// This host moves without changing portal identity. Controllers and their exact
// pending requests remain owned by the desktop when the Settings page departs.
export function PersistentConnectionView({ target, children, hidden = false }: { target?: HTMLElement; children: ReactNode; hidden?: boolean }) {
  const [host] = useState(() => document.createElement("div"));
  const fallback = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const focused = host.contains(document.activeElement) ? document.activeElement as HTMLElement : undefined;
    const destination = target?.isConnected ? target : fallback.current;
    if (destination && host.parentElement !== destination) destination.append(host);
    if (focused && destination && !destination.closest("[hidden], [inert], dialog:not([open])")) focused.focus({ preventScroll: true });
    else if (focused) focused.blur();
  }, [target, hidden, host]);
  useLayoutEffect(() => () => host.remove(), [host]);
  return <><div ref={fallback} hidden={hidden} />{createPortal(children, host)}</>;
}

export function ConnectionsPage({ onSlots, local = false }: { onSlots: (slots: ConnectionPageSlots | undefined) => void; local?: boolean }) {
  useLocale();
  const current = useRef<HTMLDivElement>(null), saved = useRef<HTMLDivElement>(null), advanced = useRef<HTMLDivElement>(null);
  const [attention, setAttention] = useState(false);
  useLayoutEffect(() => {
    if (!current.current || !advanced.current) return;
    onSlots({ current: current.current, saved: saved.current ?? undefined, advanced: advanced.current });
    const observe = () => setAttention(Boolean(advanced.current?.querySelector('[role="alert"], [data-connection-attention="true"]')));
    const observer = new MutationObserver(observe);
    observer.observe(advanced.current, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-connection-attention", "role"] });
    observe();
    return () => { observer.disconnect(); onSlots(undefined); };
  }, [onSlots]);
  return <div className="connections-page">
    <section data-settings-search-target="current-connection" aria-label={copy("settings.connections.current")}><h2>{copy("settings.connections.current")}</h2><div className="connections-current" ref={current} /></section>
    {local ? <div ref={saved} /> : null}
    {attention ? <p className="connections-attention" role="status">{copy("settings.connections.attention")}</p> : null}
    <Disclosure density={DisclosureDensity.Settings} className="connections-advanced"><DisclosureSummary>{copy("settings.connections.advanced")}</DisclosureSummary><p>{copy("settings.connections.advancedHelp")}</p><div ref={advanced} /></Disclosure>
  </div>;
}
