#!/usr/bin/env node

import { spawnSync } from "node:child_process";

const [command, ...rawArgs] = process.argv.slice(2);
const forwardedArgs = rawArgs[0] === "--" ? rawArgs.slice(1) : rawArgs;
const args = command ? [command, ...forwardedArgs] : [];

if (!command || !["dev", "build"].includes(command)) {
  console.error("Usage: run-tauri.mjs <dev|build> [tauri arguments...]");
  process.exit(1);
}

if (forwardedArgs.some((argument) => argument === "--config" || argument.startsWith("--config="))) {
  console.error("devhud: --config cannot override the pinned application, CSP, or development origin");
  process.exit(1);
}

const result = spawnSync(
  "cargo",
  [
    "run",
    "--locked",
    "--manifest-path",
    "src-tauri/Cargo.toml",
    "--features",
    "cli",
    "--bin",
    "devhud-tauri-cli",
    "--",
    ...args,
  ],
  {
    stdio: "inherit",
    shell: false,
  },
);

if (result.error) {
  console.error(`devhud: failed to start the pinned Tauri CLI: ${result.error.message}`);
  process.exit(1);
}

process.exit(result.status ?? 1);
