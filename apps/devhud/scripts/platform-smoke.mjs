#!/usr/bin/env node

import { spawn } from "node:child_process";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  statSync,
} from "node:fs";
import { release, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { waitForChildClose } from "./platform-smoke-child.mjs";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const definitions = JSON.parse(readFileSync(join(appRoot, "platforms.json"), "utf8"));
const target = definitions.targets.find(
  ({ os, arch }) => os === process.platform && arch === process.arch,
);

function fail(message) {
  console.error(`devhud: ${message}`);
  process.exit(1);
}

if (!target) {
  fail(`unsupported smoke host ${process.platform}/${process.arch}`);
}

function parseArguments() {
  const args = process.argv.slice(2);
  if (args[0] === "--") {
    args.shift();
  }
  let artifact;
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === "--artifact") {
      const value = args[index + 1];
      if (!value || value.startsWith("--")) {
        fail("--artifact requires an executable path");
      }
      artifact = value;
      index += 1;
    } else {
      fail(`unknown platform smoke argument: ${args[index]}`);
    }
  }
  return { artifact };
}

function defaultArtifact() {
  if (process.platform === "darwin") {
    return join(repoRoot, "target/release/bundle/macos/DevHUD.app/Contents/MacOS/devhud");
  }
  return join(repoRoot, `target/release/devhud${process.platform === "win32" ? ".exe" : ""}`);
}

function requiredResources(executable) {
  const binaryDir = dirname(executable);
  if (process.platform === "darwin") {
    const contents = resolve(binaryDir, "..");
    const helpers = ["GPU", "Renderer", "Plugin", "Alerts", null].map((kind) => {
      const helper = kind ? `devhud Helper (${kind})` : "devhud Helper";
      return join(contents, `Frameworks/${helper}.app/Contents/MacOS/${helper}`);
    });
    return {
      paths: [
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Chromium Embedded Framework"),
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Resources/icudtl.dat"),
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Resources/resources.pak"),
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Libraries/libcef_sandbox.dylib"),
        ...helpers,
      ],
    };
  }
  const sharunResourceDir =
    process.platform === "linux" && binaryDir.endsWith(join("shared", "bin"))
      ? resolve(binaryDir, "../../bin")
      : undefined;
  const resourceDir =
    process.platform === "linux"
      ? binaryDir.endsWith(join("share", "DevHUD"))
        ? binaryDir
        : sharunResourceDir && existsSync(join(sharunResourceDir, "libcef.so"))
          ? sharunResourceDir
        : existsSync(join(binaryDir, "libcef.so"))
          ? binaryDir
          : resolve(binaryDir, "../share/DevHUD")
      : binaryDir;
  const names =
    process.platform === "win32"
      ? [
          "libcef.dll",
          "chrome_elf.dll",
          "icudtl.dat",
          "resources.pak",
          "v8_context_snapshot.bin",
          "locales/en-US.pak",
          "bootstrap.exe",
          "bootstrapc.exe",
        ]
      : [
          "libcef.so",
          "icudtl.dat",
          "resources.pak",
          "v8_context_snapshot.bin",
          "locales/en-US.pak",
          "chrome-sandbox",
        ];
  const paths = names.map((name) => join(resourceDir, name));
  if (process.platform === "linux") {
    const sharunSandbox = sharunResourceDir
      ? join(binaryDir, "chrome-sandbox")
      : resolve(binaryDir, "../shared/bin/chrome-sandbox");
    if (existsSync(sharunSandbox)) paths[paths.length - 1] = sharunSandbox;
  }
  return { paths };
}

function validateMinimumHost() {
  if (process.platform === "darwin") {
    const kernelMajor = Number.parseInt(release().split(".")[0], 10);
    if (kernelMajor < target.minimumKernelMajor) {
      fail(`platform smoke requires ${target.minimum}, found Darwin ${release()}`);
    }
  } else if (process.platform === "win32") {
    const build = Number.parseInt(release().split(".")[2], 10);
    if (build < target.minimumBuild) {
      fail(`platform smoke requires ${target.minimum}, found Windows ${release()}`);
    }
  } else if (process.platform === "linux") {
    const distribution = Object.fromEntries(
      readFileSync("/etc/os-release", "utf8")
        .split("\n")
        .filter((line) => line.includes("="))
        .map((line) => {
          const separator = line.indexOf("=");
          return [line.slice(0, separator), line.slice(separator + 1).replace(/^"|"$/gu, "")];
        }),
    );
    if (distribution.ID !== "ubuntu" || Number.parseFloat(distribution.VERSION_ID) < 22.04) {
      fail(
        `platform smoke requires Ubuntu 22.04+, found ${distribution.ID} ${distribution.VERSION_ID}`,
      );
    }
    const hasDisplay = Boolean(process.env.DISPLAY);
    const hasWayland = Boolean(process.env.WAYLAND_DISPLAY) || process.env.XDG_SESSION_TYPE === "wayland";
    if (!hasDisplay && hasWayland) {
      fail("native Wayland is unsupported; use X11 or XWayland");
    }
    if (!hasDisplay) {
      fail("DISPLAY is required for the Ubuntu X11 platform smoke");
    }
    if (hasWayland) {
      console.warn("devhud: XWayland smoke is best effort");
    }
  }
}

