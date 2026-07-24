import { spawn } from "node:child_process";
import {
  appendFile,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { randomBytes } from "node:crypto";
import { pathToFileURL } from "node:url";

import {
  assertSafeDiagnostics,
  gateTargets,
  requiredRuntimeEvents,
  safeFailureSummary,
  validateSafeEvidence,
} from "./macos-gate-contract.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const tauri = resolve(
  appRoot,
  "node_modules/@tauri-apps/cli-cef/tauri.js",
);
const minimumSystemVersion = "14.0";
const tauriRevision = "649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769";
const cliCefVersion = "3.0.0-alpha.6";
const shortcutDiagnosticForms = Object.freeze([
  "Control+Alt+Shift+F18",
  "control+alt+shift+f18",
  "CTRL+ALT+SHIFT+F18",
]);
const secretEnvironmentNames = Object.freeze([
  "APPLE_API_ISSUER",
  "APPLE_API_KEY",
  "APPLE_API_KEY_PATH",
  "APPLE_API_PRIVATE_KEY",
  "APPLE_CERTIFICATE",
  "APPLE_CERTIFICATE_PASSWORD",
  "APPLE_ID",
  "APPLE_PASSWORD",
  "APPLE_TEAM_ID",
  "TAURI_SIGNING_PRIVATE_KEY",
  "TAURI_SIGNING_PRIVATE_KEY_PASSWORD",
]);
const appStoreConnectCredentialNames = Object.freeze([
  "APPLE_API_ISSUER",
  "APPLE_API_KEY",
  "APPLE_API_KEY_PATH",
]);
const appleIdCredentialNames = Object.freeze([
  "APPLE_ID",
  "APPLE_PASSWORD",
  "APPLE_TEAM_ID",
]);

function parseArguments(argv) {
  let evidencePath;
  let target;
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--target") {
      target = argv[index + 1];
      index += 1;
    } else if (argument === "--evidence") {
      evidencePath = resolve(argv[index + 1]);
      index += 1;
    } else {
      throw new Error("unsupported macOS gate argument");
    }
  }

  if (!target || !Object.hasOwn(gateTargets, target)) {
    throw new Error("the macOS gate requires an allowlisted native target");
  }
  return { evidencePath, target };
}

export function sanitizedRuntimeEnvironment(environmentSentinel) {
  const environment = {
    ...process.env,
    DEVHUD_GATE_ENV_SENTINEL: environmentSentinel,
  };
  for (const name of secretEnvironmentNames) {
    delete environment[name];
  }
  return environment;
}

export function signingModeForEnvironment(environment) {
  const hasCertificate = Boolean(environment.APPLE_CERTIFICATE);
  const hasCertificatePassword = Boolean(
    environment.APPLE_CERTIFICATE_PASSWORD,
  );
  if (hasCertificate !== hasCertificatePassword) {
    throw new Error("macOS signing credentials are incomplete");
  }
  if (!hasCertificate) {
    return "sign-ready";
  }

  const hasAppStoreConnectCredentials = appStoreConnectCredentialNames.every(
    (name) => Boolean(environment[name]),
  );
  const hasAppleIdCredentials = appleIdCredentialNames.every((name) =>
    Boolean(environment[name]),
  );
  if (!hasAppStoreConnectCredentials && !hasAppleIdCredentials) {
    throw new Error("macOS notarization credentials are incomplete");
  }
  return "developer-id";
}

function signingEnvironment(extra) {
  const environment = { ...process.env, ...extra };
  for (const name of secretEnvironmentNames) {
    if (!environment[name]) {
      delete environment[name];
    }
  }
  return environment;
}

