#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const nativeHostTarget = "devhud-native-messaging-host";

export function rustHostTriple(versionOutput) {
  const host = versionOutput.match(/^host: (\S+)$/mu)?.[1];
  if (!host || !/^[a-z0-9_.-]+$/u.test(host)) {
    throw new Error("unable to determine the Rust host triple");
  }
  return host;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: repoRoot,
    encoding: "utf8",
    shell: false,
    stdio: options.capture ? ["ignore", "pipe", "inherit"] : "inherit",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited ${result.status}`);
  return result.stdout ?? "";
}

export function nativeMessagingHostExecutable(buildOutput) {
  const executables = new Set();
  for (const line of buildOutput.split(/\r?\n/u)) {
    if (!line.trimStart().startsWith("{")) continue;
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      continue;
    }
    if (
      message.reason === "compiler-artifact"
      && message.target?.name === nativeHostTarget
      && message.target.kind?.includes("bin")
      && typeof message.executable === "string"
      && message.executable !== ""
    ) {
      executables.add(message.executable);
    }
  }
  if (executables.size !== 1) {
    throw new Error("unable to determine the Native Messaging host artifact");
  }
  return executables.values().next().value;
}

export function stageNativeMessagingHost({ release }) {
  const triple = rustHostTriple(run("rustc", ["-vV"], { capture: true }));
  const cargoArgs = [
    "build",
    "--locked",
    "-p",
    nativeHostTarget,
    "--message-format=json-render-diagnostics",
  ];
  if (release) cargoArgs.push("--release");
  const source = nativeMessagingHostExecutable(run("cargo", cargoArgs, { capture: true }));
  const executableSuffix = process.platform === "win32" ? ".exe" : "";
  const destination = join(
    appRoot,
    "src-tauri",
    "binaries",
    `devhud-native-messaging-host-${triple}${executableSuffix}`,
  );
  mkdirSync(dirname(destination), { recursive: true });
  copyFileSync(source, destination);
  return destination;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    stageNativeMessagingHost({ release: process.argv.includes("--release") });
  } catch (error) {
    console.error(`devhud: unable to stage Native Messaging host: ${error.message}`);
    process.exit(1);
  }
}
