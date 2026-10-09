// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type ReactNode, type Ref } from "react";
import { createPortal } from "react-dom";
import "./settings-action.css";

/** Closed presentation catalog. It grants no domain or mutation authority. */
export enum SettingsActionIcon {
  Edit = "edit", Delete = "delete", Refresh = "refresh", Copy = "copy",
  Add = "add", Save = "save", Cancel = "cancel", Connect = "connect",
  Retry = "retry", Inspect = "inspect", Open = "open", Back = "back",
  Next = "next", Download = "download", Upload = "upload", Stop = "stop",
  Start = "start", Folder = "folder", Up = "up", Confirm = "confirm",
}
export enum SettingsActionPresentation { Icon = "icon", Label = "label" }
const ActionScope = createContext(false);
export function SettingsActionScope({ children }: { children: ReactNode }) {
  return <ActionScope.Provider value>{children}</ActionScope.Provider>;
}
const paths: Record<SettingsActionIcon, string> = {
  [SettingsActionIcon.Edit]: "m14 4 6 6M4 20l4-1L20 7a2.8 2.8 0 0 0-4-4L4 15z",
  [SettingsActionIcon.Delete]: "M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7",
  [SettingsActionIcon.Refresh]: "M20 8a8 8 0 1 0 0 8M20 3v5h-5",
  [SettingsActionIcon.Copy]: "M8 8h12v13H8zM16 8V3H3v13h5",
  [SettingsActionIcon.Add]: "M12 4v16M4 12h16",
  [SettingsActionIcon.Save]: "M4 3h13l4 4v14H3V3h1M7 3v6h9V3M7 21v-8h10v8",
  [SettingsActionIcon.Cancel]: "m5 5 14 14M19 5 5 19",
  [SettingsActionIcon.Connect]: "m8 7 3-3a4 4 0 0 1 6 6l-3 3M10 11l-3 3a4 4 0 0 0 6 6l3-3M8 16l8-8",
  [SettingsActionIcon.Retry]: "M4 9a8 8 0 1 1 0 7M4 4v5h5",
  [SettingsActionIcon.Inspect]: "M16 16l5 5M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0",
  [SettingsActionIcon.Open]: "M14 3h7v7M21 3 10 14M10 3H3v18h18v-7",
  [SettingsActionIcon.Back]: "m14 4-8 8 8 8M6 12h15",
  [SettingsActionIcon.Next]: "m10 4 8 8-8 8M3 12h15",
  [SettingsActionIcon.Download]: "M12 3v12m-5-5 5 5 5-5M3 16v5h18v-5",
  [SettingsActionIcon.Upload]: "M12 15V3m-5 5 5-5 5 5M3 16v5h18v-5",
  [SettingsActionIcon.Stop]: "M5 5h14v14H5z",
  [SettingsActionIcon.Start]: "m7 3 14 9-14 9z",
  [SettingsActionIcon.Folder]: "M3 5h7l2 3h9v13H3z",
  [SettingsActionIcon.Up]: "m5 12 7-7 7 7M12 5v16",
  [SettingsActionIcon.Confirm]: "m4 12 5 5L20 6",
};
export function SettingsActionGlyph({ icon }: { icon: SettingsActionIcon }) {
  return <svg className="settings-action-glyph" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false"><path d={paths[icon]} /></svg>;
}
interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon: SettingsActionIcon;
  presentation?: SettingsActionPresentation;
  ref?: Ref<HTMLButtonElement>;
  decorativePrefix?: string;
  targetName?: string;
  targetId?: string;
  "data-settings-task-cancel"?: boolean;
}
/** Native element identity, callbacks, guards and form ownership stay with callers. */
export function SettingsActionButton({ icon, presentation = SettingsActionPresentation.Label, ref, children, decorativePrefix, targetName, targetId, ...props }: Props) {
  const scoped = useContext(ActionScope), tooltipId = useId();
  const [tooltip, setTooltip] = useState<{ left: number; top: number; owner: HTMLElement }>();
  const tooltipRef = useRef<HTMLDivElement>(null);
  const label = scoped && decorativePrefix && typeof children === "string" && children.startsWith(decorativePrefix) ? children.slice(decorativePrefix.length) : children;
  const originalName = props["aria-label"] ?? (label !== children && typeof children === "string" ? children : undefined);
  const name = scoped && (targetName || targetId) ? [originalName ?? (typeof children === "string" ? children : undefined), targetName, targetId].filter(Boolean).join(" · ") : originalName;
  useLayoutEffect(() => {
    if (!tooltip || !tooltipRef.current) return;
    const zoom = Number(getComputedStyle(document.body).zoom) || 1;
    const top = Math.max(8, Math.min(tooltip.top, window.innerHeight / zoom - tooltipRef.current.getBoundingClientRect().height / zoom - 8));
    if (top !== tooltip.top) setTooltip({ ...tooltip, top });
  }, [tooltip, children, name]);
  useEffect(() => {
    if (!tooltip) return;
    const hide = () => setTooltip(undefined);
    window.addEventListener("scroll", hide, true); window.addEventListener("resize", hide);
    return () => { window.removeEventListener("scroll", hide, true); window.removeEventListener("resize", hide); };
  }, [Boolean(tooltip)]);
  const iconOnly = scoped && presentation === SettingsActionPresentation.Icon;
  const reveal = (button: HTMLButtonElement) => {
    const rect = button.getBoundingClientRect(), zoom = Number(getComputedStyle(document.body).zoom) || 1;
    const width = window.innerWidth / zoom;
    setTooltip({ left: Math.max(8, Math.min(rect.left / zoom, width - Math.min(280, width - 16) - 8)), top: rect.bottom / zoom + 8, owner: button.closest("dialog[open]") ?? document.body });
  };
  return <><button {...props} ref={ref} className={scoped ? `${props.className ?? ""} settings-action-button${iconOnly ? " settings-action-only" : ""}` : props.className}
    data-settings-action={scoped ? icon : undefined} data-settings-action-presentation={scoped ? presentation : undefined}
    aria-label={name}
    aria-describedby={iconOnly && tooltip ? [props["aria-describedby"], tooltipId].filter(Boolean).join(" ") : props["aria-describedby"]}
    onFocus={event => { props.onFocus?.(event); if (iconOnly) reveal(event.currentTarget); }}
    onBlur={event => { props.onBlur?.(event); setTooltip(undefined); }}
    onPointerEnter={event => { props.onPointerEnter?.(event); if (iconOnly) reveal(event.currentTarget); }}
    onPointerLeave={event => { props.onPointerLeave?.(event); if (document.activeElement !== event.currentTarget) setTooltip(undefined); }}
    onKeyDown={event => { props.onKeyDown?.(event); if (event.key === "Escape") setTooltip(undefined); }}>
    {scoped ? <SettingsActionGlyph icon={icon} /> : null}{iconOnly ? <span className="settings-action-name">{label}</span> : label}
  </button>{iconOnly && tooltip ? createPortal(<div ref={tooltipRef} id={tooltipId} role="tooltip" className="settings-action-tooltip" style={{ left: tooltip.left, top: tooltip.top }}>{name ?? children}</div>, tooltip.owner) : null}</>;
}
