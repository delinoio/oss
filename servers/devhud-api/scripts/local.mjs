#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  apiContract,
  defaultOssLogtoIssuer,
  EnvironmentError,
  repositoryRoot,
  resolveLocalStatePaths,
} from "../../../scripts/dev-environment/contracts.mjs";
import {
  executeArgv,
  readServiceEnv,
  runService,
} from "../../../scripts/dev-environment/service-environment.mjs";

const scriptPath = fileURLToPath(import.meta.url);
const serviceDirectory = resolve(dirname(scriptPath), "..");
async function ossEnvironment(action) {
  const { identityKeyFile } = resolveLocalStatePaths();
  let identityKey;
  try {
    identityKey = (await readFile(identityKeyFile, "utf8")).trim();
  } catch (error) {
    if (error.code === "ENOENT" && action === "validate") {
      // Preflight validates local overrides before generating checkout state.
      identityKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
    } else {
      throw new EnvironmentError(
        "environment.local-state",
        "DevHud API local identity material is unavailable",
      );
    }
  }
  const overrides = await readServiceEnv(
    resolve(serviceDirectory, ".env"),
    apiContract.ossOverrideNames,
  );
  return {
    DEVHUD_DATABASE_URL: "postgres://devhud:devhud@127.0.0.1:5432/devhud?sslmode=disable",
    DEVHUD_PUBLIC_API_URL: "http://127.0.0.1:46307",
    DEVHUD_LOGTO_ISSUER: defaultOssLogtoIssuer,
    DEVHUD_LOGTO_AUDIENCE: "urn:devhud:oss-unconfigured",
    DEVHUD_LOGTO_DESKTOP_CLIENT_ID: "oss-unconfigured-desktop",
    DEVHUD_LOGTO_IOS_CLIENT_ID: "oss-unconfigured-ios",
    DEVHUD_LOGTO_ANDROID_CLIENT_ID: "oss-unconfigured-android",
    DEVHUD_LOGTO_ADMIN_CLIENT_ID: "oss-unconfigured-admin",
    DEVHUD_ADMIN_REDIRECT_URI: "http://localhost:46306/auth/callback",
    DEVHUD_PUBLIC_ASSET_BASE_URL: "http://127.0.0.1:46307",
    DEVHUD_IDENTITY_HMAC_KEYS: identityKey,
    ...overrides,
  };
}

async function execute(action, environment) {
  if (action === "validate") return { code: 0, signal: null };
  const fakeGo =
    process.env.DEVHUD_ENVIRONMENT_TESTING === "1" ? process.env.DEVHUD_TEST_GO : null;
  return executeArgv(
    fakeGo ? process.execPath : "go",
    [
      ...(fakeGo ? [fakeGo] : []),
      "run",
      "./servers/devhud-api/cmd/devhud-api",
      action === "migrate" ? "migrate" : "serve",
    ],
    environment,
    repositoryRoot,
  );
}

process.exitCode = await runService({
  contract: apiContract,
  action: process.argv[2],
  scriptPath,
  ossEnvironment,
  execute,
});
