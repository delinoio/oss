import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { AuthFeature, SessionProvider, useSession } from "./auth/SessionProvider";
import type { NativeSessionBridge } from "./auth/contracts";
import { DeckGatewayProvider, MobileDeckToolEntry } from "./deck/DeckToolEntry";
import type { DeckGateway } from "./deck/contracts";
import type {
  LocalStorageAdapter,
  PersistenceResetOutcome,
} from "./persistence/storage";
import {
  detectApplicationPlatform,
  platformForRuntime,
  type ApplicationPlatform,
} from "./runtime/platform";
import {
  exportDiagnostics,
  loadRuntimeInfo,
  tauriRuntimeBridge,
  type RuntimeBridge,
  type RuntimeInfo,
} from "./runtime/startup";
import {
  nativeDesktopBridge,
  type DesktopBridge,
} from "./runtime/desktop";
import { publishPersistenceReset } from "./runtime/theme";
import {
  filterTools,
  productionTools,
  type ToolCapability,
  ToolPlatform,
} from "./tools/registry";
import { BrowserCaptureComposer } from "./realqa/BrowserCaptureComposer";
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
  diagnosticsBridge,
  onResetComplete,
  settingsRevision,
  showDesktopControls,
  runtimeInfo,
}: {
  readonly bridge: DesktopBridge | null;
  readonly diagnosticsBridge: RuntimeBridge;
  readonly onResetComplete: (outcome: PersistenceResetOutcome) => void;
  readonly settingsRevision: number;
  readonly showDesktopControls: boolean;
  readonly runtimeInfo: RuntimeInfo | null;
}) {
  const { closeSettings } = useApplication();
  return (
    <Dialog title="DevHud settings" onClose={closeSettings}>
      <PersistenceAlerts />
      <SettingsPanel
        bridge={bridge}
        key={settingsRevision}
        onClose={closeSettings}
        showDesktopControls={showDesktopControls}
        startupAutostartOutcome={runtimeInfo?.autostartStartupOutcome}
        startupShortcutFailure={runtimeInfo?.shortcutStartupFailure}
      />
      <DiagnosticsExportControl bridge={diagnosticsBridge} />
      <ResetDevHudControl
        onResetComplete={onResetComplete}
        showChromePermissionGuidance={showDesktopControls}
      />
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

type ResetStatus =
  | "idle"
  | "confirming"
  | "resetting"
  | "failed"
  | "partially-retained"
  | "cleanup-failed"
  | "integration-rollback-failed"
  | "complete";

function ResetDevHudControl({
  onResetComplete,
  showChromePermissionGuidance = false,
}: {
  readonly onResetComplete?: (outcome: PersistenceResetOutcome) => void;
  readonly showChromePermissionGuidance?: boolean;
}) {
  const { persistenceReady, resetDevHud } = useApplication();
  const [status, setStatus] = useState<ResetStatus>("idle");
  const resetTriggerRef = useRef<HTMLButtonElement>(null);

  const confirmReset = async () => {
    setStatus("resetting");
    try {
      const outcome = await resetDevHud();
      onResetComplete?.(outcome);
      setStatus(outcome.status);
    } catch {
      setStatus("failed");
    }
  };
  const cancelReset = () => {
    if (status === "confirming") setStatus("idle");
  };

  return (
    <section aria-labelledby="reset-devhud-title" className="reset-section">
      <h3 id="reset-devhud-title">Reset DevHud</h3>
      <p className="muted">
        Clear local settings, widget state, and application browsing data from this
        device.
      </p>
      {showChromePermissionGuidance ? (
        <p className="muted">
          Reset also clears DevHud extension pairing and pending host data. Chrome
          owns site permissions; open <strong>chrome://extensions</strong> to review
          or remove them.
        </p>
      ) : null}
      <button
        className="secondary-button"
        disabled={!persistenceReady || status === "resetting"}
        onClick={() => setStatus("confirming")}
        ref={resetTriggerRef}
        type="button"
      >
        {status === "resetting" ? "Resetting DevHud…" : "Reset DevHud"}
      </button>
      {status === "confirming" || status === "resetting" ? (
        <Dialog
          descriptionId="reset-devhud-description"
          title="Confirm Reset DevHud"
          onClose={cancelReset}
        >
          <div className="dialog-heading">
            <div>
              <p className="eyebrow">Local data</p>
              <h2>Reset DevHud?</h2>
            </div>
          </div>
          <p id="reset-devhud-description">
            This cannot be undone. DevHud will clear its local settings, widget
            state, browsing data, and rotating logs. Diagnostic files you
            previously exported will not be changed.
            {showChromePermissionGuidance
              ? " Chrome-owned extension permissions remain; review them in chrome://extensions."
              : ""}
          </p>
          <div className="reset-actions">
            <button
              aria-describedby="reset-devhud-description"
              className="danger-button"
              disabled={status === "resetting"}
              onClick={() => void confirmReset()}
              type="button"
            >
              {status === "resetting" ? "Resetting…" : "Confirm reset"}
            </button>
            <button
              className="secondary-button"
              disabled={status === "resetting"}
              onClick={cancelReset}
              type="button"
            >
              Cancel
            </button>
          </div>
        </Dialog>
      ) : null}
      {status === "complete" ? (
        <p role="status">
          <span>DevHud local data was reset.</span>
          {showChromePermissionGuidance
            ? " Review Chrome-owned permissions in chrome://extensions."
            : ""}
        </p>
      ) : null}
      {status === "failed" ? (
        <p className="error" role="alert">
          DevHud could not reset local data. Check device storage and try again.
        </p>
      ) : null}
      {status === "cleanup-failed" ? (
        <p className="error" role="alert">
          DevHud cleared local settings, but temporary reset data or application
          browsing data may remain. Check device storage and try again.
        </p>
      ) : null}
      {status === "partially-retained" ? (
        <p className="error" role="alert">
          DevHud reset only some local data. Some saved settings or widget state
          remain. Check device storage and try again.
        </p>
      ) : null}
      {status === "integration-rollback-failed" ? (
        <p className="error" role="alert">
          DevHud could not reset local data or fully restore the previous system
          integrations. The effective shortcut and launch-at-login settings are
          shown.
        </p>
      ) : null}
    </section>
  );
}

function SettingsWindow({
  bridge,
  diagnosticsBridge,
  firstRun,
  onResetComplete,
  settingsRevision,
  startupAutostartOutcome,
  startupShortcutFailure,
}: {
  readonly bridge: DesktopBridge | null;
  readonly diagnosticsBridge: RuntimeBridge;
  readonly firstRun: boolean;
  readonly onResetComplete: (outcome: PersistenceResetOutcome) => void;
  readonly settingsRevision: number;
  readonly startupAutostartOutcome: RuntimeInfo["autostartStartupOutcome"];
  readonly startupShortcutFailure: RuntimeInfo["shortcutStartupFailure"];
}) {
  const [firstRunActive, setFirstRunActive] = useState(firstRun);
  const [closeFailed, setCloseFailed] = useState(false);
  const close = () => {
    setCloseFailed(false);
    void (bridge?.hideSettings() ?? Promise.resolve()).catch(() => {
      setCloseFailed(true);
    });
  };
  return (
    <main className="settings-shell">
      <PersistenceAlerts />
      {closeFailed ? (
        <p className="runtime-status error" role="alert">
          DevHud could not close Settings. Try again or use the tray Quit action.
        </p>
      ) : null}
      <SettingsPanel
        bridge={bridge}
        firstRun={firstRunActive}
        key={settingsRevision}
        onClose={close}
        onFirstRunCompleted={() => setFirstRunActive(false)}
        startupAutostartOutcome={startupAutostartOutcome}
        startupShortcutFailure={startupShortcutFailure}
      />
      <DiagnosticsExportControl bridge={diagnosticsBridge} />
      <ResetDevHudControl
        onResetComplete={onResetComplete}
        showChromePermissionGuidance
      />
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
  const [hideFailure, setHideFailure] = useState(false);
  const [settingsFailure, setSettingsFailure] = useState(false);
  const { openSettings } = useApplication();
  useEffect(() => {
    let active = true;
    const focusSearch = () => searchRef.current?.focus();
    focusSearch();
    window.addEventListener("devhud:shown", focusSearch);
    const hideHud = () => {
      if (bridge === null) return;
      setHideFailure(false);
      void bridge.hideHud().then(
        (outcome) => {
          if (active && outcome.status === "unchanged") setHideFailure(true);
        },
        () => {
          if (active) setHideFailure(true);
        },
      );
    };
    const hideForBlur = () => {
      hideHud();
    };
    const hideForEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        hideHud();
      }
    };
    window.addEventListener("blur", hideForBlur);
    document.addEventListener("keydown", hideForEscape);
    return () => {
      active = false;
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
        {hideFailure ? (
          <p className="runtime-status error" role="alert">
            DevHud could not hide this window. Try again or use the tray Quit action.
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
  [MobileScreen.Deck]: "Deck",
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
  const { failure, ready, session, signIn } = useSession();
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
      {runtime.status === "ready" ? (
        <>
          <EmptyTools compact />
          {ready && session.status !== "signed-in" ? (
            <section aria-labelledby="mobile-account-title" className="state-card">
              <h2 id="mobile-account-title">Internal tools</h2>
              <p>Sign in with DeliDev to show authenticated tools on this device.</p>
              <button
                className="primary-button"
                disabled={session.status === "authenticating" || session.status === "cleanup-required"}
                onClick={() => void signIn(AuthFeature.Deck)}
                type="button"
              >
                {session.status === "authenticating" ? "Signing in…" : "Sign in"}
              </button>
              {failure ? <p className="error" role="alert">{failure.guidance}</p> : null}
            </section>
          ) : null}
        </>
      ) : null}
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

function MobileDeck() {
  return (
    <section aria-label="Deck" className="mobile-screen mobile-deck-screen">
      <MobileDeckToolEntry />
    </section>
  );
}

function MobileSettings({
  onResetComplete,
}: {
  readonly onResetComplete: (outcome: PersistenceResetOutcome) => void;
}) {
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
        <ResetDevHudControl onResetComplete={onResetComplete} />
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

type DiagnosticsExportStatus =
  | "idle"
  | "exporting"
  | "exported"
  | "cancelled"
  | "failed";

function DiagnosticsExportControl({
  bridge,
}: {
  readonly bridge: RuntimeBridge;
}) {
  const [status, setStatus] = useState<DiagnosticsExportStatus>("idle");
  const startExport = async () => {
    setStatus("exporting");
    try {
      const outcome = await exportDiagnostics(bridge);
      setStatus(outcome.status);
    } catch {
      setStatus("failed");
    }
  };

  return (
    <section aria-labelledby="diagnostics-export-title" className="settings-section">
      <h2 id="diagnostics-export-title">Diagnostics export</h2>
      <p className="muted">
        Save a redacted local diagnostics file to a destination you select. DevHud
        never sends diagnostics remotely.
      </p>
      <button
        className="secondary-button"
        disabled={status === "exporting"}
        onClick={() => void startExport()}
        type="button"
      >
        {status === "exporting" ? "Choosing destination…" : "Export diagnostics"}
      </button>
      {status === "exported" ? (
        <p role="status">Diagnostics were saved to your selected destination.</p>
      ) : null}
      {status === "cancelled" ? (
        <p role="status">Diagnostics export was cancelled. No file was changed.</p>
      ) : null}
      {status === "failed" ? (
        <p className="error" role="alert">
          DevHud could not export diagnostics. Choose a writable destination and try
          again.
        </p>
      ) : null}
    </section>
  );
}

function MobileDiagnostics({
  diagnosticsBridge,
  retryRuntime,
  runtime,
}: {
  diagnosticsBridge: RuntimeBridge;
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
          <DiagnosticsExportControl bridge={diagnosticsBridge} />
        </section>
      ) : null}
    </section>
  );
}

function MobileContent({
  diagnosticsBridge,
  onResetComplete,
  retryRuntime,
  runtime,
}: {
  diagnosticsBridge: RuntimeBridge;
  onResetComplete: (outcome: PersistenceResetOutcome) => void;
  retryRuntime(): void;
  runtime: RuntimeState;
}) {
  const { mobileScreen } = useApplication();
  switch (mobileScreen) {
    case MobileScreen.Home:
      return <MobileHome retryRuntime={retryRuntime} runtime={runtime} />;
    case MobileScreen.Deck:
      return <MobileDeck />;
    case MobileScreen.Widgets:
      return <MobileWidgets />;
    case MobileScreen.Settings:
      return <MobileSettings onResetComplete={onResetComplete} />;
    case MobileScreen.Diagnostics:
      return (
        <MobileDiagnostics
          diagnosticsBridge={diagnosticsBridge}
          retryRuntime={retryRuntime}
          runtime={runtime}
        />
      );
  }
}

function MobileShell({
  diagnosticsBridge,
  onResetComplete,
  retryRuntime,
  runtime,
}: {
  diagnosticsBridge: RuntimeBridge;
  onResetComplete: (outcome: PersistenceResetOutcome) => void;
  retryRuntime(): void;
  runtime: RuntimeState;
}) {
  const { mobileScreen, setMobileScreen } = useApplication();
  const { session } = useSession();
  const mobileScreens = Object.values(MobileScreen).filter(
    (screen) => screen !== MobileScreen.Deck || session.status === "signed-in",
  );
  useEffect(() => {
    if (mobileScreen === MobileScreen.Deck && session.status !== "signed-in") {
      setMobileScreen(MobileScreen.Home);
    }
  }, [mobileScreen, session.status, setMobileScreen]);
  return (
    <main className="mobile-shell">
      <header className="app-header mobile-header">
        <Wordmark />
      </header>
      <div className="mobile-layout">
        <nav aria-label="Primary" className="mobile-nav">
          {mobileScreens.map((screen) => (
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
          <MobileContent
            diagnosticsBridge={diagnosticsBridge}
            onResetComplete={onResetComplete}
            retryRuntime={retryRuntime}
            runtime={runtime}
          />
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
  const [settingsRevision, setSettingsRevision] = useState(0);
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
    readPersistedTheme,
    reloadPersistence,
    settingsOpen,
  } = useApplication();
  useEffect(() => {
    if (bridge === null) return;
    let themePublished = false;
    let active = true;
    const unsubscribe = bridge.subscribeTheme((theme) => {
      themePublished = true;
      adoptNativeTheme(theme);
    });
    void readPersistedTheme().then((theme) => {
      if (active && !themePublished && theme !== null) adoptNativeTheme(theme);
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, [
    adoptNativeTheme,
    bridge,
    readPersistedTheme,
  ]);
  useEffect(() => {
    if (bridge === null) return;
    return bridge.subscribeReset((outcome) => {
      clearStartupDiagnostics();
      setSettingsRevision((revision) => revision + 1);
      void reloadPersistence(outcome);
    });
  }, [bridge, clearStartupDiagnostics, reloadPersistence]);
  const reconcileReset = useCallback((outcome: PersistenceResetOutcome) => {
    clearStartupDiagnostics();
    setSettingsRevision((revision) => revision + 1);
    (bridge?.publishReset ?? publishPersistenceReset)(outcome);
    if (
      outcome.status === "complete" ||
      outcome.status === "cleanup-failed"
    ) {
      bridge?.publishTheme(ThemePreference.System);
    }
  }, [bridge, clearStartupDiagnostics]);

  if (
    runtime.status === "ready" &&
    runtime.runtimeInfo.surface === "settings"
  ) {
    return (
      <SettingsWindow
        bridge={bridge}
        diagnosticsBridge={runtimeBridge}
        firstRun={runtime.runtimeInfo.firstRun === true}
        onResetComplete={reconcileReset}
        settingsRevision={settingsRevision}
        startupAutostartOutcome={runtime.runtimeInfo.autostartStartupOutcome}
        startupShortcutFailure={runtime.runtimeInfo.shortcutStartupFailure}
      />
    );
  }

  if (
    runtime.status === "ready" &&
    runtime.runtimeInfo.surface === "realqa-composer"
  ) {
    return <BrowserCaptureComposer />;
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
          <MobileShell
            diagnosticsBridge={runtimeBridge}
            onResetComplete={reconcileReset}
            retryRuntime={retryRuntime}
            runtime={runtime}
          />
        )}
      </div>
      {settingsOpen ? (
        <SettingsDialog
          bridge={bridge}
          diagnosticsBridge={runtimeBridge}
          onResetComplete={reconcileReset}
          runtimeInfo={runtime.status === "ready" ? runtime.runtimeInfo : null}
          settingsRevision={settingsRevision}
          showDesktopControls={false}
        />
      ) : null}
    </>
  );
}

export function App({
  deckGateway,
  desktopBridge,
  platform,
  runtimeBridge = tauriRuntimeBridge,
  sessionBridge,
  storage,
}: {
  readonly deckGateway?: DeckGateway;
  readonly desktopBridge?: DesktopBridge | null;
  readonly platform?: ApplicationPlatform;
  readonly runtimeBridge?: RuntimeBridge;
  readonly sessionBridge?: NativeSessionBridge;
  readonly storage?: LocalStorageAdapter;
}) {
  const synchronizePlatform = platform === undefined;
  const initialPlatform =
    platform ?? detectApplicationPlatform(navigator.userAgent);
  return (
    <SessionProvider bridge={sessionBridge}>
      <DeckGatewayProvider gateway={deckGateway}>
        <ApplicationProvider storage={storage}>
          <ApplicationSurface
            desktopBridge={desktopBridge}
            initialPlatform={initialPlatform}
            runtimeBridge={runtimeBridge}
            synchronizePlatform={synchronizePlatform}
          />
        </ApplicationProvider>
      </DeckGatewayProvider>
    </SessionProvider>
  );
}
