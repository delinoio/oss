import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { EventEmitter, once } from "node:events";
import { createServer } from "node:net";
import { mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  adminContract,
  apiContract,
  comparisonMarker,
  composeFile,
  devhudFrontendContract,
  EnvironmentError,
  repositoryRoot,
  requireMode,
  resolveInfisicalConfigPaths,
  resolveLocalStatePaths,
  resolveOssLogtoIssuer,
  validateInjectedEnvironment,
} from "./contracts.mjs";
import {
  assertFixedPort,
  composeProjectName,
  createLocalIdentityKey,
  dockerEndpointIsLocal,
  Lifecycle,
} from "./orchestrator.mjs";
import {
  closeResult,
  commandInvocation,
  dockerClientEnvironment,
  safeBaseEnvironment,
  terminateTree,
} from "./process.mjs";
import { readServiceEnv } from "./service-environment.mjs";
import { windowsTerminationEnvironment } from "../spawn-dev-server.mjs";

const testDirectory = dirname(fileURLToPath(import.meta.url));
const fakeDirectory = resolve(testDirectory, "test");
const cli = resolve(testDirectory, "cli.mjs");
const apiScript = resolve(repositoryRoot, "servers/devhud-api/scripts/local.mjs");
const lateProvider = resolve(fakeDirectory, "fake-late-provider.mjs");
const canaries = [
  "AUTH_TOKEN_MUST_NOT_LEAK",
  "INFISICAL_FAILURE_CANARY_MUST_NOT_LEAK",
  "API_DATABASE_CANARY",
  "API_AUDIENCE_CANARY",
  "API_DESKTOP_CANARY",
  "UNKNOWN_VALUE_CANARY",
  comparisonMarker,
];

function validApiEnvironment() {
  return {
    DEVHUD_DATABASE_URL: "postgres://devhud:devhud@127.0.0.1:5432/devhud?sslmode=disable",
    DEVHUD_PUBLIC_API_URL: "http://127.0.0.1:46307",
    DEVHUD_LOGTO_ISSUER: "http://localhost:3001/oidc",
    DEVHUD_LOGTO_AUDIENCE: "urn:devhud:test",
    DEVHUD_LOGTO_DESKTOP_CLIENT_ID: "desktop",
    DEVHUD_LOGTO_IOS_CLIENT_ID: "ios",
    DEVHUD_LOGTO_ANDROID_CLIENT_ID: "android",
    DEVHUD_LOGTO_ADMIN_CLIENT_ID: "admin",
    DEVHUD_ADMIN_REDIRECT_URI: "http://localhost:46306/auth/callback",
    DEVHUD_PUBLIC_ASSET_BASE_URL: "http://127.0.0.1:46307",
    DEVHUD_IDENTITY_HMAC_KEYS: "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=",
  };
}