export function execute(command, args, options = {}) {
  const {
    cwd = repositoryRoot,
    env = process.env,
    onData,
    timeoutMs = 10 * 60 * 1000,
  } = options;

  return new Promise((resolveExecution, rejectExecution) => {
    const child = spawn(command, args, {
      cwd,
      env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    let actionChain = Promise.resolve();
    let settled = false;
    const timeout = setTimeout(() => {
      child.kill("SIGKILL");
    }, timeoutMs);

    const rejectAction = (error) => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timeout);
      try {
        child.kill("SIGKILL");
      } finally {
        rejectExecution(error);
      }
    };

    const receive = (chunk) => {
      const text = chunk.toString("utf8");
      output += text;
      if (onData && !settled) {
        actionChain = actionChain.then(() => onData(text, output, child));
        void actionChain.catch(rejectAction);
      }
    };
    child.stdout.on("data", receive);
    child.stderr.on("data", receive);
    child.once("error", (error) => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timeout);
      rejectExecution(error);
    });
    child.once("close", (code, signal) => {
      clearTimeout(timeout);
      void actionChain.then(
        () => {
          if (!settled) {
            settled = true;
            resolveExecution({ code, output, signal });
          }
        },
        rejectAction,
      );
    });
  });
}

async function requireSuccess(command, args, options = {}) {
  const result = await execute(command, args, options);
  if (result.code !== 0) {
    const environment = options.env ?? process.env;
    const excludedValues = [
      ...(options.excludedValues ?? []),
      ...secretEnvironmentNames.map((name) => environment[name]),
    ];
    console.error(
      JSON.stringify({
        event: "devhud.gate.subprocess_failure",
        classification: "subprocess-failure",
        summary: safeFailureSummary(result.output, excludedValues),
      }),
    );
    throw new Error("macOS gate subprocess failed");
  }
  return result;
}

async function listFiles(root) {
  const files = [];
  const entries = await readdir(root, { withFileTypes: true });
  for (const entry of entries) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await listFiles(path)));
    } else {
      files.push(path);
    }
  }
  return files;
}

async function listDirectories(root) {
  const directories = [];
  const entries = await readdir(root, { withFileTypes: true });
  for (const entry of entries) {
    if (!entry.isDirectory()) {
      continue;
    }
    const path = join(root, entry.name);
    directories.push(path, ...(await listDirectories(path)));
  }
  return directories;
}

function one(items, classification) {
  if (items.length !== 1) {
    throw new Error(`macOS gate expected one ${classification}`);
  }
  return items[0];
}

async function architectureOf(path) {
  const result = await requireSuccess("lipo", ["-archs", path]);
  return result.output.trim().split(/\s+/u);
}

async function processSnapshot() {
  const result = await requireSuccess("ps", ["-axo", "pid=,ppid=,command="]);
  return result.output
    .split(/\r?\n/u)
    .map((line) => line.match(/^\s*(\d+)\s+(\d+)\s+(.*)$/u))
    .filter(Boolean)
    .map((match) => ({
      pid: Number(match[1]),
      parentPid: Number(match[2]),
      command: match[3],
    }));
}

async function helperProcesses(appPath) {
  return (await processSnapshot()).filter(
    (process) =>
      process.command.includes(appPath) && process.command.includes("--type="),
  );
}

export function hasMacOsCefSandboxEvidence(command) {
  return (
    /(?:^|\s)--seatbelt-client=\d+(?=\s|$)/u.test(command) &&
    !/(?:^|\s)--no-sandbox(?=\s|$)/u.test(command)
  );
}

export function structuredDiagnostics(output) {
  return output
    .split(/\r?\n/u)
    .flatMap((line) => {
      try {
        const value = JSON.parse(line);
        return typeof value.fields?.event === "string" &&
          value.fields.event.startsWith("devhud.probe.")
          ? [value.fields]
          : [];
      } catch {
        return [];
      }
    });
}

export function eventNames(diagnostics) {
  return new Set(diagnostics.map(({ event }) => event));
}

async function toggleSystemAppearance() {
  const script =
    'tell application "System Events" to tell appearance preferences to set dark mode to not dark mode';
  await requireSuccess("osascript", ["-e", script], { timeoutMs: 30_000 });
  await new Promise((resolveDelay) => setTimeout(resolveDelay, 1_000));
  await requireSuccess("osascript", ["-e", script], { timeoutMs: 30_000 });
}

