#!/usr/bin/env node

import { copyFileSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import { basename, dirname, extname, join, resolve } from "node:path";
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
  return { command: "cargo", arguments: cargoArguments };
}

export async function runMobile(rawArguments) {
  const execution = mobileExecution(rawArguments);
  for (const prerequisite of execution.prerequisites ?? []) {
    const prerequisiteResult = await spawnDevServer(prerequisite.command, prerequisite.arguments, { cwd: appRoot, stdio: "inherit", shell: false }, { terminateProcessTree: true });
    if (prerequisiteResult.code !== 0 || prerequisiteResult.signal) exitLikeChild(prerequisiteResult);
  }
  const result = await spawnDevServer(execution.command, execution.arguments, { cwd: appRoot, stdio: "inherit", shell: false }, { terminateProcessTree: true });
  const [platform, command, ...forwarded] = rawArguments;
  const target = targetValues(forwarded).at(-1);
  if (result.code === 0 && !result.signal && platform === "android" && command === "build" && target) {
    preserveAndroidArtifacts(target, forwarded);
  }
  exitLikeChild(result);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    await runMobile(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
}
