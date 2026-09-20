"use strict";

const { spawn } = require("node:child_process");
const { readFileSync, statSync } = require("node:fs");
const { createRequire } = require("node:module");
const path = require("node:path");
const { selectTarget } = require("./platforms.cjs");

const Failure = Object.freeze({ Unsupported: "unsupported-platform", Missing: "missing-binary", Version: "version-mismatch", Spawn: "spawn-failed" });
class LauncherError extends Error {
  constructor(code, message) { super(message); this.code = code; }
}

function resolveBinary(manifestPath = path.join(__dirname, "..", "package.json"), target = selectTarget()) {
  if (!target) throw new LauncherError(Failure.Unsupported, "This platform is not supported by @delino/clibox. Use a supported OS/architecture or cargo install clibox.");
  const { version } = JSON.parse(readFileSync(manifestPath, "utf8"));
  let dependencyPath;
  let dependency;
  try {
    dependencyPath = createRequire(manifestPath).resolve(`${target.name}/package.json`);
    dependency = JSON.parse(readFileSync(dependencyPath, "utf8"));
  } catch {
    throw new LauncherError(Failure.Missing, `Missing ${target.name}@${version}. Reinstall with optional dependencies enabled: npm install --include=optional or pnpm install (without --no-optional).`);
  }
  if (dependency.name !== target.name || dependency.version !== version) {
    throw new LauncherError(Failure.Version, `Reinstall @delino/clibox and ${target.name} at the same exact version (${version}).`);
  }
  const binary = path.join(path.dirname(dependencyPath), "bin", target.binary);
  try {
    if (!statSync(binary).isFile()) throw new Error("Not a file");
  } catch {
    throw new LauncherError(Failure.Missing, `The executable in ${target.name}@${version} is missing. Reinstall with optional dependencies enabled.`);
  }
  return binary;
}

function launch(binary, args, { spawnChild = spawn, parent = process } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawnChild(binary, args, { stdio: "inherit", shell: false });
    const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
    const handlers = signals.map((signal) => [signal, () => { if (child.exitCode === null && child.signalCode === null) child.kill(signal); }]);
    for (const [signal, handler] of handlers) parent.on(signal, handler);
    const cleanup = () => { for (const [signal, handler] of handlers) parent.removeListener(signal, handler); };
    child.once("error", () => {
      cleanup();
      reject(new LauncherError(Failure.Spawn, "Unable to start clibox. Check executable permissions and reinstall for this platform."));
    });
    child.once("exit", (code, signal) => { cleanup(); resolve({ code, signal }); });
  });
}

module.exports = { Failure, LauncherError, resolveBinary, launch };
