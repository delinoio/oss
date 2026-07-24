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
  hasMacOsCefSandboxEvidence,
  sanitizedRuntimeEnvironment,
  signingModeForEnvironment,
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
      tauriRevision: "f49ebda2fdba5755456b0f049e32593ca0ea331a",
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

test("rejects JSON-escaped multiline values in captured diagnostics", () => {
  const privateKey =
    '-----BEGIN PRIVATE KEY-----\n"private-key"\n-----END PRIVATE KEY-----';
  const diagnostics = JSON.stringify({ message: privateKey });

  assert.throws(
    () => assertSafeDiagnostics(diagnostics, [privateKey]),
    /redaction/u,
  );
});

test("rejects arbitrary paths in captured diagnostics", () => {
  const diagnostics = JSON.stringify({
    message:
      "failure at /Users/runner/private checkout/devhud-probe for an unexpected reason",
  });

  assert.throws(
    () => assertSafeDiagnostics(diagnostics, []),
    /redaction/u,
  );
});

test("scrubs App Store Connect private keys from the app environment", () => {
  const originalPrivateKey = process.env.APPLE_API_PRIVATE_KEY;
  process.env.APPLE_API_PRIVATE_KEY = "private-key";
  try {
    const environment = sanitizedRuntimeEnvironment("sentinel");
    assert.equal(environment.APPLE_API_PRIVATE_KEY, undefined);
  } finally {
    if (originalPrivateKey === undefined) {
      delete process.env.APPLE_API_PRIVATE_KEY;
    } else {
      process.env.APPLE_API_PRIVATE_KEY = originalPrivateKey;
    }
  }
});

test("requires positive macOS CEF sandbox evidence", () => {
  assert.equal(
    hasMacOsCefSandboxEvidence(
      "/Applications/DevHud.app/Helper --type=renderer --seatbelt-client=17",
    ),
    true,
  );
  assert.equal(
    hasMacOsCefSandboxEvidence(
      "/Applications/DevHud.app/Helper --type=renderer",
    ),
    false,
  );
  assert.equal(
    hasMacOsCefSandboxEvidence(
      "/Applications/DevHud.app/Helper --type=renderer --seatbelt-client=17 --no-sandbox",
    ),
    false,
  );
});

test("requires notarization credentials for Developer ID mode", () => {
  assert.equal(signingModeForEnvironment({}), "sign-ready");
  assert.throws(
    () =>
      signingModeForEnvironment({
        APPLE_CERTIFICATE: "certificate",
        APPLE_CERTIFICATE_PASSWORD: "password",
      }),
    /notarization credentials are incomplete/u,
  );
  assert.equal(
    signingModeForEnvironment({
      APPLE_API_ISSUER: "issuer",
      APPLE_API_KEY: "key",
      APPLE_API_KEY_PATH: "/private/key.p8",
      APPLE_CERTIFICATE: "certificate",
      APPLE_CERTIFICATE_PASSWORD: "password",
    }),
    "developer-id",
  );
  assert.equal(
    signingModeForEnvironment({
      APPLE_CERTIFICATE: "certificate",
      APPLE_CERTIFICATE_PASSWORD: "password",
      APPLE_ID: "developer@example.com",
      APPLE_PASSWORD: "password",
      APPLE_TEAM_ID: "team",
    }),
    "developer-id",
  );

  const certificate = {
    APPLE_CERTIFICATE: "certificate",
    APPLE_CERTIFICATE_PASSWORD: "password",
  };
  const appStoreConnect = {
    APPLE_API_ISSUER: "issuer",
    APPLE_API_KEY: "key",
    APPLE_API_KEY_PATH: "/private/key.p8",
  };
  const appleId = {
    APPLE_ID: "developer@example.com",
    APPLE_PASSWORD: "password",
    APPLE_TEAM_ID: "team",
  };
  for (const credentials of [appStoreConnect, appleId]) {
    const entries = Object.entries(credentials);
    for (let mask = 1; mask < 2 ** entries.length - 1; mask += 1) {
      const partialCredentials = Object.fromEntries(
        entries.filter((_, index) => mask & (1 << index)),
      );
      assert.throws(
        () =>
          signingModeForEnvironment({
            ...certificate,
            ...(credentials === appStoreConnect ? appleId : appStoreConnect),
            ...partialCredentials,
          }),
        /notarization credentials are incomplete/u,
      );
    }
  }
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

test("redacts whitespace-containing paths from subprocess summaries", () => {
  const summary = safeFailureSummary(
    'error: failed at "/Users/example/private checkout/project/file.rs": denied',
  ).join("\n");

  assert.match(summary, /error: failed/u);
  assert.doesNotMatch(summary, /Users|private checkout|project|file\.rs/u);
});

test("redacts short signing credentials from subprocess summaries", () => {
  const summary = safeFailureSummary(
    'error: signing failed with "short-password" and short\\nprivate\\nkey',
    ["short-password", "short\nprivate\nkey"],
  ).join("\n");

  assert.match(summary, /error: signing failed/u);
  assert.doesNotMatch(summary, /short-password|short\\nprivate\\nkey/u);
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
