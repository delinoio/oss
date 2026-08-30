#!/usr/bin/env node

import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import {
  desktopTauriArguments,
  desktopTauriEnvironment,
  prepareVerifiedAppImageSharun,
  repositoryAppleSigningEnvironment,
} from "./run-tauri-arguments.mjs";
import { stageNativeMessagingHost } from "./stage-native-messaging-host.mjs";

const [command, ...rawArgs] = process.argv.slice(2);
const forwardedArgs = rawArgs[0] === "--" ? rawArgs.slice(1) : rawArgs;

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

if (
  forwardedArgs.some(
    (argument) => argument.startsWith("-t") || argument === "--target" || argument.startsWith("--target="),
  )
) {
  console.error("devhud: -t/--target cannot override the pinned desktop target");
  process.exit(1);
}

let result;
let verifiedAppImageSharun;
try {
  const args = desktopTauriArguments(command, forwardedArgs, process.env);
  const repositoryEnvironment = repositoryAppleSigningEnvironment(command);
  let environment = desktopTauriEnvironment(
    command,
    forwardedArgs,
    process.platform,
    repositoryEnvironment,
  );
  if (environment.DEVHUD_PACKAGE_KIND === "linux-appimage") {
    verifiedAppImageSharun = await prepareVerifiedAppImageSharun();
    environment = { ...environment, SHARUN_LINK: verifiedAppImageSharun.url };
  }
  stageNativeMessagingHost({ release: command === "build" });
  result = await spawnDevServer(
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
      env: environment,
      stdio: "inherit",
      shell: false,
    },
    {
      terminateProcessTree: true,
    },
  );
} catch (error) {
  console.error(`devhud: failed to start the pinned Tauri CLI: ${error.message}`);
  process.exitCode = 1;
} finally {
  if (verifiedAppImageSharun) {
    try {
      await verifiedAppImageSharun.close();
    } catch (error) {
      console.error(
        `devhud: failed to close the verified AppImage launcher server: ${error.message}`,
      );
      process.exitCode = 1;
    }
  }
}

if (result && process.exitCode !== 1) exitLikeChild(result);
