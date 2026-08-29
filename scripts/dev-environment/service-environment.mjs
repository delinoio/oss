import { spawn } from "node:child_process";
import { createHmac, randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { parseEnv } from "node:util";
import {
  acceptedMarker,
  comparisonKeyName,
  comparisonMarker,
  EnvironmentError,
  formatEnvironmentError,
  rejectedMarker,
  repositoryRoot,
  requireMode,
  resolveInfisicalConfigPaths,
  validateInjectedEnvironment,
} from "./contracts.mjs";
import { commandInvocation, safeBaseEnvironment, terminateTree } from "./process.mjs";
import { spawnDevServer } from "../spawn-dev-server.mjs";

function emitRejected(error) {
  const safe =
    error instanceof EnvironmentError
      ? { code: error.code, message: error.message, names: error.names }
      : { code: "environment.invalid", message: "service configuration could not be validated", names: [] };
  process.stderr.write(`${rejectedMarker}${JSON.stringify(safe)}\n`);
}

function testEnvironment(source = process.env) {
  if (source.DEVHUD_ENVIRONMENT_TESTING !== "1") return {};
  return Object.fromEntries(
    Object.entries(source).filter(
      ([name]) => name === "DEVHUD_ENVIRONMENT_TESTING" || name.startsWith("DEVHUD_TEST_"),
    ),
  );
}

function parseRejected(text) {
  const location = text.indexOf(rejectedMarker);
  if (location === -1) return null;
  const line = text.slice(location + rejectedMarker.length).split(/\r?\n/u)[0];
  try {
    const value = JSON.parse(line);
    return new EnvironmentError(value.code, value.message, value.names);
  } catch {
    return null;
  }
}

function forwardAfterMarker(child) {
  let accepted = false;
  let stdoutBeforeAcceptance = "";
  let stderrBeforeAcceptance = "";

  child.stdout.on("data", (chunk) => {
    if (accepted) {
      process.stdout.write(chunk);
      return;
    }
    stdoutBeforeAcceptance += chunk.toString("utf8");
    const location = stdoutBeforeAcceptance.indexOf(acceptedMarker);
    if (location === -1) return;
    accepted = true;
    const after = stdoutBeforeAcceptance.slice(location + acceptedMarker.length).replace(/^\r?\n/u, "");
    stdoutBeforeAcceptance = "";
    if (after) process.stdout.write(after);
    stderrBeforeAcceptance = "";
  });
  child.stderr.on("data", (chunk) => {
    if (accepted) process.stderr.write(chunk);
    else stderrBeforeAcceptance += chunk.toString("utf8");
  });

  return () => ({ accepted, stdoutBeforeAcceptance, stderrBeforeAcceptance });
}

async function runChild(command, args, environment, cwd) {
  return spawnDevServer(
    command,
    args,
    { cwd, env: environment, shell: false, stdio: "inherit" },
    { terminateProcessTree: true },
  );
}

async function awaitProvider(child) {
  let forwardedSignal = null;
  let terminationPromise = null;
  const handlers = new Map();
  for (const signal of ["SIGINT", "SIGTERM"]) {
    const handler = () => {
      if (forwardedSignal) return;
      forwardedSignal = signal;
      terminationPromise = terminateTree(child, signal).catch(() => {
        child.kill(signal);
      });
    };
    handlers.set(signal, handler);
    process.on(signal, handler);
  }
  try {
    const result = await new Promise((resolveResult, reject) => {
      child.once("error", reject);
      child.once("close", (code, signal) => resolveResult({ code, signal }));
    });
    if (terminationPromise) await terminationPromise;
    return forwardedSignal ? { code: null, signal: forwardedSignal } : result;
  } finally {
    for (const [signal, handler] of handlers) process.off(signal, handler);
  }
}

async function runInjected({
  contract,
  action,
  comparisonName,
  execute,
  baselineVariable,
}) {
  try {
    const baseline = JSON.parse(process.env[baselineVariable] ?? "{}");
    const candidateEnvironment = { ...process.env };
    delete candidateEnvironment[baselineVariable];
    const configuration = validateInjectedEnvironment(contract, candidateEnvironment, baseline);
    const comparisonKey = baseline[comparisonKeyName];
    if (
      comparisonKey &&
      (action !== "validate" ||
        typeof comparisonName !== "string" ||
        typeof configuration[comparisonName] !== "string")
    ) {
      throw new EnvironmentError(
        "environment.comparison",
        "service configuration comparison could not be produced",
      );
    }
    const comparison = comparisonKey
      ? createHmac("sha256", comparisonKey)
          .update(configuration[comparisonName], "utf8")
          .digest("base64url")
      : null;
    process.stdout.write(`${acceptedMarker}\n`);
    if (comparison) process.stdout.write(`${comparisonMarker}${comparison}\n`);
    const result = await execute(action, {
      ...safeBaseEnvironment(baseline),
      ...testEnvironment(baseline),
      ...configuration,
      DEVHUD_ENVIRONMENT: "development",
      DEVHUD_LOCAL_MODE: "team",
    });
    if (result.signal) process.kill(process.pid, result.signal);
    return result.code ?? 0;
  } catch (error) {
    emitRejected(error);
    return 1;
  }
}

async function runTeam({ contract, action, comparisonName, scriptPath, execute }) {
  const injectedArgument = process.argv[3];
  if (injectedArgument?.startsWith("--injected=")) {
    return runInjected({
      contract,
      action,
      comparisonName,
      execute,
      baselineVariable: injectedArgument.slice("--injected=".length),
    });
  }

  const baseline = {
    ...safeBaseEnvironment(),
    ...testEnvironment(),
    ...(process.env[comparisonKeyName]
      ? { [comparisonKeyName]: process.env[comparisonKeyName] }
      : {}),
    DEVHUD_LOCAL_MODE: "team",
  };
  const baselineVariable = `DEVHUD_INTERNAL_${randomBytes(16).toString("hex").toUpperCase()}`;
  const environment = { ...baseline, [baselineVariable]: JSON.stringify(baseline) };
  const fakeExecutable =
    process.env.DEVHUD_ENVIRONMENT_TESTING === "1" && process.env.DEVHUD_TEST_INFISICAL
      ? process.env.DEVHUD_TEST_INFISICAL
      : null;
  const executable = fakeExecutable ? process.execPath : "infisical";
  const { projectConfigDirectory } = resolveInfisicalConfigPaths();
  const args = [
    "--log-level=warn",
    "--silent",
    "--telemetry=false",
    "run",
    "--env=dev",
    `--path=${contract.path}`,
    "--secret-overriding=false",
    "--expand=false",
    "--include-imports=false",
    `--project-config-dir=${projectConfigDirectory}`,
    "--",
    process.execPath,
    scriptPath,
    action,
    `--injected=${baselineVariable}`,
  ];
  const child = spawn(executable, fakeExecutable ? [fakeExecutable, ...args] : args, {
    cwd: repositoryRoot,
    env: environment,
    shell: false,
    detached: process.platform !== "win32",
    stdio: ["inherit", "pipe", "pipe"],
  });
  const buffered = forwardAfterMarker(child);
  const result = await awaitProvider(child);
  const output = buffered();
  if (!output.accepted) {
    const rejection = parseRejected(`${output.stdoutBeforeAcceptance}\n${output.stderrBeforeAcceptance}`);
    throw (
      rejection ??
      new EnvironmentError(
        "environment.unavailable",
        `${contract.service} authentication or secret path is unavailable; run pnpm env:doctor`,
      )
    );
  }
  if (result.signal) process.kill(process.pid, result.signal);
  return result.code ?? 0;
}

export async function readServiceEnv(path, allowedNames) {
  let text;
  try {
    text = await readFile(path, "utf8");
  } catch (error) {
    if (error.code === "ENOENT") return {};
    throw new EnvironmentError(
      "environment.local-file",
      "service-local OSS configuration could not be read",
    );
  }
  let parsed;
  try {
    parsed = parseEnv(text);
  } catch {
    throw new EnvironmentError(
      "environment.local-file",
      "service-local OSS configuration syntax is invalid",
    );
  }
  const unknown = Object.keys(parsed).filter((name) => !allowedNames.includes(name));
  if (unknown.length > 0) {
    throw new EnvironmentError(
      "environment.unknown",
      "service-local OSS configuration contains unknown names",
      unknown,
    );
  }
  return parsed;
}

export async function runService({
  contract,
  action,
  comparisonName,
  scriptPath,
  ossEnvironment,
  execute,
}) {
  try {
    const mode = requireMode();
    if (!["validate", "migrate", "serve"].includes(action)) {
      throw new EnvironmentError("action.invalid", "local service action is invalid");
    }
    if (mode === "team") {
      return await runTeam({
        contract,
        action,
        comparisonName,
        scriptPath,
        execute,
      });
    }
    const raw = await ossEnvironment(action);
    const configuration = validateInjectedEnvironment(contract, raw, {});
    if (action === "validate") return 0;
    const result = await execute(action, {
      ...safeBaseEnvironment(),
      ...testEnvironment(),
      ...configuration,
      DEVHUD_ENVIRONMENT: "development",
      DEVHUD_LOCAL_MODE: "oss",
    });
    if (result.signal) process.kill(process.pid, result.signal);
    return result.code ?? 0;
  } catch (error) {
    process.stderr.write(`${formatEnvironmentError(error)}\n`);
    return 1;
  }
}

export async function executeArgv(command, args, environment, cwd = repositoryRoot) {
  const invocation = commandInvocation(command);
  return runChild(
    invocation.command,
    [...invocation.prefix, ...args],
    environment,
    cwd,
  );
}
