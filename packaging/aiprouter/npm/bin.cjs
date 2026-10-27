#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
const path = require("node:path");
const { spawn } = require("node:child_process");
const executable = path.join(
  __dirname,
  "native",
  process.platform === "win32" ? "aiprouter.exe" : "aiprouter",
);
const child = spawn(executable, process.argv.slice(2), {
  stdio: "inherit",
  shell: false,
});
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => child.kill(signal));
child.on("error", () => {
  console.error(
    "AIPRouter binary is unavailable. Reinstall with install scripts enabled.",
  );
  process.exitCode = 1;
});
child.on("exit", (code, signal) => {
  process.exitCode = code ?? (signal === "SIGINT" ? 130 : 143);
});
