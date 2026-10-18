// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from "node:child_process";
import { cpSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
export function configureAndroid(source) {
  if (source.includes("// DeliTools release signing")) return source;
  const anchor = "    buildTypes {";
  if (!source.includes(anchor) || !source.includes('getByName("release") {'))
    throw new Error("Unsupported Tauri Android template");
  // Generated projects are disposable. Apply signing after each init so secrets
  // remain in the runner environment, never in generated Gradle source.
  return source
    .replace(
      anchor,
      `    // DeliTools release signing
    signingConfigs {
        create("release") {
            val keystore = System.getenv("TAURI_ANDROID_KEYSTORE_PATH")
            if (keystore != null) {
                storeFile = file(keystore)
                storePassword = System.getenv("TAURI_ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("TAURI_ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("TAURI_ANDROID_KEY_PASSWORD")
            }
        }
    }
${anchor}`,
    )
    .replace(
      'getByName("release") {',
      'getByName("release") {\n            if (System.getenv("TAURI_ANDROID_KEYSTORE_PATH") != null) {\n                signingConfig = signingConfigs.getByName("release")\n            }',
    );
}

export function releaseConfig(buildNumber) {
  if (!/^[1-9][0-9]*$/.test(buildNumber) || Number(buildNumber) > 9999)
    throw new Error("Build number must be a positive integer at most 9999");
  return {
    bundle: {
      android: { versionCode: Number(buildNumber) },
      iOS: { bundleVersion: buildNumber },
    },
  };
}

export function main(args, env = process.env) {
  const [platform, command, ...rest] = args;
  if (
    !["android", "ios"].includes(platform) ||
    !["init", "dev", "build"].includes(command)
  )
    throw new Error(
      "Expected <android|ios> <init|dev|build> [Tauri arguments]",
    );
  const forwarded = [platform, command, ...rest];
  if (command === "build" && env.DELITOOLS_BUILD_NUMBER)
    forwarded.push(
      "--config",
      JSON.stringify(releaseConfig(env.DELITOOLS_BUILD_NUMBER)),
    );
  console.log(
    JSON.stringify({ event: "delitools_mobile_command", platform, command }),
  );
  const result = spawnSync(
    process.execPath,
    [resolve(root, "../../scripts/tauri-cli.mjs"), "--", ...forwarded],
    { cwd: root, env, stdio: "inherit" },
  );
  if (result.error) throw result.error;
  if (result.status !== 0) return result.status ?? 1;
  if (platform === "android" && command === "init") {
    const gradle = resolve(root, "src-tauri/gen/android/app/build.gradle.kts");
    writeFileSync(gradle, configureAndroid(readFileSync(gradle, "utf8")));
  }
  if (command === "init") {
    const icons = spawnSync(
      process.execPath,
      [
        resolve(root, "../../scripts/tauri-cli.mjs"),
        "--",
        "icon",
        "src-tauri/icons/icon.png",
        "--output",
        "src-tauri/gen/icons",
      ],
      { cwd: root, env, stdio: "inherit" },
    );
    if (icons.error) throw icons.error;
    if (icons.status !== 0) return icons.status ?? 1;
    const destination =
      platform === "ios"
        ? "apple/Assets.xcassets/AppIcon.appiconset"
        : "android/app/src/main/res";
    cpSync(
      resolve(root, `src-tauri/gen/icons/${platform}`),
      resolve(root, `src-tauri/gen/${destination}`),
      { recursive: true },
    );
  }
  return 0;
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    process.exitCode = main(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
