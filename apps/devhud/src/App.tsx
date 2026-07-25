import { useEffect, useMemo, useRef, useState } from "react";

import { MemoryStorageAdapter, type LocalStorageAdapter } from "./persistence/storage";
import {
  detectApplicationPlatform,
  platformForRuntime,
  type ApplicationPlatform,
} from "./runtime/platform";
import {
  loadRuntimeInfo,
  tauriRuntimeBridge,
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
  useApplication,
} from "./ui/state";

type RuntimeState =
  | { status: "loading" }
  | { status: "failed"; message: string }
  | { status: "ready"; runtimeInfo: RuntimeInfo };

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
  runtimeInfo,
}: {
  readonly bridge: DesktopBridge | null;
  readonly runtimeInfo: RuntimeInfo | null;
}) {
  const { closeSettings } = useApplication();
  return (
    <Dialog title="DevHud settings" onClose={closeSettings}>
      <PersistenceAlerts />
      <SettingsPanel
        bridge={bridge}
        onClose={closeSettings}
        startupAutostartOutcome={runtimeInfo?.autostartStartupOutcome}
        startupShortcutFailure={runtimeInfo?.shortcutStartupFailure}
      />
    </Dialog>
  );
}

function SettingsWindow({
  bridge,
  firstRun,
  startupAutostartOutcome,
  startupShortcutFailure,
}: {
  readonly bridge: DesktopBridge | null;
  readonly firstRun: boolean;
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
    </main>
  );
}

function EmptyTools({
  bridge,
  compact = false,
}: {
  readonly bridge: DesktopBridge | null;
  readonly compact?: boolean;
}) {
  const { openSettings } = useApplication();
  const showSettings = () => {
    if (bridge === null) openSettings();
    else void bridge.showSettings();
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

const NO_TOOL_CAPABILITIES: ReadonlySet<ToolCapability> = new Set();

function ProductionToolSurface({
  bridge,
}: {
  readonly bridge: DesktopBridge | null;
}) {
  const availableTools = filterTools(productionTools, {
    platform: ToolPlatform.Desktop,
    grantedCapabilities: NO_TOOL_CAPABILITIES,
  });
  if (availableTools.length === 0) return <EmptyTools bridge={bridge} />;
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
  runtime,
}: {
  readonly bridge: DesktopBridge | null;
  readonly runtime: RuntimeState;
}) {
  const searchRef = useRef<HTMLInputElement>(null);
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
    else void bridge.showSettings();
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
        {runtime.status === "loading" ? (
          <p className="runtime-status" role="status">
            Starting DevHud…
          </p>
        ) : null}
        {runtime.status === "failed" ? (
          <p className="runtime-status error" role="alert">
            {runtime.message}
          </p>
        ) : null}
        <ProductionToolSurface bridge={bridge} />
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

function MobileContent({ runtime }: { readonly runtime: RuntimeState }) {
  const { mobileScreen, openSettings } = useApplication();
  if (mobileScreen === MobileScreen.Home) {
    return (
      <>
        {runtime.status === "loading" ? (
          <p className="runtime-status" role="status">
            Starting DevHud…
          </p>
        ) : null}
        {runtime.status === "failed" ? (
          <p className="runtime-status error" role="alert">
            {runtime.message}
          </p>
        ) : null}
        <EmptyTools bridge={null} compact />
      </>
    );
  }
  if (mobileScreen === MobileScreen.Widgets) {
    return (
      <section
        className="empty-state compact"
        aria-labelledby="widgets-title"
      >
        <p className="eyebrow">Widgets</p>
        <h1 id="widgets-title">No widgets available</h1>
        <p>Visible widgets are not part of this foundation preview.</p>
      </section>
    );
  }
  if (mobileScreen === MobileScreen.Settings) {
    return (
      <section
        className="empty-state compact"
        aria-labelledby="settings-title"
      >
        <p className="eyebrow">Settings</p>
        <h1 id="settings-title">Choose your appearance</h1>
        <p>Use your device preference, a light theme, or a dark theme.</p>
        <button className="primary-button" onClick={openSettings} type="button">
          Open settings
        </button>
      </section>
    );
  }
  return (
    <section
      className="empty-state compact"
      aria-labelledby="diagnostics-title"
    >
      <p className="eyebrow">Diagnostics</p>
      <h1 id="diagnostics-title">Diagnostics are unavailable</h1>
      {runtime.status === "loading" ? (
        <p role="status">Loading local diagnostics…</p>
      ) : null}
      {runtime.status === "failed" ? (
        <p role="alert">{runtime.message}</p>
      ) : null}
      {runtime.status === "ready" ? (
        <p>Local diagnostics are not exposed in this foundation preview.</p>
      ) : null}
    </section>
  );
}

function MobileShell({ runtime }: { readonly runtime: RuntimeState }) {
  const { mobileScreen, setMobileScreen } = useApplication();
  return (
    <main className="mobile-shell">
      <header className="app-header">
        <Wordmark />
      </header>
      <MobileContent runtime={runtime} />
      <nav aria-label="Mobile navigation" className="mobile-nav">
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
    </main>
  );
}

function ApplicationSurface({
  desktopBridge,
  initialPlatform,
  synchronizePlatform,
}: {
  readonly desktopBridge?: DesktopBridge | null;
  readonly initialPlatform: ApplicationPlatform;
  readonly synchronizePlatform: boolean;
}) {
  const [platform, setPlatform] = useState(initialPlatform);
  const [runtime, setRuntime] = useState<RuntimeState>({ status: "loading" });
  useEffect(() => {
    let active = true;
    void loadRuntimeInfo(tauriRuntimeBridge).then(
      (runtimeInfo) => {
        if (active) {
          setRuntime({ status: "ready", runtimeInfo });
          if (synchronizePlatform) {
            setPlatform(platformForRuntime(runtimeInfo.runtime));
          }
        }
      },
      () => {
        if (active) {
          setRuntime({
            status: "failed",
            message: "DevHud could not initialize its local runtime.",
          });
        }
      },
    );
    return () => {
      active = false;
    };
  }, [synchronizePlatform]);
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

  if (
    runtime.status === "ready" &&
    runtime.runtimeInfo.surface === "settings"
  ) {
    return (
      <SettingsWindow
        bridge={bridge}
        firstRun={runtime.runtimeInfo.firstRun === true}
        startupAutostartOutcome={runtime.runtimeInfo.autostartStartupOutcome}
        startupShortcutFailure={runtime.runtimeInfo.shortcutStartupFailure}
      />
    );
  }

  return (
    <>
      <div aria-hidden={settingsOpen} inert={settingsOpen}>
        <PersistenceAlerts />
        {platform === "desktop" ? (
          <DesktopHud bridge={bridge} runtime={runtime} />
        ) : (
          <MobileShell runtime={runtime} />
        )}
      </div>
      {settingsOpen ? (
        <SettingsDialog
          bridge={bridge}
          runtimeInfo={runtime.status === "ready" ? runtime.runtimeInfo : null}
        />
      ) : null}
    </>
  );
}

export function App({
  desktopBridge,
  platform,
  storage = new MemoryStorageAdapter(),
}: {
  readonly desktopBridge?: DesktopBridge | null;
  readonly platform?: ApplicationPlatform;
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
        synchronizePlatform={synchronizePlatform}
      />
    </ApplicationProvider>
  );
}
