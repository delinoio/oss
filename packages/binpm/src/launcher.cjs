// SPDX-License-Identifier: Apache-2.0
"use strict";

const { spawn } = require("node:child_process");
const { readFileSync, statSync } = require("node:fs");
const { createRequire } = require("node:module");
const path = require("node:path");
const { selectTarget } = require("./platforms.cjs");

function resolveBinary(
  manifestPath = path.join(__dirname, "..", "package.json"),
  target = selectTarget(),
) {
  if (!target)
    throw new Error(
      "Unsupported binpm platform. Use macOS, Windows, or glibc Linux on x64/arm64; musl is not supported.",
    );
  const { version } = JSON.parse(readFileSync(manifestPath, "utf8"));
  let dependencyPath;
  try {
    dependencyPath = createRequire(manifestPath).resolve(
      `${target.name}/package.json`,
    );
  } catch {
    throw new Error(
      `Missing ${target.name}. Reinstall @delino/binpm with optional dependencies enabled on the target platform.`,
    );
  }
  const dependency = JSON.parse(readFileSync(dependencyPath, "utf8"));
  if (dependency.name !== target.name || dependency.version !== version)
    throw new Error(
      `Version mismatch for ${target.name}; reinstall @delino/binpm on the target platform.`,
    );
  const binary = path.join(path.dirname(dependencyPath), "bin", target.binary);
  if (!statSync(binary).isFile())
    throw new Error(`Missing binpm executable in ${target.name}.`);
  return binary;
}

function launch(binary, args, { parent = process, start = spawn } = {}) {
  return new Promise((resolve, reject) => {
    const child = start(binary, args, { stdio: "inherit", windowsHide: true });
    const signals = [
      "SIGINT",
      "SIGTERM",
      "SIGHUP",
      ...(process.platform === "win32" ? ["SIGBREAK"] : []),
    ];
    const handlers = signals.map((signal) => [
      signal,
      () => {
        // Windows console interrupts reach both processes. child.kill() would
        // force termination before the native process can finish its cleanup.
        if (
          process.platform === "win32" &&
          ["SIGINT", "SIGBREAK"].includes(signal)
        )
          return;
        if (child.exitCode === null && child.signalCode === null)
          child.kill(signal);
      },
    ]);
    for (const [signal, handler] of handlers) parent.on(signal, handler);
    const cleanup = () => {
      for (const [signal, handler] of handlers)
        parent.removeListener(signal, handler);
    };
    child.once("error", () => {
      cleanup();
      reject(
        new Error(
          "Unable to start binpm. Check permissions and reinstall for this platform.",
        ),
      );
    });
    child.once("exit", (code, signal) => {
      cleanup();
      resolve({ code, signal });
    });
  });
}

module.exports = { resolveBinary, launch };
