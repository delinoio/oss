#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptRoot = dirname(fileURLToPath(import.meta.url));
export const repositoryRoot = resolve(scriptRoot, "../..");
export const metadataPath = join(repositoryRoot, "packaging/devhud/release-metadata.json");

export const artifactGroups = Object.freeze({
  desktop: Object.freeze([
    "devhud-macos-x64.dmg",
    "devhud-macos-x64-macos-app.tar.gz",
    "devhud-macos-arm64.dmg",
    "devhud-macos-arm64-macos-app.tar.gz",
    "devhud-windows-x64-windows-msi.msi",
    "devhud-windows-x64-windows-nsis.exe",
    "devhud-windows-arm64-windows-msi.msi",
    "devhud-windows-arm64-windows-nsis.exe",
    "devhud-ubuntu-x64-linux-appimage.AppImage",
    "devhud-ubuntu-x64-linux-deb.deb",
    "devhud-ubuntu-arm64-linux-appimage.AppImage",
    "devhud-ubuntu-arm64-linux-deb.deb",
  ]),
  stores: Object.freeze([
    "devhud-ios-arm64-app-store.ipa",
    "devhud-android-arm64-armv7-google-play.aab",
  ]),
  extension: Object.freeze([
    "devhud-chrome-web-store.zip",
    "devhud-chrome-github-validation.zip",
  ]),
  oci: Object.freeze([
    "devhud-api-linux-amd64-arm64.oci.tar",
    "devhud-api-sweeper-linux-amd64-arm64.oci.tar",
  ]),
});

export const updaterTargets = Object.freeze([
  Object.freeze({ id: "macos-x64", platform: "darwin", architecture: "x86_64", packageKind: "macos-app", artifact: "devhud-macos-x64-macos-app.tar.gz" }),
  Object.freeze({ id: "macos-arm64", platform: "darwin", architecture: "aarch64", packageKind: "macos-app", artifact: "devhud-macos-arm64-macos-app.tar.gz" }),
  Object.freeze({ id: "windows-x64-msi", platform: "windows", architecture: "x86_64", packageKind: "windows-msi", artifact: "devhud-windows-x64-windows-msi.msi" }),
  Object.freeze({ id: "windows-x64-nsis", platform: "windows", architecture: "x86_64", packageKind: "windows-nsis", artifact: "devhud-windows-x64-windows-nsis.exe" }),
  Object.freeze({ id: "windows-arm64-msi", platform: "windows", architecture: "aarch64", packageKind: "windows-msi", artifact: "devhud-windows-arm64-windows-msi.msi" }),
  Object.freeze({ id: "windows-arm64-nsis", platform: "windows", architecture: "aarch64", packageKind: "windows-nsis", artifact: "devhud-windows-arm64-windows-nsis.exe" }),
  Object.freeze({ id: "ubuntu-x64-appimage", platform: "linux", architecture: "x86_64", packageKind: "linux-appimage", artifact: "devhud-ubuntu-x64-linux-appimage.AppImage" }),
  Object.freeze({ id: "ubuntu-x64-deb", platform: "linux", architecture: "x86_64", packageKind: "linux-deb", artifact: "devhud-ubuntu-x64-linux-deb.deb" }),
  Object.freeze({ id: "ubuntu-arm64-appimage", platform: "linux", architecture: "aarch64", packageKind: "linux-appimage", artifact: "devhud-ubuntu-arm64-linux-appimage.AppImage" }),
  Object.freeze({ id: "ubuntu-arm64-deb", platform: "linux", architecture: "aarch64", packageKind: "linux-deb", artifact: "devhud-ubuntu-arm64-linux-deb.deb" }),
]);

