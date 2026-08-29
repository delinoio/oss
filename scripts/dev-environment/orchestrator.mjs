import { Buffer } from "node:buffer";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import {
  access,
  chmod,
  link,
  mkdir,
  open,
  readFile,
  unlink,
} from "node:fs/promises";
import { constants as fsConstants } from "node:fs";
import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import { resolve } from "node:path";
import {
  comparisonKeyName,
  comparisonMarker,
  composeFile,
  repositoryRoot,
  resolveInfisicalConfigPaths,
  resolveLocalStatePaths,
  supportedInfisicalVersion,
} from "./contracts.mjs";
import {
  collect,
  closeResult,
  commandInvocation,
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

  async #execute(command, args, options, captureOutput) {
    if (this.signal) throw new Interrupted(this.signal);
    const { stdin = "ignore", stdio, ...spawnOptions } = options;
    const child = spawn(command, args, {
      shell: false,
      ...spawnOptions,
      stdio: captureOutput ? [stdin, "pipe", "pipe"] : (stdio ?? "inherit"),
      detached: process.platform !== "win32",
    });
    this.child = child;
    const stdout = [];
    const stderr = [];
    if (captureOutput) {
      child.stdout.on("data", (chunk) => stdout.push(chunk));
      child.stderr.on("data", (chunk) => stderr.push(chunk));
    }
    let result;
    try {
      result = await closeResult(child);
    } finally {
      if (this.child === child) this.child = null;
      if (this.terminationPromise) {
        await this.terminationPromise;
        this.terminationPromise = null;
      }
    }
    if (this.signal) throw new Interrupted(this.signal);
    return captureOutput
      ? {
          ...result,
          stdout: Buffer.concat(stdout).toString("utf8"),
          stderr: Buffer.concat(stderr).toString("utf8"),
        }
      : result;
  }

  async run(command, args, options = {}) {
    return this.#execute(command, args, options, false);
  }

  async runCleanup(command, args, options = {}) {
    const interruptedSignal = this.signal;
    this.signal = null;
    try {
      return await this.#execute(command, args, options, false);
    } finally {
      this.signal ??= interruptedSignal;
    }
  }

  async collect(command, args, options = {}) {
    return this.#execute(command, args, options, true);
  }
}

function collectCommand(lifecycle, command, args, options) {
  return lifecycle
    ? lifecycle.collect(command, args, options)
    : collect(command, args, options);
}

