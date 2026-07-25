import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { LocalStorageAdapter } from "./persistence/storage";
import {
  detectApplicationPlatform,
  platformForRuntime,
  type ApplicationPlatform,
} from "./runtime/platform";
import {
  loadRuntimeInfo,
  tauriRuntimeBridge,
  type RuntimeBridge,
  type RuntimeInfo,
} from "./runtime/startup";
import {
  nativeDesktopBridge,
  type DesktopBridge,
} from "./runtime/desktop";
import {
  filterTools,
  productionTools,
  type ToolCapability,
  ToolPlatform,
} from "./tools/registry";
import { Dialog } from "./ui/Dialog";
import { SettingsPanel } from "./ui/SettingsPanel";
import {
  ApplicationProvider,
  MobileScreen,
  ThemePreference,
  useApplication,
} from "./ui/state";

type RuntimeState =
  | { status: "loading" }
  | { status: "failed"; message: string }
  | { status: "ready"; runtimeInfo: RuntimeInfo };

const runtimeFailureMessage = "DevHud could not initialize its local runtime.";

function Wordmark() {
  return (
    <div aria-label="DevHud" className="wordmark">
      <span aria-hidden="true">DH</span>
      <strong>DevHud</strong>
    </div>
  );
}

function PersistenceAlerts() {
  const { persistenceIssues } = useApplication();
  return persistenceIssues.map((issue) => (
    <p className="runtime-status error" key={issue.key} role="alert">
      {issue.guidance}
    </p>
  ));
}

function SettingsDialog({
  bridge,
  onResetComplete,
  showDesktopControls,
  runtimeInfo,
}: {
  readonly bridge: DesktopBridge | null;
  readonly onResetComplete: () => void;
  readonly showDesktopControls: boolean;
  readonly runtimeInfo: RuntimeInfo | null;
}) {
  const { closeSettings } = useApplication();
  return (
    <Dialog title="DevHud settings" onClose={closeSettings}>
      <PersistenceAlerts />
      <SettingsPanel
        bridge={bridge}
        onClose={closeSettings}
        showDesktopControls={showDesktopControls}
        startupAutostartOutcome={runtimeInfo?.autostartStartupOutcome}
        startupShortcutFailure={runtimeInfo?.shortcutStartupFailure}
      />
      <ResetDevHudControl onResetComplete={onResetComplete} />
    </Dialog>
  );
}

function ThemeField() {
  const { persistenceReady, setTheme, settings } = useApplication();
  return (
    <label className="field" htmlFor="theme-preference">
      Theme preference
      <select
        disabled={!persistenceReady}
        id="theme-preference"
        onChange={(event) => void setTheme(event.target.value as ThemePreference)}
        value={settings.theme}
      >
        <option value={ThemePreference.System}>System</option>
        <option value={ThemePreference.Light}>Light</option>
        <option value={ThemePreference.Dark}>Dark</option>
      </select>
    </label>
  );
}

type ResetStatus = "idle" | "confirming" | "resetting" | "failed" | "complete";

function ResetDevHudControl({
  onResetComplete,
}: {
  readonly onResetComplete?: () => void;
}) {
  const { resetDevHud } = useApplication();
  const [status, setStatus] = useState<ResetStatus>("idle");
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (status === "confirming") confirmRef.current?.focus();
  }, [status]);

  const confirmReset = async () => {
    setStatus("resetting");
    try {
      await resetDevHud();
      onResetComplete?.();
      setStatus("complete");
    } catch {
      setStatus("failed");
    }
  };

  return (
    <section aria-labelledby="reset-devhud-title" className="reset-section">
      <h3 id="reset-devhud-title">Reset DevHud</h3>
      <p className="muted">
        Clear local settings, widget state, and application browsing data from this
        device.
      </p>
      {status === "confirming" ? (
        <div aria-label="Confirm Reset DevHud" className="reset-confirmation" role="group">
          <p>This cannot be undone. Reset all local DevHud data?</p>
          <div className="reset-actions">
            <button
              className="danger-button"
              onClick={() => void confirmReset()}
              ref={confirmRef}
              type="button"
            >
              Confirm reset
            </button>
            <button
              className="secondary-button"
              onClick={() => setStatus("idle")}
              type="button"
            >
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <button
          className="secondary-button"
          disabled={status === "resetting"}
          onClick={() => setStatus("confirming")}
          type="button"
        >
          {status === "resetting" ? "Resetting DevHud…" : "Reset DevHud"}
        </button>
      )}
      {status === "complete" ? (
        <p role="status">DevHud local data was reset.</p>
      ) : null}
      {status === "failed" ? (
        <p className="error" role="alert">
          DevHud could not reset local data. Check device storage and try again.
        </p>
      ) : null}
    </section>
  );
}

