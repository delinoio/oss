export enum ProbeId {
  BundledAssetStartup = "bundled-asset-startup",
  IpcCapabilityDenial = "ipc-capability-denial",
  TrayLifecycle = "tray-lifecycle",
  GlobalShortcut = "global-shortcut",
  Autostart = "autostart",
  Theme = "theme",
  DevTools = "devtools",
  ExplicitShutdown = "explicit-shutdown",
  RepeatedLifecycle = "repeated-lifecycle",
  RuntimeFailure = "runtime-failure",
  HelperProcessCleanup = "helper-process-cleanup",
  DiagnosticSafety = "diagnostic-safety",
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

export enum MacOSSigningMode {
  DeveloperId = "developer-id",
  SignReady = "sign-ready",
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
  dockHidden: boolean;
  dockPolicyPersistsAfterWindowClose: boolean;
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

export interface RepeatedLifecycleEvidence {
  completedCycles: number;
  cleanShutdownCycles: number;
  orphanFreeCycles: number;
}

export interface RuntimeFailureEvidence {
  fatalKinds: readonly RuntimeFailureKind[];
  immediateExitKinds: readonly RuntimeFailureKind[];
  automaticRestartCount: 0;
}

export interface HelperCleanupEvidence {
  helperProcessCountBeforeShutdown: number;
  helperProcessCountAfterShutdown: 0;
}

export interface DiagnosticSafetyEvidence {
  shortcutValueAbsent: boolean;
  arbitraryPathAbsent: boolean;
  environmentValueAbsent: boolean;
  signingMaterialAbsent: boolean;
}

export interface PackagingEvidence {
  architecture: Architecture;
  checkedFormats: readonly PackageFormat[];
  bundledAssetsPresent: boolean;
  cefHelpersPresent: boolean;
  signingMode?: MacOSSigningMode;
  signReady: boolean;
}

export interface SignedUpdaterEvidence {
  architecture: Architecture;
  updaterFormatCompatible: boolean;
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
  [ProbeId.RepeatedLifecycle]: RepeatedLifecycleEvidence;
  [ProbeId.RuntimeFailure]: RuntimeFailureEvidence;
  [ProbeId.HelperProcessCleanup]: HelperCleanupEvidence;
  [ProbeId.DiagnosticSafety]: DiagnosticSafetyEvidence;
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
