#!/usr/bin/env node

import process from "node:process";
import { spawn } from "node:child_process";

import { isDevPortAvailable, parseDevPort } from "./dev-port.mjs";

const [app, envName, defaultPortText, command, ...args] = process.argv.slice(2);
const portText = process.env[envName] || defaultPortText;

if (!app || !envName || !defaultPortText || !command) {
  console.error(
    "Usage: run-dev-server.mjs <app> <port-env-name> <default-port> <command> [args...]",
  );
  process.exit(1);
}

let port;
try {
  port = parseDevPort(portText, envName);
} catch (error) {
  console.error(
    `[dev-server] event=configuration_failed app=${app} error=${JSON.stringify(error.message)}`,
  );
  process.exit(1);
}

if (!(await isDevPortAvailable(port))) {
  console.error(`[dev-server] event=port_unavailable app=${app} port=${port} env=${envName}`);
  process.exit(1);
}

console.error(`[dev-server] event=start app=${app} port=${port} env=${envName}`);

const child = spawn(command, [...args, "--port", String(port)], {
  stdio: "inherit",
  shell: process.platform === "win32",
});

child.once("error", (error) => {
  console.error(
    `[dev-server] event=start_failed app=${app} error=${JSON.stringify(error.message)}`,
  );
  process.exit(1);
});

child.once("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
