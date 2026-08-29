#!/usr/bin/env node

import { exitLikeChild, spawnDevServer } from "./spawn-dev-server.mjs";
import { commandInvocation } from "./dev-environment/process.mjs";

const [appName, portText, ...rawRsbuildArgs] = process.argv.slice(2);
const port = Number(portText);
const rsbuildArgs =
  rawRsbuildArgs[0] === "--" ? rawRsbuildArgs.slice(1) : rawRsbuildArgs;
const pnpm = commandInvocation("pnpm");

if (!appName || !Number.isInteger(port) || port < 1 || port > 65535) {
  console.error(
    "Usage: run-rsbuild-dev.mjs <app-name> <fixed-port> [rsbuild-args...]",
  );
  process.exit(1);
}

const addressOverride = rsbuildArgs.find(
  (arg) =>
    arg === "--host" ||
    arg.startsWith("--host=") ||
    arg === "--port" ||
    arg.startsWith("--port="),
);

if (addressOverride) {
  console.error(
    `${appName}: ${addressOverride.split("=")[0]} cannot override the app-owned fixed development address on port ${port}.`,
  );
  process.exit(1);
}

try {
  const result = await spawnDevServer(
    pnpm.command,
    [
      ...pnpm.prefix,
      "exec",
      "rsbuild",
      "dev",
      ...rsbuildArgs,
      "--port",
      String(port),
      "--strict-port",
    ],
    {
      stdio: "inherit",
      shell: false,
    },
    { terminateProcessTree: true },
  );
  exitLikeChild(result);
} catch (error) {
  console.error(`${appName}: failed to start Rsbuild dev: ${error.message}`);
  process.exit(1);
}