function spawnNode(script, args, environment) {
  const child = spawn(process.execPath, [script, ...args], {
    cwd: repositoryRoot,
    env: environment,
    shell: false,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const stdout = [];
  const stderr = [];
  child.stdout.on("data", (chunk) => stdout.push(chunk));
  child.stderr.on("data", (chunk) => stderr.push(chunk));
  const completion = new Promise((resolveResult, reject) => {
    child.once("error", reject);
    child.once("close", (code, signal) =>
      resolveResult({
        code,
        signal,
        stdout: Buffer.concat(stdout).toString("utf8"),
        stderr: Buffer.concat(stderr).toString("utf8"),
      }),
    );
  });
  return { child, completion };
}

async function runNode(script, args, environment) {
  return spawnNode(script, args, environment).completion;
}

function fakeEnvironment(temporaryDirectory) {
  return {
    ...process.env,
    DOCKER_CONTEXT: "devhud-review-context",
    DOCKER_HOST: "unix:///tmp/devhud-review-docker.sock",
    DEVHUD_ENVIRONMENT_TESTING: "1",
    DEVHUD_TEST_AUTH_STATE: resolve(temporaryDirectory, "auth-state"),
    DEVHUD_TEST_ADMIN: resolve(fakeDirectory, "fake-admin.mjs"),
    DEVHUD_TEST_FRONTEND: resolve(fakeDirectory, "fake-frontend.mjs"),
    DEVHUD_TEST_DOCKER: resolve(fakeDirectory, "fake-docker.mjs"),
    DEVHUD_TEST_DOCKER_ENDPOINT: "unix:///tmp/devhud-review-docker.sock",
    DEVHUD_TEST_EVENT_LOG: resolve(temporaryDirectory, "events.jsonl"),
    DEVHUD_TEST_GO: resolve(fakeDirectory, "fake-go.mjs"),
    DEVHUD_TEST_INFISICAL: resolve(fakeDirectory, "fake-infisical.mjs"),
    DEVHUD_TEST_INFISICAL_CONFIG_DIRECTORY: resolve(
      temporaryDirectory,
      "infisical-config",
    ),
    DEVHUD_TEST_PNPM: resolve(fakeDirectory, "fake-pnpm.mjs"),
    DEVHUD_TEST_STATE_DIRECTORY: resolve(temporaryDirectory, "state"),
  };
}

async function events(path) {
  try {
    return (await readFile(path, "utf8"))
      .trim()
      .split(/\r?\n/u)
      .filter(Boolean)
      .map((line) => JSON.parse(line));
  } catch (error) {
    if (error.code === "ENOENT") return [];
    throw error;
  }
}

async function waitForEvent(path, predicate) {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    const recorded = await events(path);
    if (recorded.some(predicate)) return;
    await delay(20);
  }
  throw new Error("timed out waiting for fake tool event");
}

async function interruptCliDuring(args, environment, predicate) {
  const child = spawn(process.execPath, [cli, ...args], {
    cwd: repositoryRoot,
    detached: process.platform !== "win32",
    env: environment,
    shell: false,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const completion = new Promise((resolveResult, reject) => {
    child.once("error", reject);
    child.once("close", (code, signal) => resolveResult({ code, signal }));
  });
  let timeout;
  try {
    await waitForEvent(environment.DEVHUD_TEST_EVENT_LOG, predicate);
    child.kill("SIGTERM");
    return await Promise.race([
      completion,
      new Promise((_, reject) => {
        timeout = setTimeout(
          () => reject(new Error("development command did not exit after SIGTERM")),
          5_000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timeout);
    if (child.exitCode === null && child.signalCode === null) {
      await terminateTree(child, "SIGKILL").catch(() => child.kill("SIGKILL"));
    }
  }
}

test("mode is a bounded enum-style selector", () => {
  assert.equal(requireMode("team"), "team");
  assert.equal(requireMode("oss"), "oss");
  assert.throws(() => requireMode("production"), /mode must be exactly/u);
});

test("Windows pnpm invocation uses cmd.exe while native executables remain direct", () => {
  assert.deepEqual(
    commandInvocation("pnpm", "win32", {
      ComSpec: "C:\\Windows\\System32\\cmd.exe",
    }),
    {
      command: "C:\\Windows\\System32\\cmd.exe",
      prefix: ["/d", "/s", "/c", "pnpm.cmd"],
    },
  );
  assert.deepEqual(commandInvocation("pnpm", "win32", {}), {
    command: "cmd.exe",
    prefix: ["/d", "/s", "/c", "pnpm.cmd"],
  });
  assert.deepEqual(commandInvocation("go", "win32"), {
    command: "go",
    prefix: [],
  });
  assert.deepEqual(commandInvocation("pnpm", "linux"), {
    command: "pnpm",
    prefix: [],
  });
});

test("Windows process-tree termination receives only required system context", () => {
  assert.deepEqual(
    windowsTerminationEnvironment({
      Path: "C:\\Windows\\System32",
      PATHEXT: ".COM;.EXE;.BAT;.CMD",
      SystemRoot: "C:\\Windows",
      windir: "C:\\Windows",
      DEVHUD_DATABASE_URL: "must-not-pass",
      DEVHUD_LOGTO_ISSUER: "must-not-pass",
      DEVHUD_R2_SECRET_ACCESS_KEY: "must-not-pass",
    }),
    {
      PATH: "C:\\Windows\\System32",
      PATHEXT: ".COM;.EXE;.BAT;.CMD",
      SYSTEMROOT: "C:\\Windows",
      WINDIR: "C:\\Windows",
    },
  );
});

test("child completion waits for close instead of exit", async () => {
  const child = new EventEmitter();
  let settled = false;
  const completion = closeResult(child).then((result) => {
    settled = true;
    return result;
  });
  child.emit("exit", 0, null);
  await Promise.resolve();
  assert.equal(settled, false);
  child.emit("close", 0, null);
  assert.deepEqual(await completion, { code: 0, signal: null });
});

test("base child environments preserve platform and Rust tool context without secrets", () => {
  assert.deepEqual(
    safeBaseEnvironment(
      {
        PATH: "/custom/cargo/bin:/usr/bin",
        CARGO_HOME: "/custom/cargo",
        RUSTUP_HOME: "/custom/rustup",
        __CF_USER_TEXT_ENCODING: "0x1F5:0:0",
        HOMEDRIVE: "C:",
        HOMEPATH: "\\Users\\developer",
        LOGONSERVER: "\\\\LOCALHOST",
        USERDOMAIN: "LOCAL",
        USERNAME: "developer",
        DISPLAY: ":99",
        XAUTHORITY: "/run/user/1000/gdm/Xauthority",
        XDG_RUNTIME_DIR: "/run/user/1000",
        WAYLAND_DISPLAY: "wayland-0",
        DEVHUD_DATABASE_URL: "must-not-pass",
        DOCKER_HOST: "must-not-pass",
      },
      "linux",
    ),
    {
      PATH: "/custom/cargo/bin:/usr/bin",
      CARGO_HOME: "/custom/cargo",
      RUSTUP_HOME: "/custom/rustup",
      __CF_USER_TEXT_ENCODING: "0x1F5:0:0",
      HOMEDRIVE: "C:",
      HOMEPATH: "\\Users\\developer",
      LOGONSERVER: "\\\\LOCALHOST",
      USERDOMAIN: "LOCAL",
      USERNAME: "developer",
      DISPLAY: ":99",
      XAUTHORITY: "/run/user/1000/gdm/Xauthority",
      XDG_RUNTIME_DIR: "/run/user/1000",
    },
  );
});

test("Linux session context is omitted from base child environments on other platforms", () => {
  for (const platform of ["darwin", "win32"]) {
    assert.deepEqual(
      safeBaseEnvironment(
        {
          PATH: "/usr/bin",
          DISPLAY: ":99",
          XAUTHORITY: "/run/user/1000/gdm/Xauthority",
          XDG_RUNTIME_DIR: "/run/user/1000",
          WAYLAND_DISPLAY: "wayland-0",
        },
        platform,
      ),
      { PATH: "/usr/bin" },
      platform,
    );
  }
});

test("temporary Infisical config paths must stay outside the checkout", () => {
  assert.throws(
    () => resolveInfisicalConfigPaths({ DEVHUD_ENVIRONMENT_TESTING: "1" }),
    /absolute temporary Infisical config directory/u,
  );
  assert.throws(
    () =>
      resolveInfisicalConfigPaths({
        DEVHUD_ENVIRONMENT_TESTING: "1",
        DEVHUD_TEST_INFISICAL_CONFIG_DIRECTORY: resolve(
          repositoryRoot,
          "temporary-config",
        ),
      }),
    /outside the checkout/u,
  );
});

test("temporary generated state paths must stay outside the checkout", () => {
  for (const testStateDirectory of [
    repositoryRoot,
    resolve(repositoryRoot, ".dev-environment"),
    resolve(repositoryRoot, "temporary-state"),
  ]) {
    assert.throws(
      () =>
        resolveLocalStatePaths({
          DEVHUD_ENVIRONMENT_TESTING: "1",
          DEVHUD_TEST_STATE_DIRECTORY: testStateDirectory,
        }),
      (error) =>
        error instanceof EnvironmentError &&
        error.code === "environment.test-state" &&
        /outside the checkout/u.test(error.message),
      testStateDirectory,
    );
  }
});

test("Docker daemon selection is the only Docker-specific ambient configuration preserved", () => {
  assert.deepEqual(
    dockerClientEnvironment({
      DOCKER_HOST: "unix:///run/user/1000/docker.sock",
      DOCKER_CONTEXT: "rootless",
      DOCKER_CONFIG: "/must-not-pass",
      DEVHUD_DATABASE_URL: "must-not-pass",
    }),
    {
      DOCKER_HOST: "unix:///run/user/1000/docker.sock",
      DOCKER_CONTEXT: "rootless",
    },
  );
});

test("Docker endpoint validation accepts only locally reachable daemon transports", () => {
  for (const endpoint of [
    "unix:///var/run/docker.sock",
    "npipe:////./pipe/docker_engine",
    "tcp://localhost:2375",
    "tcp://localhost.:2375",
    "tcp://127.0.0.1:2375",
    "tcp://127.0.0.2:2375",
    "tcp://127.255.255.254:2375",
    "tcp://[::1]:2375",
    "tcp://[0:0:0:0:0:0:0:1]:2375",
  ]) {
    assert.equal(dockerEndpointIsLocal(endpoint), true, endpoint);
  }
  for (const endpoint of [
    "npipe:////server/pipe/docker_engine",
    "npipe:////localhost/pipe/docker_engine",
    "tcp://192.0.2.10:2376",
    "tcp://128.0.0.1:2375",
    "tcp://127.0.0.1.example.test:2375",
    "tcp://localhost.example.test:2375",
    "tcp://[::2]:2375",
    "ssh://developer@example.test",
    "https://localhost:2376",
    "not-a-docker-endpoint",
    "",
  ]) {
    assert.equal(dockerEndpointIsLocal(endpoint), false, endpoint);
  }
});

test("Compose project names are stable, checkout-scoped, and do not disclose the key", () => {
  const firstKey = Buffer.alloc(32, 1).toString("base64");
  const secondKey = Buffer.alloc(32, 2).toString("base64");
  const firstName = composeProjectName(`${firstKey}\n`);
  assert.equal(firstName, composeProjectName(firstKey));
  assert.notEqual(firstName, composeProjectName(secondKey));
  assert.match(firstName, /^delino-devhud-development-[0-9a-f]{24}$/u);
  assert.equal(firstName.includes(firstKey), false);
});

test("API, administrator, and frontend allowlists reject malformed configuration", () => {
  const valid = validApiEnvironment();
  assert.deepEqual(validateInjectedEnvironment(apiContract, valid), valid);

  for (const [shape, expected] of [
    [{ ...valid, DEVHUD_DATABASE_URL: "" }, "environment.missing"],
    [{ ...valid, UNKNOWN_NAME: "canary" }, "environment.unknown"],
    [{ ...valid, PATH: "secret-path" }, "environment.unknown"],
    [{ ...valid, DEVHUD_R2_ENDPOINT: "https://example.invalid" }, "environment.partial-group"],
  ]) {
    assert.throws(
      () => validateInjectedEnvironment(apiContract, shape, { PATH: "safe-path" }),
      (error) => error instanceof EnvironmentError && error.code === expected,
    );
  }

  assert.throws(
    () =>
      validateInjectedEnvironment(adminContract, {
        DEVHUD_LOGTO_ISSUER: "http://localhost:3001/oidc",
        DEVHUD_DATABASE_URL: "must-not-cross-service-boundary",
      }),
    /unknown names/u,
  );
  assert.throws(
    () =>
      validateInjectedEnvironment(devhudFrontendContract, {
        DEVHUD_LOGTO_ISSUER: "http://localhost:3001/oidc",
        DEVHUD_DATABASE_URL: "must-not-cross-service-boundary",
      }),
    /unknown names/u,
  );

  for (const malformed of [
    "https:/issuer.example",
    "https:issuer.example",
    "https:///issuer.example",
    "http:/localhost:3001/oidc",
  ]) {
    for (const [contract, environment] of [
      [adminContract, { DEVHUD_LOGTO_ISSUER: malformed }],
      [devhudFrontendContract, { DEVHUD_LOGTO_ISSUER: malformed }],
      [apiContract, { ...valid, DEVHUD_LOGTO_ISSUER: malformed }],
    ]) {
      assert.throws(
        () => validateInjectedEnvironment(contract, environment),
        (error) =>
          error instanceof EnvironmentError &&
          error.code === "environment.invalid-values",
        malformed,
      );
    }
  }
});

test("preflight validates the raw public asset base path before WHATWG normalization", () => {
  const valid = validApiEnvironment();
  for (const assetBase of [
    "https://assets.example.com ",
    "https://assets.example.com/a/..",
    "https://assets.example.com/.",
    "https://assets.example.com/..",
    "https://assets.example.com/%2e",
    "https://assets.example.com/a/%2e%2e",
  ]) {
    assert.throws(
      () =>
        validateInjectedEnvironment(apiContract, {
          ...valid,
          DEVHUD_PUBLIC_ASSET_BASE_URL: assetBase,
        }),
      (error) =>
        error instanceof EnvironmentError &&
        error.code === "environment.invalid-values",
      assetBase,
    );
  }

  for (const assetBase of [
    "https://assets.example.com",
    "https://assets.example.com/",
  ]) {
    assert.equal(
      validateInjectedEnvironment(apiContract, {
        ...valid,
        DEVHUD_PUBLIC_ASSET_BASE_URL: assetBase,
      }).DEVHUD_PUBLIC_ASSET_BASE_URL,
      assetBase,
    );
  }
});

test("preflight rejects URL escapes rejected by the Go service parser", () => {
  const valid = validApiEnvironment();
  for (const issuer of [
    "https://issuer.example/%",
    "https://issuer.example/%2",
    "https://issuer.example/%zz",
    "https://%65xample.com",
  ]) {
    for (const [contract, environment] of [
      [adminContract, { DEVHUD_LOGTO_ISSUER: issuer }],
      [devhudFrontendContract, { DEVHUD_LOGTO_ISSUER: issuer }],
      [apiContract, { ...valid, DEVHUD_LOGTO_ISSUER: issuer }],
    ]) {
      assert.throws(
        () => validateInjectedEnvironment(contract, environment),
        (error) =>
          error instanceof EnvironmentError &&
          error.code === "environment.invalid-values",
        issuer,
      );
    }
  }

  for (const databaseURL of [
    "postgres://devhud:devhud@localhost/%",
    "postgres://devhud:devhud@localhost/%2",
    "postgres://devhud:devhud@localhost/%zz",
    "postgres://devhud:devhud@%6cocalhost/devhud",
    "postgresql://devhud:devhud@local%68ost/devhud",
  ]) {
    assert.throws(
      () =>
        validateInjectedEnvironment(apiContract, {
          ...valid,
          DEVHUD_DATABASE_URL: databaseURL,
        }),
      (error) =>
        error instanceof EnvironmentError &&
        error.code === "environment.invalid-values",
      databaseURL,
    );
  }

  const escapedIssuer = "https://issuer.example/oidc%2Ftenant";
  assert.deepEqual(
    validateInjectedEnvironment(adminContract, {
      DEVHUD_LOGTO_ISSUER: escapedIssuer,
    }),
    { DEVHUD_LOGTO_ISSUER: escapedIssuer },
  );
  assert.deepEqual(
    validateInjectedEnvironment(devhudFrontendContract, {
      DEVHUD_LOGTO_ISSUER: escapedIssuer,
    }),
    { DEVHUD_LOGTO_ISSUER: escapedIssuer },
  );
  assert.equal(
    validateInjectedEnvironment(apiContract, {
      ...valid,
      DEVHUD_LOGTO_ISSUER: escapedIssuer,
    }).DEVHUD_LOGTO_ISSUER,
    escapedIssuer,
  );

  const escapedDatabaseURL =
    "postgres://devhud:pass%40word@localhost/devhud";
  assert.equal(
    validateInjectedEnvironment(apiContract, {
      ...valid,
      DEVHUD_DATABASE_URL: escapedDatabaseURL,
    }).DEVHUD_DATABASE_URL,
    escapedDatabaseURL,
  );
});

test("preflight rejects raw URL spaces and control characters rejected by the Go service parser", () => {
  const valid = validApiEnvironment();
  const controlCharacters = [
    ...Array.from({ length: 0x20 }, (_, codePoint) =>
      String.fromCharCode(codePoint),
    ),
    String.fromCharCode(0x7f),
  ];

  for (const controlCharacter of controlCharacters) {
    const issuer = `https://issuer.exa${controlCharacter}mple.com/oidc`;
    for (const [contract, environment] of [
      [adminContract, { DEVHUD_LOGTO_ISSUER: issuer }],
      [devhudFrontendContract, { DEVHUD_LOGTO_ISSUER: issuer }],
      [apiContract, { ...valid, DEVHUD_LOGTO_ISSUER: issuer }],
    ]) {
      assert.throws(
        () => validateInjectedEnvironment(contract, environment),
        (error) =>
          error instanceof EnvironmentError &&
          error.code === "environment.invalid-values",
        `ASCII control character ${controlCharacter.charCodeAt(0)}`,
      );
    }

    const databaseURL =
      `postgres://devhud:devhud@localhost/dev${controlCharacter}hud`;
    assert.throws(
      () =>
        validateInjectedEnvironment(apiContract, {
          ...valid,
          DEVHUD_DATABASE_URL: databaseURL,
        }),
      (error) =>
        error instanceof EnvironmentError &&
        error.code === "environment.invalid-values",
      `database URL ASCII control character ${controlCharacter.charCodeAt(0)}`,
    );
  }
});

test("preflight accepts whitespace around identity HMAC key-ring entries", () => {
  const firstKey = Buffer.alloc(32, 1).toString("base64");
  const secondKey = Buffer.alloc(32, 2).toString("base64");
  const ring = `${firstKey}, ${secondKey}`;
  const valid = { ...validApiEnvironment(), DEVHUD_IDENTITY_HMAC_KEYS: ring };
  assert.equal(
    validateInjectedEnvironment(apiContract, valid).DEVHUD_IDENTITY_HMAC_KEYS,
    ring,
  );

  for (const invalidRing of [`${firstKey}, `, ` ,${secondKey}`]) {
    assert.throws(
      () =>
        validateInjectedEnvironment(apiContract, {
          ...valid,
          DEVHUD_IDENTITY_HMAC_KEYS: invalidRing,
        }),
      (error) =>
        error instanceof EnvironmentError &&
        error.code === "environment.invalid-values",
      invalidRing,
    );
  }
});

test("preflight accepts the same loopback issuer hosts as the services", () => {
  const valid = validApiEnvironment();
  for (const issuer of [
    "http://localhost.:3001/oidc",
    "http://127.0.0.2:3001/oidc",
    "http://[::1]:3001/oidc",
    "http://[0:0:0:0:0:0:0:1]:3001/oidc",
  ]) {
    assert.deepEqual(
      validateInjectedEnvironment(adminContract, { DEVHUD_LOGTO_ISSUER: issuer }),
      { DEVHUD_LOGTO_ISSUER: issuer },
    );
    assert.deepEqual(
      validateInjectedEnvironment(devhudFrontendContract, { DEVHUD_LOGTO_ISSUER: issuer }),
      { DEVHUD_LOGTO_ISSUER: issuer },
    );
    assert.deepEqual(
      validateInjectedEnvironment(apiContract, {
        ...valid,
        DEVHUD_LOGTO_ISSUER: issuer,
      }).DEVHUD_LOGTO_ISSUER,
      issuer,
    );
  }

  for (const contract of [adminContract, devhudFrontendContract, apiContract]) {
    const environment =
      contract === apiContract
        ? {
            ...valid,
            DEVHUD_LOGTO_ISSUER: "http://127.0.0.2.example.com/oidc",
          }
        : { DEVHUD_LOGTO_ISSUER: "http://127.0.0.2.example.com/oidc" };
    assert.throws(
      () => validateInjectedEnvironment(contract, environment),
      (error) =>
        error instanceof EnvironmentError &&
        error.code === "environment.invalid-values",
    );
  }
});

test("preflight rejects numeric IPv4 spellings rejected by the Go service parser", () => {
  const valid = validApiEnvironment();
  for (const issuer of [
    "http://127.1:3001/oidc",
    "http://2130706433:3001/oidc",
    "http://0177.0.0.1:3001/oidc",
    "http://0x7f000001:3001/oidc",
    "http://127.0.0.1.:3001/oidc",
  ]) {
    for (const [contract, environment] of [
      [adminContract, { DEVHUD_LOGTO_ISSUER: issuer }],
      [devhudFrontendContract, { DEVHUD_LOGTO_ISSUER: issuer }],
      [apiContract, { ...valid, DEVHUD_LOGTO_ISSUER: issuer }],
    ]) {
      assert.throws(
        () => validateInjectedEnvironment(contract, environment),
        (error) =>
          error instanceof EnvironmentError &&
          error.code === "environment.invalid-values",
        issuer,
      );
    }
  }
});

test("OSS API, administrator, and frontend overrides must resolve to the same issuer", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-issuer-test-"));
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  const environmentFile = resolve(temporaryDirectory, ".env");
  const issuer = "https://auth.example.test/oidc";
  await writeFile(environmentFile, `DEVHUD_LOGTO_ISSUER=${issuer}\n`, "utf8");

  const apiOverrides = await readServiceEnv(environmentFile, apiContract.ossOverrideNames);
  const adminOverrides = await readServiceEnv(environmentFile, adminContract.ossOverrideNames);
  const frontendOverrides = await readServiceEnv(
    environmentFile,
    devhudFrontendContract.ossOverrideNames,
  );
  assert.deepEqual(apiOverrides, { DEVHUD_LOGTO_ISSUER: issuer });
  assert.deepEqual(adminOverrides, apiOverrides);
  assert.deepEqual(frontendOverrides, apiOverrides);
  assert.equal(
    validateInjectedEnvironment(apiContract, { ...validApiEnvironment(), ...apiOverrides })
      .DEVHUD_LOGTO_ISSUER,
    issuer,
  );
  assert.deepEqual(validateInjectedEnvironment(adminContract, adminOverrides), adminOverrides);
  assert.deepEqual(
    validateInjectedEnvironment(devhudFrontendContract, frontendOverrides),
    frontendOverrides,
  );
  assert.equal(resolveOssLogtoIssuer(issuer, issuer), issuer);
  assert.equal(
    resolveOssLogtoIssuer(undefined, undefined),
    "http://localhost:3001/oidc",
  );
  assert.throws(
    () => resolveOssLogtoIssuer(issuer, "https://other.example.test/oidc"),
    (error) =>
      error instanceof EnvironmentError && error.code === "environment.issuer-mismatch",
  );
  assert.throws(
    () => resolveOssLogtoIssuer(undefined, issuer),
    (error) =>
      error instanceof EnvironmentError && error.code === "environment.issuer-mismatch",
  );
});

test("team setup initializes only through env:login and startup uses exact Infisical paths and flags without disclosure", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-team-test-"));
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  const environment = fakeEnvironment(temporaryDirectory);
  const { configFile, projectConfigDirectory } =
    resolveInfisicalConfigPaths(environment);

  for (let index = 0; index < 2; index += 1) {
    const result = await runNode(cli, ["login"], environment);
    assert.equal(result.code, 0, result.stderr);
  }

  for (let index = 0; index < 2; index += 1) {
    const result = await runNode(cli, ["start", "team"], environment);
    assert.equal(result.code, 0, result.stderr);
    const disclosed = `${result.stdout}\n${result.stderr}`;
    for (const canary of canaries) assert.equal(disclosed.includes(canary), false, canary);
  }

  const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
  assert.equal(recorded.filter((event) => event.action === "login").length, 1);
  assert.equal(recorded.filter((event) => event.action === "init").length, 1);
  assert.equal(await readFile(configFile, "utf8"), "{}\n");
  const runs = recorded.filter((event) => event.action === "run");
  assert.ok(runs.some((event) => event.path === "/devhud/api"));
  assert.ok(runs.some((event) => event.path === "/devhud/admin"));
  for (const event of runs) {
    const serialized = JSON.stringify(event);
    for (const flag of [
      "--env=dev",
      "--secret-overriding=false",
      "--expand=false",
      "--include-imports=false",
      "--log-level=warn",
      "--silent",
      "--telemetry=false",
      `--project-config-dir=${projectConfigDirectory}`,
    ]) {
      assert.ok(event.args.includes(flag), flag);
    }
    for (const canary of canaries) assert.equal(serialized.includes(canary), false, canary);
  }
  const turbo = recorded.filter(
    (event) => event.tool === "pnpm" && event.action === "turbo",
  );
  assert.deepEqual(turbo.map((event) => event.mode), ["team", "team"]);
  const firstAdminAssets = recorded.findIndex(
    (event) => event.tool === "pnpm" && event.action === "admin-assets",
  );
  const firstMigration = recorded.findIndex(
    (event) => event.tool === "go" && event.action === "migrate",
  );
  const firstTurbo = recorded.findIndex(
    (event) => event.tool === "pnpm" && event.action === "turbo",
  );
  assert.ok(
    firstAdminAssets !== -1 &&
      firstAdminAssets < firstMigration &&
      firstMigration < firstTurbo,
  );
});

test("team startup binds migration and Turbo services to the preflight issuer", async (t) => {
  const temporaryDirectory = await mkdtemp(
    resolve(tmpdir(), "devhud-team-configuration-pin-"),
  );
  const environment = {
    ...fakeEnvironment(temporaryDirectory),
    DEVHUD_TEST_RUN_TURBO_SERVICES: "1",
  };
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

  const loginResult = await runNode(cli, ["login"], environment);
  assert.equal(loginResult.code, 0, loginResult.stderr);
  const result = await runNode(cli, ["start", "team"], environment);
  assert.equal(result.code, 0, result.stderr);

  const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
  const order = recorded.map((event) => `${event.tool}:${event.action}`);
  assert.ok(order.indexOf("go:migrate") < order.indexOf("pnpm:turbo"), order.join(", "));
  assert.ok(order.indexOf("pnpm:turbo") < order.indexOf("go:serve"), order.join(", "));
  assert.ok(order.indexOf("go:serve") < order.indexOf("admin:serve"), order.join(", "));
  assert.ok(order.indexOf("admin:serve") < order.indexOf("frontend:serve"), order.join(", "));
  const { teamConfigurationPinFile } = resolveLocalStatePaths(environment);
  await assert.rejects(stat(teamConfigurationPinFile), { code: "ENOENT" });
  for (const canary of canaries) {
    assert.equal(`${result.stdout}${result.stderr}`.includes(canary), false, canary);
  }
});

test("team startup rejects issuer rotation before migration", async (t) => {
  const temporaryDirectory = await mkdtemp(
    resolve(tmpdir(), "devhud-team-migration-rotation-"),
  );
  const rotatedIssuer = "https://rotated-api-issuer-canary.example.test/oidc";
  const environment = {
    ...fakeEnvironment(temporaryDirectory),
    DEVHUD_TEST_API_LOGTO_ISSUER_AFTER_FIRST_RUN: rotatedIssuer,
  };
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

  assert.equal((await runNode(cli, ["login"], environment)).code, 0);
  const result = await runNode(cli, ["start", "team"], environment);
  assert.equal(result.code, 1);
  assert.match(
    result.stderr,
    /\[environment\.configuration-changed\].*DEVHUD_LOGTO_ISSUER/u,
  );
  assert.equal(`${result.stdout}${result.stderr}`.includes(rotatedIssuer), false);
  const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
  assert.equal(recorded.some((event) => event.tool === "go"), false);
  assert.equal(recorded.some((event) => event.tool === "pnpm"), false);
  const { teamConfigurationPinFile } = resolveLocalStatePaths(environment);
  await assert.rejects(stat(teamConfigurationPinFile), { code: "ENOENT" });
});

test("Turbo-owned services reject issuer rotation after migration", async (t) => {
  const temporaryDirectory = await mkdtemp(
    resolve(tmpdir(), "devhud-team-service-rotation-"),
  );
  const rotatedIssuer = "https://rotated-admin-issuer-canary.example.test/oidc";
  const environment = {
    ...fakeEnvironment(temporaryDirectory),
    DEVHUD_TEST_ADMIN_LOGTO_ISSUER_AFTER_FIRST_RUN: rotatedIssuer,
    DEVHUD_TEST_RUN_TURBO_SERVICES: "1",
  };
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

  assert.equal((await runNode(cli, ["login"], environment)).code, 0);
  const result = await runNode(cli, ["start", "team"], environment);
  assert.equal(result.code, 1);
  assert.match(
    result.stderr,
    /\[environment\.configuration-changed\].*DEVHUD_LOGTO_ISSUER/u,
  );
  assert.equal(`${result.stdout}${result.stderr}`.includes(rotatedIssuer), false);
  const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
  assert.ok(recorded.some((event) => event.tool === "go" && event.action === "migrate"));
  assert.ok(recorded.some((event) => event.tool === "go" && event.action === "serve"));
  assert.equal(recorded.some((event) => event.tool === "admin"), false);
  const { teamConfigurationPinFile } = resolveLocalStatePaths(environment);
  await assert.rejects(stat(teamConfigurationPinFile), { code: "ENOENT" });
});

test("team configuration pin is private, exclusive, and released after Turbo", async (t) => {
  const temporaryDirectory = await mkdtemp(
    resolve(tmpdir(), "devhud-team-pin-ownership-"),
  );
  const releaseFile = resolve(temporaryDirectory, "release-turbo");
  const firstEventLog = resolve(temporaryDirectory, "first-events.jsonl");
  const baseEnvironment = fakeEnvironment(temporaryDirectory);
  const firstEnvironment = {
    ...baseEnvironment,
    DEVHUD_TEST_BLOCK_PNPM: "1",
    DEVHUD_TEST_EVENT_LOG: firstEventLog,
    DEVHUD_TEST_PNPM_RELEASE_FILE: releaseFile,
  };
  assert.equal((await runNode(cli, ["login"], firstEnvironment)).code, 0);
  const first = spawnNode(cli, ["start", "team"], firstEnvironment);
  t.after(async () => {
    if (first.child.exitCode === null && first.child.signalCode === null) {
      await terminateTree(first.child, "SIGKILL").catch(() =>
        first.child.kill("SIGKILL"),
      );
    }
    await rm(temporaryDirectory, { recursive: true, force: true });
  });

  await waitForEvent(
    firstEventLog,
    (event) => event.tool === "pnpm" && event.action === "turbo-blocked",
  );
  const { stateDirectory, teamConfigurationPinFile } =
    resolveLocalStatePaths(baseEnvironment);
  const pin = await readFile(teamConfigurationPinFile, "utf8");
  assert.doesNotMatch(pin, /localhost|oidc|API_/u);
  if (process.platform !== "win32") {
    assert.equal((await stat(stateDirectory)).mode & 0o777, 0o700);
    assert.equal((await stat(teamConfigurationPinFile)).mode & 0o777, 0o600);
  }

  const second = await runNode(cli, ["start", "team"], {
    ...baseEnvironment,
    DEVHUD_TEST_EVENT_LOG: resolve(temporaryDirectory, "second-events.jsonl"),
  });
  assert.equal(second.code, 1);
  assert.match(second.stderr, /\[state\.team-startup-active\]/u);

  await writeFile(releaseFile, "release\n", "utf8");
  const firstResult = await first.completion;
  assert.equal(firstResult.code, 0, firstResult.stderr);
  await assert.rejects(stat(teamConfigurationPinFile), { code: "ENOENT" });
});

test("team tooling rejects suffixed or indirectly mentioned Infisical versions", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-infisical-version-"));
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

  for (const output of [
    "infisical version 0.43.116-beta.1\n",
    "infisical version 0.43.116+vendor.1\n",
    "infisical version 0.43.115\nUpdate available: 0.43.116\n",
  ]) {
    const result = await runNode(cli, ["login"], {
      ...fakeEnvironment(temporaryDirectory),
      DEVHUD_TEST_INFISICAL_VERSION_OUTPUT: output,
    });
    assert.equal(result.code, 1);
    assert.match(result.stderr, /\[tool\.version\].*0\.43\.116/u);
  }
});

test("team startup rejects missing project binding or authentication without interactive mutation", async (t) => {
  for (const scenario of ["project", "authentication"]) {
    const temporaryDirectory = await mkdtemp(
      resolve(tmpdir(), `devhud-team-readiness-${scenario}-`),
    );
    t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
    const environment = fakeEnvironment(temporaryDirectory);
    const { configFile, projectConfigDirectory } =
      resolveInfisicalConfigPaths(environment);
    if (scenario === "authentication") {
      await mkdir(projectConfigDirectory, { recursive: true });
      await writeFile(configFile, "{}\n", "utf8");
    }

    const result = await runNode(cli, ["start", "team"], environment);
    assert.equal(result.code, 1);
    assert.match(
      result.stderr,
      scenario === "project"
        ? /\[project\.uninitialized\].*pnpm env:login/u
        : /\[authentication\.required\].*pnpm env:login/u,
    );
    const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
    assert.equal(recorded.some((event) => ["login", "init"].includes(event.action)), false);
    assert.equal(recorded.some((event) => ["go", "pnpm", "docker"].includes(event.tool)), false);
    if (scenario === "project") {
      await assert.rejects(readFile(configFile, "utf8"), /ENOENT/u);
    }
  }
});

test("team authentication or path failure is fail-closed and does not invoke OSS", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-team-failure-"));
  const environment = {
    ...fakeEnvironment(temporaryDirectory),
    DEVHUD_TEST_INFISICAL_FAILURE: "/devhud/api",
  };
  const { configFile, projectConfigDirectory } =
    resolveInfisicalConfigPaths(environment);
  await mkdir(projectConfigDirectory, { recursive: true });
  await writeFile(configFile, "{}\n", "utf8");
  await writeFile(environment.DEVHUD_TEST_AUTH_STATE, "authenticated\n", "utf8");
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

  const result = await runNode(cli, ["start", "team"], environment);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /authentication or secret path is unavailable/u);
  assert.equal(`${result.stdout}${result.stderr}`.includes("INFISICAL_FAILURE_CANARY_MUST_NOT_LEAK"), false);
  assert.equal((await events(environment.DEVHUD_TEST_EVENT_LOG)).some((event) => event.tool === "docker"), false);
});

test("team startup and doctor reject mismatched API and administrator issuers", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-team-issuer-"));
  const apiIssuer = "https://api-auth-canary.example.test/oidc";
  const adminIssuer = "https://admin-auth-canary.example.test/oidc";
  const environment = {
    ...fakeEnvironment(temporaryDirectory),
    DEVHUD_TEST_API_LOGTO_ISSUER: apiIssuer,
    DEVHUD_TEST_ADMIN_LOGTO_ISSUER: adminIssuer,
  };
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

  const loginResult = await runNode(cli, ["login"], environment);
  assert.equal(loginResult.code, 0, loginResult.stderr);

  for (const args of [["start", "team"], ["doctor"]]) {
    const result = await runNode(cli, args, environment);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /\[environment\.issuer-mismatch\].*DEVHUD_LOGTO_ISSUER/u);
    const output = `${result.stdout}\n${result.stderr}`;
    assert.equal(output.includes(apiIssuer), false);
    assert.equal(output.includes(adminIssuer), false);
  }

  const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
  assert.equal(recorded.some((event) => event.tool === "go"), false);
  assert.equal(recorded.some((event) => event.tool === "pnpm"), false);
});

