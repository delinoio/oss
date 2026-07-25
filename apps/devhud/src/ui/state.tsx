import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import {
  decodeSettings,
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
  RejectedRecordWriteBlockedError,
  type LocalStorageAdapter,
  type PersistenceIssue,
  type PersistenceResetOutcome,
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
  readPersistedTheme(): Promise<ThemePreference | null>;
  reloadPersistence(): Promise<void>;
  resetDevHud(): Promise<PersistenceResetOutcome>;
  setTheme(theme: ThemePreference): Promise<boolean>;
  setShortcut(shortcut: StructuredShortcut | null): void;
  setLaunchAtLogin(enabled: boolean): void;
  adoptNativeTheme(theme: ThemePreference): void;
  adoptNativeShortcut(shortcut: StructuredShortcut): void;
  adoptNativeLaunchAtLogin(enabled: boolean): void;
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
    (nextSettings: DevHudSettings): Promise<boolean> => {
      if (!persistenceReady) return Promise.resolve(false);
      const mutation = ++settingsMutation.current;
      setSettings(nextSettings);
      return persistence.saveSettings(nextSettings).then(
        () => {
          lastSuccessfulSettings.current = nextSettings;
          if (mutation === settingsMutation.current) {
            setPersistenceIssues((issues) =>
              issues.filter((issue) => issue.key !== SETTINGS_STORAGE_KEY),
            );
          }
          return true;
        },
        (error: unknown) => {
          if (mutation !== settingsMutation.current) return false;
          setSettings(lastSuccessfulSettings.current);
          if (error instanceof RejectedRecordWriteBlockedError) {
            setPersistenceIssues((issues) => [
              ...issues.filter((issue) => issue.key !== error.key),
              { ...error.failure, key: error.key },
            ]);
            return false;
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
          return false;
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
    (shortcut: StructuredShortcut | null) => {
      void persistSettings({ ...settings, shortcut });
    },
    [persistSettings, settings],
  );
  const setLaunchAtLogin = useCallback(
    (launchAtLogin: boolean) => {
      void persistSettings({ ...settings, launchAtLogin });
    },
    [persistSettings, settings],
  );
  const adoptNativeTheme = useCallback(
    (theme: ThemePreference) => {
      setSettings((current) => {
        const nextSettings = { ...current, theme };
        lastSuccessfulSettings.current = nextSettings;
        return nextSettings;
      });
    },
    [],
  );
  const readPersistedTheme = useCallback(async () => {
    try {
      const record = await storage.read(SETTINGS_STORAGE_KEY);
      if (record === null) return defaultSettings.theme;
      const decoded = decodeSettings(record);
      return decoded.ok ? decoded.value.theme : null;
    } catch {
      return null;
    }
  }, [storage]);
  const adoptNativeShortcut = useCallback(
    (shortcut: StructuredShortcut) => {
      setSettings((current) => {
        const nextSettings = { ...current, shortcut };
        lastSuccessfulSettings.current = nextSettings;
        return nextSettings;
      });
    },
    [],
  );
  const adoptNativeLaunchAtLogin = useCallback(
    (launchAtLogin: boolean) => {
      setSettings((current) => {
        const nextSettings = { ...current, launchAtLogin };
        lastSuccessfulSettings.current = nextSettings;
        return nextSettings;
      });
    },
    [],
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
      const { loaded, outcome } = await persistence.reset();
      setSettings(loaded.settings);
      setWidgetConfigurationState(loaded.widgetConfiguration);
      lastSuccessfulSettings.current = loaded.settings;
      lastSuccessfulWidgetConfiguration.current = loaded.widgetConfiguration;
      setPersistenceIssues(loaded.issues);
      return outcome;
    } finally {
      settingsMutation.current = 0;
      widgetConfigurationMutation.current = 0;
      setPersistenceReady(true);
    }
  }, [persistence]);
  const reloadPersistence = useCallback(async () => {
    setPersistenceReady(false);
    settingsMutation.current += 1;
    widgetConfigurationMutation.current += 1;
    try {
      const loaded = await persistence.reload();
      setSettings(loaded.settings);
      setWidgetConfigurationState(loaded.widgetConfiguration);
      lastSuccessfulSettings.current = loaded.settings;
      lastSuccessfulWidgetConfiguration.current = loaded.widgetConfiguration;
      setPersistenceIssues(loaded.issues);
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
      readPersistedTheme,
      reloadPersistence,
      resetDevHud,
      setTheme,
      setShortcut,
      setLaunchAtLogin,
      adoptNativeTheme,
      adoptNativeShortcut,
      adoptNativeLaunchAtLogin,
      setWidgetConfiguration,
      mobileScreen,
      settingsOpen,
      setMobileScreen,
      openSettings,
      closeSettings,
    }),
    [
      adoptNativeLaunchAtLogin,
      adoptNativeShortcut,
      adoptNativeTheme,
      closeSettings,
      mobileScreen,
      openSettings,
      persistenceIssues,
      persistenceReady,
      readPersistedTheme,
      reloadPersistence,
      resetDevHud,
      setShortcut,
      setLaunchAtLogin,
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