async function runAppScenario({
  appPath,
  expectedExit,
  mode,
  runtimeEnvironment,
}) {
  const executable = join(appPath, "Contents/MacOS/devhud-probe");
  let helperCount = 0;
  let sandboxEnabled = false;
  let systemThemeHandled = false;
  let rendererHandled = false;
  let shortcutRequestsHandled = 0;

  const result = await execute(
    executable,
    mode ? [`--devhud-gate-${mode}`] : [],
    {
      cwd: appRoot,
      env: runtimeEnvironment,
      timeoutMs: 90_000,
      async onData(_chunk, output) {
        if (
          !systemThemeHandled &&
          output.includes("devhud.probe.system_theme_change_ready")
        ) {
          systemThemeHandled = true;
          const helpers = await helperProcesses(appPath);
          helperCount = Math.max(helperCount, helpers.length);
          sandboxEnabled =
            helpers.length > 0 &&
            helpers.every(({ command }) =>
              hasMacOsCefSandboxEvidence(command),
            );
          await toggleSystemAppearance();
        }

        if (
          mode === "renderer" &&
          !rendererHandled &&
          output.includes("devhud.probe.renderer_termination_ready")
        ) {
          rendererHandled = true;
          const renderer = (await helperProcesses(appPath)).find(({ command }) =>
            command.includes("--type=renderer"),
          );
          if (!renderer) {
            throw new Error("macOS gate renderer helper was not observed");
          }
          process.kill(renderer.pid, "SIGKILL");
        }

        const shortcutRequests =
          output.match(/devhud\.probe\.global_shortcut_ready/gu)?.length ?? 0;
        while (shortcutRequestsHandled < shortcutRequests) {
          shortcutRequestsHandled += 1;
          await requireSuccess(
            "osascript",
            [
              "-e",
              'tell application "System Events" to key code 79 using {control down, option down, shift down}',
            ],
            {
              timeoutMs: 30_000,
            },
          );
        }
      },
    },
  );

  if (result.code !== expectedExit) {
    throw new Error("macOS gate app returned an unexpected exit classification");
  }
  await new Promise((resolveDelay) => setTimeout(resolveDelay, 1_000));
  const helpersAfter = await helperProcesses(appPath);
  const diagnostics = structuredDiagnostics(result.output);
  return {
    diagnostics,
    events: eventNames(diagnostics),
    helperCount,
    helpersAfter: helpersAfter.length,
    sandboxEnabled,
  };
}

async function verifyCodeSignature(path, signingMode) {
  await requireSuccess("codesign", ["--verify", "--deep", "--strict", path]);
  const details = await requireSuccess("codesign", [
    "--display",
    "--verbose=4",
    path,
  ]);
  const developerIdObserved = details.output.includes(
    "Authority=Developer ID Application",
  );
  if (
    (signingMode === "developer-id" && !developerIdObserved) ||
    (signingMode === "sign-ready" &&
      !details.output.includes("Signature=adhoc"))
  ) {
    throw new Error("macOS gate code-signing mode did not match");
  }
}

async function verifyNotarization(path, signingMode) {
  if (signingMode === "developer-id") {
    await requireSuccess("xcrun", ["stapler", "validate", path]);
  }
}

