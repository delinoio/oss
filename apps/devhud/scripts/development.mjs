#!/usr/bin/env node

import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  adminContract,
  apiContract,
  devhudFrontendContract,
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
    resolve(repositoryRoot, "apps/devhud-admin/.env"),
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
  const fakeFrontend =
    process.env.DEVHUD_ENVIRONMENT_TESTING === "1"
      ? process.env.DEVHUD_TEST_FRONTEND
      : null;
  return executeArgv(
    process.execPath,
    fakeFrontend
      ? [fakeFrontend]
      : [resolve(serviceDirectory, "scripts/run-tauri.mjs"), "dev"],
    environment,
    serviceDirectory,
  );
}

process.exitCode = await runService({
  contract: devhudFrontendContract,
  action: process.argv[2],
  comparisonName: "DEVHUD_LOGTO_ISSUER",
  scriptPath,
  ossEnvironment,
  execute,
});
