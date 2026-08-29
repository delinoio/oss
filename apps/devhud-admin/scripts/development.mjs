#!/usr/bin/env node

import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  adminContract,
  repositoryRoot,
} from "../../../scripts/dev-environment/contracts.mjs";
import {
  executeArgv,
  readServiceEnv,
  runService,
} from "../../../scripts/dev-environment/service-environment.mjs";

const scriptPath = fileURLToPath(import.meta.url);
const serviceDirectory = resolve(dirname(scriptPath), "..");

async function ossEnvironment() {
  const overrides = await readServiceEnv(resolve(serviceDirectory, ".env"), ["DEVHUD_LOGTO_ISSUER"]);
  return { DEVHUD_LOGTO_ISSUER: "http://localhost:3001/oidc", ...overrides };
}

async function execute(action, environment) {
  if (action === "validate") return { code: 0, signal: null };
  if (action !== "serve") return { code: 1, signal: null };
  return executeArgv(
    process.execPath,
    [
      resolve(repositoryRoot, "scripts/run-rsbuild-dev.mjs"),
      "devhud-admin",
      "46306",
      "--no-env",
    ],
    environment,
    serviceDirectory,
  );
}

process.exitCode = await runService({
  contract: adminContract,
  action: process.argv[2],
  scriptPath,
  ossEnvironment,
  execute,
});
