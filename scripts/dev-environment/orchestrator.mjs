import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { access, chmod, mkdir, open, readFile } from "node:fs/promises";
import { constants as fsConstants } from "node:fs";
import { createHash, randomBytes } from "node:crypto";
import { resolve } from "node:path";
import {
  composeFile,
  infisicalConfig,
  repositoryRoot,
  resolveLocalStatePaths,
  supportedInfisicalVersion,
} from "./contracts.mjs";
import {
  collect,
  commandName,
  dockerClientEnvironment,
  inherited,
  safeBaseEnvironment,
  terminateTree,
} from "./process.mjs";

const appPorts = Object.freeze([
  [46305, "DevHud frontend", "127.0.0.1"],
  [46306, "DevHud administrator", "localhost"],
  [46307, "DevHud API", "127.0.0.1"],
]);
const dependencyPorts = Object.freeze([
  [5432, "PostgreSQL", "postgres", 5432],
  [3001, "Logto core", "logto", 3001],
  [3002, "Logto console", "logto", 3002],
]);
const serviceScripts = Object.freeze({
  api: resolve(repositoryRoot, "servers/devhud-api/scripts/local.mjs"),
  admin: resolve(repositoryRoot, "apps/devhud-admin/scripts/development.mjs"),
});

class Interrupted extends Error {
  constructor(signal) {
    super(`development startup interrupted by ${signal}`);
    this.signal = signal;
  }
}

export class Lifecycle {
  constructor() {
    this.child = null;
    this.signal = null;
    this.terminationPromise = null;
    this.handlers = new Map();
  }

  install() {
    for (const signal of ["SIGINT", "SIGTERM"]) {
      const handler = () => this.interrupt(signal);
      this.handlers.set(signal, handler);
      process.on(signal, handler);
    }
  }

  interrupt(signal) {
    if (this.signal) return;
    this.signal = signal;
    this.terminationPromise = terminateTree(this.child, signal);
    void this.terminationPromise.catch(() => {});
  }

  remove() {
    for (const [signal, handler] of this.handlers) process.off(signal, handler);
    this.handlers.clear();
  }

  async run(command, args, options = {}) {
    if (this.signal) throw new Interrupted(this.signal);
    const child = spawn(command, args, {
      shell: false,
      stdio: options.stdio ?? "inherit",
      ...options,
      detached: process.platform !== "win32",
    });
    this.child = child;
    const result = await new Promise((resolveResult, reject) => {
      child.once("error", reject);
      child.once("exit", (code, signal) => resolveResult({ code, signal }));
    });
    this.child = null;
    if (this.terminationPromise) {
      await this.terminationPromise;
      this.terminationPromise = null;
    }
    if (this.signal) throw new Interrupted(this.signal);
    return result;
  }
}

function testingOverrides(source = process.env) {
  if (source.DEVHUD_ENVIRONMENT_TESTING !== "1") return {};
  return Object.fromEntries(
    Object.entries(source).filter(
      ([name]) => name === "DEVHUD_ENVIRONMENT_TESTING" || name.startsWith("DEVHUD_TEST_"),
    ),
  );
}

function rootChildEnvironment(mode) {
  return {
    ...safeBaseEnvironment(),
    ...testingOverrides(),
    DEVHUD_LOCAL_MODE: mode,
  };
}

function dockerChildEnvironment() {
  return {
    ...rootChildEnvironment("oss"),
    ...dockerClientEnvironment(),
  };
}

async function exists(path) {
  try {
    await access(path, fsConstants.F_OK);
    return true;
  } catch (error) {
    if (error.code === "ENOENT") return false;
    throw error;
  }
}

export function portIsAvailable(port, host = "127.0.0.1") {
  return new Promise((resolvePort, reject) => {
    const server = createServer();
    server.unref();
    server.once("error", (error) => {
      if (["EADDRINUSE", "EACCES"].includes(error.code)) resolvePort(false);
      else reject(error);
    });
    server.listen({ host, port, exclusive: true }, () => {
      server.close((error) => (error ? reject(error) : resolvePort(true)));
    });
  });
}

export async function assertFixedPort(port, owner, host = "127.0.0.1") {
  if (!(await portIsAvailable(port, host))) {
    throw new Error(
      `[port.conflict] ${owner} requires fixed port ${port}; stop the process using that port and retry`,
    );
  }
}

function toolInvocation(name, overrideName) {
  const fake =
    process.env.DEVHUD_ENVIRONMENT_TESTING === "1" ? process.env[overrideName] : null;
  return fake
    ? { command: process.execPath, prefix: [fake] }
    : { command: name, prefix: [] };
}

function dockerInvocation() {
  return toolInvocation("docker", "DEVHUD_TEST_DOCKER");
}

