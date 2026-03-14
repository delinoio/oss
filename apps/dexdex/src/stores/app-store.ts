import { create } from "zustand";

export interface OpenTab {
  id: string;
  title: string;
  path: string;
}

interface AppState {
  // Sidebar
  sidebarCollapsed: boolean;
  toggleSidebar: () => void;
  setSidebarCollapsed: (collapsed: boolean) => void;

  // Tabs
  openTabs: OpenTab[];
  activeTabId: string | null;
  openTab: (tab: OpenTab) => void;
  closeTab: (id: string) => void;
  setActiveTab: (id: string) => void;

  // Theme
  theme: "light" | "dark";
  toggleTheme: () => void;

  // Workspace
  workspaceId: string;
  setWorkspaceId: (id: string) => void;
}

export const useAppStore = create<AppState>((set) => ({
  // Sidebar
  sidebarCollapsed: false,
  toggleSidebar: () =>
    set((state) => ({ sidebarCollapsed: !state.sidebarCollapsed })),
  setSidebarCollapsed: (collapsed) => set({ sidebarCollapsed: collapsed }),

  // Tabs
  openTabs: [],
  activeTabId: null,
  openTab: (tab) =>
    set((state) => {
      const exists = state.openTabs.find((t) => t.id === tab.id);
      if (exists) {
        return { activeTabId: tab.id };
      }
      return {
        openTabs: [...state.openTabs, tab],
        activeTabId: tab.id,
      };
    }),
  closeTab: (id) =>
    set((state) => {
      const filtered = state.openTabs.filter((t) => t.id !== id);
      const newActiveId =
        state.activeTabId === id
          ? filtered.length > 0
            ? filtered[filtered.length - 1].id
            : null
          : state.activeTabId;
      return { openTabs: filtered, activeTabId: newActiveId };
    }),
  setActiveTab: (id) => set({ activeTabId: id }),

  // Theme
  theme: "light",
  toggleTheme: () =>
    set((state) => ({
      theme: state.theme === "light" ? "dark" : "light",
    })),

  // Workspace
  workspaceId: "default",
  setWorkspaceId: (id) => set({ workspaceId: id }),
}));
