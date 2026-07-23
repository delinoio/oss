import {
  DesktopPlatform,
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
): ProbeScenario<K> {
  return Object.freeze({ id, run });
}

const packageFormatsByPlatform = Object.freeze({
  [DesktopPlatform.Linux]: Object.freeze([
    PackageFormat.AppImage,
    PackageFormat.Deb,
  ]),
  [DesktopPlatform.MacOS]: Object.freeze([PackageFormat.Dmg]),
  [DesktopPlatform.Windows]: Object.freeze([PackageFormat.Nsis]),
}) satisfies Readonly<Record<DesktopPlatform, readonly PackageFormat[]>>;

export const probeScenarios: readonly RunnableScenario[] = Object.freeze([
  defineScenario(ProbeId.BundledAssetStartup, (driver) =>
    driver.bundledAssetStartup(),
  ),
  defineScenario(ProbeId.IpcCapabilityDenial, (driver) =>
    driver.ipcCapabilityDenial(),
  ),
  defineScenario(ProbeId.TrayLifecycle, (driver) => driver.trayLifecycle()),
  defineScenario(ProbeId.GlobalShortcut, (driver) => driver.globalShortcut()),
  defineScenario(ProbeId.Autostart, (driver) => driver.autostart()),
  defineScenario(ProbeId.Theme, (driver) =>
    driver.theme([ThemeMode.System, ThemeMode.Light, ThemeMode.Dark]),
  ),
  defineScenario(ProbeId.DevTools, (driver) => driver.devTools()),
  defineScenario(ProbeId.ExplicitShutdown, (driver) =>
    driver.explicitShutdown(),
  ),
  defineScenario(ProbeId.RuntimeFailure, (driver) =>
    driver.runtimeFailure([
      RuntimeFailureKind.CefInitialization,
      RuntimeFailureKind.RendererTermination,
    ]),
  ),
  defineScenario(ProbeId.HelperProcessCleanup, (driver) =>
    driver.helperProcessCleanup(),
  ),
  defineScenario(ProbeId.Packaging, (driver, target) =>
    driver.packaging(packageFormatsByPlatform[target.platform]),
  ),
  defineScenario(ProbeId.SignedUpdater, (driver) => driver.signedUpdater()),
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