test("service wrapper reports name/category-only contract failures and suppresses values", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-contract-failure-"));
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  for (const [shape, expectedName] of [
    ["missing", "DEVHUD_DATABASE_URL"],
    ["unknown", "DEVHUD_UNKNOWN_CANARY"],
    ["partial", "DEVHUD_R2_ACCESS_KEY_ID"],
  ]) {
    const environment = {
      ...fakeEnvironment(temporaryDirectory),
      DEVHUD_LOCAL_MODE: "team",
      DEVHUD_TEST_SECRET_SHAPE: shape,
    };
    const result = await runNode(apiScript, ["validate"], environment);
    assert.equal(result.code, 1);
    assert.ok(result.stderr.includes(expectedName), result.stderr);
    assert.equal(result.stderr.includes("UNKNOWN_VALUE_CANARY"), false);
    assert.equal(result.stderr.includes("API_DATABASE_CANARY"), false);
  }
});

test("team provider classification waits for accepted and rejected pipe output", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-provider-pipes-"));
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  const environment = {
    ...fakeEnvironment(temporaryDirectory),
    DEVHUD_LOCAL_MODE: "team",
    DEVHUD_TEST_INFISICAL: lateProvider,
  };

  const accepted = await runNode(apiScript, ["validate"], environment);
  assert.equal(accepted.code, 0, accepted.stderr);
  assert.equal(accepted.stderr.includes("environment.unavailable"), false);
  assert.equal(accepted.stderr.includes("INFISICAL_WARNING_CANARY_MUST_NOT_LEAK"), false);

  const rejected = await runNode(apiScript, ["validate"], {
    ...environment,
    DEVHUD_TEST_LATE_PROVIDER_RESULT: "rejected",
  });
  assert.equal(rejected.code, 1);
  assert.match(rejected.stderr, /\[environment\.missing\].*DEVHUD_DATABASE_URL/u);
  assert.equal(rejected.stderr.includes("environment.unavailable"), false);
});

