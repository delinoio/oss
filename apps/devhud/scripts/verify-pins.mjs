#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import yaml from "js-yaml";

import { hasExactCspDirectiveSources } from "./frontend-output-policy.mjs";
import {
  validateCiTargetMatrix,
  validateResolvedDependencySources,
} from "./verify-pins-policy.mjs";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const pins = JSON.parse(readFileSync(join(appRoot, "cef-pins.json"), "utf8"));
const platforms = JSON.parse(readFileSync(join(appRoot, "platforms.json"), "utf8"));
const appCargo = readFileSync(join(appRoot, "src-tauri/Cargo.toml"), "utf8");
const rootCargo = readFileSync(join(repoRoot, "Cargo.toml"), "utf8");
const cargoLock = readFileSync(join(repoRoot, "Cargo.lock"), "utf8").replace(/\r\n?/gu, "\n");
const ciWorkflow = readFileSync(join(repoRoot, ".github/workflows/CI.yml"), "utf8");
const packageJson = JSON.parse(readFileSync(join(appRoot, "package.json"), "utf8"));
const pnpmLock = readFileSync(join(repoRoot, "pnpm-lock.yaml"), "utf8");
const tauriConfig = JSON.parse(readFileSync(join(appRoot, "src-tauri/tauri.conf.json"), "utf8"));
const tauriMain = readFileSync(join(appRoot, "src-tauri/src/main.rs"), "utf8");
const rsbuildConfig = readFileSync(join(appRoot, "rsbuild.config.ts"), "utf8");

const TAURI_REPOSITORY = "https://github.com/tauri-apps/tauri";
const TAURI_REVISION = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41";
const CRATES_IO_SOURCE = "registry+https://github.com/rust-lang/crates.io-index";
const TAURI_SOURCE = `git+${TAURI_REPOSITORY}?rev=${TAURI_REVISION}#${TAURI_REVISION}`;
const CANONICAL_CEF_RUST = {
  revision: "c73f792f245d71ac1716448cdb7c165c8009e20c",
  packages: {
    cef: {
      version: "150.0.0+150.0.10",
      checksum: "8dd6aaa08e30ced80c7c18445807984243a06b7f4004c264922302b7e05d5c41",
    },
    "cef-dll-sys": {
      version: "150.0.0+150.0.10",
      checksum: "d0ec349898441a7e9f91add53716d9c40f6fc381c9b41818241e4f63fb73f0b8",
    },
  },
};
const CANONICAL_DOWNLOAD_CEF = {
  version: "2.3.2",
  checksum: "c169adf067a787e1f1c58ed62906a557de85388bee4b54fb878b722ff606b113",
  revision: "0c577ce44dbd36952ac3721b577c6e423ceff44f",
};
const CANONICAL_CEF_ARCHIVES = {
  "aarch64-apple-darwin": {
    name: "cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_macosarm64_minimal.tar.bz2",
    sha1: "e73f7ce767420791b1965e15816a955d88cf1f9a",
  },
  "x86_64-apple-darwin": {
    name: "cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_macosx64_minimal.tar.bz2",
    sha1: "13e95f8bd0e13abe5283f67537d18b1b22f38ce7",
  },
  "aarch64-pc-windows-msvc": {
    name: "cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_windowsarm64_minimal.tar.bz2",
    sha1: "1e059f57e1f641a8925d140ae3724175605fb282",
  },
  "x86_64-pc-windows-msvc": {
    name: "cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_windows64_minimal.tar.bz2",
    sha1: "bce95ec52696c6725447fd0bf993cc928aefecd4",
  },
  "aarch64-unknown-linux-gnu": {
    name: "cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_linuxarm64_minimal.tar.bz2",
    sha1: "03e7a836ee73326280b8a3032e9741898133447e",
  },
  "x86_64-unknown-linux-gnu": {
    name: "cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_linux64_minimal.tar.bz2",
    sha1: "74a1186c566cbbac38c6b0f5298fc0bcfc1b9606",
  },
};

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

function packageBlock(name, version) {
  const escapedName = escapeRegExp(name);
  const escapedVersion = escapeRegExp(version);
  const pattern = new RegExp(
    `\\[\\[package\\]\\]\\nname = "${escapedName}"\\nversion = "${escapedVersion}"[\\s\\S]*?(?=\\n\\[\\[package\\]\\]|$)`,
    "u",
  );
  return cargoLock.match(pattern)?.[0] ?? "";
}

