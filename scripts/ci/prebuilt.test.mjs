import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import yaml from "js-yaml";
import { prebuiltCache } from "../../.github/actions/setup-prebuilt/cache.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const read = path => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8");
const lock = JSON.parse(read("prebuilt-dependencies.lock.json"));
const workflow = path => yaml.load(read(`.github/${path}`));

test("CLI cache follows the execution host and both immutable digests", () => {
  for (const [target, asset] of Object.entries(lock.dependencies["tauri-cli"].assets)) {
    const options = { platform: asset.platform, arch: asset.arch, lock };
    const cache = prebuiltCache(root, "tauri-cli", options);
    assert.ok(cache.path.endsWith(target));
    for (const value of [target, asset.sha256, asset.binarySha256, lock.dependencies["tauri-cli"].release]) assert.ok(cache.key.includes(value));
    const changed = structuredClone(lock);
    changed.dependencies["tauri-cli"].assets[target].binarySha256 = "1".repeat(64);
    assert.notEqual(prebuiltCache(root, "tauri-cli", { ...options, lock: changed }).key, cache.key);
  }
});

test("native and mobile consumers restore/verify before building and save only successful main caches", () => {
  const action = workflow("actions/setup-prebuilt/action.yml");
  const steps = action.runs.steps;
  assert.ok(steps.find(step => step.id === "restore").uses.match(/@[a-f0-9]{40}$/u));
  assert.ok(steps.some(step => step.run === "node scripts/tauri-cli.mjs --print-path"));
  assert.ok(!steps.some(step => step.uses?.includes("cache/save")));
  const ci = workflow("workflows/CI.yml");
  for (const name of ["devhud-desktop", "devhud-ios-simulator", "devhud-android-emulator"]) {
    const job = ci.jobs[name];
    const prepare = job.steps.findIndex(step => step.uses === "./.github/actions/setup-prebuilt");
    const build = job.steps.findIndex(step => /mobile:generate|scripts\/run-tauri|pnpm.*devhud build|Build DevHUD/u.test(step.run ?? step.name ?? ""));
    assert.ok(prepare >= 0 && build > prepare, name);
    const save = job.steps.find(step => step.with?.key === "${{ steps.prebuilt.outputs.cache-key }}");
    assert.ok(save.if.includes("success()") && save.if.includes("refs/heads/main"));
  }
  const candidates = workflow("workflows/package-devhud-private.yml");
  for (const name of ["desktop", "ios-simulators", "mobile"]) assert.ok(candidates.jobs[name].steps.some(step => step.uses === "./.github/actions/setup-prebuilt"));
});

