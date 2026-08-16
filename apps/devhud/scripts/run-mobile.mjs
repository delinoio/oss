#!/usr/bin/env node

import { pathToFileURL } from "node:url";

import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";

const platforms = new Set(["android", "ios"]);
const commands = new Set(["dev", "build"]);
const targets = {
  android: new Set(["aarch64", "armv7", "x86_64"]),
  ios: new Set(["aarch64", "aarch64-sim", "x86_64"]),
};

export function mobileCargoArguments(rawArguments) {
  const [platform, command, ...forwarded] = rawArguments;
  if (!platforms.has(platform) || !commands.has(command)) {
    throw new Error("Usage: run-mobile.mjs <android|ios> <dev|build> [Tauri arguments...]");
  }
  if (forwarded.some((argument) => argument === "--config" || argument.startsWith("--config=") || argument.startsWith("-c"))) {
    throw new Error("devhud: mobile configuration overrides are not allowed");
  }
  const targetIndexes = forwarded.flatMap((argument, index) => argument === "--target" ? [index] : []);
  for (const index of targetIndexes) {
    const target = forwarded[index + 1];
    if (!target || !targets[platform].has(target)) throw new Error(`devhud: unsupported ${platform} target ${target ?? "missing"}`);
  }
  return [
    "run", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli",
    "--bin", "devhud-tauri-cli", "--", platform, command, ...forwarded,
  ];
}

export async function runMobile(rawArguments) {
  const args = mobileCargoArguments(rawArguments);
  const result = await spawnDevServer("cargo", args, { stdio: "inherit", shell: false }, { terminateProcessTree: true });
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