assert(pins.schemaVersion === 1, "unsupported CEF pin schema");
assert(pins.tauri.repository === TAURI_REPOSITORY, "Tauri repository is not authoritative");
assert(pins.tauri.revision === TAURI_REVISION, "Tauri revision changed");
assert(
  rootCargo.includes('"apps/devhud/src-tauri"'),
  "DevHUD Rust host is not a root Cargo workspace member",
);
assert(
  tauriMain.includes("#[tauri::cef_entry_point]"),
  "DevHUD must route CEF helper processes through the CEF entry point",
);
assert(tauriConfig.identifier === "io.delino.devhud", "application identifier changed");
assert(
  tauriConfig.bundle.macOS.minimumSystemVersion === "13.0",
  "macOS bundle minimum changed",
);
assert(
  tauriConfig.bundle.macOS.signingIdentity === "-",
  "macOS development bundle must be ad hoc signed",
);
assert(
  tauriConfig.bundle.macOS.hardenedRuntime === false,
  "macOS ad hoc bundle must not enable hardened runtime",
);
assert(tauriConfig.build.devUrl === "http://127.0.0.1:46305", "development origin changed");
assert(tauriConfig.build.frontendDist === "../dist", "bundled frontend path changed");
const productionCsp = tauriConfig.app.security.csp;
assert(
  hasExactCspDirectiveSources(productionCsp, "connect-src", ["'none'"]),
  "production connection CSP changed",
);
assert(
  hasExactCspDirectiveSources(productionCsp, "style-src", ["'self'"]),
  "production style CSP changed",
);
assert(
  rsbuildConfig.includes('"style-src \'self\' \'unsafe-inline\'"'),
  "development CSP does not permit injected styles",
);
assert(
  rsbuildConfig.includes('"connect-src ws://127.0.0.1:46305"'),
  "development CSP does not permit the fixed HMR endpoint",
);
assert(rsbuildConfig.includes('host: "127.0.0.1"'), "development host is not loopback-only");
assert(rsbuildConfig.includes("port: 46305"), "fixed development port changed");
assert(rsbuildConfig.includes("strictPort: true"), "strict development port failure is disabled");

const dependencyFiles = [appCargo, cargoLock, packageJson, pnpmLock].map((value) =>
  typeof value === "string" ? value : JSON.stringify(value),
);
const dependencyText = dependencyFiles.join("\n");
assert(!dependencyText.includes("feat/cef"), "moving feat/cef dependency detected");
assert(!/tauri[^\n]*branch\s*=/u.test(dependencyText), "branch-based Tauri dependency detected");
assert(
  !appCargo.includes("[patch."),
  "local Cargo patch declared by the DevHUD manifest",
);
assert(!dependencyText.includes("github.com/delinoio/tauri"), "Tauri fork dependency detected");

for (const dependency of ["tauri", "tauri-build", "tauri-cli"]) {
  const linePattern = new RegExp(
    `${escapeRegExp(dependency)}\\s*=\\s*\\{[^\\n]*git\\s*=\\s*"${escapeRegExp(TAURI_REPOSITORY)}"[^\\n]*rev\\s*=\\s*"${TAURI_REVISION}"`,
    "u",
  );
  assert(linePattern.test(appCargo), `${dependency} is not pinned to the required revision`);
}

for (const [name, version] of Object.entries(pins.tauri.packages)) {
  const block = packageBlock(name, version);
  assert(block, `${name} ${version} is absent from Cargo.lock`);
  assert(
    block.includes(`${TAURI_REPOSITORY}?rev=${TAURI_REVISION}#${TAURI_REVISION}`),
    `${name} did not resolve from the exact Tauri revision`,
  );
}

assert(pins.cefRust.revision === CANONICAL_CEF_RUST.revision, "CEF Rust revision changed");
assert(
  Object.keys(pins.cefRust).filter((name) => name !== "revision").length ===
    Object.keys(CANONICAL_CEF_RUST.packages).length,
  "CEF Rust package set changed",
);
for (const [name, canonical] of Object.entries(CANONICAL_CEF_RUST.packages)) {
  const record = pins.cefRust[name];
  assert(record?.version === canonical.version, `${name} version changed`);
  assert(record?.checksum === canonical.checksum, `${name} manifest checksum changed`);
  const block = packageBlock(name, canonical.version);
  assert(block, `${name} ${canonical.version} is absent from Cargo.lock`);
  assert(block.includes(`checksum = "${canonical.checksum}"`), `${name} lockfile checksum changed`);
}

