import { copy, useLocale } from "./localization";
import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

interface SidebarOutlet {
  target: HTMLElement | null;
  closeDrawer: () => void;
  drawerOpen: boolean;
  paneVisible?: boolean;
  openDrawer?: () => void;
}

const SidebarOutletContext = createContext<SidebarOutlet>({ target: null, closeDrawer: () => undefined, drawerOpen: false });

export function SidebarOutletProvider({ target, closeDrawer, drawerOpen, paneVisible = true, openDrawer, children }: SidebarOutlet & { children: ReactNode }) {
  useLocale();
  return <SidebarOutletContext.Provider value={{ target, closeDrawer, drawerOpen, paneVisible, openDrawer }}>{children}</SidebarOutletContext.Provider>;
}

export function SidebarSurface({ active, title, children, className = "", showHeading = true }: { active: boolean; title: string; children: ReactNode; className?: string; showHeading?: boolean }) {
  useLocale();
  const { target, paneVisible = true } = useContext(SidebarOutletContext);
  const panel = <section className={`sidebar-surface-content${className ? ` ${className}` : ""}`} aria-label={copy("sidebar-context.navigationAndFilters_2aa4c3", { v0: title })} hidden={!active}>
    {showHeading ? <h2>{title}</h2> : null}
    <SidebarActivityContext.Provider value={active && paneVisible}>{children}</SidebarActivityContext.Provider>
  </section>;
  return target ? createPortal(panel, target) : panel;
}

export function useCloseSidebarDrawer() {
  return useContext(SidebarOutletContext).closeDrawer;
}

export function useSidebarDrawerOpen() {
  return useContext(SidebarOutletContext).drawerOpen;
}
export function useOpenSidebarDrawer() { return useContext(SidebarOutletContext).openDrawer ?? (() => undefined); }

const SidebarActivityContext = createContext(true);
export function useSidebarActivity() { return useContext(SidebarActivityContext); }
export function useSidebarPaneVisible() { return useContext(SidebarOutletContext).paneVisible ?? true; }
