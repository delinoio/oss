import { spawn } from "node:child_process";
import { appendFile, access, writeFile } from "node:fs/promises";
import { constants as fsConstants } from "node:fs";
import { resolve } from "node:path";

const args = process.argv.slice(2);
const eventLog = process.env.DEVHUD_TEST_EVENT_LOG;
const authState = process.env.DEVHUD_TEST_AUTH_STATE;

async function log(event) {
  if (eventLog) await appendFile(eventLog, `${JSON.stringify(event)}\n`, "utf8");
}

async function exists(path) {
  try {
    await access(path, fsConstants.F_OK);
    return true;
  } catch {
    return false;
  }
}

if (args.includes("--version")) {
  if (process.env.DEVHUD_TEST_BLOCK_INFISICAL_VERSION === "1") {
    await log({ tool: "infisical", action: "version-blocked" });
    await new Promise(() => setInterval(() => {}, 1_000));
  }
  process.stdout.write(
    process.env.DEVHUD_TEST_INFISICAL_VERSION_OUTPUT ??
      "infisical version 0.43.116\n",
  );
} else if (args.includes("user") && args.includes("token")) {
  await log({ tool: "infisical", action: "auth-probe" });
  if (authState && (await exists(authState))) process.stdout.write("AUTH_TOKEN_MUST_NOT_LEAK\n");
  else process.exitCode = 1;
} else if (args.includes("login")) {
  await log({ tool: "infisical", action: "login" });
  await writeFile(authState, "authenticated\n", "utf8");
} else if (args.includes("init")) {
  await log({ tool: "infisical", action: "init" });
  await writeFile(resolve(process.cwd(), ".infisical.json"), "{}\n", "utf8");
} else if (args.includes("run")) {
  const path = args.find((arg) => arg.startsWith("--path="))?.slice("--path=".length);
  await log({ tool: "infisical", action: "run", path, args });
  if (process.env.DEVHUD_TEST_INFISICAL_FAILURE === path) {
    process.stderr.write("INFISICAL_FAILURE_CANARY_MUST_NOT_LEAK\n");
    process.exitCode = 1;
  } else {
    const separator = args.indexOf("--");
    const command = args[separator + 1];
    const commandArgs = args.slice(separator + 2);
    const api = {
      DEVHUD_DATABASE_URL: "postgres://devhud:API_DATABASE_CANARY@127.0.0.1:5432/devhud?sslmode=disable",
      DEVHUD_PUBLIC_API_URL: "http://127.0.0.1:46307",
      DEVHUD_LOGTO_ISSUER:
        process.env.DEVHUD_TEST_API_LOGTO_ISSUER ??
        "http://localhost:3001/oidc",
      DEVHUD_LOGTO_AUDIENCE: "urn:API_AUDIENCE_CANARY",
      DEVHUD_LOGTO_DESKTOP_CLIENT_ID: "API_DESKTOP_CANARY",
      DEVHUD_LOGTO_IOS_CLIENT_ID: "API_IOS_CANARY",
      DEVHUD_LOGTO_ANDROID_CLIENT_ID: "API_ANDROID_CANARY",
      DEVHUD_LOGTO_ADMIN_CLIENT_ID: "API_ADMIN_CANARY",
      DEVHUD_ADMIN_REDIRECT_URI: "http://localhost:46306/auth/callback",
      DEVHUD_PUBLIC_ASSET_BASE_URL: "http://127.0.0.1:46307",
      DEVHUD_IDENTITY_HMAC_KEYS: "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=",
    };
    const admin = {
      DEVHUD_LOGTO_ISSUER:
        process.env.DEVHUD_TEST_ADMIN_LOGTO_ISSUER ??
        "http://localhost:3001/oidc",
    };
    const secrets = path === "/devhud/api" ? api : admin;
    if (process.env.DEVHUD_TEST_SECRET_SHAPE === "unknown") secrets.DEVHUD_UNKNOWN_CANARY = "UNKNOWN_VALUE_CANARY";
    if (process.env.DEVHUD_TEST_SECRET_SHAPE === "partial") secrets.DEVHUD_R2_ENDPOINT = "https://example.invalid";
    if (process.env.DEVHUD_TEST_SECRET_SHAPE === "missing") delete secrets.DEVHUD_DATABASE_URL;
    const child = spawn(command, commandArgs, {
      cwd: process.cwd(),
      env: { ...process.env, ...secrets },
      shell: false,
      stdio: "inherit",
    });
    const result = await new Promise((resolveResult, reject) => {
      child.once("error", reject);
      child.once("exit", (code, signal) => resolveResult({ code, signal }));
    });
    if (result.signal) process.kill(process.pid, result.signal);
    process.exitCode = result.code ?? 0;
  }
} else {
  process.exitCode = 2;
}