async function runScenario(
  executable,
  mode,
  expectedExit,
  expectedMarkers,
  { rustLog = "info" } = {},
) {
  const cacheRoot = mkdtempSync(join(tmpdir(), "devhud-smoke-cache-"));
  const logRoot = join(cacheRoot, "logs");
  try {
    const child = spawn(executable, [], {
      detached: process.platform !== "win32",
      env: {
        ...process.env,
        DEVHUD_PLATFORM_SMOKE: mode,
        DEVHUD_SMOKE_CACHE_DIR: cacheRoot,
        DEVHUD_SMOKE_LOG_DIR: logRoot,
        RUST_LOG: rustLog,
      },
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    let output = "";
    child.stdout.on("data", (chunk) => {
      output += chunk.toString();
    });
    child.stderr.on("data", (chunk) => {
      output += chunk.toString();
    });
    const exitCode = await waitForChildClose(child, mode, 30_000);
    if (exitCode !== expectedExit) {
      const outcome = child.signalCode
        ? `was terminated by ${child.signalCode}`
        : `exited ${exitCode}`;
      throw new Error(`${mode} smoke ${outcome}, expected exit ${expectedExit}\n${output}`);
    }
    for (const marker of expectedMarkers) {
      if (!output.includes(marker)) {
        throw new Error(`${mode} smoke did not emit ${marker}\n${output}`);
      }
    }
    const persistedLogPaths = readdirSync(logRoot)
      .filter((name) => /^devhud\.\d{4}-\d{2}-\d{2}\.jsonl$/u.test(name))
      .sort()
      .map((name) => join(logRoot, name));
    if (persistedLogPaths.length === 0) {
      throw new Error(`${mode} smoke did not create a dated diagnostic log`);
    }
    const persistedOutput = persistedLogPaths.map((path) => readFileSync(path, "utf8")).join("");
    const persistedEntries = persistedOutput
      .trim()
      .split("\n")
      .map((line, index) => {
        try {
          return JSON.parse(line);
        } catch (error) {
          throw new Error(`${mode} smoke persisted invalid JSON on line ${index + 1}: ${error.message}`);
        }
      });
    for (const [index, entry] of persistedEntries.entries()) {
      if (typeof entry.timestamp !== "string" || Number.isNaN(Date.parse(entry.timestamp))) {
        throw new Error(`${mode} smoke persisted an invalid timestamp on line ${index + 1}`);
      }
    }
    for (const marker of expectedMarkers) {
      if (!persistedOutput.includes(marker)) {
        throw new Error(`${mode} smoke did not persist ${marker}\n${persistedOutput}`);
      }
    }
  } finally {
    rmSync(cacheRoot, { recursive: true, force: true });
  }
}

const { artifact } = parseArguments();
if (process.platform === "linux" && !artifact) {
  fail(
    "Linux platform smoke requires --artifact <path> from an installed or root-prepared package layout",
  );
}
validateMinimumHost();
let executable;
try {
  executable = realpathSync(resolve(artifact ?? defaultArtifact()));
} catch (error) {
  fail(`production artifact is unavailable: ${error.message}`);
}
const resources = requiredResources(executable);
const missing = [executable, ...resources.paths].filter((path) => {
  try {
    return !readdirSync(dirname(path)).includes(path.slice(dirname(path).length + 1));
  } catch {
    return true;
  }
});
if (missing.length > 0) {
  fail(`CEF helper/resource discovery failed: ${missing.join(", ")}`);
}

if (process.platform === "linux") {
  const sandbox = resources.paths.find((path) => path.endsWith("chrome-sandbox"));
  const metadata = statSync(sandbox);
  const sandboxMode = metadata.mode & 0o7777;
  if (metadata.uid !== 0 || metadata.gid !== 0 || sandboxMode !== 0o4755) {
    fail(
      `CEF SUID sandbox must be owned by root:root with mode 4755; found ${metadata.uid}:${metadata.gid} mode ${sandboxMode.toString(8)}`,
    );
  }
}

for (let iteration = 1; iteration <= 3; iteration += 1) {
  await runScenario(executable, "normal", 0, [
    "cef_resources_verified",
    "frontend_ready",
    "smoke_shutdown_requested",
    "host_shutdown_complete",
  ]);
  console.log(`devhud: clean startup/shutdown ${iteration}/3 passed`);
}

await runScenario(executable, "renderer-crash", 0, [
  "renderer_crash_requested",
  "renderer_terminated",
]);
console.log("devhud: renderer diagnostic smoke passed");

await runScenario(
  executable,
  "missing-resource",
  78,
  ["cef_fatal_initialization", "resource-pack"],
  { rustLog: "off" },
);
console.log(`devhud: platform smoke passed for ${target.id} (${target.minimum})`);
