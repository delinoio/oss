import { spawn } from "node:child_process";
import { access, appendFile } from "node:fs/promises";
import { resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

async function exists(path) {
  try {
    await access(path);
    return true;
  } catch (error) {
    if (error.code === "ENOENT") return false;
    throw error;
  }
}

const eventLog = process.env.DEVHUD_TEST_EVENT_LOG;
const forbidden = [
  "DEVHUD_DATABASE_URL",
  "DEVHUD_LOGTO_ISSUER",
  "DEVHUD_IDENTITY_HMAC_KEYS",
  "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_KEY",
  "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_EXPECTED",
  "DEVHUD_R2_SECRET_ACCESS_KEY",
  "DOCKER_HOST",
  "DOCKER_CONTEXT",
];
if (forbidden.some((name) => process.env[name])) {
  process.stderr.write("Turbo received a service configuration value\n");
  process.exitCode = 5;
} else if (!["team", "oss"].includes(process.env.DEVHUD_LOCAL_MODE)) {
  process.stderr.write("Turbo did not receive the bounded mode selector\n");
  process.exitCode = 6;
} else if (eventLog) {
  await appendFile(
    eventLog,
    `${JSON.stringify({ tool: "pnpm", action: "turbo", args: process.argv.slice(2), mode: process.env.DEVHUD_LOCAL_MODE })}\n`,
    "utf8",
  );
  if (
    process.env.DEVHUD_LOCAL_MODE === "team" &&
    process.env.DEVHUD_TEST_RUN_TURBO_SERVICES === "1"
  ) {
    for (const script of [
      resolve(process.cwd(), "servers/devhud-api/scripts/local.mjs"),
      resolve(process.cwd(), "apps/devhud-admin/scripts/development.mjs"),
    ]) {
      const child = spawn(process.execPath, [script, "serve"], {
        cwd: process.cwd(),
        env: process.env,
        shell: false,
        stdio: "inherit",
      });
      const result = await new Promise((resolveResult, reject) => {
        child.once("error", reject);
        child.once("close", (code, signal) => resolveResult({ code, signal }));
      });
      if (result.code !== 0 || result.signal) {
        process.exitCode = result.code ?? 1;
        break;
      }
    }
  }
  if (process.env.DEVHUD_TEST_BLOCK_PNPM === "1") {
    await appendFile(
      eventLog,
      `${JSON.stringify({ tool: "pnpm", action: "turbo-blocked" })}\n`,
      "utf8",
    );
    const releaseFile = process.env.DEVHUD_TEST_PNPM_RELEASE_FILE;
    if (releaseFile) {
      while (!(await exists(releaseFile))) await delay(20);
    } else {
      await new Promise(() => setInterval(() => {}, 1_000));
    }
  }
}
