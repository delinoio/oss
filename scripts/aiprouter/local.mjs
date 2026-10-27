// SPDX-License-Identifier: Apache-2.0
import { createHash, randomBytes } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  collect,
  dockerClientEnvironment,
  safeBaseEnvironment,
  spawnResult,
  terminateTree,
} from "../dev-environment/process.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const log = (event, message) => console.log(JSON.stringify({ event, message }));

export async function startLocal({
  directory = root,
  action = "start",
  run,
  wait = waitFor,
} = {}) {
  if (!["start", "stop", "logs"].includes(action)) {
    throw new Error(
      "Usage: node scripts/aiprouter/local.mjs [start|stop|logs]",
    );
  }
  const project = `aiprouter-dev-${createHash("sha256").update(directory).digest("hex").slice(0, 12)}`;
  const state = resolve(directory, ".dev-environment/aiprouter");
  const envfile = resolve(state, ".env");
  const environment = {
    ...safeBaseEnvironment(),
    ...dockerClientEnvironment(),
    AIPROUTER_LOCAL_ENV_FILE: envfile,
  };
  run ??= (args, options) =>
    docker(args, { ...options, env: environment, cwd: directory });
  await run(["compose", "version"], { quiet: true });
  await run(["info"], { quiet: true });
  if (!existsSync(envfile)) {
    if (action !== "start") {
      log(
        "local_not_started",
        "No local environment has been created in this checkout.",
      );
      return;
    }
    const volumes = await run(
      [
        "volume",
        "ls",
        "--filter",
        `label=com.docker.compose.project=${project}`,
        "--quiet",
      ],
      { quiet: true },
    );
    if (volumes.trim()) {
      throw new Error(
        "Local database exists but its encryption configuration is missing. Restore .dev-environment/aiprouter/.env before starting.",
      );
    }
    mkdirSync(state, { recursive: true, mode: 0o700 });
    writeFileSync(envfile, localEnvironment(), { flag: "wx", mode: 0o600 });
  }
  chmodSync(envfile, 0o600);
  const compose = [
    "compose",
    "--project-name",
    project,
    "--env-file",
    envfile,
    "-f",
    resolve(directory, "packaging/aiprouter/compose.local.yml"),
  ];
  const command = (...args) => run([...compose, ...args]);
  if (action === "stop") {
    await command("down");
    log(
      "local_stopped",
      "Local containers stopped. Database and encryption keys are preserved.",
    );
    return;
  }
  if (action === "logs") {
    await command("logs", "--follow", "--tail", "100");
    return;
  }
  log(
    "local_starting",
    "Building the local console and API; the first run downloads build dependencies.",
  );
  await command("build");
  // Keep admission stopped while schema migrations run, including subsequent starts.
  await command("stop", "aiprouter");
  try {
    await command("up", "-d", "--wait", "postgres", "fixtures");
    await wait("http://127.0.0.1:18090/healthz");
    await command("run", "--rm", "--no-deps", "aiprouter", "migrate");
    await command("up", "-d", "aiprouter");
    await wait("http://127.0.0.1:8080/readyz");
  } catch (error) {
    await command("stop").catch(() => {});
    throw error;
  }
  log(
    "local_ready",
    "Open http://127.0.0.1:8080 and sign in as the synthetic Alice user. No password is required. Containers run in the background.",
  );
  log(
    "local_limits",
    "Local evaluation only: mail is discarded; generated image/video storage requires real R2 configuration. No real provider is preconfigured.",
  );
  log(
    "local_commands",
    "Stop: pnpm dev:aiprouter:stop | Logs: pnpm dev:aiprouter:logs",
  );
}

function localEnvironment() {
  return [
    `POSTGRES_PASSWORD=${randomBytes(24).toString("hex")}`,
    "AIPROUTER_PUBLIC_URL=http://127.0.0.1:8080",
    "AIPROUTER_OIDC_ISSUER=http://127.0.0.1:18090",
    "AIPROUTER_OIDC_CLIENT_ID=aiprouter-fixture",
    `AIPROUTER_OIDC_CLIENT_SECRET=${randomBytes(24).toString("hex")}`,
    `AIPROUTER_ENCRYPTION_KEYS='${JSON.stringify({ v1: randomBytes(32).toString("base64") })}'`,
    "AIPROUTER_ENCRYPTION_ACTIVE_KEY=v1",
    'AIPROUTER_OPERATORS=[{"issuer":"http://127.0.0.1:18090","subject":"alice"}]',
    "AIPROUTER_SMTP_ADDR=127.0.0.1:1025",
    "AIPROUTER_SMTP_FROM=aiprouter@example.invalid",
    "AIPROUTER_R2_ENDPOINT=https://r2.example.invalid",
    "AIPROUTER_R2_BUCKET=aiprouter-local",
    "AIPROUTER_R2_ACCESS_KEY_ID=local-placeholder",
    "AIPROUTER_R2_SECRET_ACCESS_KEY=local-placeholder",
    "",
  ].join("\n");
}

async function docker(args, options) {
  if (options.quiet) {
    const result = await collect("docker", args, options);
    if (result.code !== 0)
      throw new Error(
        "Docker is unavailable. Install Docker Compose and start Docker Desktop or the Docker daemon, then retry.",
      );
    return result.stdout;
  }
  const { child, completion } = spawnResult("docker", args, {
    ...options,
    stdio: "inherit",
    detached: process.platform !== "win32",
  });
  const interrupt = () => {
    void terminateTree(child);
  };
  process.once("SIGINT", interrupt);
  process.once("SIGTERM", interrupt);
  try {
    const result = await completion;
    if (result.code !== 0)
      throw new Error(
        "Local Compose command failed. Check the Docker output above, then retry pnpm dev:aiprouter.",
      );
    return "";
  } finally {
    process.removeListener("SIGINT", interrupt);
    process.removeListener("SIGTERM", interrupt);
  }
}

async function waitFor(url) {
  for (let attempt = 0; attempt < 90; attempt++) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(1000) });
      await response.body?.cancel();
      if (response.ok) return;
    } catch {
      /* The service may still be starting. */
    }
    await new Promise((done) => setTimeout(done, 1000));
  }
  throw new Error(
    `Local service did not become ready: ${url}. Inspect pnpm dev:aiprouter:logs.`,
  );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  startLocal({ action: process.argv[2] ?? "start" }).catch((error) => {
    console.error(
      JSON.stringify({ event: "local_failed", message: error.message }),
    );
    process.exitCode = 1;
  });
}