export const signingInputs = Object.freeze([
  "DEVHUD_CHROME_EXTENSION_ID",
  "DEVHUD_CHROME_EXTENSION_PUBLIC_KEY",
  "DEVHUD_UPDATER_SIGNING_KEY_B64",
  "DEVHUD_MACOS_DEVELOPER_ID_P12_B64",
  "DEVHUD_MACOS_DEVELOPER_ID_P12_PASSWORD",
  "DEVHUD_MACOS_SIGNING_IDENTITY",
  "APPLE_API_ISSUER",
  "APPLE_API_KEY_ID",
  "APPLE_API_PRIVATE_KEY_B64",
  "DEVHUD_WINDOWS_SIGNING_PFX_B64",
  "DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD",
  "DEVHUD_WINDOWS_CERTIFICATE_SHA256",
  "DEVHUD_WINDOWS_TIMESTAMP_URL",
  "DEVHUD_IOS_DISTRIBUTION_P12_B64",
  "DEVHUD_IOS_DISTRIBUTION_P12_PASSWORD",
  "DEVHUD_IOS_APP_PROFILE_B64",
  "DEVHUD_IOS_WIDGET_PROFILE_B64",
  "DEVHUD_IOS_WIDGET_INTENT_PROFILE_B64",
  "DEVHUD_APPLE_TEAM_ID",
  "DEVHUD_ANDROID_UPLOAD_KEYSTORE_B64",
  "DEVHUD_ANDROID_KEYSTORE_PASSWORD",
  "DEVHUD_ANDROID_KEY_ALIAS",
  "DEVHUD_ANDROID_KEY_PASSWORD",
  "DEVHUD_ANDROID_CERTIFICATE_SHA256",
]);

function json(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function cargoPackageVersion(path) {
  const source = readFileSync(path, "utf8");
  const packageSection = source.split(/^\[(?!package\]$)/mu, 1)[0];
  const version = packageSection.match(/^version\s*=\s*"([^"]+)"\s*$/mu)?.[1];
  if (!version) throw new Error(`unable to read package version from ${path}`);
  return version;
}

export function validateVersion(version) {
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.exec(version);
  if (!match) throw new Error("DevHud release version must be stable MAJOR.MINOR.PATCH SemVer");
  for (const component of match.slice(1).map(Number)) {
    if (!Number.isSafeInteger(component) || component > 65_535) {
      throw new Error("DevHud version components must fit Chrome's 0..65535 manifest range");
    }
  }
  return version;
}

export function validateStoreBuildNumber(value) {
  const text = String(value);
  if (!/^[1-9]\d*$/u.test(text)) throw new Error("store build number must be a positive decimal integer");
  const parsed = Number(text);
  if (!Number.isSafeInteger(parsed) || parsed > 2_100_000_000) throw new Error("store build number exceeds the supported range");
  return parsed;
}

export function loadReleaseMetadata(path = metadataPath) {
  const metadata = json(path);
  if (metadata.schemaVersion !== 1) throw new Error("unsupported DevHud release metadata schema");
  const version = validateVersion(metadata.version);
  const storeBuildNumber = validateStoreBuildNumber(metadata.storeBuildNumber);
  const releaseNotes = metadata.releaseNotes;
  for (const locale of ["en", "ko"]) {
    if (typeof releaseNotes?.[locale] !== "string" || releaseNotes[locale].trim() === "" || Buffer.byteLength(releaseNotes[locale]) > 32 * 1024) {
      throw new Error(`release note ${locale} must be nonempty and at most 32 KiB`);
    }
  }
  return { schemaVersion: 1, version, storeBuildNumber, releaseNotes: { en: releaseNotes.en, ko: releaseNotes.ko } };
}

export function sourceVersions(root = repositoryRoot) {
  return Object.freeze({
    appPackage: json(join(root, "apps/devhud/package.json")).version,
    extensionPackage: json(join(root, "apps/devhud-chrome-extension/package.json")).version,
    tauriConfig: json(join(root, "apps/devhud/src-tauri/tauri.conf.json")).version,
    tauriCargo: cargoPackageVersion(join(root, "apps/devhud/src-tauri/Cargo.toml")),
    nativeHostCargo: cargoPackageVersion(join(root, "crates/devhud-native-messaging-host/Cargo.toml")),
  });
}

export function validateSourceVersions(version, versions = sourceVersions()) {
  const mismatches = Object.entries(versions).filter(([, candidate]) => candidate !== version);
  if (mismatches.length > 0) {
    throw new Error(`DevHud release version ${version} does not match ${mismatches.map(([name, candidate]) => `${name}=${candidate}`).join(", ")}`);
  }
}

