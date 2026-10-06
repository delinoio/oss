#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import yaml from "js-yaml";
import { readDependency } from "../../../scripts/prebuilt-dependencies.mjs";

import { hasExactCspDirectiveSources } from "./frontend-output-policy.mjs";
import { createDevHudDevelopmentCsp } from "./development-csp.mjs";
import {
  validateCiTargetMatrix,
  validateDependencyPresence,
  validateResolvedDependencySources,
} from "./verify-pins-policy.mjs";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const pins = JSON.parse(readFileSync(join(appRoot, "cef-pins.json"), "utf8"));
const platforms = JSON.parse(readFileSync(join(appRoot, "platforms.json"), "utf8"));
const appCargo = readFileSync(join(appRoot, "src-tauri/Cargo.toml"), "utf8");
const rootCargo = readFileSync(join(repoRoot, "Cargo.toml"), "utf8").replace(/\r\n?/gu, "\n");
const cargoLock = readFileSync(join(repoRoot, "Cargo.lock"), "utf8").replace(/\r\n?/gu, "\n");
const ciWorkflow = readFileSync(join(repoRoot, ".github/workflows/CI.yml"), "utf8");
const ciMatrices = JSON.parse(readFileSync(join(repoRoot, "scripts/ci/native-matrices.json"), "utf8"));
const packageJson = JSON.parse(readFileSync(join(appRoot, "package.json"), "utf8"));
const pnpmLock = readFileSync(join(repoRoot, "pnpm-lock.yaml"), "utf8");
const tauriConfig = JSON.parse(readFileSync(join(appRoot, "src-tauri/tauri.conf.json"), "utf8"));
const desktopTauriConfig = JSON.parse(
  readFileSync(join(appRoot, "src-tauri/tauri.desktop.conf.json"), "utf8"),
);
const privateReleaseTauriConfig = JSON.parse(
  readFileSync(join(appRoot, "src-tauri/tauri.private-release.conf.json"), "utf8"),
);
const tauriMain = readFileSync(join(appRoot, "src-tauri/src/main.rs"), "utf8");
const nativeBridgeRust = readFileSync(join(appRoot, "src-tauri/src/bridge.rs"), "utf8");
const nativeBridgeTypeScript = readFileSync(join(appRoot, "src/native-bridge.ts"), "utf8");
const runtimeRevisionConsumers = [
  ["renderer diagnostics", readFileSync(join(appRoot, "src/diagnostics.ts"), "utf8")],
  ["API client validation", readFileSync(join(repoRoot, "packages/devhud-api-client/src/validation.ts"), "utf8")],
  ["API diagnostics validation", readFileSync(join(repoRoot, "servers/devhud-api/internal/rpc/diagnostics.go"), "utf8")],
];
const rsbuildConfig = readFileSync(join(appRoot, "rsbuild.config.ts"), "utf8");
const developmentCsp = createDevHudDevelopmentCsp("https://identity.example/oidc");
const updaterRoot = JSON.parse(readFileSync(join(appRoot, "updater-trust-root.json"), "utf8"));
const updaterRust = readFileSync(join(appRoot, "src-tauri/src/updater.rs"), "utf8");
const nativeMessagingLifecycle = readFileSync(join(appRoot, "src-tauri/windows/native-messaging-lifecycle.wxs"), "utf8");

