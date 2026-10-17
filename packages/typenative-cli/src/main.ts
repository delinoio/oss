#!/usr/bin/env node
import { randomUUID } from "node:crypto";
import { parseArgs } from "node:util";
import {
  Code,
  createProject,
  DiagnosticError,
  hostTarget,
  inspectTools,
  probeNative,
  resolveExecutable,
  Target,
  versions,
} from "./core.js";

const start = performance.now();
const buildId = randomUUID();
let target: Target | undefined = hostTarget();
let phase = "arguments";
let json = process.argv.includes("--json");

function emit(code: Code, message: string, details: object = {}) {
  const record = {
    versions,
    buildId,
    target: target ?? null,
    phase,
    durationMs: Math.round(performance.now() - start),
    code,
    sourceLocation: null,
    message,
    ...details,
  };
  if (json) process.stdout.write(JSON.stringify(record) + "\n");
  else {
    const color = process.env.NO_COLOR === undefined;
    process.stderr.write(
      `${color ? (code === Code.Ok ? "\u001b[32m" : "\u001b[31m") : ""}${code}${color ? "\u001b[0m" : ""}: ${message}\n`,
    );
    if ("checks" in details)
      for (const check of (
        details as { checks: { code: Code; message: string }[] }
      ).checks) {
        process.stderr.write(`  ${check.code}: ${check.message}\n`);
      }
  }
}

try {
  let parsed;
  try {
    parsed = parseArgs({
      allowPositionals: true,
      strict: true,
      options: {
        json: { type: "boolean" },
        help: { type: "boolean" },
        target: { type: "string" },
        scriptc: { type: "string" },
        probe: { type: "boolean" },
      },
    });
  } catch {
    throw new DiagnosticError(
      Code.Arguments,
      "Invalid arguments. Use tn --help.",
    );
  }
  const { values, positionals } = parsed;
  json = values.json ?? false;
  const [command, destination] = positionals;
  if (values.help || command === undefined) {
    emit(
      Code.Ok,
      "Usage: tn create <directory> | tn doctor [--target <target>] [--scriptc <executable>] [--probe] [--json]. dev/build/run are not implemented.",
    );
  } else if (command === "create") {
    phase = "create";
    if (
      !destination ||
      positionals.length !== 2 ||
      values.target ||
      values.scriptc ||
      values.probe
    ) {
      throw new DiagnosticError(
        Code.Arguments,
        "Use tn create <directory> [--json].",
      );
    }
    await createProject(destination);
    emit(
      Code.Ok,
      "Created a React source scaffold. Native application execution is not implemented.",
      { frameworkReady: false },
    );
  } else if (command === "doctor") {
    phase = "doctor";
    if (positionals.length !== 1)
      throw new DiagnosticError(
        Code.Arguments,
        "doctor does not accept positional arguments.",
      );
    if (values.target !== undefined) {
      if (!Object.values(Target).includes(values.target as Target))
        throw new DiagnosticError(Code.Arguments, "Unknown target identifier.");
      target = values.target as Target;
    }
    if (!target)
      throw new DiagnosticError(
        Code.Host,
        "Select one of the documented targets with --target.",
      );
    const compiler = resolveExecutable(values.scriptc ?? "scriptc");
    const checks = inspectTools(target, compiler);
    const ok = checks.every((check) => check.ok);
    emit(
      checks.find((check) => !check.ok)?.code ?? Code.Ok,
      "Selected prerequisite checks only; SDK completeness and TypeNative platform acceptance are unverified.",
      { checks, frameworkReady: false, platformAcceptance: "unverified" },
    );
    if (!ok) process.exitCode = 1;
    else if (values.probe) {
      phase = "native-probe";
      const result = await probeNative(compiler, target);
      emit(
        Code.Ok,
        "Fixed native console probe passed. React, Blitz, plugins and engine absence are unverified.",
        { probe: result, frameworkReady: false },
      );
    }
  } else if (["dev", "build", "run"].includes(command)) {
    phase = command;
    throw new DiagnosticError(
      Code.Pipeline,
      "The React/Blitz native pipeline is not implemented. No application was launched.",
    );
  } else
    throw new DiagnosticError(
      Code.Arguments,
      "Unknown command. Use tn --help.",
    );
} catch (error) {
  const known = error instanceof DiagnosticError;
  emit(
    known ? error.code : Code.Internal,
    known
      ? error.message
      : "The operation failed. No raw tool output or application data was logged.",
  );
  process.exitCode = 1;
}