function SettingsWindow({
  bridge,
  firstRun,
  onResetComplete,
  startupAutostartOutcome,
  startupShortcutFailure,
}: {
  readonly bridge: DesktopBridge | null;
  readonly firstRun: boolean;
  readonly onResetComplete: () => void;
  readonly startupAutostartOutcome: RuntimeInfo["autostartStartupOutcome"];
  readonly startupShortcutFailure: RuntimeInfo["shortcutStartupFailure"];
}) {
  const [firstRunActive, setFirstRunActive] = useState(firstRun);
  const close = () => {
    void bridge?.hideSettings();
  };
  return (
    <main className="settings-shell">
      <PersistenceAlerts />
      <SettingsPanel
        bridge={bridge}
        firstRun={firstRunActive}
        onClose={close}
        onFirstRunCompleted={() => setFirstRunActive(false)}
        startupAutostartOutcome={startupAutostartOutcome}
        startupShortcutFailure={startupShortcutFailure}
      />
      <ResetDevHudControl onResetComplete={onResetComplete} />
    </main>
  );
}

function EmptyTools({
  compact = false,
  onOpenSettings,
}: {
  readonly compact?: boolean;
  readonly onOpenSettings?: () => void;
}) {
  const { openSettings, setMobileScreen } = useApplication();
  const showSettings = () => {
    if (compact) {
      setMobileScreen(MobileScreen.Settings);
      return;
    }
    (onOpenSettings ?? openSettings)();
  };
  return (
    <section
      className={compact ? "empty-state compact" : "empty-state"}
      aria-labelledby="tools-empty-title"
    >
      <p className="eyebrow">Local foundation</p>
      <h2 id="tools-empty-title">No tools yet</h2>
      <p>No tools are available in this foundation preview.</p>
      <button className="primary-button" onClick={showSettings} type="button">
        {compact ? "Open settings" : "Settings"}
      </button>
    </section>
  );
}

function RuntimeFailure({
  message,
  onRetry,
}: {
  message: string;
  onRetry(): void;
}) {
  return (
    <section aria-labelledby="runtime-error-title" className="state-card error-card">
      <p className="eyebrow">Local runtime</p>
      <h2 id="runtime-error-title">DevHud could not start</h2>
      <p role="alert">{message}</p>
      <button className="secondary-button" onClick={onRetry} type="button">
        Try again
      </button>
    </section>
  );
}

const NO_TOOL_CAPABILITIES: ReadonlySet<ToolCapability> = new Set();

function ProductionToolSurface({
  onOpenSettings,
}: {
  readonly onOpenSettings: () => void;
}) {
  const availableTools = filterTools(productionTools, {
    platform: ToolPlatform.Desktop,
    grantedCapabilities: NO_TOOL_CAPABILITIES,
  });
  if (availableTools.length === 0) {
    return <EmptyTools onOpenSettings={onOpenSettings} />;
  }
  return (
    <section aria-labelledby="available-tools-title">
      <h2 id="available-tools-title">Available tools</h2>
      {availableTools.map(({ EntryPoint, toolId }) => (
        <EntryPoint key={toolId} />
      ))}
    </section>
  );
}

