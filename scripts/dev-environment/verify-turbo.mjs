#!/usr/bin/env node

import assert from "node:assert/strict";
import { collect, commandInvocation, safeBaseEnvironment } from "./process.mjs";
import { repositoryRoot } from "./contracts.mjs";

const pnpm = commandInvocation("pnpm");
const result = await collect(
  pnpm.command,
  [
    ...pnpm.prefix,
    "exec",
    "turbo",
    "run",
    "dev",
    "--dry=json",
    "--filter=devhud",
    "--filter=devhud-admin",
    "--filter=@delinoio/devhud-api",
  ],
  {
    cwd: repositoryRoot,
    env: {
      ...safeBaseEnvironment(),
      DEVHUD_LOCAL_MODE: "oss",
      TURBO_TELEMETRY_DISABLED: "1",
    },
  },
);

assert.equal(result.code, 0, "turbo dry run must succeed");
const resolved = JSON.parse(result.stdout);
assert.deepEqual(resolved.globalCacheInputs.environmentVariables.specified.env, []);
assert.equal(
  resolved.globalCacheInputs.environmentVariables.specified.passThroughEnv,
  null,
);

const devTasks = resolved.tasks.filter((task) => task.task === "dev");
assert.deepEqual(
  devTasks.map((task) => task.package).sort(),
  ["@delinoio/devhud-api", "devhud", "devhud-admin"],
);
for (const task of devTasks) {
  assert.deepEqual(task.resolvedTaskDefinition.env, ["DEVHUD_LOCAL_MODE"]);
  assert.equal(task.resolvedTaskDefinition.passThroughEnv, null);
  assert.deepEqual(task.environmentVariables.specified.env, ["DEVHUD_LOCAL_MODE"]);
  assert.equal(task.environmentVariables.specified.passThroughEnv, null);
}

const environmentConfiguration = JSON.stringify({
  global: resolved.globalCacheInputs.environmentVariables,
  tasks: resolved.tasks.map((task) => ({
    taskId: task.taskId,
    resolved: task.resolvedTaskDefinition,
    environment: task.environmentVariables,
  })),
});
for (const forbidden of [
  "DEVHUD_DATABASE_URL",
  "DEVHUD_LOGTO_ISSUER",
  "DEVHUD_IDENTITY_HMAC_KEYS",
  "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_KEY",
  "DEVHUD_R2_SECRET_ACCESS_KEY",
  "INFISICAL_TOKEN",
]) {
  assert.equal(environmentConfiguration.includes(forbidden), false, forbidden);
}

process.stdout.write("Resolved Turbo environment contains only DEVHUD_LOCAL_MODE on dev tasks.\n");
