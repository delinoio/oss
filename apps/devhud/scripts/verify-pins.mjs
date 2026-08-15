#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(appRoot, "../..");
const pins = JSON.parse(readFileSync(join(appRoot, "cef-pins.json"), "utf8"));
const platforms = JSON.parse(readFileSync(join(appRoot, "platforms.json"), "utf8"));
const appCargo = readFileSync(join(appRoot, "src-tauri/Cargo.toml"), "utf8");
const rootCargo = readFileSync(join(repoRoot, "Cargo.toml"), "utf8");
const cargoLock = readFileSync(join(repoRoot, "Cargo.lock"), "utf8");
const desktopWorkflow = readFileSync(join(repoRoot, ".github/workflows/devhud-desktop.yml"), "utf8");
const packageJson = JSON.parse(readFileSync(join(appRoot, "package.json"), "utf8"));
const pnpmLock = readFileSync(join(repoRoot, "pnpm-lock.yaml"), "utf8");
const tauriConfig = JSON.parse(readFileSync(join(appRoot, "src-tauri/tauri.conf.json"), "utf8"));
const rsbuildConfig = readFileSync(join(appRoot, "rsbuild.config.ts"), "utf8");

const TAURI_REPOSITORY = "https://github.com/tauri-apps/tauri";
const TAURI_REVISION = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41";

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
assert(tauriConfig.identifier === "io.delino.devhud", "application identifier changed");
assert(tauriConfig.build.devUrl === "http://127.0.0.1:46305", "development origin changed");
assert(tauriConfig.build.frontendDist === "../dist", "bundled frontend path changed");
assert(tauriConfig.app.security.csp.includes("connect-src 'none'"), "local-only CSP changed");
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
  !appCargo.includes("[patch.") && !rootCargo.includes("[patch."),
  "local Cargo patch detected in the DevHUD dependency graph",
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

for (const [name, record] of Object.entries(pins.cefRust).filter(([name]) => name !== "revision")) {
  const block = packageBlock(name, record.version);
  assert(block, `${name} ${record.version} is absent from Cargo.lock`);
  assert(block.includes(`checksum = "${record.checksum}"`), `${name} checksum changed`);
}

const downloadBlock = packageBlock("download-cef", pins.downloadCef.version);
assert(downloadBlock, "download-cef is absent from Cargo.lock");
assert(
  downloadBlock.includes(`checksum = "${pins.downloadCef.checksum}"`),
  "download-cef checksum changed",
);

const cargoFeatures = spawnSync(
  "cargo",
  ["tree", "--locked", "-p", "devhud", "-e", "features"],
  { cwd: repoRoot, encoding: "utf8", shell: process.platform === "win32" },
);
assert(cargoFeatures.status === 0, cargoFeatures.stderr || "cargo feature resolution failed");
for (const feature of pins.runtime.requiredFeatures) {
  const [crate, name] = feature.split("/");
  assert(
    cargoFeatures.stdout.includes(`${crate} feature "${name}"`),
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
  assert(/^[a-f0-9]{40}$/u.test(archive.sha1), `invalid CEF archive SHA-1 for ${target}`);
  assert(archive.name.includes(pins.runtime.cefVersion), `CEF archive version mismatch for ${target}`);
}
for (const { id, os, arch, rustTarget, runner } of platforms.targets) {
  assert(pins.runtime.archives[rustTarget], `platform definition has no CEF archive: ${id}`);
  assert(["darwin", "win32", "linux"].includes(os), `invalid operating system for ${id}`);
  assert(["x64", "arm64"].includes(arch), `invalid architecture for ${id}`);
  assert(typeof runner === "string" && runner.length > 0, `missing native runner for ${id}`);
  assert(desktopWorkflow.includes(`- id: ${id}`), `native CI matrix is missing ${id}`);
  assert(desktopWorkflow.includes(`runner: ${runner}`), `native CI matrix is missing runner ${runner}`);
}

for (const [name, version] of Object.entries({
  "@rsbuild/core": "2.1.10",
  "@rsbuild/plugin-react": "2.0.1",
  react: "19.2.8",
  "react-dom": "19.2.8",
  typescript: "5.9.3",
})) {
  const actual = packageJson.dependencies?.[name] ?? packageJson.devDependencies?.[name];
  assert(actual === version, `${name} must be exactly ${version}`);
}
assert(pnpmLock.includes("apps/devhud:"), "DevHUD is absent from pnpm-lock.yaml");

console.log(`devhud: verified Tauri ${TAURI_REVISION} and six CEF platform pins`);