test("consumer manifests never compile a CLI wrapper and retain platform runtime isolation", () => {
  for (const app of ["devhud", "delidev"]) {
    const cargo = read(`apps/${app}/src-tauri/Cargo.toml`);
    assert.doesNotMatch(cargo, /tauri-cli|features = \["cef"|tauri\/cef/u);
    assert.match(cargo, /tauri-runtime-cef =/u);
    const native = read(`apps/${app}/src-tauri/src/main.rs`);
    assert.match(native, /SandboxPolicy::Auto/u);
    assert.match(native, /SandboxPolicy::Required/u);
    assert.match(native, /SecretStorage::System/u);
    assert.match(native, /#\[tauri_runtime_cef::cef_entry_point\]/u);
  }
  const cargo = read("apps/devhud/src-tauri/Cargo.toml");
  assert.match(cargo, /tauri-runtime-wry =/u);
  assert.match(cargo, /gtk = \{ package = "gtk4"/u);
  for (const app of ["devhud", "delidev"]) assert.match(read(`apps/${app}/src-tauri/Cargo.toml`), /"xdg-portal", "tokio"/u);
  assert.doesNotMatch(read("Cargo.lock"), /name = "tauri-cli"/u);
});


test("GTK4 desktop metadata and CI agree on the Ubuntu 24.04 native baseline", async () => {
  const platforms = JSON.parse(read("apps/devhud/platforms.json")).targets.filter(target => target.os === "linux");
  const matrix = JSON.parse(read("scripts/ci/native-matrices.json"))["devhud-desktop"].filter(target => target.os === "linux");
  const privateMatrix = workflow("workflows/package-devhud-private.yml").jobs.desktop.strategy.matrix.include.filter(target => target.id.startsWith("ubuntu-"));
  const { targets } = await import("../../apps/delidev/scripts/native-package.mjs");
  for (const platform of platforms) {
    assert.equal(platform.minimumVersion, "24.04");
    const runner = platform.arch === "arm64" ? "ubuntu-24.04-arm" : "ubuntu-24.04";
    assert.equal(platform.runner, runner);
    const id = platform.arch === "arm64" ? "ubuntu-arm64-" : "ubuntu-x64-";
    for (const entry of [...matrix, ...privateMatrix].filter(entry => entry.id.startsWith(id))) assert.equal(entry.runner, runner);
    assert.equal(targets.find(entry => entry.platform === "linux" && entry.arch === platform.arch).runner, runner);
  }
  assert.equal(workflow("workflows/CI.yml").jobs["devhud-rust-conformance"]["runs-on"], "ubuntu-24.04");
});


test("generated mobile child commands resolve the verified CLI and preserve the caller environment", async () => {
  const { tauriEnvironment } = await import("../tauri-cli.mjs");
  const environment = { PATH: "/cargo/bin:/usr/bin", TAURI_ENV_PLATFORM: "ios" };
  assert.deepEqual(tauriEnvironment("/cache/pinned/bin/cargo-tauri", environment, "darwin"), { PATH: "/cache/pinned/bin:/cargo/bin:/usr/bin", TAURI_ENV_PLATFORM: "ios" });
  assert.equal(environment.PATH, "/cargo/bin:/usr/bin");
  const windows = tauriEnvironment(String.raw`C:\cache\bin\cargo-tauri.exe`, { Path: String.raw`C:\cargo\bin`, PATH: "shadow", SystemRoot: "system" }, "win32");
  assert.equal(windows.Path, String.raw`C:\cache\bin;C:\cargo\bin`);
  assert.equal(windows.PATH, undefined);
  assert.equal(windows.SystemRoot, "system");
  assert.equal(tauriEnvironment("/cache/bin/cargo-tauri", {}, "linux").PATH, "/cache/bin");
});

test("Linux GUI smokes use an explicit tray fixture and reserve AppImage environment for AppImages", () => {
  const publicJob = workflow("workflows/CI.yml").jobs["devhud-desktop"];
  const smoke = publicJob.steps.find(step => step.name === "Run Ubuntu X11 platform smoke").run;
  assert.match(smoke, /unset APPIMAGE APPDIR/u);
  assert.match(smoke, /with-linux-smoke-tray\.py pnpm/u);
  const privateJob = workflow("workflows/package-devhud-private.yml").jobs.desktop;
  const commands = privateJob.steps.map(step => step.run ?? "").join("\n");
  assert.equal(commands.match(/with-linux-smoke-tray\.py pnpm/gu)?.length, 2);
  for (const job of [publicJob, privateJob]) {
    const commands = job.steps.map(step => step.run ?? "").join("\n");
    for (const requirement of ["python3-dbus", "python3-gi", "-rwsr-xr-x 0/0"]) assert.ok(commands.includes(requirement), requirement);
  }
});

test("Linux Debian packages install the portal and confirmation dialog backends", () => {
  for (const config of ["apps/devhud/src-tauri/tauri.desktop.conf.json", "apps/delidev/src-tauri/tauri.conf.json"]) {
    const dependencies = JSON.parse(read(config)).bundle.linux.deb.depends;
    assert.ok(dependencies.includes("xdg-desktop-portal"));
    assert.ok(dependencies.includes("zenity"));
    assert.ok(dependencies.some(value => value.includes("xdg-desktop-portal-gtk")));
  }
});

test("shared AppImage helper changes select both the native consumer and its wrapper tests", async () => {
  const { planJobs, Event } = await import("./plan.mjs");
  const selected = planJobs(Event.Push, ["scripts/appimage-tools.mjs"]).jobs;
  assert.equal(selected["devhud-desktop"], true);
  assert.equal(selected["devhud-frontend"], true);
});
