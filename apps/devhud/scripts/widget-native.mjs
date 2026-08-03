import { execFileSync } from "node:child_process";
import { readdir } from "node:fs/promises";
import { resolve } from "node:path";

import { run } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const platform = process.argv[2];
const operation = process.argv[3] ?? "test";
const artifactCheck = resolve(appRoot, "scripts/check-widget-artifacts.mjs");
const mobileBuild = resolve(appRoot, "scripts/mobile.mjs");

if (!new Set(["android", "ios"]).has(platform)) {
  throw new Error("Usage: node scripts/widget-native.mjs <android|ios> [build|test]");
}
if (!new Set(["build", "test"]).has(operation)) {
  throw new Error("Widget native operation must be build or test.");
}

if (platform === "android") {
  const androidRoot = resolve(appRoot, "native-widgets/android");
  const gradle = resolve(
    appRoot,
    "src-tauri/gen/android",
    process.platform === "win32" ? "gradlew.bat" : "gradlew",
  );
  const tasks = [":widget-foundation:assembleDebug"];
  if (operation === "test") tasks.push(":widget-foundation:testDebugUnitTest");
  await run(gradle, ["-p", androidRoot, ...tasks, "--no-daemon"], {
    cwd: androidRoot,
  });
} else {
  if (process.platform !== "darwin") {
    throw new Error(
      "The WidgetKit target requires macOS, Xcode, and XcodeGen.",
    );
  }
  const iosRoot = resolve(appRoot, "native-widgets/ios");
  await run("xcodegen", ["generate", "--spec", "project.yml"], {
    cwd: iosRoot,
  });
  const common = [
    "-project",
    "DevHudWidget.xcodeproj",
    "-scheme",
    "DevHudWidget",
    "-sdk",
    "iphonesimulator",
    "CODE_SIGNING_ALLOWED=NO",
  ];
  await run(
    "xcodebuild",
    [...common, "-destination", "generic/platform=iOS Simulator", "build"],
    { cwd: iosRoot },
  );
  if (operation === "test") {
    const simulator =
      process.env.DEVHUD_IOS_SIMULATOR_DESTINATION ??
      (await firstAvailableSimulatorDestination());
    await run(
      "xcodebuild",
      [...common, "-destination", simulator, "test"],
      { cwd: iosRoot },
    );
  }
}

if (operation === "test") {
  await run(process.execPath, [mobileBuild, "build", platform, "artifact"], {
    cwd: appRoot,
  });

  const outputRoot =
    platform === "android"
      ? resolve(appRoot, "src-tauri/gen/android/app/build/outputs/apk")
      : resolve(appRoot, "src-tauri/gen/apple/build");
  const suffix = platform === "android" ? ".apk" : ".app";
  const artifactFlag =
    platform === "android" ? "--android-apk" : "--ios-app";
  const artifacts = await collectArtifacts(outputRoot, suffix);
  if (artifacts.length === 0) {
    throw new Error(
      `The distributed ${platform} build produced no ${suffix} artifact to inspect.`,
    );
  }
  await run(
    process.execPath,
    [
      artifactCheck,
      ...artifacts.flatMap((artifact) => [artifactFlag, artifact]),
    ],
    { cwd: appRoot },
  );
}

async function collectArtifacts(directory, suffix) {
  const artifacts = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name);
    if (entry.name.endsWith(suffix)) {
      artifacts.push(path);
    } else if (entry.isDirectory()) {
      artifacts.push(...(await collectArtifacts(path, suffix)));
    }
  }
  return artifacts;
}

async function firstAvailableSimulatorDestination() {
  const output = execFileSync(
    "xcrun",
    ["simctl", "list", "devices", "available", "--json"],
    { cwd: appRoot, encoding: "utf8" },
  );
  const listing = JSON.parse(output);
  for (const [runtime, devices] of Object.entries(listing.devices ?? {})) {
    if (!runtime.startsWith("com.apple.CoreSimulator.SimRuntime.iOS-")) {
      continue;
    }
    const device = devices.find((candidate) => candidate.isAvailable);
    if (device) return `platform=iOS Simulator,id=${device.udid}`;
  }
  throw new Error("No available iOS simulator was reported by simctl.");
}