test("administrator preflight probes the configured localhost binding", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-admin-port-"));
  const server = createServer();
  await new Promise((resolveListen, reject) => {
    server.once("error", reject);
    server.listen(46306, "localhost", resolveListen);
  });
  t.after(async () => {
    await new Promise((resolveClose) => server.close(resolveClose));
    await rm(temporaryDirectory, { recursive: true, force: true });
  });

  const environment = fakeEnvironment(temporaryDirectory);
  const result = await runNode(cli, ["start", "team"], environment);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /DevHud administrator requires fixed port 46306/u);
  assert.deepEqual(await events(environment.DEVHUD_TEST_EVENT_LOG), []);
});

for (const [selector, remoteEndpoints] of [
  ["context", ["tcp://192.0.2.10:2376", "npipe:////server/pipe/docker_engine"]],
  ["host", ["ssh://developer@example.test", "npipe:////server/pipe/docker_engine"]],
]) {
  for (const remoteEndpoint of remoteEndpoints) {
    test(
      `OSS startup rejects a remote Docker ${selector} endpoint before Compose startup: ${remoteEndpoint}`,
      async (t) => {
        const temporaryDirectory = await mkdtemp(
          resolve(tmpdir(), `devhud-remote-docker-${selector}-`),
        );
        t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
        const environment = fakeEnvironment(temporaryDirectory);
        if (selector === "context") {
          environment.DOCKER_CONTEXT = "remote-review-context";
          environment.DOCKER_HOST = "unix:///tmp/ignored-local-docker.sock";
          environment.DEVHUD_TEST_DOCKER_ENDPOINT = remoteEndpoint;
        } else {
          delete environment.DOCKER_CONTEXT;
          environment.DOCKER_HOST = remoteEndpoint;
        }

        const result = await runNode(cli, ["start", "oss"], environment);
        assert.equal(result.code, 1);
        assert.match(result.stderr, /\[docker\.remote-daemon\]/u);
        const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
        assert.equal(recorded.some((event) => event.action === "up"), false);
        assert.equal(recorded.some((event) => event.tool === "go"), false);
        assert.equal(recorded.some((event) => event.tool === "pnpm"), false);
      },
    );
  }
}

