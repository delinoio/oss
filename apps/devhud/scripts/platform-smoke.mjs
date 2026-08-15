#!/usr/bin/env node

import { spawn } from "node:child_process";
import {
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  renameSync,
  rmSync,
} from "node:fs";
import { release, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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
      artifact = args[index + 1];
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
      missingScenario: join(
        contents,
        "Frameworks/Chromium Embedded Framework.framework/Resources/resources.pak",
      ),
      paths: [
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Chromium Embedded Framework"),
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Resources/icudtl.dat"),
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Resources/resources.pak"),
        join(contents, "Frameworks/Chromium Embedded Framework.framework/Libraries/libcef_sandbox.dylib"),
        ...helpers,
      ],
    };
  }
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
  return {
    missingScenario: join(binaryDir, "resources.pak"),
    paths: names.map((name) => join(binaryDir, name)),
  };
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

async function runScenario(executable, mode, expectedExit, expectedMarkers) {
  const cacheRoot = mkdtempSync(join(tmpdir(), "devhud-smoke-cache-"));
  try {
    const child = spawn(executable, [], {
      env: {
        ...process.env,
        DEVHUD_PLATFORM_SMOKE: mode,
        DEVHUD_SMOKE_CACHE_DIR: cacheRoot,
        RUST_LOG: "info",
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
    const exitCode = await new Promise((resolveExit, reject) => {
      const timer = setTimeout(() => {
        child.kill("SIGKILL");
        reject(new Error(`${mode} smoke timed out`));
      }, 30_000);
      child.once("error", reject);
      child.once("exit", (code) => {
        clearTimeout(timer);
        resolveExit(code);
      });
    });
    if (exitCode !== expectedExit) {
      throw new Error(`${mode} smoke exited ${exitCode}, expected ${expectedExit}\n${output}`);
    }
    for (const marker of expectedMarkers) {
      if (!output.includes(marker)) {
        throw new Error(`${mode} smoke did not emit ${marker}\n${output}`);
      }
    }
  } finally {
    rmSync(cacheRoot, { recursive: true, force: true });
  }
}

const { artifact } = parseArguments();
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

const withheld = `${resources.missingScenario}.devhud-smoke-withheld`;
renameSync(resources.missingScenario, withheld);
try {
  await runScenario(executable, "normal", 78, [
    "cef_fatal_initialization",
    "resources.pak",
  ]);
} finally {
  renameSync(withheld, resources.missingScenario);
}
console.log(`devhud: platform smoke passed for ${target.id} (${target.minimum})`);
