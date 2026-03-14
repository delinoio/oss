import { create } from "zustand";

export interface Tab {
  id: string;
  label: string;
  path: string;
}

interface AppState {
  sidebarCollapsed: boolean;
  toggleSidebar: () => void;

  activeWorkspaceId: string | null;
  setActiveWorkspaceId: (id: string) => void;

  tabs: Tab[];
  activeTabId: string | null;
  openTab: (tab: Tab) => void;
  closeTab: (tabId: string) => void;
  setActiveTab: (tabId: string) => void;

  theme: "light" | "dark";
  setTheme: (theme: "light" | "dark") => void;
}

export const useAppStore = create<AppState>((set, get) => ({
  sidebarCollapsed: false,
  toggleSidebar: () =>
    set((state) => ({ sidebarCollapsed: !state.sidebarCollapsed })),

  activeWorkspaceId: null,
  setActiveWorkspaceId: (id) => set({ activeWorkspaceId: id }),

  tabs: [],
  activeTabId: null,
  openTab: (tab) => {
    const existing = get().tabs.find((t) => t.id === tab.id);
    if (existing) {
      set({ activeTabId: tab.id });
    } else {
      set((state) => ({
        tabs: [...state.tabs, tab],
        activeTabId: tab.id,
      }));
    }
  },
  closeTab: (tabId) => {
    const { tabs, activeTabId } = get();
    const idx = tabs.findIndex((t) => t.id === tabId);
    const newTabs = tabs.filter((t) => t.id !== tabId);
    let newActiveId = activeTabId;
    if (activeTabId === tabId) {
      newActiveId =
        newTabs[Math.min(idx, newTabs.length - 1)]?.id ?? null;
    }
    set({ tabs: newTabs, activeTabId: newActiveId });
  },
  setActiveTab: (tabId) => set({ activeTabId: tabId }),

  theme: "light",
  setTheme: (theme) => {
    set({ theme });
    document.documentElement.classList.toggle("dark", theme === "dark");
  },
}));
