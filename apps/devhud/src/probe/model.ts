export enum ProbeId {
  BundledAssetStartup = "bundled-asset-startup",
  IpcCapabilityDenial = "ipc-capability-denial",
  TrayLifecycle = "tray-lifecycle",
  GlobalShortcut = "global-shortcut",
  Autostart = "autostart",
  Theme = "theme",
  DevTools = "devtools",
  ExplicitShutdown = "explicit-shutdown",
  RuntimeFailure = "runtime-failure",
  HelperProcessCleanup = "helper-process-cleanup",
  Packaging = "packaging",
  SignedUpdater = "signed-updater",
}

export enum DesktopPlatform {
  Linux = "linux",
  MacOS = "macos",
  Windows = "windows",
}

export enum Architecture {
  Arm64 = "arm64",
  X64 = "x64",
}

export enum DisplayProtocol {
  NotApplicable = "not-applicable",
  X11 = "x11",
  XWayland = "xwayland",
}

export enum PackageFormat {
  AppImage = "appimage",
  Deb = "deb",
  Dmg = "dmg",
  Nsis = "nsis",
}

export enum RuntimeFailureKind {
  CefInitialization = "cef-initialization",
  RendererTermination = "renderer-termination",
}

export enum ThemeMode {
  Dark = "dark",
  Light = "light",
  System = "system",
}

export interface ProbeTarget {
  platform: DesktopPlatform;
  architecture: Architecture;
  displayProtocol: DisplayProtocol;
}

export interface BundledAssetEvidence {
  origin: string;
  sandboxEnabled: boolean;
  remoteRequestCount: 0;
}

export interface IpcCapabilityEvidence {
  allowedCommandCompleted: boolean;
  undeclaredCommandDenied: boolean;
}

export interface TrayLifecycleEvidence {
  created: boolean;
  remainsResidentAfterWindowClose: boolean;
  quitTerminates: boolean;
}

export interface GlobalShortcutEvidence {
  registered: boolean;
  togglesProbeWindow: boolean;
  releasedOnShutdown: boolean;
}

export interface AutostartEvidence {
  disabledByDefault: boolean;
  enableDisableRoundTrip: boolean;
}

export interface ThemeEvidence {
  observedModes: readonly ThemeMode[];
  systemChangeObserved: boolean;
}

export interface DevToolsEvidence {
  opened: boolean;
  capabilityBoundaryPreserved: boolean;
  remoteNavigationDenied: boolean;
}

export interface ShutdownEvidence {
  requestedExplicitly: boolean;
  exitCode: 0;
}

export interface RuntimeFailureEvidence {
  fatalKinds: readonly RuntimeFailureKind[];
  automaticRestartCount: 0;
}

export interface HelperCleanupEvidence {
  helperProcessCountBeforeShutdown: number;
  helperProcessCountAfterShutdown: 0;
}

export interface PackagingEvidence {
  checkedFormats: readonly PackageFormat[];
  bundledAssetsPresent: boolean;
  cefHelpersPresent: boolean;
  signReady: boolean;
}

export interface SignedUpdaterEvidence {
  signedBundleCreated: boolean;
  validSignatureAccepted: boolean;
  invalidSignatureRejected: boolean;
}

export interface ProbeEvidenceMap {
  [ProbeId.BundledAssetStartup]: BundledAssetEvidence;
  [ProbeId.IpcCapabilityDenial]: IpcCapabilityEvidence;
  [ProbeId.TrayLifecycle]: TrayLifecycleEvidence;
  [ProbeId.GlobalShortcut]: GlobalShortcutEvidence;
  [ProbeId.Autostart]: AutostartEvidence;
  [ProbeId.Theme]: ThemeEvidence;
  [ProbeId.DevTools]: DevToolsEvidence;
  [ProbeId.ExplicitShutdown]: ShutdownEvidence;
  [ProbeId.RuntimeFailure]: RuntimeFailureEvidence;
  [ProbeId.HelperProcessCleanup]: HelperCleanupEvidence;
  [ProbeId.Packaging]: PackagingEvidence;
  [ProbeId.SignedUpdater]: SignedUpdaterEvidence;
}

export type ProbeResult =
  | {
      id: ProbeId;
      status: "passed";
      evidence: ProbeEvidenceMap[ProbeId];
    }
  | {
      id: ProbeId;
      status: "failed";
      reason: string;
    }
  | {
      id: ProbeId;
      status: "blocked";
      reason: string;
      upstreamReference: string;
    };

export interface ProbeReport {
  target: ProbeTarget;
  results: readonly ProbeResult[];
  passed: boolean;
}
