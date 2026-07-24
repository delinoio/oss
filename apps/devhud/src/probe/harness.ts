import {
  DesktopPlatform,
  DisplayProtocol,
  PackageFormat,
  ProbeId,
  RuntimeFailureKind,
  ThemeMode,
  type AutostartEvidence,
  type BundledAssetEvidence,
  type DevToolsEvidence,
  type GlobalShortcutEvidence,
  type HelperCleanupEvidence,
  type IpcCapabilityEvidence,
  type PackagingEvidence,
  type ProbeEvidenceMap,
  type ProbeReport,
  type ProbeResult,
  type ProbeTarget,
  type RuntimeFailureEvidence,
  type ShutdownEvidence,
  type SignedUpdaterEvidence,
  type ThemeEvidence,
  type TrayLifecycleEvidence,
} from "./model";

export interface ProbeDriver {
  bundledAssetStartup(): Promise<BundledAssetEvidence>;
  ipcCapabilityDenial(): Promise<IpcCapabilityEvidence>;
  trayLifecycle(): Promise<TrayLifecycleEvidence>;
  globalShortcut(): Promise<GlobalShortcutEvidence>;
  autostart(): Promise<AutostartEvidence>;
  theme(modes: readonly ThemeMode[]): Promise<ThemeEvidence>;
  devTools(): Promise<DevToolsEvidence>;
  explicitShutdown(): Promise<ShutdownEvidence>;
  runtimeFailure(
    kinds: readonly RuntimeFailureKind[],
  ): Promise<RuntimeFailureEvidence>;
  helperProcessCleanup(): Promise<HelperCleanupEvidence>;
  packaging(formats: readonly PackageFormat[]): Promise<PackagingEvidence>;
  signedUpdater(): Promise<SignedUpdaterEvidence>;
}

export class ProbeBlockedError extends Error {
  readonly upstreamReference: string;

  constructor(reason: string, upstreamReference: string) {
    super(reason);
    this.name = "ProbeBlockedError";
    this.upstreamReference = upstreamReference;
  }
}

interface ProbeScenario<K extends ProbeId> {
  readonly id: K;
  run(
    driver: ProbeDriver,
    target: ProbeTarget,
  ): Promise<ProbeEvidenceMap[K]>;
}

type RunnableScenario = ProbeScenario<ProbeId>;

function defineScenario<K extends ProbeId>(
  id: K,
  run: (
    driver: ProbeDriver,
    target: ProbeTarget,
  ) => Promise<ProbeEvidenceMap[K]>,
  validates: (
    evidence: ProbeEvidenceMap[K],
    target: ProbeTarget,
  ) => boolean,
): ProbeScenario<K> {
  return Object.freeze({
    id,
    async run(driver: ProbeDriver, target: ProbeTarget) {
      const evidence = await run(driver, target);
      if (!validates(evidence, target)) {
        throw new Error(
          `Probe ${id} returned evidence that does not satisfy its gate conditions`,
        );
      }
      return evidence;
    },
  });
}

const packageFormatsByPlatform = Object.freeze({
  [DesktopPlatform.Linux]: Object.freeze([
    PackageFormat.AppImage,
    PackageFormat.Deb,
  ]),
  [DesktopPlatform.MacOS]: Object.freeze([PackageFormat.Dmg]),
  [DesktopPlatform.Windows]: Object.freeze([PackageFormat.Nsis]),
}) satisfies Readonly<Record<DesktopPlatform, readonly PackageFormat[]>>;

const displayProtocolsByPlatform: Readonly<
  Record<DesktopPlatform, readonly DisplayProtocol[]>
> = Object.freeze({
  [DesktopPlatform.Linux]: Object.freeze([
    DisplayProtocol.X11,
    DisplayProtocol.XWayland,
  ]),
  [DesktopPlatform.MacOS]: Object.freeze([DisplayProtocol.NotApplicable]),
  [DesktopPlatform.Windows]: Object.freeze([DisplayProtocol.NotApplicable]),
});

const bundledOrigin = "http://tauri.localhost";
const requiredThemeModes = Object.freeze([
  ThemeMode.System,
  ThemeMode.Light,
  ThemeMode.Dark,
]);
const requiredRuntimeFailureKinds = Object.freeze([
  RuntimeFailureKind.CefInitialization,
  RuntimeFailureKind.RendererTermination,
]);

function arraysEqual<T>(actual: readonly T[], expected: readonly T[]): boolean {
  return (
    actual.length === expected.length &&
    actual.every((value, index) => value === expected[index])
  );
}

