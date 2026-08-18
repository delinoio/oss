#!/usr/bin/env node

import { spawn } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readdirSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, extname, join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath, pathToFileURL } from "node:url";

import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";

const platforms = new Set(["android", "ios"]);
const commands = new Set(["dev", "build"]);
const targets = {
  android: new Set(["aarch64", "armv7", "x86_64"]),
  ios: new Set(["aarch64", "aarch64-sim", "x86_64"]),
};
const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const scriptPath = fileURLToPath(import.meta.url);
const iosOptionsPath = join(tmpdir(), "io.delino.devhud-server-addr");

function targetValues(arguments_) {
  return arguments_.flatMap((argument, index) => {
    if (argument === "--target") return [arguments_[index + 1]];
    if (argument.startsWith("--target=")) return [argument.slice("--target=".length)];
    return [];
  });
}

function filesWithExtension(root, extension) {
  if (!existsSync(root)) return [];
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const path = join(root, entry.name);
    return entry.isDirectory() ? filesWithExtension(path, extension) : extname(entry.name) === extension ? [path] : [];
  });
}

export function preserveAndroidArtifacts(target, forwarded, roots = { appRoot, repoRoot }) {
  const requested = [
    ["--apk", ".apk", join(roots.appRoot, "src-tauri/gen/android/app/build/outputs/apk/universal/release")],
    ["--aab", ".aab", join(roots.appRoot, "src-tauri/gen/android/app/build/outputs/bundle/universalRelease")],
  ].filter(([flag]) => forwarded.includes(flag));
  const destinationRoot = join(roots.repoRoot, "target/devhud-mobile/android", target);
  for (const [flag, extension, sourceRoot] of requested) {
    const artifacts = filesWithExtension(sourceRoot, extension);
    if (artifacts.length === 0) throw new Error(`devhud: Android build requested ${flag} but produced no ${extension} artifact`);
    mkdirSync(destinationRoot, { recursive: true });
    for (const artifact of artifacts) {
      copyFileSync(artifact, join(destinationRoot, `${target}-${basename(artifact)}`));
    }
  }
}

export function mobileCargoArguments(rawArguments) {
  const [platform, command, ...forwarded] = rawArguments;
  if (!platforms.has(platform) || !commands.has(command)) {
    throw new Error("Usage: run-mobile.mjs <android|ios> <dev|build> [Tauri arguments...]");
  }
  if (forwarded.some((argument) => argument === "--config" || argument.startsWith("--config=") || argument.startsWith("-c"))) {
    throw new Error("devhud: mobile configuration overrides are not allowed");
  }
  for (const target of targetValues(forwarded)) {
    if (!target || !targets[platform].has(target)) throw new Error(`devhud: unsupported ${platform} target ${target ?? "missing"}`);
  }
  return [
    "run", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli",
    "--bin", "devhud-tauri-cli", "--", platform, command, ...forwarded,
  ];
}

export function mobileExecution(rawArguments) {
  const cargoArguments = mobileCargoArguments(rawArguments);
  const [platform, command, ...forwarded] = rawArguments;
  const requestedTargets = targetValues(forwarded);
  const nonTargetArguments = forwarded.filter((argument, index) => (
    argument !== "--target"
    && forwarded[index - 1] !== "--target"
    && !argument.startsWith("--target=")
  ));
  const directIntelSimulatorBuild = platform === "ios"
    && command === "build"
    && requestedTargets.length === 1
    && requestedTargets[0] === "x86_64"
    && nonTargetArguments.every((argument) => argument === "--ci" || argument === "--no-sign");
  if (directIntelSimulatorBuild) {
    return {
      command: "xcodebuild",
      prerequisites: [
        { command: "pnpm", arguments: ["build:frontend"] },
        { command: "cargo", arguments: ["build", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli", "--bin", "devhud-tauri-cli"] },
      ],
      optionsServerArguments: [...rawArguments, "--open"],
      arguments: [
        "-workspace", "src-tauri/gen/apple/devhud.xcodeproj/project.xcworkspace",
        "-scheme", "devhud_iOS",
        "-sdk", "iphonesimulator",
        "-configuration", "release",
        "-destination", "generic/platform=iOS Simulator",
        "ARCHS=x86_64",
        "CODE_SIGNING_ALLOWED=NO",
        "build",
      ],
    };
  }
  return { command: "cargo", arguments: cargoArguments };
}

function childOutcome(child) {
  let settled = false;
  const promise = new Promise((resolve) => {
    const finish = (result) => {
      settled = true;
      resolve(result);
    };
    child.once("error", (error) => finish({ error }));
    child.once("exit", (code, signal) => finish({ code, signal }));
  });
  return { promise, settled: () => settled };
}

async function waitForOptionsServer(child, outcome, timeoutMs = 120_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (existsSync(iosOptionsPath) && statSync(iosOptionsPath).size > 0) return;
    if (outcome.settled() || child.exitCode !== null || child.signalCode !== null) {
      const result = await outcome.promise;
      throw result.error ?? new Error(`devhud: iOS options server stopped before Xcode started (${result.signal ?? `exit ${result.code}`})`);
    }
    await delay(100);
  }
  throw new Error("devhud: timed out waiting for the pinned Tauri iOS options server");
}

async function runIntelSimulator(execution) {
  rmSync(iosOptionsPath, { force: true });
  const optionsServer = spawn(
    process.execPath,
    [scriptPath, "--options-server", ...execution.optionsServerArguments],
    { cwd: appRoot, stdio: "inherit", shell: false },
  );
  const outcome = childOutcome(optionsServer);
  let result;
  try {
    await waitForOptionsServer(optionsServer, outcome);
    result = await spawnDevServer(execution.command, execution.arguments, { cwd: appRoot, stdio: "inherit", shell: false }, { terminateProcessTree: true });
  } finally {
    if (optionsServer.exitCode === null && optionsServer.signalCode === null) optionsServer.kill("SIGTERM");
    await outcome.promise;
    rmSync(iosOptionsPath, { force: true });
  }
  return result;
}

export async function runMobile(rawArguments, { forceCargo = false } = {}) {
  const execution = forceCargo
    ? { command: "cargo", arguments: mobileCargoArguments(rawArguments) }
    : mobileExecution(rawArguments);
  for (const prerequisite of execution.prerequisites ?? []) {
    const prerequisiteResult = await spawnDevServer(prerequisite.command, prerequisite.arguments, { cwd: appRoot, stdio: "inherit", shell: false }, { terminateProcessTree: true });
    if (prerequisiteResult.code !== 0 || prerequisiteResult.signal) exitLikeChild(prerequisiteResult);
  }
  const result = execution.optionsServerArguments
    ? await runIntelSimulator(execution)
    : await spawnDevServer(execution.command, execution.arguments, { cwd: appRoot, stdio: "inherit", shell: false }, { terminateProcessTree: true });
  const [platform, command, ...forwarded] = rawArguments;
  const target = targetValues(forwarded).at(-1);
  if (result.code === 0 && !result.signal && platform === "android" && command === "build" && target) {
    preserveAndroidArtifacts(target, forwarded);
  }
  exitLikeChild(result);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const rawArguments = process.argv.slice(2);
    const optionsServer = rawArguments[0] === "--options-server";
    await runMobile(optionsServer ? rawArguments.slice(1) : rawArguments, { forceCargo: optionsServer });
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
}
