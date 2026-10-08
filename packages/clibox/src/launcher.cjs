"use strict";

const { spawn } = require("node:child_process");
const { readFileSync, statSync } = require("node:fs");
const { createRequire } = require("node:module");
const path = require("node:path");
const { Platform, selectTarget } = require("./platforms.cjs");

const Failure = Object.freeze({ Unsupported: "unsupported-platform", Missing: "missing-binary", Version: "version-mismatch", Spawn: "spawn-failed" });
const TerminalInterruptAcknowledgementDescriptor = 3;
const TerminalInterruptAcknowledgementEnvironment = "CLIBOX_TERMINAL_INTERRUPT_ACK_FD";
const TerminalInterruptGraceMs = 10;
class LauncherError extends Error {
  constructor(code, message) { super(message); this.code = code; }
}

function resolveBinary(manifestPath = path.join(__dirname, "..", "package.json"), target = selectTarget()) {
  if (!target) throw new LauncherError(Failure.Unsupported, "This platform is not supported by @delino/clibox. Use a supported OS/architecture.");
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

function launch(binary, args, { spawnChild = spawn, parent = process, environment = process.env, platform = process.platform, setTimer = setTimeout, clearTimer = clearTimeout } = {}) {
  return new Promise((resolve, reject) => {
    let settled = false;
    let forwardingOnly = false;
    const pendingInterrupts = new Map();
    const usesTerminalInterruptAcknowledgement = platform !== Platform.Windows;
    const child = spawnChild(binary, args, {
      stdio: usesTerminalInterruptAcknowledgement ? ["inherit", "inherit", "inherit", "pipe"] : "inherit",
      shell: false,
      ...(usesTerminalInterruptAcknowledgement ? {
        env: {
          ...environment,
          [TerminalInterruptAcknowledgementEnvironment]: String(TerminalInterruptAcknowledgementDescriptor),
        },
      } : {}),
    });
    const acknowledgement = usesTerminalInterruptAcknowledgement ? child.stdio?.[TerminalInterruptAcknowledgementDescriptor] : null;
    const acknowledgeTerminalInterrupt = (chunk) => {
      // Bytes have no event identity. Credit belongs only to one current grace
      // attempt and must never survive a fallback or overlap another attempt.
      if (!settled && !forwardingOnly && chunk.length > 0 && pendingInterrupts.size === 1) {
        pendingInterrupts.values().next().value.acknowledged = true;
      }
    };
    const ignoreAcknowledgementError = () => {};
    acknowledgement?.on("data", acknowledgeTerminalInterrupt);
    acknowledgement?.on("error", ignoreAcknowledgementError);
    const signals = ["SIGINT", "SIGTERM", "SIGHUP", ...(platform === Platform.Windows ? ["SIGBREAK"] : [])];
    const handlers = signals.map((signal) => [signal, () => {
      // Windows broadcasts console Ctrl+C/Break to both processes. Node's kill
      // API forcibly terminates Windows children, so forwarding would race the
      // native handler's cleanup and numeric exit status. Unix signals must
      // always be forwarded: a supervisor can target this launcher's PID even
      // when stdin is attached to a foreground terminal.
      if (platform === Platform.Windows && (signal === "SIGINT" || signal === "SIGBREAK")) return;
      if (signal === "SIGINT") {
        if (settled) return;
        if (pendingInterrupts.size > 0) forwardingOnly = true;
        if (forwardingOnly) {
          if (child.exitCode === null && child.signalCode === null) child.kill(signal);
          return;
        }
        // Suppress only an early acknowledgement for an unambiguous attempt.
        // After uncertainty, duplicate terminal forwarding is preferable to
        // losing a later launcher-only interrupt. This lasts for this launch.
        const attempt = { acknowledged: false };
        const timer = setTimer(() => {
          pendingInterrupts.delete(timer);
          if (settled) return;
          if (!forwardingOnly && attempt.acknowledged) return;
          forwardingOnly = true;
          if (child.exitCode === null && child.signalCode === null) child.kill(signal);
        }, TerminalInterruptGraceMs);
        pendingInterrupts.set(timer, attempt);
        return;
      }
      if (child.exitCode === null && child.signalCode === null) child.kill(signal);
    }]);
    for (const [signal, handler] of handlers) parent.on(signal, handler);
    const cleanup = () => {
      settled = true;
      for (const timer of pendingInterrupts.keys()) clearTimer(timer);
      pendingInterrupts.clear();
      acknowledgement?.removeListener("data", acknowledgeTerminalInterrupt);
      acknowledgement?.removeListener("error", ignoreAcknowledgementError);
      for (const [signal, handler] of handlers) parent.removeListener(signal, handler);
    };
    child.once("error", () => {
      cleanup();
      reject(new LauncherError(Failure.Spawn, "Unable to start clibox. Check executable permissions and reinstall for this platform."));
    });
    child.once("exit", (code, signal) => { cleanup(); resolve({ code, signal }); });
  });
}

module.exports = { Failure, LauncherError, resolveBinary, launch };