function DesktopHud({
  bridge,
  retryRuntime,
  runtime,
}: {
  readonly bridge: DesktopBridge | null;
  readonly retryRuntime: () => void;
  readonly runtime: RuntimeState;
}) {
  const searchRef = useRef<HTMLInputElement>(null);
  const [settingsFailure, setSettingsFailure] = useState(false);
  const { openSettings } = useApplication();
  useEffect(() => {
    const focusSearch = () => searchRef.current?.focus();
    focusSearch();
    window.addEventListener("devhud:shown", focusSearch);
    const hideForBlur = () => {
      void bridge?.hideHud();
    };
    const hideForEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        void bridge?.hideHud();
      }
    };
    window.addEventListener("blur", hideForBlur);
    document.addEventListener("keydown", hideForEscape);
    return () => {
      window.removeEventListener("devhud:shown", focusSearch);
      window.removeEventListener("blur", hideForBlur);
      document.removeEventListener("keydown", hideForEscape);
    };
  }, [bridge]);

  const showSettings = () => {
    if (bridge === null) openSettings();
    else {
      setSettingsFailure(false);
      void bridge.showSettings().catch(() => setSettingsFailure(true));
    }
  };
  return (
    <main className="desktop-shell">
      <header className="app-header">
        <Wordmark />
        <button className="text-button" onClick={showSettings} type="button">
          Settings
        </button>
      </header>
      <section className="hud-panel" aria-labelledby="hud-title">
        <h1 id="hud-title">Developer tools, kept local.</h1>
        <label className="search-label" htmlFor="tool-search">
          Search tools
        </label>
        <input
          ref={searchRef}
          id="tool-search"
          placeholder="Search available tools"
          type="search"
        />
        {settingsFailure ? (
          <p className="runtime-status error" role="alert">
            DevHud could not open Settings. Try again from the tray.
          </p>
        ) : null}
        {runtime.status === "loading" ? (
          <p className="runtime-status" role="status">
            Starting DevHud…
          </p>
        ) : null}
        {runtime.status === "failed" ? (
          <RuntimeFailure message={runtime.message} onRetry={retryRuntime} />
        ) : (
          <ProductionToolSurface onOpenSettings={showSettings} />
        )}
      </section>
    </main>
  );
}

const mobileScreenLabels: Record<MobileScreen, string> = {
  [MobileScreen.Home]: "Home",
  [MobileScreen.Widgets]: "Widgets",
  [MobileScreen.Settings]: "Settings",
  [MobileScreen.Diagnostics]: "Diagnostics",
};

function MobileScreenHeading({
  eyebrow,
  title,
}: {
  eyebrow: string;
  title: string;
}) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  return (
    <div className="screen-heading">
      <p className="eyebrow">{eyebrow}</p>
      <h1 ref={headingRef} tabIndex={-1}>
        {title}
      </h1>
    </div>
  );
}

function MobileHome({
  retryRuntime,
  runtime,
}: {
  retryRuntime(): void;
  runtime: RuntimeState;
}) {
  return (
    <section aria-label="Home" className="mobile-screen">
      <MobileScreenHeading eyebrow="Home" title="Developer tools, kept local." />
      {runtime.status === "loading" ? (
        <section aria-labelledby="home-loading-title" className="state-card">
          <h2 id="home-loading-title">Starting DevHud</h2>
          <p role="status">Loading the local application runtime…</p>
        </section>
      ) : null}
      {runtime.status === "failed" ? (
        <RuntimeFailure message={runtime.message} onRetry={retryRuntime} />
      ) : null}
      {runtime.status === "ready" ? <EmptyTools compact /> : null}
    </section>
  );
}

function MobileWidgets() {
  const { persistenceIssues, persistenceReady, widgetConfiguration } = useApplication();
  const widgetIssue = persistenceIssues.find(
    (issue) => issue.key === "devhud.widget-configuration.v1",
  );
  return (
    <section aria-label="Widgets" className="mobile-screen">
      <MobileScreenHeading eyebrow="Widgets" title="Widgets" />
      {!persistenceReady ? (
        <section aria-labelledby="widgets-loading-title" className="state-card">
          <h2 id="widgets-loading-title">Loading widgets</h2>
          <p role="status">Loading local widget state…</p>
        </section>
      ) : null}
      {persistenceReady && widgetIssue !== undefined ? (
        <section aria-labelledby="widgets-error-title" className="state-card error-card">
          <h2 id="widgets-error-title">Widget state is unavailable</h2>
          <p role="alert">{widgetIssue.guidance}</p>
        </section>
      ) : null}
      {persistenceReady &&
      widgetIssue === undefined &&
      widgetConfiguration.slots.length === 0 ? (
        <section aria-labelledby="widgets-empty-title" className="state-card">
          <h2 id="widgets-empty-title">No widgets available</h2>
          <p>
            Visible widgets are not included. DevHud has not registered a system widget on
            this device.
          </p>
        </section>
      ) : null}
    </section>
  );
}

