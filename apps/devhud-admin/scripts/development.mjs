#!/usr/bin/env node

import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  adminContract,
  apiContract,
  repositoryRoot,
  resolveOssLogtoIssuer,
} from "../../../scripts/dev-environment/contracts.mjs";
import {
  executeArgv,
  readServiceEnv,
  runService,
} from "../../../scripts/dev-environment/service-environment.mjs";

const scriptPath = fileURLToPath(import.meta.url);
const serviceDirectory = resolve(dirname(scriptPath), "..");

async function ossEnvironment() {
  const adminOverrides = await readServiceEnv(
    resolve(serviceDirectory, ".env"),
    adminContract.ossOverrideNames,
  );
  const apiOverrides = await readServiceEnv(
    resolve(repositoryRoot, "servers/devhud-api/.env"),
    apiContract.ossOverrideNames,
  );
  return {
    DEVHUD_LOGTO_ISSUER: resolveOssLogtoIssuer(
      adminOverrides.DEVHUD_LOGTO_ISSUER,
      apiOverrides.DEVHUD_LOGTO_ISSUER,
    ),
  };
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
  comparisonName: "DEVHUD_LOGTO_ISSUER",
  scriptPath,
  ossEnvironment,
  execute,
});
