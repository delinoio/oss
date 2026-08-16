#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const overrideRoot = join(appRoot, "mobile/overrides");
const generatedRoot = join(appRoot, "src-tauri/gen");
const generatedNames = { android: "android", ios: "apple" };
const mobileTauriFeatures = 'features = ["wry"]';

const nativeHostFiles = {
  android: () => filesBelow(join(appRoot, "src-tauri/mobile/android/src/main/java")).map((source) => ({
    source,
    destination: join(generatedRoot, "android/app/src/main/java", relative(join(appRoot, "src-tauri/mobile/android/src/main/java"), source)),
  })),
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

export function assertGeneratedOverlays(platform) {
  for (const { source, destination } of overlayFiles(platform)) {
    if (existsSync(destination)) {
      const expected = readFileSync(source);
      const actual = readFileSync(destination);
      if (!expected.equals(actual)) throw new Error(`generated mobile overlay is stale: ${relative(appRoot, destination)}`);
    }
  }
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