function MobileSettings() {
  const { persistenceIssues, persistenceReady } = useApplication();
  const settingsIssue = persistenceIssues.find(
    (issue) => issue.key === "devhud.settings.v1",
  );
  return (
    <section aria-label="Settings" className="mobile-screen">
      <MobileScreenHeading eyebrow="Settings" title="Appearance" />
      <div className="settings-card">
        <ThemeField />
        {!persistenceReady ? (
          <p className="muted" role="status">
            Loading local settings…
          </p>
        ) : null}
        {settingsIssue !== undefined ? (
          <p className="error" role="alert">
            {settingsIssue.guidance}
          </p>
        ) : null}
        <p className="muted">
          Your System, Light, or Dark choice stays on this device. DevHud has no account or
          cloud sync.
        </p>
        <ResetDevHudControl />
      </div>
    </section>
  );
}

function diagnosticPlatformLabel(platform: RuntimeInfo["operatingSystem"]): string {
  const labels: Record<RuntimeInfo["operatingSystem"], string> = {
    android: "Android",
    ios: "iOS",
    linux: "Linux",
    macos: "macOS",
    windows: "Windows",
  };
  return labels[platform];
}

function MobileDiagnostics({
  retryRuntime,
  runtime,
}: {
  retryRuntime(): void;
  runtime: RuntimeState;
}) {
  return (
    <section aria-label="Diagnostics" className="mobile-screen">
      <MobileScreenHeading eyebrow="Diagnostics" title="Local diagnostics" />
      {runtime.status === "loading" ? (
        <section aria-labelledby="diagnostics-loading-title" className="state-card">
          <h2 id="diagnostics-loading-title">Loading diagnostics</h2>
          <p role="status">Reading safe local runtime information…</p>
        </section>
      ) : null}
      {runtime.status === "failed" ? (
        <RuntimeFailure message={runtime.message} onRetry={retryRuntime} />
      ) : null}
      {runtime.status === "ready" ? (
        <section aria-labelledby="diagnostics-ready-title" className="diagnostics-card">
          <h2 id="diagnostics-ready-title">Runtime details</h2>
          <dl>
            <div>
              <dt>Application ID</dt>
              <dd>{runtime.runtimeInfo.applicationId}</dd>
            </div>
            <div>
              <dt>Platform</dt>
              <dd>{diagnosticPlatformLabel(runtime.runtimeInfo.operatingSystem)}</dd>
            </div>
            <div>
              <dt>Web runtime</dt>
              <dd>System WebView</dd>
            </div>
            <div>
              <dt>Updates</dt>
              <dd>{runtime.runtimeInfo.updatePolicy}</dd>
            </div>
          </dl>
          <p className="muted">
            Diagnostics stay on this device and contain no account, telemetry, or remote
            service data.
          </p>
        </section>
      ) : null}
    </section>
  );
}

function MobileContent({
  retryRuntime,
  runtime,
}: {
  retryRuntime(): void;
  runtime: RuntimeState;
}) {
  const { mobileScreen } = useApplication();
  switch (mobileScreen) {
    case MobileScreen.Home:
      return <MobileHome retryRuntime={retryRuntime} runtime={runtime} />;
    case MobileScreen.Widgets:
      return <MobileWidgets />;
    case MobileScreen.Settings:
      return <MobileSettings />;
    case MobileScreen.Diagnostics:
      return <MobileDiagnostics retryRuntime={retryRuntime} runtime={runtime} />;
  }
}

function MobileShell({
  retryRuntime,
  runtime,
}: {
  retryRuntime(): void;
  runtime: RuntimeState;
}) {
  const { mobileScreen, setMobileScreen } = useApplication();
  return (
    <main className="mobile-shell">
      <header className="app-header mobile-header">
        <Wordmark />
      </header>
      <div className="mobile-layout">
        <nav aria-label="Primary" className="mobile-nav">
          {Object.values(MobileScreen).map((screen) => (
            <button
              aria-current={mobileScreen === screen ? "page" : undefined}
              key={screen}
              onClick={() => setMobileScreen(screen)}
              type="button"
            >
              {mobileScreenLabels[screen]}
            </button>
          ))}
        </nav>
        <div className="mobile-content" key={mobileScreen}>
          <MobileContent retryRuntime={retryRuntime} runtime={runtime} />
        </div>
      </div>
    </main>
  );
}

