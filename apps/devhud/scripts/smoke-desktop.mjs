import { spawn } from "node:child_process";
import { resolve } from "node:path";

import { packageManagerCommand, runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
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

await runPackageManager(["run", "build"], { cwd: appRoot });

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

const invocation = packageManagerCommand(
  ["exec", "tauri", "dev", "--no-watch", "--features", "desktop-cef"],
  { cwd: appRoot },
);
const output = [];
const child = spawn(invocation.command, invocation.args, {
  ...invocation.options,
  env: {
    ...process.env,
    DEVHUD_PROBE_SMOKE: "1",
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
  child.once("exit", (code) => resolveExit(code));
});
clearTimeout(timeout);

const combinedOutput = output.join("");
if (
  exitCode !== 0 ||
  !combinedOutput.includes("devhud.probe.capability_denial_observed")
) {
  throw new Error(
    `desktop smoke did not complete the bundled IPC handshake (exit ${exitCode ?? "signal"})`,
  );
}

console.log(
  JSON.stringify({
    check: "devhud-desktop-smoke",
    status: "passed",
  }),
);
