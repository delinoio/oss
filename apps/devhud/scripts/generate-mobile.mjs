#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dump as dumpYaml, load as loadYaml } from "js-yaml";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const overrideRoot = join(appRoot, "mobile/overrides");
const generatedRoot = join(appRoot, "src-tauri/gen");
const generatedNames = { android: "android", ios: "apple" };
const mobileTauriFeatures = 'features = ["wry"]';

const nativeHostFiles = {
  android: () => [
    ...filesBelow(join(appRoot, "src-tauri/mobile/android/src/main/java")).map((source) => ({
      source,
      destination: join(generatedRoot, "android/app/src/main/java", relative(join(appRoot, "src-tauri/mobile/android/src/main/java"), source)),
    })),
    ...filesBelow(join(appRoot, "src-tauri/mobile/android/src/main/res")).map((source) => ({
      source,
      destination: join(generatedRoot, "android/app/src/main/res", relative(join(appRoot, "src-tauri/mobile/android/src/main/res"), source)),
    })),
  ],
  // build.rs compiles the iOS Swift package; copying it into the app target
  // would compile the plugin twice without the package's Tauri dependency.
  ios: () => [],
};

function filesBelow(root) {
  if (!existsSync(root)) return [];
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const path = join(root, entry.name);
    return entry.isDirectory() ? filesBelow(path) : [path];
  });
}

export function overlayFiles(platform) {
  const root = join(overrideRoot, platform);
  return [
    ...filesBelow(root).map((source) => ({ source, destination: join(generatedRoot, generatedNames[platform], relative(root, source)) })),
    ...nativeHostFiles[platform](),
  ];
}

export function assertOverlayCopies(files, materialized, relativeRoot = appRoot) {
  if (!materialized) return;
  for (const { source, destination } of files) {
    if (!existsSync(destination)) throw new Error(`generated mobile overlay is missing: ${relative(relativeRoot, destination)}`);
    const expected = readFileSync(source);
    const actual = readFileSync(destination);
    if (!expected.equals(actual)) throw new Error(`generated mobile overlay is stale: ${relative(relativeRoot, destination)}`);
  }
}

export function assertGeneratedOverlays(platform) {
  const generatedPlatformRoot = join(generatedRoot, generatedNames[platform]);
  assertOverlayCopies(overlayFiles(platform), existsSync(generatedPlatformRoot));
  const cargo = readFileSync(join(appRoot, "src-tauri/Cargo.toml"), "utf8");
  const mobileSection = cargo.match(/\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]([\s\S]*?)(?=\n\[|$)/u)?.[1] ?? "";
  if (!mobileSection.includes(mobileTauriFeatures) || /cef|tray-icon|unstable/iu.test(mobileSection)) throw new Error("mobile Cargo features were broadened by project generation");
}

function restoreMobileCargoFeatures() {
  const path = join(appRoot, "src-tauri/Cargo.toml");
  const cargo = readFileSync(path, "utf8");
  const section = /(?<=\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]\n)tauri = \{[^\n]+\}/u;
  const line = cargo.match(section)?.[0];
  if (!line) throw new Error("mobile Tauri dependency alias is missing");
  const restored = line.replace(/features = \[[^\]]*\]/u, mobileTauriFeatures);
  writeFileSync(path, cargo.replace(section, restored));
}

