import { resolve } from "node:path";

import { run, runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const mobileCli = resolve(
  appRoot,
  "node_modules/@tauri-apps/cli-mobile/tauri.js",
);
const generationPolicy = resolve(
  appRoot,
  "scripts/enforce-mobile-generation.mjs",
);
const contractCheck = resolve(appRoot, "scripts/check-mobile-contracts.mjs");
const node = process.execPath;
const [operation, platform, targetSet = "production"] = process.argv.slice(2);

const platformNames = new Set(["android", "ios"]);
const operations = new Set(["build", "generate"]);
const targetSets = new Set(["production", "ci", "artifact"]);

if (
  !operations.has(operation) ||
  !platformNames.has(platform) ||
  !targetSets.has(targetSet)
) {
  throw new Error(
    "Usage: node scripts/mobile.mjs <generate|build> <android|ios> [production|ci|artifact]",
  );
}

if (platform === "ios" && process.platform !== "darwin") {
  throw new Error(
    "The standard Tauri iOS generator and Xcode build require a macOS host.",
  );
}

if (operation === "generate") {
  await run(
    node,
    [mobileCli, platform, "init", "--ci", "--skip-targets-install"],
    { cwd: appRoot },
  );
  await run(node, [generationPolicy, platform], { cwd: appRoot });
  await run(node, [contractCheck], { cwd: appRoot });
  process.exit(0);
}

await runPackageManager(["run", "build"], { cwd: appRoot });

if (platform === "android") {
  const targets =
    targetSet === "production" ? ["aarch64", "armv7"] : ["x86_64"];
  await run(
    node,
    [
      mobileCli,
      "android",
      "build",
      "--ci",
      "--apk",
      "--split-per-abi",
      ...(targetSet === "ci" ? ["--debug"] : []),
      "--features",
      "mobile-system-webview,custom-protocol",
      "--target",
      ...targets,
    ],
    { cwd: appRoot },
  );
} else {
  const targets = targetSet === "production" ? ["aarch64"] : ["x86_64"];
  await run("xcodegen", ["generate", "--spec", "project.yml"], {
    cwd: resolve(appRoot, "src-tauri/gen/apple"),
  });
  await run(
    node,
    [
      mobileCli,
      "ios",
      "build",
      "--ci",
      "--no-sign",
      ...(targetSet === "ci" ? ["--debug"] : []),
      "--features",
      "mobile-system-webview,custom-protocol",
      "--target",
      ...targets,
    ],
    { cwd: appRoot },
  );
}
