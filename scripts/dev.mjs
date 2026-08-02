#!/usr/bin/env node

import path from "node:path";
import process from "node:process";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

import { isDevPortAvailable, parseDevPort } from "./dev-port.mjs";

const MAX_PORT_SEARCH_DISTANCE = 1_000;
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const fixedPreviewPorts = new Set([46_251, 46_261]);

const devServers = [
  { app: "public-docs", env: "PUBLIC_DOCS_DEV_PORT", defaultPort: 46_249 },
  { app: "nodeup-docs", env: "NODEUP_DOCS_DEV_PORT", defaultPort: 46_250 },
  { app: "binpm-docs", env: "BINPM_DOCS_DEV_PORT", defaultPort: 46_260 },
  { app: "delidev-app", env: "DELIDEV_APP_DEV_PORT", defaultPort: 4_173 },
  { app: "devhud", env: "DEVHUD_DEV_PORT", defaultPort: 3_000 },
];

async function findAvailablePort(defaultPort, reservedPorts) {
  const lastCandidate = Math.min(65_535, defaultPort + MAX_PORT_SEARCH_DISTANCE);
  for (let port = defaultPort + 1; port <= lastCandidate; port += 1) {
    if (
      !reservedPorts.has(port) &&
      !fixedPreviewPorts.has(port) &&
      (await isDevPortAvailable(port))
    ) {
      return port;
    }
  }
  throw new Error(
    `no available port found from ${defaultPort + 1} through ${lastCandidate}`,
  );
}

async function resolveDevPorts() {
  const resolved = new Map();
  const reservedPorts = new Set();
  const defaultsAvailable = new Map(
    await Promise.all(
      devServers.map(async ({ env, defaultPort }) => [
        env,
        await isDevPortAvailable(defaultPort),
      ]),
    ),
  );

  for (const server of devServers) {
    const configuredValue = process.env[server.env];
    if (configuredValue === undefined || configuredValue === "") {
      continue;
    }

    const configuredPort = parseDevPort(configuredValue, server.env);
    if (reservedPorts.has(configuredPort)) {
      throw new Error(`${server.env} duplicates another configured development port`);
    }
    if (!(await isDevPortAvailable(configuredPort))) {
      throw new Error(`${server.env}=${configuredPort} is already in use`);
    }

    reservedPorts.add(configuredPort);
    resolved.set(server.env, configuredPort);
  }

  for (const server of devServers) {
    if (resolved.has(server.env)) {
      continue;
    }
    if (defaultsAvailable.get(server.env) && !reservedPorts.has(server.defaultPort)) {
      reservedPorts.add(server.defaultPort);
      resolved.set(server.env, server.defaultPort);
    }
  }

  for (const server of devServers) {
    if (resolved.has(server.env)) {
      continue;
    }

    const selectedPort = await findAvailablePort(server.defaultPort, reservedPorts);
    reservedPorts.add(selectedPort);
    resolved.set(server.env, selectedPort);
    console.error(
      `[dev] event=port_remap app=${server.app} default_port=${server.defaultPort} selected_port=${selectedPort} env=${server.env}`,
    );
  }

  return resolved;
}

function forwardSignals(child) {
  // An interactive Ctrl-C reaches the entire foreground process group, so only
  // forward SIGINT when there is no TTY and the child would not receive it.
  if (!process.stdin.isTTY) {
    process.once("SIGINT", () => child.kill("SIGINT"));
  }
  process.once("SIGTERM", () => child.kill("SIGTERM"));
}

async function main() {
  const resolvedPorts = await resolveDevPorts();
  const turboArgs = process.argv.slice(2);
  if (turboArgs[0] === "--") {
    turboArgs.shift();
  }

  const env = { ...process.env };
  for (const [envName, port] of resolvedPorts) {
    env[envName] = String(port);
  }

  console.error(
    `[dev] event=start command=turbo_run_dev remapped_ports=${devServers.filter((server) => resolvedPorts.get(server.env) !== server.defaultPort).length}`,
  );

  const child = spawn(
    "go",
    [
      "-C",
      repoRoot,
      "run",
      "./cmds/derun",
      "run",
      "--",
      "pnpm",
      "exec",
      "turbo",
      "run",
      "dev",
      ...turboArgs,
    ],
    { cwd: repoRoot, env, stdio: "inherit" },
  );

  forwardSignals(child);

  child.once("error", (error) => {
    console.error(`[dev] event=start_failed error=${JSON.stringify(error.message)}`);
    process.exit(1);
  });

  child.once("exit", (code, signal) => {
    if (signal) {
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 1);
  });
}

main().catch((error) => {
  console.error(`[dev] event=configuration_failed error=${JSON.stringify(error.message)}`);
  process.exit(1);
});
