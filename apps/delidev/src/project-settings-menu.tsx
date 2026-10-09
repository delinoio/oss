// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useSidebarActionMenuOwner } from "./session-row-actions";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationEditor } from "./settings";
import { SettingsTaskDialog, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";

// Fetch the original project explicitly; never infer it from repository membership.
export function ProjectSettingsMenu({ projectId, label, active }: { projectId: string; label: string; active: boolean }) {
 useLocale();
 const owner = useSidebarActionMenuOwner(), visible = owner.open === `project:${projectId}`;
 const opener = useRef<HTMLButtonElement>(null), popup = useRef<HTMLDivElement>(null), group = useRef<HTMLButtonElement | null>(null);
 const restore = () => (opener.current?.isConnected ? opener.current : group.current?.isConnected ? group.current : null)?.focus({ preventScroll: true });
 const showing = useRef(visible); showing.current = visible;
 useEffect(() => () => { if (showing.current) { owner.setOpen(""); if (group.current?.isConnected) group.current.focus({ preventScroll: true }); } }, []);
 const closeMenu = (focus = true) => { owner.setOpen(""); if (focus) restore(); };
 useEffect(() => { if (!active && visible) closeMenu(); }, [active, visible]);
 useLayoutEffect(() => {
  if (!visible || !popup.current || !opener.current) return;
  const menu = popup.current; menu.showPopover?.();
  const position = () => {
   const bounds = opener.current?.getBoundingClientRect(); if (!bounds) return;
   let left = bounds.right + 8, top = bounds.top;
   if (left + menu.offsetWidth > window.innerWidth - 8) {
    left = bounds.left; const below = window.innerHeight - bounds.bottom - 16, above = bounds.top - 16;
    const useBelow = menu.offsetHeight <= below || below >= above;
    menu.style.maxHeight = `${Math.max(32, useBelow ? below : above)}px`;
    top = useBelow ? bounds.bottom + 8 : bounds.top - menu.offsetHeight - 8;
   }
   menu.style.left = `${Math.max(8, Math.min(left, window.innerWidth - menu.offsetWidth - 8))}px`;
   menu.style.top = `${Math.max(8, Math.min(top, window.innerHeight - menu.offsetHeight - 8))}px`;
  };
  position(); const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(position); observer?.observe(menu);
  menu.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
  const outside = (event: PointerEvent) => { if (!menu.contains(event.target as Node) && !opener.current?.contains(event.target as Node)) closeMenu(false); };
  const leave = () => closeMenu();
  document.addEventListener("pointerdown", outside); window.addEventListener("resize", leave);
  return () => { observer?.disconnect(); menu.hidePopover?.(); document.removeEventListener("pointerdown", outside); window.removeEventListener("resize", leave); };
 }, [visible]);
 const [open, setOpen] = useState(false);
 const project = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: projectId }, { enabled: active && open, retry: false });
 const [row, setRow] = useState<Resource>();
 useEffect(() => { if (!open) { setRow(undefined); return; } const value = project.data?.resource; if (!row && value?.id === projectId && value.kind === EntityKind.PROJECT && supportsResourceSchema(value)) setRow(value); }, [open, row, projectId, project.data]);
 return <><button ref={opener} type="button" className="sidebar-project-more" aria-label={copy("sidebar.projectActions", { name: label, id: projectId })} aria-haspopup="menu" aria-expanded={visible} disabled={!active} onClick={event => {
  group.current = event.currentTarget.closest(".sidebar-project-group")?.querySelector<HTMLButtonElement>(".sidebar-project-row") ?? null;
  if (visible) closeMenu(); else owner.setOpen(`project:${projectId}`);
 }}><svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" fill="currentColor"><circle cx="3" cy="8" r="1"/><circle cx="8" cy="8" r="1"/><circle cx="13" cy="8" r="1"/></svg></button>
 {visible ? <div ref={popup} popover="manual" role="menu" tabIndex={-1} className="sidebar-session-menu sidebar-project-menu" aria-label={copy("sidebar.projectActions", { name: label, id: projectId })} onKeyDown={event => {
  if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeMenu(); }
  if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) { event.preventDefault(); popup.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus(); }
  if (event.key === "Tab") closeMenu();
 }}><button type="button" role="menuitem" disabled={!active} onClick={() => {
  if (!active) return; flushSync(() => owner.setOpen("")); restore(); setOpen(true);
 }}><svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor"><path d="M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1z"/><circle cx="12" cy="12" r="4"/></svg>{copy("configuration-fields.projectSettings")}</button></div> : null}
 {open ? <SettingsTaskDialog fallbackFocus={() => group.current} title={copy("configuration-fields.projectSettings")} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} close={() => setOpen(false)}>
 {row && row.id === projectId && row.kind === EntityKind.PROJECT && supportsResourceSchema(row) ? <ConfigurationEditor kind={EntityKind.PROJECT} initial={row} active={active} saved={() => setOpen(false)} cancel={() => setOpen(false)} /> : <><p role="status">{copy("configuration-fields.unavailable")}</p><Problem error={project.error} /><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={project.isFetching || !active} onClick={() => void project.refetch()}>{copy("ui.retryCurrentRead")}</SettingsActionButton></>}
 </SettingsTaskDialog> : null}</>;
}
