/**
 * Application state store using React context pattern.
 * Provides theme management, sidebar state, and workspace state.
 */

import { createContext, useContext } from "react";

export type Theme = "light" | "dark";

export interface AppState {
  theme: Theme;
  sidebarOpen: boolean;
  activeWorkspaceId: string;
  connectionStatus: "connected" | "disconnected" | "reconnecting";
}

export interface AppActions {
  toggleTheme: () => void;
  setTheme: (theme: Theme) => void;
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  setConnectionStatus: (status: AppState["connectionStatus"]) => void;
}

export type AppStore = AppState & AppActions;

export const AppStoreContext = createContext<AppStore | null>(null);

export function useAppStore(): AppStore {
  const store = useContext(AppStoreContext);
  if (!store) {
    throw new Error("useAppStore must be used within AppStoreProvider");
  }
  return store;
}

/**
 * Get persisted theme or default to light.
 */
export function getPersistedTheme(): Theme {
  try {
    const stored = localStorage.getItem("dexdex-theme");
    if (stored === "dark" || stored === "light") {
      return stored;
    }
  } catch {
    // localStorage not available
  }
  return "light";
}

/**
 * Persist theme to localStorage.
 */
export function persistTheme(theme: Theme): void {
  try {
    localStorage.setItem("dexdex-theme", theme);
  } catch {
    // localStorage not available
  }
}

/**
 * Apply theme class to document root.
 */
export function applyThemeToDocument(theme: Theme): void {
  if (typeof document !== "undefined") {
    if (theme === "dark") {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
  }
}
