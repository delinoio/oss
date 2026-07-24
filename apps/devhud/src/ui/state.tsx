import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import {
  defaultSettings,
  defaultWidgetConfiguration,
  type DevHudSettings,
  type StructuredShortcut,
  type ThemePreference,
  type WidgetConfiguration,
} from "../persistence/contracts";
import {
  DevHudPersistence,
  FutureVersionWriteBlockedError,
  type LocalStorageAdapter,
  type PersistenceIssue,
} from "../persistence/storage";
import { tauriPersistenceAdapter } from "../persistence/tauri";

export { ThemePreference } from "../persistence/contracts";

export enum MobileScreen {
  Home = "home",
  Widgets = "widgets",
  Settings = "settings",
  Diagnostics = "diagnostics",
}

interface ApplicationState {
  readonly settings: DevHudSettings;
  readonly widgetConfiguration: WidgetConfiguration;
  readonly persistenceIssues: readonly PersistenceIssue[];
  readonly persistenceReady: boolean;
  readonly mobileScreen: MobileScreen;
  readonly settingsOpen: boolean;
}

interface ApplicationActions {
  setTheme(theme: ThemePreference): void;
  setLaunchAtLogin(launchAtLogin: boolean): void;
  setShortcut(shortcut: StructuredShortcut | null): void;
  setWidgetConfiguration(configuration: WidgetConfiguration): void;
  setMobileScreen(screen: MobileScreen): void;
  openSettings(): void;
  closeSettings(): void;
}

const ApplicationContext = createContext<(ApplicationState & ApplicationActions) | null>(null);

export function ApplicationProvider({
  children,
  storage = tauriPersistenceAdapter,
}: {
  children: ReactNode;
  storage?: LocalStorageAdapter;
}) {
  const persistence = useMemo(() => new DevHudPersistence(storage), [storage]);
  const [settings, setSettings] = useState<DevHudSettings>(defaultSettings);
  const [widgetConfiguration, setWidgetConfigurationState] = useState<WidgetConfiguration>(
    defaultWidgetConfiguration,
  );
  const [persistenceIssues, setPersistenceIssues] = useState<readonly PersistenceIssue[]>([]);
  const [persistenceReady, setPersistenceReady] = useState(false);
  const [mobileScreen, setMobileScreen] = useState(MobileScreen.Home);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsMutation = useRef(0);
  const widgetConfigurationMutation = useRef(0);
  const lastSuccessfulSettings = useRef<DevHudSettings>(defaultSettings);
  const lastSuccessfulWidgetConfiguration = useRef<WidgetConfiguration>(
    defaultWidgetConfiguration,
  );

  useEffect(() => {
    document.documentElement.dataset.theme = settings.theme;
  }, [settings.theme]);

  useEffect(() => {
    let active = true;
    void persistence.load().then((loaded) => {
      if (!active) return;
      if (settingsMutation.current === 0) {
        setSettings(loaded.settings);
        lastSuccessfulSettings.current = loaded.settings;
      }
      if (widgetConfigurationMutation.current === 0) {
        setWidgetConfigurationState(loaded.widgetConfiguration);
        lastSuccessfulWidgetConfiguration.current = loaded.widgetConfiguration;
      }
      setPersistenceIssues(loaded.issues);
      setPersistenceReady(true);
    });
    return () => {
      active = false;
    };
  }, [persistence]);

  const persistSettings = useCallback(
    (nextSettings: DevHudSettings) => {
      const mutation = ++settingsMutation.current;
      setSettings(nextSettings);
      void persistence.saveSettings(nextSettings).then(
        () => {
          lastSuccessfulSettings.current = nextSettings;
          if (mutation === settingsMutation.current) setPersistenceIssues([]);
        },
        (error: unknown) => {
          if (mutation !== settingsMutation.current) return;
          setSettings(lastSuccessfulSettings.current);
          if (error instanceof FutureVersionWriteBlockedError) {
            setPersistenceIssues([{ kind: "future-version", guidance: error.guidance }]);
            return;
          }
          setPersistenceIssues([
            {
              kind: "storage",
              guidance:
                "DevHud could not save local settings. The last saved settings were kept.",
            },
          ]);
        },
      );
    },
    [persistence],
  );

  const setTheme = useCallback(
    (theme: ThemePreference) => persistSettings({ ...settings, theme }),
    [persistSettings, settings],
  );
  const setLaunchAtLogin = useCallback(
    (launchAtLogin: boolean) => persistSettings({ ...settings, launchAtLogin }),
    [persistSettings, settings],
  );
  const setShortcut = useCallback(
    (shortcut: StructuredShortcut | null) => persistSettings({ ...settings, shortcut }),
    [persistSettings, settings],
  );
  const setWidgetConfiguration = useCallback(
    (configuration: WidgetConfiguration) => {
      const mutation = ++widgetConfigurationMutation.current;
      setWidgetConfigurationState(configuration);
      void persistence.saveWidgetConfiguration(configuration).then(
        () => {
          lastSuccessfulWidgetConfiguration.current = configuration;
          if (mutation === widgetConfigurationMutation.current) setPersistenceIssues([]);
        },
        (error: unknown) => {
          if (mutation !== widgetConfigurationMutation.current) return;
          setWidgetConfigurationState(lastSuccessfulWidgetConfiguration.current);
          if (error instanceof FutureVersionWriteBlockedError) {
            setPersistenceIssues([{ kind: "future-version", guidance: error.guidance }]);
            return;
          }
          setPersistenceIssues([
            {
              kind: "storage",
              guidance:
                "DevHud could not save widget configuration. The last saved configuration was kept.",
            },
          ]);
        },
      );
    },
    [persistence],
  );

  const openSettings = useCallback(() => setSettingsOpen(true), []);
  const closeSettings = useCallback(() => setSettingsOpen(false), []);

  const value = useMemo(
    () => ({
      settings,
      widgetConfiguration,
      persistenceIssues,
      persistenceReady,
      setTheme,
      setLaunchAtLogin,
      setShortcut,
      setWidgetConfiguration,
      mobileScreen,
      settingsOpen,
      setMobileScreen,
      openSettings,
      closeSettings,
    }),
    [
      closeSettings,
      mobileScreen,
      openSettings,
      persistenceIssues,
      persistenceReady,
      setLaunchAtLogin,
      setShortcut,
      setTheme,
      setWidgetConfiguration,
      settings,
      settingsOpen,
      widgetConfiguration,
    ],
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
