#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");

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

export function stageNativeMessagingHost({ release }) {
  const triple = rustHostTriple(run("rustc", ["-vV"], { capture: true }));
  const cargoArgs = ["build", "--locked", "-p", "devhud-native-messaging-host"];
  if (release) cargoArgs.push("--release");
  run("cargo", cargoArgs);
  const executableSuffix = process.platform === "win32" ? ".exe" : "";
  const profile = release ? "release" : "debug";
  const source = join(repoRoot, "target", profile, `devhud-native-messaging-host${executableSuffix}`);
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
