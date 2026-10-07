// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { copy, useLocale } from "./localization";

export enum SessionIconKind {
  Diff = "diff", Files = "files", Terminals = "terminals", Browser = "browser",
  Diagnostics = "diagnostics", Info = "info", Conversation = "conversation", Warning = "warning",
}

const paths: Record<SessionIconKind, ReactNode> = {
  [SessionIconKind.Diff]: <><path d="M5 3h8l4 4v14H5zM13 3v5h4M8 12h6M11 9v6M8 18h6" /></>,
  [SessionIconKind.Files]: <><path d="M5 3h8l4 4v14H5zM13 3v5h4" /></>,
  [SessionIconKind.Terminals]: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 9 3 3-3 3M13 16h4" /></>,
  [SessionIconKind.Browser]: <><circle cx="12" cy="12" r="9" /><ellipse cx="12" cy="12" rx="4" ry="9" /><path d="M3 12h18M5 7h14M5 17h14" /></>,
  [SessionIconKind.Diagnostics]: <path d="M2 12h5l3-8 4 16 3-8h5" />,
  [SessionIconKind.Info]: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7v1" /></>,
  [SessionIconKind.Conversation]: <path d="M21 11a9 9 0 0 1-9 9H7l-5 3 2-6a9 9 0 1 1 17-6ZM8 11h.01M12 11h.01M16 11h.01" />,
  [SessionIconKind.Warning]: <><path d="m12 3 10 18H2zM12 9v5M12 17v1" /></>,
};

export function SessionIcon({ kind }: { kind: SessionIconKind }) {
  return <svg className="session-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">{paths[kind]}</svg>;
}

export function SessionNotice({ children, details }: { children: ReactNode; details: (opener: HTMLButtonElement) => void }) {
  useLocale();
  return <div className="session-notice" role="alert">
    <SessionIcon kind={SessionIconKind.Warning} />
    <div>{children}</div>
    <button type="button" onClick={event => details(event.currentTarget)}>{copy("session.showDetails")}</button>
  </div>;
}

/** The action children stay mounted when the popup closes. */
export function SessionActions({ children }: { children: ReactNode }) {
  useLocale();
  const id = useId();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const body = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (open) body.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  return <div className="session-actions-popup" ref={root} onKeyDown={event => {
    if (event.key === "Escape" && open) {
      event.stopPropagation(); setOpen(false); trigger.current?.focus();
    }
  }}>
    <button ref={trigger} type="button" aria-label={copy("session.moreActions")} aria-expanded={open} aria-controls={id} onClick={() => setOpen(value => !value)}>···</button>
    <div ref={body} id={id} className="session-actions-list" role="group" aria-label={copy("session.moreActions")} hidden={!open} onClick={event => {
      if (!(event.target instanceof Element) || !event.target.closest("button:not(:disabled)")) return;
      // Child dialogs capture this visible opener after the popup is hidden.
      trigger.current?.focus(); setOpen(false);
    }}>{children}</div>
  </div>;
}