function ApplicationSurface({
  desktopBridge,
  initialPlatform,
  runtimeBridge,
  synchronizePlatform,
}: {
  readonly desktopBridge?: DesktopBridge | null;
  readonly initialPlatform: ApplicationPlatform;
  readonly runtimeBridge: RuntimeBridge;
  readonly synchronizePlatform: boolean;
}) {
  const [platform, setPlatform] = useState(initialPlatform);
  const [runtime, setRuntime] = useState<RuntimeState>({ status: "loading" });
  const [runtimeAttempt, setRuntimeAttempt] = useState(0);
  const clearStartupDiagnostics = useCallback(() => {
    setRuntime((current) =>
      current.status === "ready"
        ? {
            status: "ready",
            runtimeInfo: {
              ...current.runtimeInfo,
              autostartStartupOutcome: null,
              shortcutStartupFailure: null,
            },
          }
        : current,
    );
  }, []);
  const retryRuntime = useCallback(() => {
    setRuntime({ status: "loading" });
    setRuntimeAttempt((attempt) => attempt + 1);
  }, []);

  useEffect(() => {
    let active = true;
    void loadRuntimeInfo(runtimeBridge).then(
      (runtimeInfo) => {
        if (!active) return;
        setRuntime({ status: "ready", runtimeInfo });
        if (synchronizePlatform) setPlatform(platformForRuntime(runtimeInfo.runtime));
      },
      () => {
        if (active) setRuntime({ status: "failed", message: runtimeFailureMessage });
      },
    );
    return () => {
      active = false;
    };
  }, [runtimeAttempt, runtimeBridge, synchronizePlatform]);
  const bridge = useMemo(
    () =>
      desktopBridge === undefined
        ? runtime.status === "ready"
          ? nativeDesktopBridge(runtime.runtimeInfo.runtime)
          : null
        : desktopBridge,
    [desktopBridge, runtime],
  );
  const {
    adoptNativeTheme,
    settingsOpen,
  } = useApplication();
  useEffect(() => bridge?.subscribeTheme(adoptNativeTheme), [
    adoptNativeTheme,
    bridge,
  ]);
  const reconcileReset = useCallback(() => {
    clearStartupDiagnostics();
    bridge?.publishTheme(ThemePreference.System);
  }, [bridge, clearStartupDiagnostics]);

  if (
    runtime.status === "ready" &&
    runtime.runtimeInfo.surface === "settings"
  ) {
    return (
      <SettingsWindow
        bridge={bridge}
        firstRun={runtime.runtimeInfo.firstRun === true}
        onResetComplete={reconcileReset}
        startupAutostartOutcome={runtime.runtimeInfo.autostartStartupOutcome}
        startupShortcutFailure={runtime.runtimeInfo.shortcutStartupFailure}
      />
    );
  }

  return (
    <>
      <div aria-hidden={settingsOpen} inert={settingsOpen}>
        {platform === "desktop" ? <PersistenceAlerts /> : null}
        {platform === "desktop" ? (
          <DesktopHud
            bridge={bridge}
            retryRuntime={retryRuntime}
            runtime={runtime}
          />
        ) : (
          <MobileShell retryRuntime={retryRuntime} runtime={runtime} />
        )}
      </div>
      {settingsOpen ? (
        <SettingsDialog
          bridge={bridge}
          onResetComplete={reconcileReset}
          runtimeInfo={runtime.status === "ready" ? runtime.runtimeInfo : null}
          showDesktopControls={false}
        />
      ) : null}
    </>
  );
}

export function App({
  desktopBridge,
  platform,
  runtimeBridge = tauriRuntimeBridge,
  storage,
}: {
  readonly desktopBridge?: DesktopBridge | null;
  readonly platform?: ApplicationPlatform;
  readonly runtimeBridge?: RuntimeBridge;
  readonly storage?: LocalStorageAdapter;
}) {
  const synchronizePlatform = platform === undefined;
  const initialPlatform =
    platform ?? detectApplicationPlatform(navigator.userAgent);
  return (
    <ApplicationProvider storage={storage}>
      <ApplicationSurface
        desktopBridge={desktopBridge}
        initialPlatform={initialPlatform}
        runtimeBridge={runtimeBridge}
        synchronizePlatform={synchronizePlatform}
      />
    </ApplicationProvider>
  );
}