test("OSS startup never invokes Infisical, orders health before migration and Turbo, and preserves volumes", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-oss-test-"));
  const environment = fakeEnvironment(temporaryDirectory);
  const localState = resolveLocalStatePaths(environment);
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  const result = await runNode(cli, ["start", "oss"], environment);
  assert.equal(result.code, 0, result.stderr);
  const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
  assert.equal(recorded.some((event) => event.tool === "infisical"), false);
  const order = recorded.map((event) => `${event.tool}:${event.action}`);
  assert.ok(order.indexOf("docker:up") < order.indexOf("go:migrate"), order.join(", "));
  assert.ok(order.indexOf("go:migrate") < order.indexOf("pnpm:turbo"), order.join(", "));
  assert.ok(order.indexOf("pnpm:turbo") < order.lastIndexOf("docker:down"), order.join(", "));
  const downEvent = recorded.findLast((event) => event.tool === "docker" && event.action === "down");
  assert.equal(downEvent.args.includes("--volumes"), false);
  assert.equal(downEvent.args.includes("-v"), false);
  const key = await stat(localState.identityKeyFile);
  if (process.platform !== "win32") assert.equal(key.mode & 0o777, 0o600);
  const output = `${result.stdout}\n${result.stderr}`;
  const identity = (await readFile(localState.identityKeyFile, "utf8")).trim();
  const dockerEvents = recorded.filter((event) => event.tool === "docker");
  assert.ok(dockerEvents.length > 0);
  for (const event of dockerEvents) {
    assert.equal(event.dockerHost, environment.DOCKER_HOST);
    assert.equal(event.dockerContext, environment.DOCKER_CONTEXT);
  }
  const composeEvents = recorded.filter(
    (event) => event.tool === "docker" && ["port", "up", "down"].includes(event.action),
  );
  const projectNames = composeEvents.map((event) => {
    const location = event.args.indexOf("--project-name");
    assert.notEqual(location, -1, JSON.stringify(event));
    return event.args[location + 1];
  });
  assert.deepEqual([...new Set(projectNames)], [composeProjectName(identity)]);
  assert.equal(JSON.stringify(recorded).includes(identity), false);
  assert.equal(output.includes(identity), false);
});

