#!/usr/bin/env node

import { spawn } from "node:child_process";
import net from "node:net";

const [appName, command, defaultPort, overrideEnvName, ...rspressArgs] =
  process.argv.slice(2);
const hasOverride = overrideEnvName && overrideEnvName !== "-";
const portText = hasOverride
  ? process.env[overrideEnvName] || defaultPort
  : defaultPort;
const port = Number(portText);
const defaultHost = "127.0.0.1";

if (
  !appName ||
  !["dev", "preview"].includes(command) ||
  !defaultPort ||
  !overrideEnvName ||
  !Number.isInteger(port) ||
  port < 1 ||
  port > 65535
) {
  console.error(
    "Usage: run-rspress-port.mjs <app-name> <dev|preview> <default-port> <override-env-name|-> [rspress-args...]",
  );
  process.exit(1);
}

function findHostArg(args) {
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];

    if (
      arg === "--host" &&
      args[index + 1] &&
      !args[index + 1].startsWith("-")
    ) {
      return args[index + 1];
    }

    if (arg.startsWith("--host=")) {
      return arg.slice("--host=".length);
    }
  }

  return defaultHost;
}

function hasHostArg(args) {
  return args.some((arg) => arg === "--host" || arg.startsWith("--host="));
}

function checkPortAvailable(portToCheck, hostToCheck) {
  return new Promise((resolve, reject) => {
    const server = net.createServer();

    server.once("error", (error) => {
      if (error.code === "EADDRINUSE") {
        resolve(false);
        return;
      }

      reject(error);
    });

    server.once("listening", () => {
      server.close(() => resolve(true));
    });

    server.listen(portToCheck, hostToCheck);
  });
}

function printPortConflict(portInUse) {
  console.error(`${appName}: port ${portInUse} is already in use.`);
  console.error("");
  console.error("Recovery:");
  console.error(
    `  1. Find the listener: lsof -nP -iTCP:${portInUse} -sTCP:LISTEN`,
  );
  console.error("  2. Stop that process, then rerun this command.");

  if (hasOverride) {
    console.error(
      `  3. For an explicit temporary override, run: ${overrideEnvName}=<free-port> pnpm --filter ${appName} ${command}`,
    );
  }

  console.error("");
  console.error(`The fixed ${command} port remains ${defaultPort}.`);
}

const host = findHostArg(rspressArgs);
const args = hasHostArg(rspressArgs)
  ? [command, ...rspressArgs, "--port", String(port)]
  : [command, ...rspressArgs, "--host", host, "--port", String(port)];

const isAvailable = await checkPortAvailable(port, host);
if (!isAvailable) {
  printPortConflict(port);
  process.exit(1);
}

const child = spawn("rspress", args, {
  // Rspress exposes no strict-port CLI flag, so the app configs read this
  // process-local marker and delegate conflict enforcement to Rsbuild.
  env: { ...process.env, DELINO_RSPRESS_STRICT_PORT: "1" },
  stdio: "inherit",
  shell: process.platform === "win32",
});

child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code ?? 0);
});

child.on("error", (error) => {
  console.error(
    `${appName}: failed to start Rspress ${command}: ${error.message}`,
  );
  process.exit(1);
});
