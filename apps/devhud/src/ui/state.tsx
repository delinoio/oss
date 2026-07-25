import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import {
  defaultSettings,
  defaultWidgetConfiguration,
  SETTINGS_STORAGE_KEY,
  WIDGET_CONFIGURATION_STORAGE_KEY,
  type DevHudSettings,
  type StructuredShortcut,
  type ThemePreference,
  type WidgetConfiguration,
} from "../persistence/contracts";
import {
  DevHudPersistence,
  PersistenceResetError,
  RejectedRecordWriteBlockedError,
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
  resetDevHud(): Promise<void>;
  setTheme(theme: ThemePreference): void;
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
      if (!persistenceReady) return;
      const mutation = ++settingsMutation.current;
      setSettings(nextSettings);
      void persistence.saveSettings(nextSettings).then(
        () => {
          lastSuccessfulSettings.current = nextSettings;
          if (mutation === settingsMutation.current) {
            setPersistenceIssues((issues) =>
              issues.filter((issue) => issue.key !== SETTINGS_STORAGE_KEY),
            );
          }
        },
        (error: unknown) => {
          if (mutation !== settingsMutation.current) return;
          setSettings(lastSuccessfulSettings.current);
          if (error instanceof RejectedRecordWriteBlockedError) {
            setPersistenceIssues((issues) => [
              ...issues.filter((issue) => issue.key !== error.key),
              { ...error.failure, key: error.key },
            ]);
            return;
          }
          setPersistenceIssues((issues) => [
            ...issues.filter((issue) => issue.key !== SETTINGS_STORAGE_KEY),
            {
              key: SETTINGS_STORAGE_KEY,
              kind: "storage",
              guidance:
                "DevHud could not save local settings. The last saved settings were kept.",
            },
          ]);
        },
      );
    },
    [persistence, persistenceReady],
  );

  const setTheme = useCallback(
    (theme: ThemePreference) => persistSettings({ ...settings, theme }),
    [persistSettings, settings],
  );
  const setShortcut = useCallback(
    (shortcut: StructuredShortcut | null) => persistSettings({ ...settings, shortcut }),
    [persistSettings, settings],
  );
  const setWidgetConfiguration = useCallback(
    (configuration: WidgetConfiguration) => {
      if (!persistenceReady) return;
      const mutation = ++widgetConfigurationMutation.current;
      setWidgetConfigurationState(configuration);
      void persistence.saveWidgetConfiguration(configuration).then(
        () => {
          lastSuccessfulWidgetConfiguration.current = configuration;
          if (mutation === widgetConfigurationMutation.current) {
            setPersistenceIssues((issues) =>
              issues.filter((issue) => issue.key !== WIDGET_CONFIGURATION_STORAGE_KEY),
            );
          }
        },
        (error: unknown) => {
          if (mutation !== widgetConfigurationMutation.current) return;
          setWidgetConfigurationState(lastSuccessfulWidgetConfiguration.current);
          if (error instanceof RejectedRecordWriteBlockedError) {
            setPersistenceIssues((issues) => [
              ...issues.filter((issue) => issue.key !== error.key),
              { ...error.failure, key: error.key },
            ]);
            return;
          }
          setPersistenceIssues((issues) => [
            ...issues.filter((issue) => issue.key !== WIDGET_CONFIGURATION_STORAGE_KEY),
            {
              key: WIDGET_CONFIGURATION_STORAGE_KEY,
              kind: "storage",
              guidance:
                "DevHud could not save widget configuration. The last saved configuration was kept.",
            },
          ]);
        },
      );
    },
    [persistence, persistenceReady],
  );

  const resetDevHud = useCallback(async () => {
    setPersistenceReady(false);
    settingsMutation.current += 1;
    widgetConfigurationMutation.current += 1;
    try {
      const loaded = await persistence.reset();
      setSettings(loaded.settings);
      setWidgetConfigurationState(loaded.widgetConfiguration);
      lastSuccessfulSettings.current = loaded.settings;
      lastSuccessfulWidgetConfiguration.current = loaded.widgetConfiguration;
      setPersistenceIssues(loaded.issues);
    } catch (error: unknown) {
      if (error instanceof PersistenceResetError) {
        const { loaded } = error;
        setSettings(loaded.settings);
        setWidgetConfigurationState(loaded.widgetConfiguration);
        lastSuccessfulSettings.current = loaded.settings;
        lastSuccessfulWidgetConfiguration.current = loaded.widgetConfiguration;
        setPersistenceIssues(loaded.issues);
      }
      throw error;
    } finally {
      settingsMutation.current = 0;
      widgetConfigurationMutation.current = 0;
      setPersistenceReady(true);
    }
  }, [persistence]);

  const openSettings = useCallback(() => setSettingsOpen(true), []);
  const closeSettings = useCallback(() => setSettingsOpen(false), []);

  const value = useMemo(
    () => ({
      settings,
      widgetConfiguration,
      persistenceIssues,
      persistenceReady,
      resetDevHud,
      setTheme,
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
      resetDevHud,
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
