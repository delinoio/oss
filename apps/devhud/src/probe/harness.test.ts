import { describe, expect, it } from "vitest";

import {
  ProbeBlockedError,
  probeScenarios,
  runProbeHarness,
  type ProbeDriver,
} from "./harness";
import {
  Architecture,
  DesktopPlatform,
  DisplayProtocol,
  PackageFormat,
  ProbeId,
  RuntimeFailureKind,
  ThemeMode,
} from "./model";

const target = {
  platform: DesktopPlatform.Linux,
  architecture: Architecture.X64,
  displayProtocol: DisplayProtocol.X11,
} as const;

function passingDriver(
  calls: ProbeId[],
  expectedPackageFormats: readonly PackageFormat[] = [
    PackageFormat.AppImage,
    PackageFormat.Deb,
  ],
): ProbeDriver {
  return {
    async bundledAssetStartup() {
      calls.push(ProbeId.BundledAssetStartup);
      return {
        origin: "http://tauri.localhost",
        sandboxEnabled: true,
        remoteRequestCount: 0,
      };
    },
    async ipcCapabilityDenial() {
      calls.push(ProbeId.IpcCapabilityDenial);
      return {
        allowedCommandCompleted: true,
        undeclaredCommandDenied: true,
      };
    },
    async trayLifecycle() {
      calls.push(ProbeId.TrayLifecycle);
      return {
        created: true,
        remainsResidentAfterWindowClose: true,
        quitTerminates: true,
      };
    },
    async globalShortcut() {
      calls.push(ProbeId.GlobalShortcut);
      return {
        registered: true,
        togglesProbeWindow: true,
        releasedOnShutdown: true,
      };
    },
    async autostart() {
      calls.push(ProbeId.Autostart);
      return {
        disabledByDefault: true,
        enableDisableRoundTrip: true,
      };
    },
    async theme(modes) {
      calls.push(ProbeId.Theme);
      expect(modes).toEqual([
        ThemeMode.System,
        ThemeMode.Light,
        ThemeMode.Dark,
      ]);
      return {
        observedModes: modes,
        systemChangeObserved: true,
      };
    },
    async devTools() {
      calls.push(ProbeId.DevTools);
      return {
        opened: true,
        capabilityBoundaryPreserved: true,
        remoteNavigationDenied: true,
      };
    },
    async explicitShutdown() {
      calls.push(ProbeId.ExplicitShutdown);
      return {
        requestedExplicitly: true,
        exitCode: 0,
      };
    },
    async runtimeFailure(kinds) {
      calls.push(ProbeId.RuntimeFailure);
      expect(kinds).toEqual([
        RuntimeFailureKind.CefInitialization,
        RuntimeFailureKind.RendererTermination,
      ]);
      return {
        fatalKinds: kinds,
        automaticRestartCount: 0,
      };
    },
    async helperProcessCleanup() {
      calls.push(ProbeId.HelperProcessCleanup);
      return {
        helperProcessCountBeforeShutdown: 3,
        helperProcessCountAfterShutdown: 0,
      };
    },
    async packaging(formats) {
      calls.push(ProbeId.Packaging);
      expect(formats).toEqual(expectedPackageFormats);
      return {
        checkedFormats: formats,
        bundledAssetsPresent: true,
        cefHelpersPresent: true,
        signReady: true,
      };
    },
    async signedUpdater() {
      calls.push(ProbeId.SignedUpdater);
      return {
        signedBundleCreated: true,
        validSignatureAccepted: true,
        invalidSignatureRejected: true,
      };
    },
  };
}

describe("probe harness", () => {
  it("defines every required gate scenario once in deterministic order", () => {
    expect(probeScenarios.map(({ id }) => id)).toEqual(Object.values(ProbeId));
  });

  it("runs all platform driver probes sequentially", async () => {
    const calls: ProbeId[] = [];
    const report = await runProbeHarness(target, passingDriver(calls));

    expect(calls).toEqual(Object.values(ProbeId));
    expect(report.target).toEqual(target);
    expect(report.results).toHaveLength(Object.values(ProbeId).length);
    expect(report.passed).toBe(true);
  });

  it.each([
    {
      platform: DesktopPlatform.Linux,
      displayProtocol: DisplayProtocol.X11,
      packageFormats: [PackageFormat.AppImage, PackageFormat.Deb],
    },
    {
      platform: DesktopPlatform.MacOS,
      displayProtocol: DisplayProtocol.NotApplicable,
      packageFormats: [PackageFormat.Dmg],
    },
    {
      platform: DesktopPlatform.Windows,
      displayProtocol: DisplayProtocol.NotApplicable,
      packageFormats: [PackageFormat.Nsis],
    },
  ])(
    "requests only $platform packaging formats",
    async ({ platform, displayProtocol, packageFormats }) => {
      const report = await runProbeHarness(
        { ...target, platform, displayProtocol },
        passingDriver([], packageFormats),
      );

      expect(report.passed).toBe(true);
    },
  );

  it("preserves an actionable upstream blocker", async () => {
    const driver = passingDriver([]);
    driver.runtimeFailure = async () => {
      throw new ProbeBlockedError(
        "Renderer termination callback is unavailable on Linux.",
        "https://github.com/tauri-apps/tauri/blob/649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769/crates/tauri-runtime-cef/src/webview.rs#L354-L360",
      );
    };

    const report = await runProbeHarness(target, driver);
    const result = report.results.find(
      ({ id }) => id === ProbeId.RuntimeFailure,
    );

    expect(result).toMatchObject({
      status: "blocked",
      reason: "Renderer termination callback is unavailable on Linux.",
    });
    expect(report.passed).toBe(false);
  });

  it("records driver failures without skipping later scenarios", async () => {
    const calls: ProbeId[] = [];
    const driver = passingDriver(calls);
    driver.globalShortcut = async () => {
      calls.push(ProbeId.GlobalShortcut);
      throw new Error("shortcut registration failed");
    };

    const report = await runProbeHarness(target, driver);

    expect(report.results).toContainEqual({
      id: ProbeId.GlobalShortcut,
      status: "failed",
      reason: "shortcut registration failed",
    });
    expect(calls.at(-1)).toBe(ProbeId.SignedUpdater);
  });

  it("rejects evidence that does not satisfy required gate conditions", async () => {
    const invalidCases: readonly [
      ProbeId,
      (driver: ProbeDriver) => void,
    ][] = [
      [
        ProbeId.IpcCapabilityDenial,
        (driver) => {
          driver.ipcCapabilityDenial = async () => ({
            allowedCommandCompleted: true,
            undeclaredCommandDenied: false,
          });
        },
      ],
      [
        ProbeId.DevTools,
        (driver) => {
          driver.devTools = async () => ({
            opened: true,
            capabilityBoundaryPreserved: true,
            remoteNavigationDenied: false,
          });
        },
      ],
      [
        ProbeId.Packaging,
        (driver) => {
          driver.packaging = async (formats) => ({
            checkedFormats: formats,
            bundledAssetsPresent: true,
            cefHelpersPresent: true,
            signReady: false,
          });
        },
      ],
    ];

    for (const [id, invalidate] of invalidCases) {
      const driver = passingDriver([]);
      invalidate(driver);

      const report = await runProbeHarness(target, driver);

      expect(report.results).toContainEqual({
        id,
        status: "failed",
        reason: `Probe ${id} returned evidence that does not satisfy its gate conditions`,
      });
      expect(report.passed).toBe(false);
    }
  });
});
