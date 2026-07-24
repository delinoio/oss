import { spawn } from "node:child_process";
import { resolve } from "node:path";

import { runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const supportedHosts = new Set(["darwin", "linux", "win32"]);

if (!supportedHosts.has(process.platform)) {
  console.log(
    JSON.stringify({
      check: "devhud-desktop-smoke",
      status: "skipped",
      reason: `unsupported-host-${process.platform}`,
    }),
  );
  process.exit(0);
}

if (
  process.platform === "linux" &&
  !process.env.DISPLAY
) {
  console.log(
    JSON.stringify({
      check: "devhud-desktop-smoke",
      status: "skipped",
      reason: "headless-linux-host-without-x11",
      compileValidation: "run pnpm build:desktop on this host",
    }),
  );
  process.exit(0);
}

let binaryPath;

if (process.platform === "darwin") {
  // CEF resolves its macOS framework relative to the .app bundle, so the raw
  // target/debug binary cannot represent a valid desktop startup.
  await runPackageManager(["run", "build"], { cwd: appRoot });
  await runPackageManager(
    [
      "exec",
      "tauri",
      "build",
      "--debug",
      "--bundles",
      "app",
      "--features",
      "desktop-cef",
      "--config",
      '{"bundle":{"active":true}}',
      "--no-sign",
    ],
    { cwd: appRoot },
  );
  binaryPath = resolve(
    repositoryRoot,
    "target",
    "debug",
    "bundle",
    "macos",
    "DevHud.app",
    "Contents",
    "MacOS",
    "devhud",
  );
} else {
  await runPackageManager(["run", "build:desktop"], { cwd: appRoot });
  binaryPath = resolve(
    repositoryRoot,
    "target",
    "debug",
    process.platform === "win32" ? "devhud.exe" : "devhud",
  );
}
const output = [];
const child = spawn(binaryPath, [], {
  cwd: appRoot,
  env: {
    ...process.env,
    DEVHUD_SMOKE: "1",
  },
  stdio: ["ignore", "pipe", "pipe"],
});

for (const stream of [child.stdout, child.stderr]) {
  stream.setEncoding("utf8");
  stream.on("data", (chunk) => {
    output.push(chunk);
    process.stderr.write(chunk);
  });
}

const timeout = setTimeout(() => {
  child.kill();
}, 10 * 60 * 1000);

const exitCode = await new Promise((resolveExit, reject) => {
  child.once("error", reject);
  child.once("close", (code) => resolveExit(code));
});
clearTimeout(timeout);

const combinedOutput = output.join("");
if (
  exitCode !== 0 ||
  !combinedOutput.includes("devhud.runtime.ready")
) {
  throw new Error(
    `desktop smoke did not observe the ready runtime (exit ${exitCode ?? "signal"})`,
  );
}

console.log(
  JSON.stringify({
    check: "devhud-desktop-smoke",
    status: "passed",
  }),
);
