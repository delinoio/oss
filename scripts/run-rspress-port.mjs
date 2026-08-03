#!/usr/bin/env node

import net from "node:net";

import { exitLikeChild, spawnDevServer } from "./spawn-dev-server.mjs";

const [appName, command, defaultPort, overrideEnvName, ...rawRspressArgs] =
  process.argv.slice(2);
const rspressArgs =
  rawRspressArgs[0] === "--" ? rawRspressArgs.slice(1) : rawRspressArgs;
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
  const packageCommand = `pnpm --filter ${appName} ${command}`;

  console.error(`${appName}: port ${portInUse} is already in use.`);
  console.error("");
  console.error("Recovery:");

  if (process.platform === "win32") {
    console.error(
      `  1. Find the listener (PowerShell): Get-NetTCPConnection -LocalPort ${portInUse} -State Listen`,
    );
  } else {
    console.error(
      `  1. Find the listener: lsof -nP -iTCP:${portInUse} -sTCP:LISTEN`,
    );
  }

  console.error("  2. Stop that process, then rerun this command.");

  if (hasOverride) {
    if (process.platform === "win32") {
      console.error(
        `  3. Explicit temporary override (PowerShell): $env:${overrideEnvName}='<free-port>'; ${packageCommand}`,
      );
      console.error(
        `     Explicit temporary override (cmd.exe): set "${overrideEnvName}=<free-port>" && ${packageCommand}`,
      );
    } else {
      console.error(
        `  3. For an explicit temporary override, run: ${overrideEnvName}=<free-port> ${packageCommand}`,
      );
    }
  }

  console.error("");
  console.error(`The fixed ${command} port remains ${defaultPort}.`);
}

if (hasHostArg(rspressArgs)) {
  console.error(
    `${appName}: --host cannot override the fixed loopback host ${defaultHost}.`,
  );
  process.exit(1);
}

const host = defaultHost;
const args = [
  command,
  ...rspressArgs,
  "--host",
  host,
  "--port",
  String(port),
];

const isAvailable = await checkPortAvailable(port, host);
if (!isAvailable) {
  printPortConflict(port);
  process.exit(1);
}

try {
  const result = await spawnDevServer("rspress", args, {
    // Rspress exposes no strict-port CLI flag, so the app configs read this
    // process-local marker and delegate conflict enforcement to Rsbuild.
    env: { ...process.env, DELINO_RSPRESS_STRICT_PORT: "1" },
    stdio: "inherit",
    shell: process.platform === "win32",
  });
  exitLikeChild(result);
} catch (error) {
  console.error(
    `${appName}: failed to start Rspress ${command}: ${error.message}`,
  );
  process.exit(1);
}