export function configureIosWidgetProject(projectPath = join(generatedRoot, "apple", "project.yml")) {
  const project = loadYaml(readFileSync(projectPath, "utf8"));
  if (typeof project !== "object" || project === null || typeof project.targets !== "object" || project.targets === null) throw new Error("generated iOS project has no targets");
  const application = Object.entries(project.targets).find(([, target]) => target?.type === "application" && target?.platform === "iOS");
  if (application === undefined) throw new Error("generated iOS application target is missing");
  const [, applicationTarget] = application;
  const currentProjectVersion = applicationTarget.info?.properties?.CFBundleVersion;
  const marketingVersion = applicationTarget.info?.properties?.CFBundleShortVersionString;
  if ((typeof currentProjectVersion !== "string" && typeof currentProjectVersion !== "number") || String(currentProjectVersion).trim() === "") throw new Error("generated iOS application target has no current project version");
  if ((typeof marketingVersion !== "string" && typeof marketingVersion !== "number") || String(marketingVersion).trim() === "") throw new Error("generated iOS application target has no marketing version");
  const applicationVersions = { CURRENT_PROJECT_VERSION: currentProjectVersion, MARKETING_VERSION: marketingVersion };
  project.targets.DevHudWidget = {
    type: "app-extension",
    platform: "iOS",
    deploymentTarget: "16.0",
    sources: [
      { path: "DevHudWidget", excludes: ["Info.plist", "DevHudWidget.entitlements"] },
      { path: "DevHudWidgetShared/SelectDeck.intentdefinition" },
      { path: "DevHudWidgetShared/en.lproj" },
      { path: "DevHudWidgetShared/ko.lproj" },
    ],
    settings: { base: { PRODUCT_BUNDLE_IDENTIFIER: "io.delino.devhud.widget", PRODUCT_NAME: "DevHUD Deck", ...applicationVersions, SKIP_INSTALL: "YES", SWIFT_VERSION: "5.9", TARGETED_DEVICE_FAMILY: "1,2", INFOPLIST_FILE: "DevHudWidget/Info.plist", CODE_SIGN_ENTITLEMENTS: "DevHudWidget/DevHudWidget.entitlements" } },
  };
  project.targets.DevHudWidgetIntent = {
    type: "app-extension",
    platform: "iOS",
    deploymentTarget: "16.0",
    sources: [
      { path: "DevHudWidgetIntent", excludes: ["Info.plist", "DevHudWidgetIntent.entitlements"] },
      { path: "DevHudWidgetShared/SelectDeck.intentdefinition" },
      { path: "DevHudWidgetShared/en.lproj" },
      { path: "DevHudWidgetShared/ko.lproj" },
    ],
    settings: { base: { PRODUCT_BUNDLE_IDENTIFIER: "io.delino.devhud.widget.intent", PRODUCT_NAME: "DevHUD Deck Selection", ...applicationVersions, SKIP_INSTALL: "YES", SWIFT_VERSION: "5.9", TARGETED_DEVICE_FAMILY: "1,2", INFOPLIST_FILE: "DevHudWidgetIntent/Info.plist", CODE_SIGN_ENTITLEMENTS: "DevHudWidgetIntent/DevHudWidgetIntent.entitlements" } },
  };
  const [applicationName] = application;
  applicationTarget.dependencies = [...(applicationTarget.dependencies ?? []).filter((dependency) => !["DevHudWidget", "DevHudWidgetIntent"].includes(dependency?.target)), { target: "DevHudWidget", embed: true }, { target: "DevHudWidgetIntent", embed: true }];
  project.targets[applicationName] = applicationTarget;
  writeFileSync(projectPath, dumpYaml(project, { lineWidth: 140, noRefs: true }));
}

function generate(platform) {
  const result = spawnSync(
    "cargo",
    ["run", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli", "--bin", "devhud-tauri-cli", "--", platform, "init", "--ci"],
    { cwd: appRoot, stdio: "inherit", shell: false },
  );
  if (result.status !== 0) throw new Error(`Tauri ${platform} project generation failed`);
  restoreMobileCargoFeatures();
  for (const { source, destination } of overlayFiles(platform)) {
    mkdirSync(dirname(destination), { recursive: true });
    cpSync(source, destination, { force: true });
  }
  if (platform === "ios") {
    configureIosWidgetProject();
    const xcodegen = spawnSync("xcodegen", ["generate", "--spec", "project.yml"], {
      cwd: join(generatedRoot, "apple"), stdio: "inherit", shell: false,
    });
    if (xcodegen.status !== 0) throw new Error("xcodegen failed after applying the iOS native host source");
  }
  assertGeneratedOverlays(platform);
}

export function generateMobile(args) {
  const check = args.includes("--check");
  const platformIndex = args.indexOf("--platform");
  const requested = platformIndex === -1 ? null : args[platformIndex + 1];
  if (requested && !["android", "ios"].includes(requested)) throw new Error(`unsupported platform ${requested}`);

  for (const platform of requested ? [requested] : ["android", ...(process.platform === "darwin" ? ["ios"] : [])]) {
    if (check) assertGeneratedOverlays(platform);
    else generate(platform);
  }

  console.log(check ? "devhud: mobile project overlays are synchronized" : "devhud: mobile projects generated from pinned Tauri and synchronized overlays");
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) generateMobile(process.argv.slice(2));