function infisicalInvocation() {
  return toolInvocation("infisical", "DEVHUD_TEST_INFISICAL");
}

function composeArgs(projectName, ...args) {
  return ["compose", "--project-name", projectName, "--file", composeFile, ...args];
}

async function assertAppPorts() {
  for (const [port, owner, host] of appPorts) {
    await assertFixedPort(port, owner, host);
  }
}

async function dependencyOwnedByCompose(projectName, service, containerPort, hostPort) {
  const docker = dockerInvocation();
  const result = await collect(
    docker.command,
    [
      ...docker.prefix,
      ...composeArgs(projectName, "port", service, String(containerPort)),
    ],
    { cwd: repositoryRoot, env: dockerChildEnvironment() },
  );
  if (result.code !== 0) return false;
  return result.stdout
    .trim()
    .split(/\r?\n/u)
    .some((line) => line === `127.0.0.1:${hostPort}`);
}

async function assertDependencyPorts(projectName) {
  for (const [port, owner, service, containerPort] of dependencyPorts) {
    if (
      !(await portIsAvailable(port)) &&
      !(await dependencyOwnedByCompose(projectName, service, containerPort, port))
    ) {
      throw new Error(
        `[port.conflict] ${owner} requires fixed port ${port}; stop the conflicting process and retry`,
      );
    }
  }
}

async function exactInfisicalVersion() {
  const infisical = infisicalInvocation();
  let result;
  try {
    result = await collect(infisical.command, [...infisical.prefix, "--version"], {
      cwd: repositoryRoot,
      env: rootChildEnvironment("team"),
    });
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    throw new Error(
      `[tool.missing] Infisical CLI ${supportedInfisicalVersion} is required and is not installed automatically`,
    );
  }
  if (result.code !== 0) {
    throw new Error(
      `[tool.missing] Infisical CLI ${supportedInfisicalVersion} is required and is not installed automatically`,
    );
  }
  const versions = result.stdout.match(/\d+\.\d+\.\d+/gu) ?? [];
  if (!versions.includes(supportedInfisicalVersion)) {
    throw new Error(
      `[tool.version] Infisical CLI ${supportedInfisicalVersion} is required; install that exact supported version`,
    );
  }
}

async function authenticated() {
  const infisical = infisicalInvocation();
  const result = await collect(
    infisical.command,
    [
      ...infisical.prefix,
      "--log-level=error",
      "--silent",
      "--telemetry=false",
      "user",
      "get",
      "token",
      "--plain",
    ],
    { cwd: repositoryRoot, env: rootChildEnvironment("team") },
  );
  // stdout contains a credential by design and must always be discarded.
  return result.code === 0;
}

async function runInteractiveInfisical(args) {
  const infisical = infisicalInvocation();
  const result = await inherited(infisical.command, [...infisical.prefix, ...args], {
    cwd: repositoryRoot,
    env: rootChildEnvironment("team"),
  });
  if (result.code !== 0 || result.signal) {
    throw new Error("[authentication.failed] Infisical authentication or local project initialization did not complete");
  }
}

export async function login() {
  await exactInfisicalVersion();
  if (!(await authenticated())) await runInteractiveInfisical(["--telemetry=false", "login"]);
  if (!(await exists(infisicalConfig))) {
    await runInteractiveInfisical(["--telemetry=false", "init"]);
  }
  process.stdout.write("Team environment authentication and local project configuration are ready.\n");
}

async function runService(lifecycle, mode, service, action) {
  const result = await lifecycle.run(
    process.execPath,
    [serviceScripts[service], action],
    { cwd: repositoryRoot, env: rootChildEnvironment(mode) },
  );
  if (result.code !== 0 || result.signal) {
    throw new Error(`[service.${action}] ${service} ${action} failed; see the name/category diagnostic above`);
  }
}

async function checkDocker() {
  const docker = dockerInvocation();
  let result;
  try {
    result = await collect(docker.command, [...docker.prefix, "compose", "version"], {
      cwd: repositoryRoot,
      env: dockerChildEnvironment(),
    });
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    throw new Error("[tool.missing] Docker with Compose support is required for pnpm dev:oss");
  }
  if (result.code !== 0) {
    throw new Error("[tool.missing] Docker with Compose support is required for pnpm dev:oss");
  }
}

export async function createLocalIdentityKey(source = process.env) {
  const { identityKeyFile, stateDirectory } = resolveLocalStatePaths(source);
  await mkdir(stateDirectory, { recursive: true, mode: 0o700 });
  let handle;
  try {
    handle = await open(identityKeyFile, "wx", 0o600);
    await handle.writeFile(`${randomBytes(32).toString("base64")}\n`, { encoding: "utf8" });
  } catch (error) {
    if (error.code !== "EEXIST") throw error;
  } finally {
    await handle?.close();
  }
  if (process.platform !== "win32") {
    await chmod(stateDirectory, 0o700);
    await chmod(identityKeyFile, 0o600);
  }
}

