import assert from "node:assert/strict";
import { test } from "node:test";

import {
  assertSafeDiagnostics,
  gateTargets,
  requiredRuntimeEvents,
  safeFailureSummary,
  validateSafeEvidence,
} from "./macos-gate-contract.mjs";
import {
  eventNames,
  execute,
  structuredDiagnostics,
} from "./macos-gate.mjs";

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

test("parses probe events from tracing JSON fields", () => {
  const diagnostic = {
    level: "INFO",
    fields: {
      message: "feasibility probe window created",
      event: "devhud.probe.window_created",
      runtime: "cef",
    },
  };
  const parsed = structuredDiagnostics(
    `${JSON.stringify(diagnostic)}\n{"event":"devhud.probe.ignored"}\n`,
  );

  assert.deepEqual(parsed, [diagnostic.fields]);
  assert.deepEqual(eventNames(parsed), new Set(["devhud.probe.window_created"]));
});

test("stops a child when an output action fails", async () => {
  const startedAt = Date.now();
  await assert.rejects(
    execute(
      process.execPath,
      [
        "-e",
        'process.stdout.write("ready"); setInterval(() => {}, 1_000);',
      ],
      {
        timeoutMs: 2_000,
        onData() {
          throw new Error("output action failed");
        },
      },
    ),
    /output action failed/u,
  );

  assert.ok(Date.now() - startedAt < 1_000);
});

test("rejects excluded values in captured diagnostics", () => {
  assert.throws(
    () => assertSafeDiagnostics("safe-prefix-sensitive-value", ["sensitive-value"]),
    /redaction/u,
  );
  assert.doesNotThrow(() => assertSafeDiagnostics("safe-event", ["excluded"]));
});

test("summarizes subprocess failures without sensitive values", () => {
  const summary = safeFailureSummary(
    "\u001b[1m\u001b[91merror: failed\u001b[0m at /Users/example/private/project/file.rs with " +
      "Control+Alt+Shift+F18 and A".repeat(80),
  ).join("\n");

  assert.match(summary, /error: failed/u);
  assert.equal(summary.includes("\u001b"), false);
  assert.doesNotMatch(summary, /Users|project|F18|A{48}/u);
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
