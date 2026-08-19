#!/usr/bin/env node

import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import { desktopTauriArguments } from "./run-tauri-arguments.mjs";
import { stageNativeMessagingHost } from "./stage-native-messaging-host.mjs";

const [command, ...rawArgs] = process.argv.slice(2);
const forwardedArgs = rawArgs[0] === "--" ? rawArgs.slice(1) : rawArgs;
const args = desktopTauriArguments(command, forwardedArgs);

if (!command || !["dev", "build"].includes(command)) {
  console.error("Usage: run-tauri.mjs <dev|build> [tauri arguments...]");
  process.exit(1);
}

if (
  forwardedArgs.some(
    (argument) => argument.startsWith("-c") || argument === "--config" || argument.startsWith("--config="),
  )
) {
  console.error("devhud: -c/--config cannot override the pinned application, CSP, or development origin");
  process.exit(1);
}

try {
  stageNativeMessagingHost({ release: command === "build" });
  const result = await spawnDevServer(
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
    {
      terminateProcessTree: true,
    },
  );
  exitLikeChild(result);
} catch (error) {
  console.error(`devhud: failed to start the pinned Tauri CLI: ${error.message}`);
  process.exit(1);
}