export function composeProjectName(identityKey) {
  const digest = createHash("sha256")
    .update(identityKey.trim(), "utf8")
    .digest("hex")
    .slice(0, 24);
  return `delino-devhud-development-${digest}`;
}

async function localComposeProjectName() {
  const { identityKeyFile } = resolveLocalStatePaths();
  await createLocalIdentityKey();
  return composeProjectName(await readFile(identityKeyFile, "utf8"));
}

export async function down({ quiet = false, projectName } = {}) {
  const resolvedProjectName = projectName ?? (await localComposeProjectName());
  const docker = dockerInvocation();
  let result;
  try {
    result = await inherited(
      docker.command,
      [
        ...docker.prefix,
        ...composeArgs(resolvedProjectName, "down", "--remove-orphans"),
      ],
      {
        cwd: repositoryRoot,
        env: dockerChildEnvironment(),
        stdio: quiet ? "ignore" : "inherit",
      },
    );
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    throw new Error("[tool.missing] Docker with Compose support is required for OSS cleanup");
  }
  if (result.code !== 0 || result.signal) {
    throw new Error("[dependencies.down] repository-owned OSS dependencies could not be stopped");
  }
  if (!quiet) process.stdout.write("OSS dependencies stopped; persistent volumes were preserved.\n");
}

async function startTurbo(lifecycle, mode) {
  const pnpm = toolInvocation(commandName("pnpm"), "DEVHUD_TEST_PNPM");
  return lifecycle.run(
    pnpm.command,
    [
      ...pnpm.prefix,
      "exec",
      "turbo",
      "run",
      "dev",
      "--filter=devhud",
      "--filter=devhud-admin",
      "--filter=@delinoio/devhud-api",
    ],
    { cwd: repositoryRoot, env: rootChildEnvironment(mode) },
  );
}

async function startTeam(lifecycle) {
  await assertAppPorts();
  await login();
  await runService(lifecycle, "team", "api", "validate");
  await runService(lifecycle, "team", "admin", "validate");
  await runService(lifecycle, "team", "api", "migrate");
  return startTurbo(lifecycle, "team");
}

async function startOss(lifecycle) {
  await checkDocker();
  await assertAppPorts();
  const projectName = await localComposeProjectName();
  await assertDependencyPorts(projectName);
  await runService(lifecycle, "oss", "api", "validate");
  await runService(lifecycle, "oss", "admin", "validate");
  let dependenciesStarted = false;
  try {
    const docker = dockerInvocation();
    const up = await lifecycle.run(
      docker.command,
      [
        ...docker.prefix,
        ...composeArgs(
          projectName,
          "up",
          "--detach",
          "--wait",
          "--wait-timeout",
          "120",
          "postgres",
          "logto",
        ),
      ],
      { cwd: repositoryRoot, env: dockerChildEnvironment() },
    );
    dependenciesStarted = true;
    if (up.code !== 0 || up.signal) {
      throw new Error("[dependencies.start] OSS dependencies did not become healthy");
    }
    await runService(lifecycle, "oss", "api", "migrate");
    return await startTurbo(lifecycle, "oss");
  } finally {
    if (dependenciesStarted || lifecycle.signal) {
      await down({ quiet: Boolean(lifecycle.signal), projectName });
    }
  }
}

export async function start(mode) {
  if (!["team", "oss"].includes(mode)) {
    throw new Error("[mode.invalid] development mode must be exactly one of: team, oss");
  }
  const lifecycle = new Lifecycle();
  lifecycle.install();
  try {
    return mode === "team" ? await startTeam(lifecycle) : await startOss(lifecycle);
  } catch (error) {
    if (error instanceof Interrupted) return { code: null, signal: error.signal };
    throw error;
  } finally {
    lifecycle.remove();
  }
}

export async function doctor() {
  await exactInfisicalVersion();
  if (!(await exists(infisicalConfig))) {
    throw new Error("[project.uninitialized] local Infisical project configuration is missing; run pnpm env:login");
  }
  if (!(await authenticated())) {
    throw new Error("[authentication.required] Infisical authentication is required; run pnpm env:login");
  }
  const lifecycle = new Lifecycle();
  lifecycle.install();
  try {
    await runService(lifecycle, "team", "api", "validate");
    await runService(lifecycle, "team", "admin", "validate");
  } finally {
    lifecycle.remove();
  }
  process.stdout.write("Team development environment is healthy.\n");
}
