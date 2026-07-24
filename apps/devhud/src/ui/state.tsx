import { createContext, use, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

export enum ThemePreference {
  System = "system",
  Light = "light",
  Dark = "dark",
}

export enum MobileScreen {
  Home = "home",
  Widgets = "widgets",
  Settings = "settings",
  Diagnostics = "diagnostics",
}

interface ApplicationState {
  readonly theme: ThemePreference;
  readonly mobileScreen: MobileScreen;
  readonly settingsOpen: boolean;
}

interface ApplicationActions {
  setTheme(theme: ThemePreference): void;
  setMobileScreen(screen: MobileScreen): void;
  openSettings(): void;
  closeSettings(): void;
}

const ApplicationContext = createContext<(ApplicationState & ApplicationActions) | null>(null);

export function ApplicationProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState(ThemePreference.System);
  const [mobileScreen, setMobileScreen] = useState(MobileScreen.Home);
  const [settingsOpen, setSettingsOpen] = useState(false);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  const openSettings = useCallback(() => setSettingsOpen(true), []);
  const closeSettings = useCallback(() => setSettingsOpen(false), []);

  const value = useMemo(
    () => ({
      theme,
      mobileScreen,
      settingsOpen,
      setTheme,
      setMobileScreen,
      openSettings,
      closeSettings,
    }),
    [closeSettings, mobileScreen, openSettings, settingsOpen, theme],
  );

  return <ApplicationContext value={value}>{children}</ApplicationContext>;
}

export function useApplication() {
  const application = use(ApplicationContext);
  if (application === null) {
    throw new Error("DevHud UI must be rendered inside ApplicationProvider.");
  }
  return application;
}