async function verifyUpdaterSignature({
  privatePublicKey,
  signaturePath,
  tempRoot,
  updaterPath,
}) {
  const decodedPublic = Buffer.from(privatePublicKey.trim(), "base64").toString(
    "utf8",
  );
  const rawPublic = decodedPublic
    .split(/\r?\n/u)
    .find((line) => line.startsWith("RW"));
  if (!rawPublic) {
    throw new Error("macOS gate updater public key format is invalid");
  }

  const encodedSignature = await readFile(signaturePath, "utf8");
  const decodedSignaturePath = join(tempRoot, "updater-signature");
  await writeFile(
    decodedSignaturePath,
    Buffer.from(encodedSignature.trim(), "base64"),
    { mode: 0o600 },
  );
  await requireSuccess("minisign", [
    "-V",
    "-m",
    updaterPath,
    "-P",
    rawPublic,
    "-x",
    decodedSignaturePath,
    "-q",
  ]);

  const invalidUpdater = join(tempRoot, "invalid-updater");
  await cp(updaterPath, invalidUpdater);
  await appendFile(invalidUpdater, Buffer.from([0]));
  const invalid = await execute("minisign", [
    "-V",
    "-m",
    invalidUpdater,
    "-P",
    rawPublic,
    "-x",
    decodedSignaturePath,
    "-q",
  ]);
  if (invalid.code === 0) {
    throw new Error("macOS gate updater accepted an invalid signature");
  }
}