const TAURI_REPOSITORY = "https://github.com/tauri-apps/tauri";
const TAURI_REVISION = "c8c75b1f7f43e7cb1e7d773ed2f6f96fad2fe975";
const CRATES_IO_SOURCE = "registry+https://github.com/rust-lang/crates.io-index";
const TAURI_SOURCE = `git+${TAURI_REPOSITORY}?rev=${TAURI_REVISION}#${TAURI_REVISION}`;
const CANONICAL_CEF_RUST = {
  revision: "5b1f7e0e4c1247ab1360a0af094f45df70c234c8",
  packages: {
    cef: {
      version: "151.8.1+151.3.24",
      checksum: "3d4fbc354286b45619122580b769f6cff53ed19565fe10359253ae4c235495db",
    },
    "cef-dll-sys": {
      version: "151.8.1+151.3.24",
      checksum: "d073a3cd9fb40877a1ae38560cf5df2bb4601e02198880b61ef5476a8e28c35e",
    },
  },
};
const CANONICAL_DOWNLOAD_CEF = {
  version: "2.3.2",
  checksum: "c169adf067a787e1f1c58ed62906a557de85388bee4b54fb878b722ff606b113",
  revision: "0c577ce44dbd36952ac3721b577c6e423ceff44f",
};
const CANONICAL_APPIMAGE_SHARUN = {
  repository: "https://github.com/pkgforge-dev/Anylinux-sharun",
  version: "3.0.0",
  assets: {
    arm64: {
      name: "sharun-aarch64",
      sha256: "414bf636f8a76d5144357e4e64f351f7518f5da727f1c9626c16c48150d8dcdc",
    },
    x64: {
      name: "sharun-x86_64",
      sha256: "2f1a73799dceb7f1b682118bf714308ba38a18e10ebdbdb09078dac9337508d8",
    },
  },
};
const CANONICAL_APPIMAGE_ANYLINUX = {
  "repository": "https://github.com/FabianLars/Anylinux-AppImages",
  "revision": "3e280d1b2270fecfcb2c2c823b490c782ecf1277",
  "source": {
    "path": "useful-tools/lib/anylinux.c",
    "sha256": "f50650ad96d177559bdd0737df509fa20bd4af08887631c738fc1f31bfac185f"
  },
  "license": {
    "path": "LICENSE",
    "sha256": "78a983481f226ae0ee56d72fb73a027ce51f72ff40ec8ab3b55e8866ca9f2f7f"
  }
};
const CANONICAL_CEF_ARCHIVES = {
  "aarch64-apple-darwin": {
    name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_macosarm64_minimal.tar.bz2",
    sha1: "82af2c0cadaafc4ad057f54c14cb3791cc139852",
  },
  "x86_64-apple-darwin": {
    name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_macosx64_minimal.tar.bz2",
    sha1: "0e3e0fcc35eeb13a8938763049845a8294cf34b9",
  },
  "aarch64-pc-windows-msvc": {
    name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_windowsarm64_minimal.tar.bz2",
    sha1: "2cfa69c278ffc2a202e3b6857207b2fe7192ebe1",
  },
  "x86_64-pc-windows-msvc": {
    name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_windows64_minimal.tar.bz2",
    sha1: "19ed00643db89ad6d04903b3bd0adec57627db17",
  },
  "aarch64-unknown-linux-gnu": {
    name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_linuxarm64_minimal.tar.bz2",
    sha1: "95acd2a46975e2c60afa6b427ec50c0a3be6236f",
  },
  "x86_64-unknown-linux-gnu": {
    name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_linux64_minimal.tar.bz2",
    sha1: "b1e99d3e3ff4213f99f7cda0211db89454398811",
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
  tauriMain.includes("#[tauri_runtime_cef::cef_entry_point]"),
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
assert(
  tauriConfig.bundle.externalBin === undefined,
  "shared desktop/mobile Tauri config must not require a desktop sidecar",
);
assert(
  JSON.stringify(desktopTauriConfig.bundle?.externalBin) ===
    JSON.stringify(["binaries/devhud-native-messaging-host"]),
  "desktop Native Messaging sidecar configuration changed",
);
assert(
  desktopTauriConfig.bundle?.windows?.nsis?.installerHooks === "./windows/hooks.nsh",
  "desktop Native Messaging removal hook changed",
);
assert(
  JSON.stringify(desktopTauriConfig.bundle?.windows?.wix?.fragmentPaths) ===
    JSON.stringify(["./windows/native-messaging-lifecycle.wxs"]),
  "desktop MSI must own the Native Messaging install and uninstall lifecycle",
);
assert(
  JSON.stringify(desktopTauriConfig.bundle?.windows?.wix?.componentRefs) ===
    JSON.stringify(["DevHudNativeMessagingLifecycle"]),
  "desktop MSI must link the Native Messaging lifecycle fragment",
);
assert(
  JSON.stringify(privateReleaseTauriConfig.bundle?.externalBin) ===
    JSON.stringify(["binaries/devhud-native-messaging-host"]),
  "private release must package the pinned Native Messaging sidecar",
);
assert(
  privateReleaseTauriConfig.bundle?.macOS?.signingIdentity === null &&
    privateReleaseTauriConfig.bundle?.macOS?.hardenedRuntime === true &&
    privateReleaseTauriConfig.bundle?.macOS?.entitlements === "Entitlements.release.plist",
  "private macOS release must use the production hardened-runtime signing path",
);
assert(
  privateReleaseTauriConfig.bundle?.windows?.signCommand ===
    "powershell -NoProfile -ExecutionPolicy Bypass -File windows/sign.ps1 %1",
  "private Windows release must fail closed through the repository-owned signer",
);
assert(
  JSON.stringify(privateReleaseTauriConfig.bundle?.windows?.wix?.fragmentPaths) ===
    JSON.stringify(["./windows/native-messaging-lifecycle.wxs"]),
  "private MSI must own the Native Messaging install and uninstall lifecycle",
);
assert(
  JSON.stringify(privateReleaseTauriConfig.bundle?.windows?.wix?.componentRefs) ===
    JSON.stringify(["DevHudNativeMessagingLifecycle"]),
  "private MSI must link the Native Messaging lifecycle fragment",
);
assert(
  nativeMessagingLifecycle.includes('Id="DevHudNativeMessagingLifecycle"') &&
    nativeMessagingLifecycle.includes('<DirectoryRef Id="TARGETDIR">') &&
    nativeMessagingLifecycle.includes('<Directory Id="LocalAppDataFolder">') &&
    nativeMessagingLifecycle.includes('Action="createAndRemoveOnUninstall"') &&
    nativeMessagingLifecycle.includes('Id="DevHudRemoveNativeMessagingManifest"'),
  "MSI Native Messaging lifecycle ownership changed",
);
assert(
  nativeMessagingLifecycle.includes('Id="DevHudRollbackNativeMessagingRegistration"') &&
    nativeMessagingLifecycle.includes('Execute="rollback"') &&
    nativeMessagingLifecycle.includes('<Custom Action="DevHudRollbackNativeMessagingRegistration" After="InstallFiles">NOT Installed</Custom>'),
  "private MSI Native Messaging registration rollback changed",
);
assert(
  nativeMessagingLifecycle.includes('Id="DevHudRegisterNativeMessaging"') &&
    nativeMessagingLifecycle.includes('Execute="deferred"') &&
    nativeMessagingLifecycle.includes('<Custom Action="DevHudRegisterNativeMessaging" After="DevHudRollbackNativeMessagingRegistration">NOT Installed</Custom>') &&
    !nativeMessagingLifecycle.includes('After="InstallFinalize"'),
  "private MSI Native Messaging registration must complete before install finalization",
);
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
  hasExactCspDirectiveSources(developmentCsp, "style-src", ["'self'", "'unsafe-inline'"]),
  "development CSP does not permit injected styles",
);
assert(
  hasExactCspDirectiveSources(developmentCsp, "connect-src", [
    "'self'",
    "http://127.0.0.1:46307",
    "https://devhud.api.delino.io",
    "https://api.github.com",
    "https://identity.example",
    "ws://127.0.0.1:46305",
  ]),
  "development connection CSP changed",
);
assert(
  rsbuildConfig.includes("createDevHudDevelopmentCsp") &&
    rsbuildConfig.includes("process.env.DEVHUD_LOGTO_ISSUER"),
  "development CSP is not bound to the validated issuer",
);
assert(rsbuildConfig.includes('host: "127.0.0.1"'), "development host is not loopback-only");
assert(rsbuildConfig.includes("port: 46305"), "fixed development port changed");
assert(rsbuildConfig.includes("strictPort: true"), "strict development port failure is disabled");

const dependencyFiles = [appCargo, cargoLock, packageJson, pnpmLock].map((value) =>
  typeof value === "string" ? value : JSON.stringify(value),
);
const dependencyText = dependencyFiles.join("\n");
assert(!dependencyText.includes("feat/cef"), "moving feat/cef dependency detected");
assert(!/(?:^|\n)tauri[\w-]*\s*=[^\n]*branch\s*=/u.test(dependencyText), "branch-based Tauri dependency detected");
assert(
  !appCargo.includes("[patch."),
  "local Cargo patch declared by the DevHUD manifest",
);
const rootPatchSections = rootCargo.match(/\[patch\.[^\]]+\]/gu) ?? [];
assert(
  rootPatchSections.length === 1 && rootPatchSections[0] === "[patch.crates-io]",
  "workspace Cargo patches must be limited to the official Tauri compatibility patch",
);
const rootPatch = rootCargo.match(/\[patch\.crates-io\]\n([\s\S]*?)(?=\n\[|$)/u)?.[1] ?? "";
const rootPatchEntries = rootPatch.split("\n").filter((line) => line.trim() && !line.trim().startsWith("#"));
assert(rootPatchEntries.length === 6, "official Tauri compatibility patch set changed");
for (const dependency of ["tauri", "tauri-build", "tauri-runtime", "tauri-plugin", "tauri-utils"]) {
  const linePattern = new RegExp(
    `^${escapeRegExp(dependency)}\\s*=\\s*\\{\\s*git\\s*=\\s*"${escapeRegExp(TAURI_REPOSITORY)}",\\s*rev\\s*=\\s*"${TAURI_REVISION}"\\s*\\}$`,
    "mu",
  );
  assert(linePattern.test(rootPatch), `${dependency} compatibility patch is not pinned to the required revision`);
}
const WINIT_SOURCE = "git+https://github.com/tauri-apps/winit-gtk4?branch=master#3d7c7cd19d3c752f75762ceea535444e63bdf8de";
assert(/^dpi = \{ git = "https:\/\/github\.com\/tauri-apps\/winit-gtk4", branch = "master" \}$/mu.test(rootPatch), "upstream GTK4 dpi compatibility patch changed");
assert(cargoLock.includes(`source = "${WINIT_SOURCE}"`) && !/winit-gtk4[^\n]*#(?!3d7c7cd19d3c752f75762ceea535444e63bdf8de)[a-f0-9]{40}/u.test(cargoLock), "GTK4 dependency revision changed");
const prebuiltCli = readDependency(repoRoot, "tauri-cli");
assert(prebuiltCli.source.repository === TAURI_REPOSITORY && prebuiltCli.source.revision === TAURI_REVISION, "prebuilt CLI source drifted");
assert(prebuiltCli.repository === "delinoio/prebuilt" && prebuiltCli.release === `tauri-cli-${TAURI_REVISION}-r1`, "prebuilt CLI release changed");
assert(prebuiltCli.toolchain === "nightly-2026-09-28" && Object.keys(prebuiltCli.assets).length === 6, "prebuilt CLI toolchain or hosts changed");
assert(!appCargo.includes("tauri-cli =") && !cargoLock.includes('name = "tauri-cli"'), "consumer must not compile the Tauri CLI");
assert(JSON.stringify(pins.runtime.sandboxPolicy) === JSON.stringify({ macos: "required", linux: "required", windows: "unsandboxedException" }), "desktop sandbox policy changed");
assert(tauriMain.includes("SandboxPolicy::Required") && tauriMain.includes("SandboxPolicy::Auto") && tauriMain.includes("SecretStorage::System"), "runtime sandbox or secret storage selection changed");
assert(!dependencyText.includes("github.com/delinoio/tauri"), "Tauri fork dependency detected");
assert(nativeBridgeRust.includes(TAURI_REVISION), "native runtime diagnostics Tauri revision drifted from the immutable pin");
assert(nativeBridgeRust.includes(pins.runtime.cefVersion), "native runtime diagnostics CEF revision drifted from the immutable pin");
assert(nativeBridgeTypeScript.includes('tauriRevision: ""'), "browser runtime diagnostics must not claim a Tauri revision");
assert(nativeBridgeTypeScript.includes('cefRevision: ""'), "browser runtime diagnostics must not claim a CEF revision");
for (const [consumer, source] of runtimeRevisionConsumers) {
  assert(source.includes(TAURI_REVISION), `${consumer} Tauri revision drifted from the immutable pin`);
  assert(source.includes(pins.runtime.cefVersion), `${consumer} CEF revision drifted from the immutable pin`);
}
assert(updaterRoot.keyId === "devhud-release-root-v1" && updaterRoot.algorithm === "ed25519", "desktop updater trust-root identity changed");
assert(updaterRust.includes(updaterRoot.publicKey) && updaterRust.includes(updaterRoot.fingerprint), "native updater trust root drifted from committed metadata");
assert(updaterRust.includes(`ROOT_PRODUCTION_READY: bool = ${String(updaterRoot.productionReady)}`), "native updater readiness gate drifted from committed metadata");
assert(/#\[cfg\(not\(test\)\)\][\s\S]{0,320}manifest_trust_root[\s\S]{0,320}ROOT_PUBLIC_KEY_BASE64[\s\S]{0,160}ROOT_FINGERPRINT[\s\S]{0,160}ROOT_PRODUCTION_READY/u.test(updaterRust), "non-test updater verification must inject the production trust root and readiness gate");
assert(/#\[cfg\(test\)\][\s\S]{0,320}manifest_trust_root[\s\S]{0,320}TEST_ROOT_PUBLIC_KEY_BASE64[\s\S]{0,160}TEST_ROOT_FINGERPRINT[\s\S]{0,160}ready: true/u.test(updaterRust), "native updater tests must inject a dedicated ready fixture root");
assert(updaterRust.includes("if !trust_root.ready"), "manifest verification must fail closed when its injected trust root is not ready");
assert(!updaterRust.includes("cfg!(test)"), "test builds must not bypass production trust-root readiness inline");
assert(updaterRust.includes("https://devhud.api.delino.io"), "native updater endpoint is not fixed");
assert(!updaterRust.toLowerCase().includes("bootstrap"), "bootstrap must not participate in desktop updater discovery");
assert(!tauriConfig.app.security.csp.includes("devhud.api.delino.io"), "frontend CSP must not receive updater network access");
assert(!updaterRust.includes("AUTHORIZATION") && !updaterRust.includes("COOKIE"), "desktop updater must not ship credential headers");
for (const [id, packageKind] of Object.entries({ "macos-x64": "macos-app", "macos-arm64": "macos-app", "windows-x64": "windows-nsis", "windows-arm64": "windows-nsis", "ubuntu-x64": "linux-deb", "ubuntu-arm64": "linux-deb" })) {
  const row = ciMatrices["devhud-desktop"].find((entry) => entry.id === id || entry.id === `${id}-nsis` || entry.id === `${id}-deb`);
  assert(row?.package === packageKind, `${id} updater package kind is not fixed`);
}
assert(ciWorkflow.includes("DEVHUD_PACKAGE_KIND: ${{ matrix.package }}"), "desktop package builds do not compile the installed package kind");

assert(appCargo.includes('tauri-plugin-deep-link = "=2.4.9"'), "desktop deep-link plugin version changed");
assert(appCargo.includes('tauri-plugin-single-instance = { version = "=2.4.3", features = ["deep-link"] }'), "desktop single-instance deep-link integration changed");

for (const dependency of ["tauri", "tauri-build", "tauri-runtime-cef", "tauri-runtime-wry"]) {
  const linePattern = new RegExp(
    `${escapeRegExp(dependency)}\\s*=\\s*\\{[^\\n]*git\\s*=\\s*"${escapeRegExp(TAURI_REPOSITORY)}"[^\\n]*rev\\s*=\\s*"${TAURI_REVISION}"`,
    "u",
  );
  assert(linePattern.test(appCargo), `${dependency} is not pinned to the required revision`);
}

for (const [name, version] of Object.entries(pins.tauri.packages)) {
  if (name === "tauri-cli") {
    assert(prebuiltCli.verificationOutput === `tauri-cli ${version}`, "prebuilt CLI version drifted");
    continue;
  }
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
assert(
  JSON.stringify(pins.appImage?.sharun) === JSON.stringify(CANONICAL_APPIMAGE_SHARUN),
  "AppImage sharun launcher pins changed",
);
assert(
  JSON.stringify(pins.appImage?.anylinux) === JSON.stringify(CANONICAL_APPIMAGE_ANYLINUX),
  "AppImage anylinux source and license pins changed",
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
const nativeMessagingHostManifestPath = resolve(
  repoRoot,
  "crates/devhud-native-messaging-host/Cargo.toml",
);
const nativeMessagingHostPackage = cargoMetadata.packages.find(
  (pkg) => resolve(pkg.manifest_path) === nativeMessagingHostManifestPath,
);
assert(nativeMessagingHostPackage, "DevHUD Native Messaging host is absent from Cargo metadata");
assert(
  nativeMessagingHostPackage.source === null,
  "DevHUD Native Messaging host must remain an exact local workspace package",
);
const dependencyClosure = validateResolvedDependencySources(
  cargoMetadata,
  devhudPackage.id,
  new Set([CRATES_IO_SOURCE, TAURI_SOURCE, WINIT_SOURCE]),
  new Set([nativeMessagingHostPackage.id]),
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
validateCiTargetMatrix(yaml.load(ciWorkflow), platforms.targets, ciMatrices);

for (const { os, rustTarget } of platforms.targets) {
  const targetMetadataResult = spawnSync(
    "cargo",
    [
      "metadata",
      "--format-version",
      "1",
      "--locked",
      "--all-features",
      "--filter-platform",
      rustTarget,
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
    targetMetadataResult.status === 0,
    targetMetadataResult.error?.message ||
      targetMetadataResult.stderr ||
      `Cargo dependency resolution failed for ${rustTarget}`,
  );
  const targetMetadata = JSON.parse(targetMetadataResult.stdout);
  const targetDevhud = targetMetadata.packages.find(
    (pkg) => resolve(pkg.manifest_path) === devhudManifestPath,
  );
  assert(targetDevhud, `DevHUD is absent from Cargo metadata for ${rustTarget}`);
  validateDependencyPresence(
    targetMetadata,
    targetDevhud.id,
    "rdev",
    os !== "darwin",
    rustTarget,
  );
}

for (const [name, version] of Object.entries({
  "@rsbuild/core": "2.1.10",
  "@rsbuild/plugin-react": "2.0.1",
  "js-yaml": "4.3.2",
  react: "19.2.8",
  "react-dom": "19.2.8",
  typescript: "5.9.3",
})) {
  const actual = packageJson.dependencies?.[name] ?? packageJson.devDependencies?.[name];
  assert(actual === version, `${name} must be exactly ${version}`);
}
assert(pnpmLock.includes("apps/devhud:"), "DevHUD is absent from pnpm-lock.yaml");

console.log(
  `devhud: verified Tauri ${TAURI_REVISION}, six CEF platform pins, and two AppImage launcher pins`,
);
