import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

interface SidebarOutlet {
  target: HTMLElement | null;
  closeDrawer: () => void;
  drawerOpen: boolean;
}

const SidebarOutletContext = createContext<SidebarOutlet>({ target: null, closeDrawer: () => undefined, drawerOpen: false });

export function SidebarOutletProvider({ target, closeDrawer, drawerOpen, children }: SidebarOutlet & { children: ReactNode }) {
  return <SidebarOutletContext.Provider value={{ target, closeDrawer, drawerOpen }}>{children}</SidebarOutletContext.Provider>;
}

export function SidebarSurface({ active, title, children }: { active: boolean; title: string; children: ReactNode }) {
  const { target } = useContext(SidebarOutletContext);
  const panel = <section className="sidebar-surface-content" aria-label={`${title} navigation and filters`} hidden={!active}>
    <h2>{title}</h2>
    {children}
  </section>;
  return target ? createPortal(panel, target) : panel;
}

export function useCloseSidebarDrawer() {
  return useContext(SidebarOutletContext).closeDrawer;
}

export function useSidebarDrawerOpen() {
  return useContext(SidebarOutletContext).drawerOpen;
}