async function main() {
  if (process.platform !== "darwin") {
    throw new Error("the mandatory macOS gate must execute on macOS");
  }

  const { evidencePath, target } = parseArguments(process.argv.slice(2));
  const targetContract = gateTargets[target];
  if (process.arch !== targetContract.architecture) {
    throw new Error("the macOS gate must build on the native target architecture");
  }

  const tempRoot = await mkdtemp(join(tmpdir(), "devhud-gate-sensitive-path-"));
  const privateKeyPath = join(tempRoot, "updater.key");
  const publicKeyPath = `${privateKeyPath}.pub`;
  const updaterPassword = randomBytes(24).toString("base64url");
  const environmentSentinel = randomBytes(24).toString("hex");
  const runtimeEnvironment = sanitizedRuntimeEnvironment(environmentSentinel);
  const signingMode = signingModeForEnvironment(process.env);
  const hasCertificate = signingMode === "developer-id";

  try {
    await requireSuccess(
      process.execPath,
      [
        tauri,
        "signer",
        "generate",
        "--write-keys",
        privateKeyPath,
        `--password=${updaterPassword}`,
        "--force",
        "--ci",
      ],
      { cwd: appRoot, excludedValues: [updaterPassword] },
    );
    const publicKey = await readFile(publicKeyPath, "utf8");
    const privateKey = await readFile(privateKeyPath, "utf8");
    const generatedConfig = {
      version: "0.1.0",
      bundle: {
        active: true,
        targets: ["app", "dmg"],
        createUpdaterArtifacts: true,
        macOS: {
          minimumSystemVersion,
          hardenedRuntime: true,
          ...(hasCertificate ? {} : { signingIdentity: "-" }),
        },
      },
      plugins: {
        updater: {
          endpoints: [
            "https://github.com/delinoio/oss/releases/latest/download/devhud-latest.json",
          ],
          pubkey: publicKey,
        },
      },
    };
    const configPath = join(tempRoot, "tauri-gate-config.json");
    await writeFile(configPath, `${JSON.stringify(generatedConfig)}\n`, {
      mode: 0o600,
    });

    await requireSuccess("pnpm", ["run", "build"], { cwd: appRoot });
    const bundleRoot = resolve(
      repositoryRoot,
      "target",
      target,
      "release",
      "bundle",
    );
    await rm(bundleRoot, { recursive: true, force: true });
    await requireSuccess(
      process.execPath,
      [
        tauri,
        "build",
        "--target",
        target,
        "--features",
        "macos-gate",
        "--bundles",
        "app,dmg",
        "--config",
        configPath,
        "--ci",
      ],
      {
        cwd: appRoot,
        env: signingEnvironment({
          TAURI_SIGNING_PRIVATE_KEY: privateKeyPath,
          TAURI_SIGNING_PRIVATE_KEY_PASSWORD: updaterPassword,
        }),
        timeoutMs: 60 * 60 * 1000,
      },
    );

    const bundleFiles = await listFiles(bundleRoot);
    const bundleDirectories = await listDirectories(bundleRoot);
    const appPath = one(
      bundleDirectories.filter(
        (path) =>
          path.endsWith(".app") &&
          !path.slice(0, -4).includes(".app/"),
      ),
      "application bundle",
    );
    const dmgPath = one(
      bundleFiles.filter((path) => path.endsWith(".dmg")),
      "DMG",
    );
    const updaterPath = one(
      bundleFiles.filter((path) => path.endsWith(".app.tar.gz")),
      "updater bundle",
    );
    const updaterSignaturePath = one(
      bundleFiles.filter((path) => path === `${updaterPath}.sig`),
      "updater signature",
    );

    const executable = join(appPath, "Contents/MacOS/devhud-probe");
    const executableArchitectures = await architectureOf(executable);
    if (
      executableArchitectures.length !== 1 ||
      executableArchitectures[0] !== targetContract.executableArchitecture
    ) {
      throw new Error("macOS application has an unexpected architecture");
    }
    const infoPlist = join(appPath, "Contents/Info.plist");
    const minimumVersion = await requireSuccess("plutil", [
      "-extract",
      "LSMinimumSystemVersion",
      "raw",
      infoPlist,
    ]);
    const dockMetadata = await requireSuccess("plutil", [
      "-extract",
      "LSUIElement",
      "raw",
      infoPlist,
    ]);
    if (
      minimumVersion.output.trim() !== minimumSystemVersion ||
      !["1", "true", "YES"].includes(dockMetadata.output.trim())
    ) {
      throw new Error("macOS bundle metadata failed the gate");
    }

    const frameworkRoot = join(appPath, "Contents/Frameworks");
    const frameworkDirectories = await listDirectories(frameworkRoot);
    const helperApps = frameworkDirectories.filter(
      (path) =>
        path.endsWith(".app") &&
        basename(path).includes("Helper"),
    );
    if (
      helperApps.length < 4 ||
      !frameworkDirectories.some((path) =>
        path.endsWith("Chromium Embedded Framework.framework"),
      )
    ) {
      throw new Error("macOS CEF helpers are incomplete");
    }
    for (const helperApp of helperApps) {
      const helperName = basename(helperApp, ".app");
      const helperExecutable = join(
        helperApp,
        "Contents",
        "MacOS",
        helperName,
      );
      const architectures = await architectureOf(helperExecutable);
      if (
        architectures.length !== 1 ||
        architectures[0] !== targetContract.executableArchitecture
      ) {
        throw new Error("macOS CEF helper has an unexpected architecture");
      }
    }
    await verifyCodeSignature(appPath, signingMode);
    await verifyNotarization(appPath, signingMode);
    await verifyCodeSignature(dmgPath, signingMode);

    const mountPoint = join(tempRoot, "mounted-dmg");
    await mkdir(mountPoint);
    await requireSuccess("hdiutil", [
      "attach",
      "-readonly",
      "-nobrowse",
      "-mountpoint",
      mountPoint,
      dmgPath,
    ]);
    try {
      const mountedDirectories = await listDirectories(mountPoint);
      const mountedApp = one(
        mountedDirectories.filter(
          (path) =>
            path.endsWith(".app") &&
            !path.slice(mountPoint.length + 1, -4).includes(".app/"),
        ),
        "mounted application bundle",
      );
      await verifyCodeSignature(mountedApp, signingMode);
      await verifyNotarization(mountedApp, signingMode);
    } finally {
      await requireSuccess("hdiutil", ["detach", mountPoint]);
    }

    await verifyUpdaterSignature({
      privatePublicKey: publicKey,
      signaturePath: updaterSignaturePath,
      tempRoot,
      updaterPath,
    });

    const sensitiveAppPath = join(
      tempRoot,
      "devhud-arbitrary-path-sentinel.app",
    );
    await requireSuccess("ditto", [appPath, sensitiveAppPath], {
      timeoutMs: 5 * 60 * 1000,
    });

    const normalResults = [];
    normalResults.push(
      await runAppScenario({
        appPath: sensitiveAppPath,
        expectedExit: 0,
        mode: "normal",
        runtimeEnvironment,
      }),
    );
    normalResults.push(
      await runAppScenario({
        appPath: sensitiveAppPath,
        expectedExit: 0,
        mode: "normal",
        runtimeEnvironment,
      }),
    );

    const rendererResult = await runAppScenario({
      appPath: sensitiveAppPath,
      expectedExit: 71,
      mode: "renderer",
      runtimeEnvironment,
    });
    if (
      !rendererResult.events.has("devhud.probe.renderer_termination") ||
      !rendererResult.events.has("devhud.probe.renderer_termination_ready") ||
      rendererResult.helpersAfter !== 0
    ) {
      throw new Error("macOS renderer termination was not fatal");
    }

    normalResults.push(
      await runAppScenario({
        appPath: sensitiveAppPath,
        expectedExit: 0,
        mode: "normal",
        runtimeEnvironment,
      }),
    );

    for (const result of normalResults) {
      for (const event of requiredRuntimeEvents) {
        if (!result.events.has(event)) {
          throw new Error("macOS runtime evidence is incomplete");
        }
      }
      if (
        !result.events.has("devhud.probe.explicit_shutdown_requested") ||
        result.events.has("devhud.probe.capability_boundary_failed") ||
        result.helpersAfter !== 0
      ) {
        throw new Error("macOS normal shutdown failed the gate");
      }
    }

    const framework = join(
      sensitiveAppPath,
      "Contents/Frameworks/Chromium Embedded Framework.framework",
    );
    const disabledFramework = `${framework}.gate-disabled`;
    await rename(framework, disabledFramework);
    let initializationResult;
    try {
      initializationResult = await runAppScenario({
        appPath: sensitiveAppPath,
        expectedExit: 70,
        mode: undefined,
        runtimeEnvironment,
      });
    } finally {
      await rename(disabledFramework, framework);
    }
    if (
      !initializationResult.events.has(
        "devhud.probe.cef_initialization_failure",
      ) ||
      initializationResult.helpersAfter !== 0
    ) {
      throw new Error("macOS CEF initialization failure was not fatal");
    }

    const allDiagnostics = [
      ...normalResults.flatMap(({ diagnostics }) => diagnostics),
      ...rendererResult.diagnostics,
      ...initializationResult.diagnostics,
    ];
    const serializedDiagnostics = allDiagnostics
      .map((value) => JSON.stringify(value))
      .join("\n");
    assertSafeDiagnostics(serializedDiagnostics, [
      tempRoot,
      environmentSentinel,
      updaterPassword,
      privateKey,
      publicKey,
      ...secretEnvironmentNames.map((name) => process.env[name]),
      ...shortcutDiagnosticForms,
    ]);

    const maximumHelperCount = Math.max(
      ...normalResults.map(({ helperCount }) => helperCount),
      rendererResult.helperCount,
    );
    const evidence = {
      schemaVersion: 1,
      target: {
        platform: "macos",
        architecture: targetContract.architecture,
        minimumSystemVersion,
      },
      upstream: {
        tauriRevision,
        cliCefVersion,
      },
      runtime: {
        bundledAssets: true,
        sandboxEnabled: normalResults.every(
          ({ sandboxEnabled }) => sandboxEnabled,
        ),
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
        repeatedCycles: normalResults.length,
        helperCountBeforeShutdown: maximumHelperCount,
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
        signingMode,
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
    validateSafeEvidence(evidence);

    if (evidencePath) {
      await mkdir(resolve(evidencePath, ".."), { recursive: true });
      await writeFile(evidencePath, `${JSON.stringify(evidence, null, 2)}\n`);
    }
    console.log(
      JSON.stringify({
        check: "devhud-macos-cef-gate",
        target: targetContract.architecture,
        signingMode,
        status: "passed",
      }),
    );
  } finally {
    await rm(tempRoot, { recursive: true, force: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  try {
    await main();
  } catch {
    console.error(
      JSON.stringify({
        event: "devhud.gate.failed",
        classification: "gate-failure",
      }),
    );
    process.exitCode = 1;
  }
}
