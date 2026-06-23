#!/usr/bin/env node

import net from "node:net";
import { spawn } from "node:child_process";

const [command, defaultPort, envName] = process.argv.slice(2);
const portText = process.env[envName] || defaultPort;
const port = Number(portText);

if (!command || !defaultPort || !envName || !Number.isInteger(port) || port < 1 || port > 65535) {
  console.error("Usage: run-rspress-port.mjs <dev|preview> <default-port> <override-env-name>");
  process.exit(1);
}

function checkPortAvailable(portToCheck) {
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

    server.listen(portToCheck, "127.0.0.1");
  });
}

function printPortConflict(portInUse) {
  console.error(`nodeup-docs: port ${portInUse} is already in use.`);
  console.error("");
  console.error("Recovery:");
  console.error(`  1. Find the listener: lsof -nP -iTCP:${portInUse} -sTCP:LISTEN`);
  console.error("  2. Stop that process, then rerun this command.");
  console.error(
    `  3. For a temporary override, run: ${envName}=<free-port> pnpm --filter nodeup-docs ${command}`,
  );
  console.error("");
  console.error(`The default ${command} port remains ${defaultPort}.`);
}

const isAvailable = await checkPortAvailable(port);
if (!isAvailable) {
  printPortConflict(port);
  process.exit(1);
}

const child = spawn("rspress", [command, "--port", String(port)], {
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
  console.error(`nodeup-docs: failed to start Rspress ${command}: ${error.message}`);
  process.exit(1);
});
