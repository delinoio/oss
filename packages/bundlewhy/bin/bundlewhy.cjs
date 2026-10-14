#!/usr/bin/env node
// bundlewhy npm launcher: resolves the native binary for this platform and
// runs it without a shell, forwarding arguments, stdio, signals and exit codes.
"use strict";

const { spawn } = require("node:child_process");
const path = require("node:path");

/** Supported platform package suffixes, keyed by `${platform}-${arch}`. */
const PLATFORM_PACKAGES = Object.freeze({
  "darwin-arm64": "darwin-arm64",
  "linux-x64": "linux-x64-gnu",
  "win32-x64": "win32-x64-msvc",
});

function isMusl() {
  if (process.platform !== "linux") return false;
  const report =
    typeof process.report?.getReport === "function"
      ? process.report.getReport()
      : null;
  return !report?.header?.glibcVersionRuntime;
}

function resolveBinary(
  env = process.env,
  platform = process.platform,
  arch = process.arch,
  musl = isMusl(),
) {
  if (env.BUNDLEWHY_BINARY) return { binary: env.BUNDLEWHY_BINARY };
  const suffix = PLATFORM_PACKAGES[`${platform}-${arch}`];
  if (!suffix || musl) {
    return {
      error: {
        code: "BW_PLATFORM_UNSUPPORTED",
        message: `bundlewhy does not provide a binary for ${platform}-${arch}${musl ? " (musl)" : ""}`,
        recovery:
          "Supported platforms: macOS arm64, Linux x64 (glibc) and Windows x64.",
      },
    };
  }
  const packageName = `@delino/bundlewhy-${suffix}`;
  const executable = platform === "win32" ? "bundlewhy.exe" : "bundlewhy";
  try {
    const manifest = require.resolve(`${packageName}/package.json`);
    return { binary: path.join(path.dirname(manifest), "bin", executable) };
  } catch {
    return {
      error: {
        code: "BW_PLATFORM_PACKAGE_MISSING",
        message: `${packageName} is not installed`,
        recovery:
          "Reinstall @delino/bundlewhy with optional dependencies enabled.",
      },
    };
  }
}

function main() {
  const resolved = resolveBinary();
  if (resolved.error) {
    process.stderr.write(
      `error[${resolved.error.code}]: ${resolved.error.message}\n  recovery: ${resolved.error.recovery}\n`,
    );
    process.exitCode = 1;
    return;
  }
  const child = spawn(resolved.binary, process.argv.slice(2), {
    stdio: "inherit",
    shell: false,
  });
  const forward = (signal) => {
    if (process.platform !== "win32") child.kill(signal);
  };
  // On Windows the console delivers Ctrl+C to the child directly.
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"])
    process.on(signal, () => forward(signal));
  child.on("error", (error) => {
    process.stderr.write(
      `error[BW_LAUNCH_FAILED]: ${error.message}\n  recovery: Reinstall @delino/bundlewhy.\n`,
    );
    process.exitCode = 1;
  });
  child.on("exit", (code, signal) => {
    if (signal)
      process.exitCode =
        128 + (require("node:os").constants.signals[signal] ?? 1);
    else process.exitCode = code ?? 1;
  });
}

module.exports = { PLATFORM_PACKAGES, resolveBinary };

if (require.main === module) main();
