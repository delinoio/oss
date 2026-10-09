// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { BudgetState, EntityKind, ResourceQuery, SessionAction, SessionQuery } from "@delinoio/delidev-api-client";
import { sessionControlEligibility, useSessionControl, validSessionActionResource } from "./session-control";
import { SessionForkAction } from "./session-fork";
import { copy } from "./localization";
import { Problem } from "./ui";

const Context = createContext<{ open: string; setOpen: (id: string) => void }>({ open: "", setOpen: () => undefined });
export function useSidebarActionMenuOwner() { return useContext(Context); }
export function useSessionActionMenuOpen() { return Boolean(useContext(Context).open); }
export function SessionRowActionsProvider({ children, active }: { children: ReactNode; active: boolean }) {
  const [open, setOpen] = useState("");
  useEffect(() => { if (!active) setOpen(""); }, [active]);
  return <Context.Provider value={{ open, setOpen }}>{children}</Context.Provider>;
}
export function SessionRowActions({ id, descriptionId, dismissHover }: { id: string; descriptionId?: string; dismissHover: () => void }) {
  const owner = useContext(Context);
  const visible = owner.open === id;
  const opener = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const group = useRef<HTMLButtonElement | null>(null);
  const sidebar = useRef<HTMLElement | null>(null);
  const restoreGroup = () => (group.current?.isConnected ? group.current : sidebar.current?.querySelector<HTMLButtonElement>(".sidebar-project-row"))?.focus({ preventScroll: true });
  const read = useQuery(ResourceQuery.getResource, { kind: EntityKind.SESSION, id }, { enabled: visible, staleTime: 0 });
  const budget = useQuery(SessionQuery.getSessionBudget, { sessionId: id }, { enabled: visible, staleTime: 0 });
  const observed = validSessionActionResource(read.data?.resource, id) ? read.data.resource : undefined;
  const { resource, control, action } = useSessionControl(id, observed, visible);
  const ready = Boolean(resource && observed && !read.isFetching && !read.error);
  const eligibility = sessionControlEligibility(resource, budget.isFetching || Boolean(budget.error) || !budget.data?.view || budget.data.view.session?.id !== id || budget.data.view.state === BudgetState.THRESHOLD_REACHED);
  const locked = !ready || control.busy || control.uncertain;
  const close = (restore = true) => {
    owner.setOpen("");
    if (restore) { if (opener.current?.isConnected) opener.current.focus({ preventScroll: true }); else restoreGroup(); }
  };
  useLayoutEffect(() => {
    if (!visible || !popup.current || !opener.current) return;
    const menu = popup.current;
    menu.showPopover?.();
    const position = () => {
      const bounds = opener.current?.getBoundingClientRect();
      if (!bounds) return;
      let left = bounds.right + 8, top = bounds.top;
      if (left + menu.offsetWidth > window.innerWidth - 8) {
        left = bounds.left;
        const below = window.innerHeight - bounds.bottom - 16;
        const above = bounds.top - 16;
        const useBelow = menu.offsetHeight <= below || below >= above;
        menu.style.maxHeight = `${Math.max(32, useBelow ? below : above)}px`;
        top = useBelow ? bounds.bottom + 8 : bounds.top - menu.offsetHeight - 8;
      }
      menu.style.left = `${Math.max(8, Math.min(left, window.innerWidth - menu.offsetWidth - 8))}px`;
      menu.style.top = `${Math.max(8, Math.min(top, window.innerHeight - menu.offsetHeight - 8))}px`;
    };
    position();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(position);
    observer?.observe(menu);
    menu.focus();
    const outside = (event: PointerEvent) => { if (!menu.contains(event.target as Node) && !opener.current?.contains(event.target as Node)) close(false); };
    const leave = () => close();
    window.document.addEventListener("pointerdown", outside);
    window.addEventListener("resize", leave);
    return () => { observer?.disconnect(); menu.hidePopover?.(); window.document.removeEventListener("pointerdown", outside); window.removeEventListener("resize", leave); };
  }, [visible]);
  const showing = useRef(visible); showing.current = visible;
  useLayoutEffect(() => {
    if (visible && ready && window.document.activeElement === popup.current) popup.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
  }, [visible, ready]);
  useEffect(() => () => { if (showing.current) { owner.setOpen(""); restoreGroup(); } }, []);
  const controlAction = (value: SessionAction) => { if (!locked && (value !== SessionAction.RESUME || eligibility.resume)) action(value); };
  return <><button ref={opener} type="button" className="sidebar-session-more" data-open={visible} aria-label={copy("sidebar.sessionActions")} aria-describedby={descriptionId} aria-haspopup="menu" aria-expanded={visible} onClick={event => {
    sidebar.current = event.currentTarget.closest(".sidebar");
    group.current = event.currentTarget.closest(".sidebar-project-group")?.querySelector<HTMLButtonElement>(".sidebar-project-row") ?? null;
    dismissHover(); if (visible) close(); else owner.setOpen(id);
  }}><svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" fill="currentColor"><circle cx="3" cy="8" r="1"/><circle cx="8" cy="8" r="1"/><circle cx="13" cy="8" r="1"/></svg></button>
  {visible ? <div ref={popup} popover="manual" role="menu" tabIndex={-1} className="sidebar-session-menu" aria-describedby={descriptionId} aria-label={copy("sidebar.sessionActions")} onFocus={dismissHover} onKeyDown={event => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault(); const choices = Array.from(popup.current!.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
      const index = choices.indexOf(window.document.activeElement as HTMLButtonElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? choices.length - 1 : (index + (event.key === "ArrowUp" ? -1 : 1) + choices.length) % choices.length;
      choices[next]?.focus();
    }
    if (event.key === "Tab") close();
  }}>
    <button type="button" role="menuitem" disabled={locked} onClick={() => controlAction(SessionAction.STOP)}><svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true"><rect x="3" y="3" width="10" height="10" rx="1" /></svg>{copy("session.stop_cae7d5")}</button>
    <button type="button" role="menuitem" disabled={locked || !eligibility.resume} onClick={() => controlAction(SessionAction.RESUME)}><svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true"><path d="m5 3 7 5-7 5z" /></svg>{eligibility.startupRetry ? copy("session.startupRetry") : copy("session.resume_d640c7")}</button>
    {ready && resource ? <SessionForkAction source={resource} forkOnly disabled={locked} onOpen={() => close()} /> : null}
    <button type="button" role="menuitem" disabled={locked} onClick={() => controlAction(eligibility.archiveAction)}><svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true"><path d="M2 3h12v3H2zM3 6v8h10V6M6 9h4" /></svg>{eligibility.archiveAction === SessionAction.RESTORE ? copy("session.restore_a76e13") : copy("session.archive_66f480")}</button>
    {!ready ? <p role="status">{copy("sidebar.sessionActionsRead")}</p> : null}<Problem error={read.error || budget.error || control.error} />
    {read.error || !observed && !read.isFetching ? <button type="button" disabled={read.isFetching} onClick={() => void read.refetch()}>{copy("sidebar.retrySessionActionsRead")}</button> : null}
    {budget.error ? <button type="button" disabled={budget.isFetching} onClick={() => void budget.refetch()}>{copy("session-budget.refreshBudget_13bcdd")}</button> : null}
    {control.error && !control.uncertain ? <button type="button" disabled={read.isFetching} onClick={() => void read.refetch()}>{copy("sidebar.refreshSessionActions")}</button> : null}
    {control.uncertain ? <button type="button" disabled={control.busy} onClick={control.retry}>{copy("session.retryTheSameControlRequest_609aff")}</button> : null}
  </div> : null}</>;
}
