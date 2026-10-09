// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import {
  cpSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  existsSync,
} from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { tauriCommand } from "../../../scripts/tauri-cli.mjs";
const app = resolve(dirname(fileURLToPath(import.meta.url)), ".."),
  root = resolve(app, "../..");
function run(program, args, cwd = app, env = {}) {
  const result = spawnSync(program, args, {
    cwd,
    env: { ...process.env, ...env },
    stdio: "inherit",
    shell: false,
  });
  if (result.error || result.status !== 0)
    throw new Error(`Mobile build failed: ${program}`);
}
function tauri(args) {
  run(...tauriCommand(args));
}
export function androidManifest(source) {
  source = source
    .replace(/\s+android:allowBackup="[^"]*"/g, "")
    .replace(/\s+android:fullBackupContent="[^"]*"/g, "")
    .replace(/\s+android:windowSoftInputMode="[^"]*"/g, "")
    .replace(
      /\s*<uses-permission android:name="android.permission.POST_NOTIFICATIONS" \/>/g,
      "",
    );
  return source
    .replace(
      "<application\n",
      '<application\n        android:allowBackup="false"\n        android:fullBackupContent="false"\n',
    )
    .replace(
      'android:usesCleartextTraffic="${usesCleartextTraffic}"',
      'android:usesCleartextTraffic="false"',
    )
    .replace(
      'android:launchMode="singleTask"',
      'android:launchMode="singleTask"\n            android:windowSoftInputMode="adjustResize"',
    )
    .replace(
      '<uses-permission android:name="android.permission.INTERNET" />',
      '<uses-permission android:name="android.permission.INTERNET" />\n    <uses-permission android:name="android.permission.POST_NOTIFICATIONS" />',
    );
}
export function iosBuildScript(source) {
  return source.replace(
    "script: cargo tauri ios xcode-script",
    'script: if [ "${DELIDEV_MOBILE_PREBUILT_RUST:-0}" = "1" ]; then test -s "${PROJECT_DIR}/Externals/arm64/${CONFIGURATION}/libapp.a"; exit $?; fi; cargo tauri ios xcode-script',
  );
}
export function generate(platform) {
  const manifest = join(app, "src-tauri/Cargo.toml"),
    cargo = readFileSync(manifest, "utf8");
  try {
    tauri([platform, "init", "--ci", "--skip-targets-install"]);
  } finally {
    writeFileSync(manifest, cargo);
  }
  if (platform === "android") {
    const file = join(
      app,
      "src-tauri/gen/android/app/src/main/AndroidManifest.xml",
    );
    writeFileSync(file, androidManifest(readFileSync(file, "utf8")));
    cpSync(
      join(app, "src-tauri/mobile/android/src/main/java"),
      join(app, "src-tauri/gen/android/app/src/main/java"),
      { recursive: true },
    );
  } else {
    const file = join(app, "src-tauri/gen/apple/project.yml");
    writeFileSync(file, iosBuildScript(readFileSync(file, "utf8")));
    run("xcodegen", ["generate"], dirname(file));
  }
}
export function execution(command) {
  switch (command) {
    case "ios-simulator":
      return {
        platform: "ios",
        target: "aarch64-apple-ios-sim",
        configuration: "debug",
      };
    case "ios-device":
      return {
        platform: "ios",
        target: "aarch64-apple-ios",
        configuration: "release",
      };
    case "android-emulator":
      return {
        platform: "android",
        args: [
          "android",
          "build",
          "--ci",
          "--debug",
          "--target",
          "x86_64",
          "--apk",
        ],
      };
    case "android-bundle":
      return {
        platform: "android",
        args: [
          "android",
          "build",
          "--ci",
          "--target",
          "aarch64",
          "armv7",
          "--aab",
        ],
      };
    default:
      throw new Error("Unknown mobile target");
  }
}
function build(command) {
  const e = execution(command);
  generate(e.platform);
  if (e.platform === "android") {
    tauri(e.args);
    return;
  }
  if (command === "ios-device") {
    tauri(["ios", "build", "--ci", "--target", "aarch64"]);
    return;
  }
  // cargo-mobile2 requires an installed simulator runtime even for a build-only
  // archive. Direct xcodebuild uses the installed SDK and never launches a device.
  // Remove this path when the pinned CLI permits build-only simulator targets.
  run("pnpm", ["build"]);
  run(
    "cargo",
    [
      "build",
      "-p",
      "delidev-mobile",
      "--target",
      e.target,
      "--features",
      "custom-protocol",
    ],
    root,
  );
  const target = resolve(root, process.env.CARGO_TARGET_DIR ?? "target"),
    external = join(
      app,
      "src-tauri/gen/apple/Externals/arm64",
      e.configuration,
    );
  mkdirSync(external, { recursive: true });
  cpSync(
    join(target, e.target, e.configuration, "libdelidev_mobile.a"),
    join(external, "libapp.a"),
  );
  cpSync(join(app, "dist"), join(app, "src-tauri/gen/apple/assets"), {
    recursive: true,
  });
  run("xcodebuild", [
    "-project",
    "src-tauri/gen/apple/delidev-mobile.xcodeproj",
    "-scheme",
    "delidev-mobile_iOS",
    "-sdk",
    "iphonesimulator",
    "-configuration",
    e.configuration,
    "-destination",
    "generic/platform=iOS Simulator",
    "ARCHS=arm64",
    "CODE_SIGNING_ALLOWED=NO",
    "DELIDEV_MOBILE_PREBUILT_RUST=1",
    "-derivedDataPath",
    join(target, "delidev-mobile-ios"),
    "build",
  ]);
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  try {
    const command = process.argv[2];
    if (command === "generate") {
      generate("ios");
      generate("android");
    } else if (command === "generate-ios") {
      generate("ios");
    } else build(command);
  } catch {
    process.stderr.write("Mobile generation or target build failed.\n");
    process.exitCode = 1;
  }