assert(
  pins.downloadCef.revision === CANONICAL_DOWNLOAD_CEF.revision,
  "download-cef revision changed",
);
assert(
  pins.downloadCef.version === CANONICAL_DOWNLOAD_CEF.version,
  "download-cef version changed",
);
assert(
  pins.downloadCef.checksum === CANONICAL_DOWNLOAD_CEF.checksum,
  "download-cef manifest checksum changed",
);
const downloadBlock = packageBlock("download-cef", CANONICAL_DOWNLOAD_CEF.version);
assert(downloadBlock, "download-cef is absent from Cargo.lock");
assert(
  downloadBlock.includes(`checksum = "${CANONICAL_DOWNLOAD_CEF.checksum}"`),
  "download-cef lockfile checksum changed",
);

const cargoMetadataResult = spawnSync(
  "cargo",
  [
    "metadata",
    "--locked",
    "--format-version",
    "1",
    "--all-features",
    "--manifest-path",
    join(appRoot, "src-tauri/Cargo.toml"),
  ],
  {
    cwd: repoRoot,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
    shell: process.platform === "win32",
  },
);
assert(
  cargoMetadataResult.status === 0,
  cargoMetadataResult.error?.message ||
    cargoMetadataResult.stderr ||
    "Cargo dependency resolution failed",
);
const cargoMetadata = JSON.parse(cargoMetadataResult.stdout);
const devhudManifestPath = resolve(appRoot, "src-tauri/Cargo.toml");
const devhudPackage = cargoMetadata.packages.find(
  (pkg) => resolve(pkg.manifest_path) === devhudManifestPath,
);
assert(devhudPackage, "DevHUD is absent from Cargo metadata");
const dependencyClosure = validateResolvedDependencySources(
  cargoMetadata,
  devhudPackage.id,
  new Set([CRATES_IO_SOURCE, TAURI_SOURCE]),
);
for (const feature of pins.runtime.requiredFeatures) {
  const [crate, name] = feature.split("/");
  const featureResolved = [...dependencyClosure.packageIds].some((packageId) => {
    const pkg = dependencyClosure.packagesById.get(packageId);
    const node = dependencyClosure.nodesById.get(packageId);
    return pkg.name === crate && node.features.includes(name);
  });
  assert(
    featureResolved,
    `required production feature is not resolved: ${feature}`,
  );
}

const targetIds = new Set(platforms.targets.map(({ rustTarget }) => rustTarget));
assert(targetIds.size === 6, "desktop target definitions must contain exactly six targets");
assert(
  Object.keys(pins.runtime.archives).length === 6,
  "CEF pins must contain exactly six platform archives",
);
for (const [target, archive] of Object.entries(pins.runtime.archives)) {
  assert(targetIds.has(target), `CEF archive has no platform definition: ${target}`);
  const canonical = CANONICAL_CEF_ARCHIVES[target];
  assert(canonical, `CEF archive has no immutable verifier pin: ${target}`);
  assert(/^[a-f0-9]{40}$/u.test(archive.sha1), `invalid CEF archive SHA-1 for ${target}`);
  assert(archive.name === canonical.name, `CEF archive name changed for ${target}`);
  assert(archive.sha1 === canonical.sha1, `CEF archive SHA-1 changed for ${target}`);
  assert(archive.name.includes(pins.runtime.cefVersion), `CEF archive version mismatch for ${target}`);
}
for (const { id, os, arch, rustTarget, runner } of platforms.targets) {
  assert(pins.runtime.archives[rustTarget], `platform definition has no CEF archive: ${id}`);
  assert(["darwin", "win32", "linux"].includes(os), `invalid operating system for ${id}`);
  assert(["x64", "arm64"].includes(arch), `invalid architecture for ${id}`);
  assert(typeof runner === "string" && runner.length > 0, `missing native runner for ${id}`);
}
validateCiTargetMatrix(yaml.load(ciWorkflow), platforms.targets);

for (const [name, version] of Object.entries({
  "@rsbuild/core": "2.1.10",
  "@rsbuild/plugin-react": "2.0.1",
  "js-yaml": "4.3.1",
  react: "19.2.8",
  "react-dom": "19.2.8",
  typescript: "5.9.3",
})) {
  const actual = packageJson.dependencies?.[name] ?? packageJson.devDependencies?.[name];
  assert(actual === version, `${name} must be exactly ${version}`);
}
assert(pnpmLock.includes("apps/devhud:"), "DevHUD is absent from pnpm-lock.yaml");

console.log(`devhud: verified Tauri ${TAURI_REVISION} and six CEF platform pins`);