test("OSS startup is exclusive per checkout and releases ownership after cleanup", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-oss-lock-"));
  const releaseFile = resolve(temporaryDirectory, "release-turbo");
  const firstEventLog = resolve(temporaryDirectory, "first-events.jsonl");
  const baseEnvironment = fakeEnvironment(temporaryDirectory);
  const firstEnvironment = {
    ...baseEnvironment,
    DEVHUD_TEST_BLOCK_PNPM: "1",
    DEVHUD_TEST_EVENT_LOG: firstEventLog,
    DEVHUD_TEST_PNPM_RELEASE_FILE: releaseFile,
  };
  const first = spawnNode(cli, ["start", "oss"], firstEnvironment);
  t.after(async () => {
    if (first.child.exitCode === null && first.child.signalCode === null) {
      await terminateTree(first.child, "SIGKILL").catch(() =>
        first.child.kill("SIGKILL"),
      );
    }
    await rm(temporaryDirectory, { recursive: true, force: true });
  });

  await waitForEvent(
    firstEventLog,
    (event) => event.tool === "pnpm" && event.action === "turbo-blocked",
  );

  const secondEventLog = resolve(temporaryDirectory, "second-events.jsonl");
  const second = await runNode(cli, ["start", "oss"], {
    ...baseEnvironment,
    DEVHUD_TEST_EVENT_LOG: secondEventLog,
  });
  assert.equal(second.code, 1);
  assert.match(second.stderr, /\[state\.oss-startup-active\]/u);
  assert.deepEqual(await events(secondEventLog), []);

  await writeFile(releaseFile, "release\n", "utf8");
  const firstResult = await first.completion;
  assert.equal(firstResult.code, 0, firstResult.stderr);
  await assert.rejects(
    stat(resolve(baseEnvironment.DEVHUD_TEST_STATE_DIRECTORY, "oss-startup.lock")),
    { code: "ENOENT" },
  );

  const third = await runNode(cli, ["start", "oss"], {
    ...baseEnvironment,
    DEVHUD_TEST_EVENT_LOG: resolve(temporaryDirectory, "third-events.jsonl"),
  });
  assert.equal(third.code, 0, third.stderr);
});

test("partial OSS startup is reaped and down remains volume-preserving", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-oss-failure-"));
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  const environment = fakeEnvironment(temporaryDirectory);
  const failedEnvironment = {
    ...environment,
    DEVHUD_TEST_DOCKER_UP_FAILURE: "1",
  };
  const result = await runNode(cli, ["start", "oss"], failedEnvironment);
  assert.equal(result.code, 1);
  const recorded = await events(failedEnvironment.DEVHUD_TEST_EVENT_LOG);
  assert.ok(recorded.some((event) => event.action === "up"));
  assert.ok(recorded.some((event) => event.action === "down"));

  const retry = await runNode(cli, ["start", "oss"], {
    ...environment,
    DEVHUD_TEST_EVENT_LOG: resolve(temporaryDirectory, "retry-events.jsonl"),
  });
  assert.equal(retry.code, 0, retry.stderr);
});