function validateTarget(target: ProbeTarget): void {
  if (
    !displayProtocolsByPlatform[target.platform].includes(
      target.displayProtocol,
    )
  ) {
    throw new Error(
      `Invalid probe target: ${target.platform} does not support display protocol ${target.displayProtocol}`,
    );
  }
}

export const probeScenarios: readonly RunnableScenario[] = Object.freeze([
  defineScenario(
    ProbeId.BundledAssetStartup,
    (driver) => driver.bundledAssetStartup(),
    (evidence) =>
      evidence.origin === bundledOrigin &&
      evidence.sandboxEnabled === true &&
      evidence.remoteRequestCount === 0,
  ),
  defineScenario(
    ProbeId.IpcCapabilityDenial,
    (driver) => driver.ipcCapabilityDenial(),
    (evidence) =>
      evidence.allowedCommandCompleted === true &&
      evidence.undeclaredCommandDenied === true,
  ),
  defineScenario(
    ProbeId.TrayLifecycle,
    (driver) => driver.trayLifecycle(),
    (evidence) =>
      evidence.created === true &&
      evidence.remainsResidentAfterWindowClose === true &&
      evidence.quitTerminates === true,
  ),
  defineScenario(
    ProbeId.GlobalShortcut,
    (driver) => driver.globalShortcut(),
    (evidence) =>
      evidence.registered === true &&
      evidence.togglesProbeWindow === true &&
      evidence.releasedOnShutdown === true,
  ),
  defineScenario(
    ProbeId.Autostart,
    (driver) => driver.autostart(),
    (evidence) =>
      evidence.disabledByDefault === true &&
      evidence.enableDisableRoundTrip === true,
  ),
  defineScenario(
    ProbeId.Theme,
    (driver) => driver.theme(requiredThemeModes),
    (evidence) =>
      arraysEqual(evidence.observedModes, requiredThemeModes) &&
      evidence.systemChangeObserved === true,
  ),
  defineScenario(
    ProbeId.DevTools,
    (driver) => driver.devTools(),
    (evidence) =>
      evidence.opened === true &&
      evidence.capabilityBoundaryPreserved === true &&
      evidence.remoteNavigationDenied === true,
  ),
  defineScenario(
    ProbeId.ExplicitShutdown,
    (driver) => driver.explicitShutdown(),
    (evidence) =>
      evidence.requestedExplicitly === true && evidence.exitCode === 0,
  ),
  defineScenario(
    ProbeId.RuntimeFailure,
    (driver) => driver.runtimeFailure(requiredRuntimeFailureKinds),
    (evidence) =>
      arraysEqual(evidence.fatalKinds, requiredRuntimeFailureKinds) &&
      arraysEqual(evidence.immediateExitKinds, requiredRuntimeFailureKinds) &&
      evidence.automaticRestartCount === 0,
  ),
  defineScenario(
    ProbeId.HelperProcessCleanup,
    (driver) => driver.helperProcessCleanup(),
    (evidence) =>
      evidence.helperProcessCountBeforeShutdown > 0 &&
      evidence.helperProcessCountAfterShutdown === 0,
  ),
  defineScenario(
    ProbeId.Packaging,
    (driver, target) =>
      driver.packaging(packageFormatsByPlatform[target.platform]),
    (evidence, target) =>
      arraysEqual(
        evidence.checkedFormats,
        packageFormatsByPlatform[target.platform],
      ) &&
      evidence.bundledAssetsPresent === true &&
      evidence.cefHelpersPresent === true &&
      evidence.signReady === true,
  ),
  defineScenario(
    ProbeId.SignedUpdater,
    (driver) => driver.signedUpdater(),
    (evidence) =>
      evidence.signedBundleCreated === true &&
      evidence.validSignatureAccepted === true &&
      evidence.invalidSignatureRejected === true,
  ),
]);

function failureReason(error: unknown): string {
  if (error instanceof Error && error.message.length > 0) {
    return error.message;
  }
  return "Probe driver failed without a diagnostic";
}

export async function runProbeHarness(
  target: ProbeTarget,
  driver: ProbeDriver,
): Promise<ProbeReport> {
  validateTarget(target);

  const results: ProbeResult[] = [];

  for (const scenario of probeScenarios) {
    try {
      const evidence = await scenario.run(driver, target);
      results.push({
        id: scenario.id,
        status: "passed",
        evidence,
      });
    } catch (error) {
      if (error instanceof ProbeBlockedError) {
        results.push({
          id: scenario.id,
          status: "blocked",
          reason: error.message,
          upstreamReference: error.upstreamReference,
        });
      } else {
        results.push({
          id: scenario.id,
          status: "failed",
          reason: failureReason(error),
        });
      }
    }
  }

  return {
    target,
    results,
    passed: results.every((result) => result.status === "passed"),
  };
}
