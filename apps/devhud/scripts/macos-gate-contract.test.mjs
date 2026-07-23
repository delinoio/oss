import assert from "node:assert/strict";
import { test } from "node:test";

import {
  assertSafeDiagnostics,
  gateTargets,
  requiredRuntimeEvents,
  validateSafeEvidence,
} from "./macos-gate-contract.mjs";

function passingEvidence() {
  return {
    schemaVersion: 1,
    target: {
      platform: "macos",
      architecture: "arm64",
      minimumSystemVersion: "14.0",
    },
    upstream: {
      tauriRevision: "649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769",
      cliCefVersion: "3.0.0-alpha.6",
    },
    runtime: {
      bundledAssets: true,
      sandboxEnabled: true,
      ipcAllowed: true,
      ipcDenied: true,
      trayCreated: true,
      dockHidden: true,
      closeKeepsResident: true,
      shortcutRegistered: true,
      shortcutTogglesWindow: true,
      shortcutReleased: true,
      autostartDisabledByDefault: true,
      autostartRoundTrip: true,
      systemThemeObserved: true,
      lightThemeObserved: true,
      darkThemeObserved: true,
      devtoolsOpened: true,
      devtoolsBoundaryPreserved: true,
      explicitShutdown: true,
      repeatedCycles: 3,
      helperCountBeforeShutdown: 4,
      helperCountAfterShutdown: 0,
    },
    failures: {
      initializationFatal: true,
      rendererTerminationFatal: true,
      automaticRestartAbsent: true,
      fatalHelpersCleaned: true,
    },
    packaging: {
      dmgCreated: true,
      targetArchitecture: true,
      cefHelpersBundled: true,
      minimumSystemVersion: true,
      hiddenDockMetadata: true,
      codeSignatureVerified: true,
      signReady: true,
      signingMode: "sign-ready",
    },
    updater: {
      targetSpecificBundle: true,
      signedBundle: true,
      updaterFormatCompatible: true,
      validSignatureAccepted: true,
      invalidSignatureRejected: true,
    },
    diagnostics: {
      shortcutValueAbsent: true,
      arbitraryPathAbsent: true,
      environmentValueAbsent: true,
      signingMaterialAbsent: true,
    },
    passed: true,
  };
}

test("defines native macOS 14+ targets for x64 and ARM64", () => {
  assert.deepEqual(Object.keys(gateTargets), [
    "aarch64-apple-darwin",
    "x86_64-apple-darwin",
  ]);
  assert.equal(new Set(Object.values(gateTargets).map((v) => v.architecture)).size, 2);
});

test("requires all safe runtime event identifiers", () => {
  assert.equal(requiredRuntimeEvents.length, new Set(requiredRuntimeEvents).size);
  assert.ok(requiredRuntimeEvents.every((event) => event.startsWith("devhud.probe.")));
});

test("rejects excluded values in captured diagnostics", () => {
  assert.throws(
    () => assertSafeDiagnostics("safe-prefix-sensitive-value", ["sensitive-value"]),
    /redaction/u,
  );
  assert.doesNotThrow(() => assertSafeDiagnostics("safe-event", ["excluded"]));
});

test("accepts only passing path-free evidence", () => {
  assert.doesNotThrow(() => validateSafeEvidence(passingEvidence()));

  const failed = passingEvidence();
  failed.runtime.sandboxEnabled = false;
  assert.throws(() => validateSafeEvidence(failed), /failed condition/u);

  const leaked = passingEvidence();
  leaked.packaging.output = "/tmp/private-output";
  assert.throws(() => validateSafeEvidence(leaked), /prohibited/u);
});
