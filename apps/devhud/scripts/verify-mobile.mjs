#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { assertGeneratedOverlays } from "./generate-mobile.mjs";
import { assertAndroidArtifactEntries, assertAndroidNativeLibrary, assertMobileContracts, assertMobileDependencyClosures, assertMobileDependencyResolution, mobileCargoTreeDigest } from "./mobile-policy.mjs";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const text = (path) => readFileSync(join(appRoot, path), "utf8");
const json = (path) => JSON.parse(text(path));

function commandOutput(command, args, encoding = "utf8") {
  const result = spawnSync(command, args, { encoding, maxBuffer: 64 * 1024 * 1024, shell: false });
  if (result.status !== 0) throw new Error(`${command} failed while inspecting the Android artifact`);
  return result.stdout;
}

function mobileCargoTree(target) {
  const result = spawnSync("cargo", [
    "tree", "--locked", "--manifest-path", "apps/devhud/src-tauri/Cargo.toml", "-p", "devhud",
    "--target", target, "--edges", "normal", "--prefix", "depth", "--format", "{p} {f}",
  ], { cwd: repoRoot, encoding: "utf8", maxBuffer: 64 * 1024 * 1024, shell: false });
  if (result.status !== 0) throw new Error(`cargo tree failed while resolving mobile target ${target}: ${result.stderr.trim()}`);
  return result.stdout;
}

function androidAapt() {
  const sdk = process.env.ANDROID_HOME ?? process.env.ANDROID_SDK_ROOT;
  if (!sdk) throw new Error("ANDROID_HOME or ANDROID_SDK_ROOT is required for artifact verification");
  const buildTools = join(sdk, "build-tools");
  const versions = readdirSync(buildTools).toSorted().reverse();
  const executable = versions.map((version) => join(buildTools, version, process.platform === "win32" ? "aapt.exe" : "aapt")).find(existsSync);
  if (!executable) throw new Error("Android aapt was not found");
  return executable;
}

function assertAndroidArtifact(artifact, abi) {
  if (!existsSync(artifact)) throw new Error(`Android artifact is missing: ${artifact}`);
  commandOutput("unzip", ["-t", artifact]);
  const entries = commandOutput("unzip", ["-Z1", artifact]).trim().split("\n");
  const format = artifact.endsWith(".aab") ? "aab" : artifact.endsWith(".apk") ? "apk" : "unknown";
  assertAndroidArtifactEntries(entries, abi, format);
  const prefix = format === "aab" ? "base/" : "";
  const expectedLibrary = `${prefix}lib/${abi}/libdevhud_lib.so`;
  const dexEntry = format === "aab" ? "base/dex/classes.dex" : "classes.dex";

  const dex = commandOutput("unzip", ["-p", artifact, dexEntry], null).toString("latin1");
  if (!dex.includes("Lio/delino/devhud/bridge/DevhudNativePlugin;") || !dex.includes("Landroid/webkit/WebView;")) throw new Error("Android native bridge or System WebView host is missing from the artifact");
  const nativeLibrary = commandOutput("unzip", ["-p", artifact, expectedLibrary], null).toString("latin1");
  assertAndroidNativeLibrary(nativeLibrary);

  if (format === "aab") return;

  const aapt = androidAapt();
  const badging = commandOutput(aapt, ["dump", "badging", artifact]);
  if (!badging.includes("package: name='io.delino.devhud'") || !badging.includes("sdkVersion:'29'")) throw new Error("Android artifact identity or minimum SDK changed");
  if (!badging.includes("uses-permission: name='android.permission.INTERNET'") || !badging.includes("uses-permission: name='android.permission.POST_NOTIFICATIONS'")) throw new Error("Android artifact is missing required System WebView networking or notification permissions");
  const manifest = commandOutput(aapt, ["dump", "xmltree", artifact, "AndroidManifest.xml"]);
  for (const value of ['="devhud"', '="auth"', '="/callback"', '="deck"', "DevHudWidgetProvider"]) if (!manifest.includes(value)) throw new Error("Android artifact auth callback or widget registration changed");
}

const platforms = json("mobile-platforms.json");
assertMobileDependencyResolution(text("scripts/verify-mobile.mjs"));

assertMobileContracts({
  platforms,
  tauri: json("src-tauri/tauri.conf.json"),
  ios: json("src-tauri/tauri.ios.conf.json"),
  android: json("src-tauri/tauri.android.conf.json"),
  cargo: text("src-tauri/Cargo.toml"),
  androidManifest: text("mobile/overrides/android/app/src/main/AndroidManifest.xml"),
  androidDebugManifest: text("mobile/overrides/android/app/src/debug/AndroidManifest.xml"),
  androidBackupRules: text("mobile/overrides/android/app/src/main/res/xml/backup_rules.xml"),
  androidDataExtractionRules: text("mobile/overrides/android/app/src/main/res/xml/data_extraction_rules.xml"),
  androidPluginManifest: text("src-tauri/mobile/android/src/main/AndroidManifest.xml"),
  androidNativeBridge: text("src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"),
  androidChannelEnglish: text("mobile/overrides/android/app/src/main/res/values/devhud_strings.xml"),
  androidChannelKorean: text("mobile/overrides/android/app/src/main/res/values-ko/devhud_strings.xml"),
  iosNativeBridge: text("src-tauri/mobile/ios/Sources/DevhudNativePlugin.swift"),
  iosPlist: text("src-tauri/Info.ios.plist"),
  packageJson: json("package.json"),
  nativeBridge: text("src/native-bridge.ts"),
  app: text("src/App.tsx"),
  workflow: readFileSync(join(repoRoot, ".github/workflows/CI.yml"), "utf8"),
});

const closureTargets = [...new Set(platforms.targets.map(({ rustTarget }) => rustTarget))];
const dependencyClosures = Object.fromEntries(closureTargets.map((target) => [target, mobileCargoTreeDigest(mobileCargoTree(target), repoRoot)]));
assertMobileDependencyClosures(platforms, dependencyClosures);

if (existsSync(join(appRoot, "src-tauri/gen/android"))) assertGeneratedOverlays("android");

const artifactIndex = process.argv.indexOf("--android-artifact");
const abiIndex = process.argv.indexOf("--android-abi");
if ((artifactIndex === -1) !== (abiIndex === -1)) throw new Error("--android-artifact and --android-abi must be provided together");
if (artifactIndex !== -1) assertAndroidArtifact(resolve(process.argv[artifactIndex + 1]), process.argv[abiIndex + 1]);

const forbiddenMobileText = [
  text("src-tauri/mobile/android/build.gradle.kts"),
  text("src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"),
  text("src-tauri/mobile/android/src/main/java/io/delino/devhud/widget/DevHudWidgetStore.kt"),
  text("src-tauri/mobile/android/src/main/java/io/delino/devhud/widget/DevHudWidgetProvider.kt"),
  text("src-tauri/mobile/ios/Package.swift"),
  text("src-tauri/mobile/ios/Sources/DevhudNativePlugin.swift"),
  text("mobile/overrides/ios/DevHudWidget/DevHudWidget.swift"),
].join("\n");
if (/cef|chromium|chrome-extension|browser-extension|global-hook|desktop-hook|okhttp|retrofit/iu.test(forbiddenMobileText)) throw new Error("forbidden mobile dependency or desktop hook detected");

console.log("devhud: verified synchronized iOS/Android contracts, system webviews, architectures, deep links, and least privileges");