test(
  "interrupted OSS steady state still runs lifecycle-tracked cleanup",
  {
    // child.kill cannot emulate an interactive Windows console control event,
    // so exercise the real root signal-handler path only on POSIX hosts.
    skip: process.platform === "win32",
  },
  async (t) => {
    const temporaryDirectory = await mkdtemp(
      resolve(tmpdir(), "devhud-oss-interrupt-"),
    );
    const environment = {
      ...fakeEnvironment(temporaryDirectory),
      DEVHUD_TEST_BLOCK_PNPM: "1",
    };
    t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

    const result = await interruptCliDuring(
      ["start", "oss"],
      environment,
      (event) => event.tool === "pnpm" && event.action === "turbo-blocked",
    );
    assert.equal(result.signal, "SIGTERM");
    const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
    assert.ok(
      recorded.some((event) => event.tool === "docker" && event.action === "down"),
    );
  },
);

test(
  "a signal during failed-start cleanup terminates the Docker process tree",
  { skip: process.platform === "win32" },
  async (t) => {
    const temporaryDirectory = await mkdtemp(
      resolve(tmpdir(), "devhud-cleanup-interrupt-"),
    );
    const environment = {
      ...fakeEnvironment(temporaryDirectory),
      DEVHUD_TEST_DOCKER_UP_FAILURE: "1",
      DEVHUD_TEST_BLOCK_DOCKER_DOWN: "1",
    };
    t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

    const result = await interruptCliDuring(
      ["start", "oss"],
      environment,
      (event) => event.tool === "docker" && event.action === "down-blocked",
    );
    assert.equal(result.signal, "SIGTERM");
  },
);

for (const port of [5432, 3001, 3002, 46305, 46306, 46307]) {
  test(`fixed port ${port} fails instead of remapping`, async (t) => {
    const server = createServer();
    await new Promise((resolveListen, reject) => {
      server.once("error", reject);
      server.listen(port, "127.0.0.1", resolveListen);
    });
    t.after(() => new Promise((resolveClose) => server.close(resolveClose)));
    await assert.rejects(() => assertFixedPort(port, "test service"), new RegExp(String(port), "u"));
  });
}

for (const stage of ["dependency startup", "migration", "steady state"]) {
  test(`interruption during ${stage} terminates the active argv-spawned process tree`, async () => {
    const lifecycle = new Lifecycle();
    const running = lifecycle.run(
      process.execPath,
      ["-e", "setInterval(() => {}, 1000)"],
      { stdio: "ignore" },
    );
    setTimeout(() => lifecycle.interrupt("SIGTERM"), 25);
    await assert.rejects(running, /interrupted by SIGTERM/u);
  });
}

test(
  "stalled POSIX lifecycle termination escalates and reaps the process group",
  { skip: process.platform === "win32", timeout: 20_000 },
  async (t) => {
    const lifecycle = new Lifecycle();
    const running = lifecycle.run(
      process.execPath,
      [
        "-e",
        'process.on("SIGTERM", () => {}); process.send("ready"); setInterval(() => {}, 1000)',
      ],
      { stdio: ["ignore", "ignore", "ignore", "ipc"] },
    );
    const child = lifecycle.child;
    t.after(() => {
      try {
        process.kill(-child.pid, "SIGKILL");
      } catch (error) {
        if (error.code !== "ESRCH") throw error;
      }
    });

    await once(child, "message");
    lifecycle.interrupt("SIGTERM");
    await assert.rejects(running, /interrupted by SIGTERM/u);
    assert.throws(() => process.kill(-child.pid, 0), { code: "ESRCH" });
  },
);

test(
  "POSIX lifecycle termination reaps descendants after the process group leader exits",
  { skip: process.platform === "win32", timeout: 10_000 },
  async (t) => {
    const lifecycle = new Lifecycle();
    const running = lifecycle.collect(process.execPath, [
      "-e",
      [
        'const { spawn } = require("node:child_process");',
        'const descendant = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: ["ignore", "inherit", "inherit"] });',
        "descendant.unref();",
      ].join(" "),
    ]);
    const leader = lifecycle.child;
    t.after(() => {
      try {
        process.kill(-leader.pid, "SIGKILL");
      } catch (error) {
        if (error.code !== "ESRCH") throw error;
      }
    });

    let settled = false;
    void running.then(
      () => {
        settled = true;
      },
      () => {
        settled = true;
      },
    );
    await once(leader, "exit");
    await Promise.resolve();
    assert.equal(leader.exitCode, 0);
    assert.equal(settled, false);

    lifecycle.interrupt("SIGTERM");
    await assert.rejects(running, /interrupted by SIGTERM/u);
  },
);

test("cleanup tracks its child after an earlier lifecycle interruption", async () => {
  const lifecycle = new Lifecycle();
  lifecycle.signal = "SIGTERM";
  const running = lifecycle.runCleanup(
    process.execPath,
    ["-e", "setInterval(() => {}, 1000)"],
    { stdio: "ignore" },
  );
  setTimeout(() => lifecycle.interrupt("SIGTERM"), 25);
  await assert.rejects(running, /interrupted by SIGTERM/u);
  assert.equal(lifecycle.signal, "SIGTERM");
});

for (const [name, args, blockingVariable, tool, blockedAction] of [
  [
    "team Infisical version preflight",
    ["start", "team"],
    "DEVHUD_TEST_BLOCK_INFISICAL_VERSION",
    "infisical",
    "version-blocked",
  ],
  [
    "OSS Docker version preflight",
    ["start", "oss"],
    "DEVHUD_TEST_BLOCK_DOCKER_VERSION",
    "docker",
    "version-blocked",
  ],
  [
    "standalone login Infisical preflight",
    ["login"],
    "DEVHUD_TEST_BLOCK_INFISICAL_VERSION",
    "infisical",
    "version-blocked",
  ],
  [
    "standalone Docker cleanup",
    ["down"],
    "DEVHUD_TEST_BLOCK_DOCKER_DOWN",
    "docker",
    "down-blocked",
  ],
]) {
  test(`interruption during ${name} terminates its process tree`, async (t) => {
    const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-preflight-signal-"));
    const environment = {
      ...fakeEnvironment(temporaryDirectory),
      [blockingVariable]: "1",
    };
    t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));

    const result = await interruptCliDuring(
      args,
      environment,
      (event) => event.tool === tool && event.action === blockedAction,
    );
    assert.equal(result.signal, "SIGTERM");
    const recorded = await events(environment.DEVHUD_TEST_EVENT_LOG);
    assert.equal(recorded.some((event) => event.tool === "go"), false);
    assert.equal(recorded.some((event) => event.tool === "pnpm"), false);
  });
}