function inheritCommand(lifecycle, command, args, options) {
  return lifecycle
    ? lifecycle.run(command, args, options)
    : inherited(command, args, options);
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
    : commandInvocation(name);
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

async function dependencyOwnedByCompose(
  lifecycle,
  projectName,
  service,
  containerPort,
  hostPort,
) {
  const docker = dockerInvocation();
  const result = await lifecycle.collect(
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

async function assertDependencyPorts(lifecycle, projectName) {
  for (const [port, owner, service, containerPort] of dependencyPorts) {
    if (
      !(await portIsAvailable(port)) &&
      !(await dependencyOwnedByCompose(
        lifecycle,
        projectName,
        service,
        containerPort,
        port,
      ))
    ) {
      throw new Error(
        `[port.conflict] ${owner} requires fixed port ${port}; stop the conflicting process and retry`,
      );
    }
  }
}

async function exactInfisicalVersion(lifecycle) {
  const infisical = infisicalInvocation();
  let result;
  try {
    result = await collectCommand(
      lifecycle,
      infisical.command,
      [...infisical.prefix, "--version"],
      {
        cwd: repositoryRoot,
        env: rootChildEnvironment("team"),
      },
    );
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

async function authenticated(lifecycle) {
  const infisical = infisicalInvocation();
  const result = await collectCommand(
    lifecycle,
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

async function requireTeamEnvironment(lifecycle) {
  await exactInfisicalVersion(lifecycle);
  const { configFile } = resolveInfisicalConfigPaths();
  if (!(await exists(configFile))) {
    throw new Error("[project.uninitialized] local Infisical project configuration is missing; run pnpm env:login");
  }
  if (!(await authenticated(lifecycle))) {
    throw new Error("[authentication.required] Infisical authentication is required; run pnpm env:login");
  }
}

async function runInteractiveInfisical(args, cwd = repositoryRoot, lifecycle) {
  const infisical = infisicalInvocation();
  const result = await inheritCommand(
    lifecycle,
    infisical.command,
    [...infisical.prefix, ...args],
    {
      cwd,
      env: rootChildEnvironment("team"),
    },
  );
  if (result.code !== 0 || result.signal) {
    throw new Error("[authentication.failed] Infisical authentication or local project initialization did not complete");
  }
}

export async function login(lifecycle) {
  await exactInfisicalVersion(lifecycle);
  if (!(await authenticated(lifecycle))) {
    await runInteractiveInfisical(
      ["--telemetry=false", "login"],
      repositoryRoot,
      lifecycle,
    );
  }
  const { configFile, projectConfigDirectory } = resolveInfisicalConfigPaths();
  if (!(await exists(configFile))) {
    await mkdir(projectConfigDirectory, { recursive: true });
    await runInteractiveInfisical(
      ["--telemetry=false", "init"],
      projectConfigDirectory,
      lifecycle,
    );
  }
  process.stdout.write("Team environment authentication and local project configuration are ready.\n");
}

function extractComparisons(stdout) {
  const comparisons = [];
  const forwarded = [];
  for (const line of stdout.match(/[^\n]*\n|[^\n]+$/gu) ?? []) {
    const normalized = line.replace(/\r?\n$/u, "");
    if (normalized.startsWith(comparisonMarker)) {
      comparisons.push(normalized.slice(comparisonMarker.length));
    } else {
      forwarded.push(line);
    }
  }
  return { comparisons, forwarded: forwarded.join("") };
}

async function runService(lifecycle, mode, service, action, comparisonKey) {
  const options = {
    cwd: repositoryRoot,
    env: {
      ...rootChildEnvironment(mode),
      ...(comparisonKey ? { [comparisonKeyName]: comparisonKey } : {}),
    },
  };
  const result = comparisonKey
    ? await lifecycle.collect(
        process.execPath,
        [serviceScripts[service], action],
        options,
      )
    : await lifecycle.run(
        process.execPath,
        [serviceScripts[service], action],
        options,
      );
  let comparisons = [];
  if (comparisonKey) {
    const output = extractComparisons(result.stdout);
    comparisons = output.comparisons;
    if (output.forwarded) process.stdout.write(output.forwarded);
    if (result.stderr) process.stderr.write(result.stderr);
  }
  if (result.code !== 0 || result.signal) {
    throw new Error(`[service.${action}] ${service} ${action} failed; see the name/category diagnostic above`);
  }
  if (comparisonKey) {
    if (
      comparisons.length !== 1 ||
      !/^[A-Za-z0-9_-]{43}$/u.test(comparisons[0])
    ) {
      throw new Error(
        `[environment.comparison] ${service} did not produce one valid configuration comparison`,
      );
    }
    return comparisons[0];
  }
  return null;
}

async function validateTeamServices(lifecycle) {
  const comparisonKey = randomBytes(32).toString("base64url");
  const apiComparison = await runService(
    lifecycle,
    "team",
    "api",
    "validate",
    comparisonKey,
  );
  const adminComparison = await runService(
    lifecycle,
    "team",
    "admin",
    "validate",
    comparisonKey,
  );
  const apiDigest = Buffer.from(apiComparison, "base64url");
  const adminDigest = Buffer.from(adminComparison, "base64url");
  if (
    apiDigest.byteLength !== adminDigest.byteLength ||
    !timingSafeEqual(apiDigest, adminDigest)
  ) {
    throw new Error(
      "[environment.issuer-mismatch] DevHud API and administrator team configuration must use the same DEVHUD_LOGTO_ISSUER",
    );
  }
}

async function checkDocker(lifecycle) {
  const docker = dockerInvocation();
  let result;
  try {
    result = await lifecycle.collect(
      docker.command,
      [...docker.prefix, "compose", "version"],
      {
        cwd: repositoryRoot,
        env: dockerChildEnvironment(),
      },
    );
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    throw new Error("[tool.missing] Docker with Compose support is required for pnpm dev:oss");
  }
  if (result.code !== 0) {
    throw new Error("[tool.missing] Docker with Compose support is required for pnpm dev:oss");
  }
  await assertLocalDockerDaemon(lifecycle, docker);
}

export function dockerEndpointIsLocal(endpoint) {
  if (typeof endpoint !== "string" || endpoint.trim() === "") return false;
  let parsed;
  try {
    parsed = new URL(endpoint.trim());
  } catch {
    return false;
  }
  if (["unix:", "npipe:"].includes(parsed.protocol)) return true;
  return (
    parsed.protocol === "tcp:" &&
    ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname)
  );
}

async function inspectDockerEndpoint(lifecycle, docker, context) {
  const result = await lifecycle.collect(
    docker.command,
    [
      ...docker.prefix,
      "context",
      "inspect",
      ...(context ? [context] : []),
      "--format",
      '{{ (index .Endpoints "docker").Host }}',
    ],
    { cwd: repositoryRoot, env: dockerChildEnvironment() },
  );
  const endpoint = result.stdout.trim();
  if (result.code !== 0 || result.signal || endpoint === "") {
    throw new Error(
      "[docker.context] selected Docker context endpoint could not be inspected",
    );
  }
  return endpoint;
}

async function assertLocalDockerDaemon(lifecycle, docker) {
  const selectors = dockerClientEnvironment();
  const selectedContext = selectors.DOCKER_CONTEXT?.trim();
  const selectedHost = selectors.DOCKER_HOST?.trim();
  const endpoint = selectedContext
    ? await inspectDockerEndpoint(lifecycle, docker, selectedContext)
    : selectedHost || (await inspectDockerEndpoint(lifecycle, docker));
  if (!dockerEndpointIsLocal(endpoint)) {
    throw new Error(
      "[docker.remote-daemon] pnpm dev:oss requires a local Docker socket, named pipe, or loopback TCP endpoint",
    );
  }
}

function validateLocalIdentityKey(identityKey) {
  const normalized = identityKey.trim();
  const decoded = Buffer.from(normalized, "base64");
  if (
    decoded.byteLength !== 32 ||
    decoded.toString("base64") !== normalized
  ) {
    throw new Error(
      "[state.identity-invalid] generated checkout identity is incomplete or invalid; stop OSS development processes and remove only the ignored identity key before retrying",
    );
  }
  return normalized;
}

export async function createLocalIdentityKey(source = process.env) {
  const { identityKeyFile, stateDirectory } = resolveLocalStatePaths(source);
  await mkdir(stateDirectory, { recursive: true, mode: 0o700 });
  const temporaryKeyFile = resolve(
    stateDirectory,
    `.identity-hmac-key-${process.pid}-${randomBytes(8).toString("hex")}.tmp`,
  );
  let handle;
  try {
    handle = await open(temporaryKeyFile, "wx", 0o600);
    await handle.writeFile(`${randomBytes(32).toString("base64")}\n`, { encoding: "utf8" });
    await handle.sync();
    await handle.close();
    handle = null;
    try {
      await link(temporaryKeyFile, identityKeyFile);
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
    }
  } finally {
    await handle?.close();
    try {
      await unlink(temporaryKeyFile);
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  }
  if (process.platform !== "win32") {
    await chmod(stateDirectory, 0o700);
    await chmod(identityKeyFile, 0o600);
  }
  return validateLocalIdentityKey(await readFile(identityKeyFile, "utf8"));
}

export function composeProjectName(identityKey) {
  const digest = createHash("sha256")
    .update(identityKey.trim(), "utf8")
    .digest("hex")
    .slice(0, 24);
  return `delino-devhud-development-${digest}`;
}

async function localComposeProjectName() {
  return composeProjectName(await createLocalIdentityKey());
}

export async function down({ quiet = false, projectName, lifecycle } = {}) {
  const resolvedProjectName = projectName ?? (await localComposeProjectName());
  const docker = dockerInvocation();
  let result;
  try {
    const args = [
      ...docker.prefix,
      ...composeArgs(resolvedProjectName, "down", "--remove-orphans"),
    ];
    const options = {
      cwd: repositoryRoot,
      env: dockerChildEnvironment(),
      stdio: quiet ? "ignore" : "inherit",
    };
    result = lifecycle
      ? await lifecycle.runCleanup(docker.command, args, options)
      : await inherited(docker.command, args, options);
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
  const pnpm = toolInvocation("pnpm", "DEVHUD_TEST_PNPM");
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
  await requireTeamEnvironment(lifecycle);
  await validateTeamServices(lifecycle);
  await runService(lifecycle, "team", "api", "migrate");
  return startTurbo(lifecycle, "team");
}

async function startOss(lifecycle) {
  await checkDocker(lifecycle);
  await assertAppPorts();
  const projectName = await localComposeProjectName();
  await assertDependencyPorts(lifecycle, projectName);
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
      await down({ quiet: Boolean(lifecycle.signal), projectName, lifecycle });
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
  const lifecycle = new Lifecycle();
  lifecycle.install();
  try {
    await requireTeamEnvironment(lifecycle);
    await validateTeamServices(lifecycle);
  } finally {
    lifecycle.remove();
  }
  process.stdout.write("Team development environment is healthy.\n");
}