export function validateStoreBuildSources(storeBuildNumber, root = repositoryRoot) {
  const ios = json(join(root, "apps/devhud/src-tauri/tauri.ios.conf.json")).bundle?.iOS?.bundleVersion;
  const android = json(join(root, "apps/devhud/src-tauri/tauri.android.conf.json")).bundle?.android?.versionCode;
  if (ios !== String(storeBuildNumber) || android !== storeBuildNumber) {
    throw new Error(`DevHud store build ${storeBuildNumber} does not match iOS=${ios} and Android=${android}`);
  }
}

export function signingStatus(environment = process.env) {
  return signingInputs.map((name) => Object.freeze({ name, present: typeof environment[name] === "string" && environment[name] !== "" }));
}

export function validateSigningInputs(environment = process.env) {
  const missing = signingStatus(environment).filter(({ present }) => !present).map(({ name }) => name);
  if (missing.length > 0) throw new Error(`signed-private packaging requires signing configuration: ${missing.join(", ")}`);
}

export function releasePlan({ metadata = loadReleaseMetadata(), environment = process.env, signed = false } = {}) {
  validateSourceVersions(metadata.version);
  validateStoreBuildSources(metadata.storeBuildNumber);
  const trustRoot = json(join(repositoryRoot, "apps/devhud/updater-trust-root.json"));
  if (signed) {
    validateSigningInputs(environment);
    if (trustRoot.productionReady !== true) throw new Error("signed-private packaging requires the production-ready updater trust root");
  }
  const artifacts = Object.fromEntries(Object.entries(artifactGroups).map(([group, names]) => [group, [...names]]));
  return {
    schemaVersion: 1,
    project: "devhud",
    version: metadata.version,
    tag: `devhud@v${metadata.version}`,
    storeBuildNumber: metadata.storeBuildNumber,
    readiness: signed ? "private-signed-candidate" : "plan-only",
    publication: { pushesTag: false, createsRelease: false, submitsStores: false, pushesImages: false, deploys: false },
    releaseNotes: metadata.releaseNotes,
    artifacts,
    updaterTargets: updaterTargets.map((target) => ({ ...target, manifest: `updater/manifests/stable/${target.platform}/${target.architecture}/${target.packageKind}.json`, artifactSignature: `updater/signatures/stable/${target.platform}/${target.architecture}/${target.packageKind}.artifact.ed25519`, manifestSignature: `updater/signatures/stable/${target.platform}/${target.architecture}/${target.packageKind}.manifest.ed25519` })),
    signingMaterial: signingStatus(environment),
    updaterTrustRoot: { keyId: trustRoot.keyId, fingerprint: trustRoot.fingerprint, productionReady: trustRoot.productionReady === true },
  };
}

export function sha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

export function validateExtensionParity(artifactsDirectory) {
  const store = join(artifactsDirectory, "devhud-chrome-web-store.zip");
  const validation = join(artifactsDirectory, "devhud-chrome-github-validation.zip");
  if (sha256(store) !== sha256(validation)) throw new Error("Chrome Web Store and GitHub validation ZIPs are not byte-equivalent");
}

function parseArguments(arguments_) {
  const result = { command: arguments_[0] ?? "plan", signed: false, output: null };
  for (let index = 1; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (argument === "--signed") result.signed = true;
    else if (argument === "--output") result.output = arguments_[++index];
    else throw new Error(`unknown argument: ${argument}`);
  }
  if (result.command !== "plan") throw new Error(`unsupported command: ${result.command}`);
  if (result.output === undefined) throw new Error("--output requires a path");
  return result;
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parseArguments(arguments_);
  const serialized = `${JSON.stringify(releasePlan({ signed: options.signed }), null, 2)}\n`;
  if (options.output) writeFileSync(resolve(options.output), serialized);
  else process.stdout.write(serialized);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(`[devhud.release] ${error.message}`);
    process.exit(1);
  }
}