test("repository policy is immutable, orchestration-only, and free of first-party Vercel setup", async () => {
  const compose = await readFile(composeFile, "utf8");
  const postgresInit = await readFile(
    resolve(repositoryRoot, "scripts/dev-environment/postgres-init.sql"),
    "utf8",
  );
  assert.match(compose, /postgres:15-bookworm@sha256:5d1d70e254e3c5d7d76847a9deebb18478cd518df37abf6b278d4bdb1fe5d96c/u);
  assert.match(compose, /svhd\/logto:1\.41\.0@sha256:7f79547e3d1fe569a3ecae757968a7cfc579687aa8164eec35113c0adc983c5b/u);
  assert.doesNotMatch(compose, /image:\s+[^\n]+:(?:latest|15-bookworm)\s*$/mu);
  assert.doesNotMatch(compose, /^name:/mu);
  assert.doesNotMatch(compose, /docker-entrypoint-initdb\.d/u);
  assert.match(
    compose,
    /logto-database-init:[\s\S]*condition: service_healthy[\s\S]*logto-init:[\s\S]*logto-database-init:[\s\S]*condition: service_completed_successfully/u,
  );
  assert.match(compose, /command: \["cli", "db", "seed", "--", "--swe"\]/u);
  assert.match(compose, /command: \["start"\]/u);
  assert.doesNotMatch(compose, /command: \["npm",/u);
  assert.match(postgresInit, /WHERE NOT EXISTS[\s\S]*\\gexec/u);

  const turbo = JSON.parse(await readFile(resolve(repositoryRoot, "turbo.json"), "utf8"));
  assert.deepEqual(turbo.tasks.dev.env, [
    "CARGO_HOME",
    "DEVHUD_LOCAL_MODE",
    "DISPLAY",
    "RUSTUP_HOME",
    "XAUTHORITY",
    "XDG_RUNTIME_DIR",
  ]);
  assert.ok(turbo.tasks.build.inputs.includes(".env"));
  assert.ok(turbo.tasks["build:api"].inputs.includes(".env"));
  for (const name of ["globalEnv", "globalPassThroughEnv", "passThroughEnv"]) {
    assert.equal(turbo[name], undefined);
    assert.equal(turbo.tasks.dev[name], undefined);
  }

  const rootPackage = JSON.parse(await readFile(resolve(repositoryRoot, "package.json"), "utf8"));
  for (const command of ["env:login", "env:doctor", "dev", "dev:oss", "dev:oss:down"]) {
    assert.equal(typeof rootPackage.scripts[command], "string");
  }
  assert.match(rootPackage.scripts.dev, /start team/u);
  assert.match(
    await readFile(
      resolve(repositoryRoot, "apps/devhud-admin/scripts/development.mjs"),
      "utf8",
    ),
    /--no-env/u,
  );
  assert.match(
    await readFile(resolve(repositoryRoot, "apps/devhud/scripts/development.mjs"), "utf8"),
    /devhudFrontendContract/u,
  );
  const issuerPattern = /^DEVHUD_LOGTO_ISSUER=(.+)$/mu;
  const adminExample = await readFile(
    resolve(repositoryRoot, "apps/devhud-admin/.env.example"),
    "utf8",
  );
  const apiExample = await readFile(
    resolve(repositoryRoot, "servers/devhud-api/.env.example"),
    "utf8",
  );
  assert.match(adminExample, issuerPattern);
  assert.match(apiExample, issuerPattern);
  assert.equal(adminExample.match(issuerPattern)?.[1], apiExample.match(issuerPattern)?.[1]);

  const ignore = await readFile(resolve(repositoryRoot, ".gitignore"), "utf8");
  const prepare = await readFile(resolve(repositoryRoot, "scripts/prepare-apps.sh"), "utf8");
  assert.doesNotMatch(ignore, /^\.vercel\/?$/mu);
  assert.match(ignore, /^\.env\.\*$/mu);
  assert.match(ignore, /^!\.env\.example$/mu);
  assert.doesNotMatch(prepare, /VERCEL/u);
  assert.match(prepare, /CI/u);
  assert.match(prepare, /prepare:app/u);
  await assert.rejects(readFile(resolve(repositoryRoot, "scripts/setup/env-vars.sh"), "utf8"), /ENOENT/u);
  await stat(resolve(repositoryRoot, "apps/mpapp/.env.example"));
  await stat(resolve(repositoryRoot, ".agents/skills"));
});

test("generated identity material is stable, private, and never logged", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-identity-test-"));
  const environment = fakeEnvironment(temporaryDirectory);
  const localState = resolveLocalStatePaths(environment);
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  assert.throws(
    () => resolveLocalStatePaths({ DEVHUD_ENVIRONMENT_TESTING: "1" }),
    /absolute temporary state directory/u,
  );
  const created = await Promise.all(
    Array.from({ length: 8 }, () => createLocalIdentityKey(environment)),
  );
  const first = await readFile(localState.identityKeyFile, "utf8");
  assert.deepEqual([...new Set(created)], [first.trim()]);
  assert.equal(Buffer.from(first.trim(), "base64").byteLength, 32);
  assert.deepEqual(await readdir(localState.stateDirectory), ["identity-hmac-key"]);
  assert.equal(await createLocalIdentityKey(environment), first.trim());
  assert.equal(await readFile(localState.identityKeyFile, "utf8"), first);
  if (process.platform !== "win32") {
    assert.equal((await stat(localState.identityKeyFile)).mode & 0o777, 0o600);
  }
});

test("incomplete generated identity material fails closed", async (t) => {
  const temporaryDirectory = await mkdtemp(resolve(tmpdir(), "devhud-identity-invalid-"));
  const environment = fakeEnvironment(temporaryDirectory);
  const localState = resolveLocalStatePaths(environment);
  t.after(() => rm(temporaryDirectory, { recursive: true, force: true }));
  await mkdir(localState.stateDirectory, { recursive: true });
  await writeFile(localState.identityKeyFile, "", "utf8");

  await assert.rejects(
    createLocalIdentityKey(environment),
    /\[state\.identity-invalid\].*incomplete or invalid/u,
  );
  assert.deepEqual(await readdir(localState.stateDirectory), ["identity-hmac-key"]);
});

test("root documentation development commands bypass the DevHud team environment", async () => {
  const rootPackage = JSON.parse(
    await readFile(resolve(repositoryRoot, "package.json"), "utf8"),
  );
  const commands = [
    {
      name: "dev:public-docs",
      value: "pnpm --filter public-docs dev",
      contracts: [
        "docs/project-public-docs.md",
        "docs/apps-public-docs-foundation.md",
      ],
    },
    {
      name: "dev:nodeup-docs",
      value: "pnpm --filter nodeup-docs dev",
      contracts: [
        "docs/project-nodeup.md",
        "docs/apps-nodeup-docs-foundation.md",
      ],
    },
    {
      name: "dev:binpm-docs",
      value: "pnpm --filter binpm-docs dev",
      contracts: [
        "docs/project-binpm.md",
        "docs/apps-binpm-docs-foundation.md",
      ],
    },
  ];
  for (const command of commands) {
    assert.equal(rootPackage.scripts[command.name], command.value, command.name);
    for (const contract of command.contracts) {
      const contents = await readFile(resolve(repositoryRoot, contract), "utf8");
      assert.ok(contents.includes(`pnpm ${command.name}`), contract);
    }
  }
});

test("environment source of truth, catalog, domain contracts, READMEs, and AGENTS stay synchronized", async () => {
  const requiredConsumers = [
    "docs/README.md",
    "docs/project-devhud.md",
    "docs/apps-devhud-foundation.md",
    "docs/apps-devhud-admin-contract.md",
    "docs/servers-devhud-api-contract.md",
    "apps/devhud-admin/README.md",
    "servers/devhud-api/README.md",
    "AGENTS.md",
    "apps/AGENTS.md",
    "servers/AGENTS.md",
  ];
  for (const relativePath of requiredConsumers) {
    const contents = await readFile(resolve(repositoryRoot, relativePath), "utf8");
    assert.match(contents, /repository-environment-contract\.md/u, relativePath);
  }

  const contract = await readFile(
    resolve(repositoryRoot, "docs/repository-environment-contract.md"),
    "utf8",
  );
  for (const command of [
    "pnpm env:login",
    "pnpm env:doctor",
    "pnpm dev",
    "pnpm dev:oss",
    "pnpm dev:oss:down",
  ]) {
    assert.ok(contract.includes(command), command);
  }
  for (const classification of [
    "Committed configuration",
    "Team development secrets",
    "Generated local material",
    "User-owned credentials",
    "Production, deployment, release, and signing secrets",
  ]) {
    assert.ok(contract.includes(classification), classification);
  }

  // `/devhud/admin` is also the stable public administration route, so its bare
  // value cannot distinguish a public link from the internal team secret path.
  const forbiddenPublicEnvironmentDetail = /\/devhud\/api|Infisical/u;
  for (const publicRoot of ["apps/public-docs", "apps/binpm-docs", "apps/nodeup-docs"]) {
    const entries = await readdir(resolve(repositoryRoot, publicRoot), {
      recursive: true,
      withFileTypes: true,
    });
    for (const entry of entries) {
      if (
        !entry.isFile() ||
        entry.parentPath.includes("doc_build") ||
        !/\.(?:css|html|js|json|md|mdx|mjs|ts|tsx)$/u.test(entry.name)
      ) {
        continue;
      }
      const publicText = await readFile(resolve(entry.parentPath, entry.name), "utf8").catch(
        () => "",
      );
      assert.doesNotMatch(
        publicText,
        forbiddenPublicEnvironmentDetail,
        `${publicRoot}/${entry.name}`,
      );
    }
  }
});
