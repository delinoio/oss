import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import yaml from "js-yaml";
import { prebuiltCache } from "../../.github/actions/setup-prebuilt/cache.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const read = (path) =>
  readFileSync(new URL(`../../${path}`, import.meta.url), "utf8");
const lock = JSON.parse(read("prebuilt-dependencies.lock.json"));
const workflow = (path) => yaml.load(read(`.github/${path}`));

test("CLI cache follows the execution host and both immutable digests", () => {
  for (const [target, asset] of Object.entries(
    lock.dependencies["tauri-cli"].assets,
  )) {
    const options = { platform: asset.platform, arch: asset.arch, lock };
    const cache = prebuiltCache(root, "tauri-cli", options);
    assert.ok(cache.path.endsWith(target));
    for (const value of [
      target,
      asset.sha256,
      asset.binarySha256,
      lock.dependencies["tauri-cli"].release,
    ])
      assert.ok(cache.key.includes(value));
    const changed = structuredClone(lock);
    changed.dependencies["tauri-cli"].assets[target].binarySha256 = "1".repeat(
      64,
    );
    assert.notEqual(
      prebuiltCache(root, "tauri-cli", { ...options, lock: changed }).key,
      cache.key,
    );
  }
});

test("generated mobile child commands resolve the verified CLI and preserve the caller environment", async () => {
  const { tauriEnvironment } = await import("../tauri-cli.mjs");
  const environment = {
    PATH: "/cargo/bin:/usr/bin",
    TAURI_ENV_PLATFORM: "ios",
  };
  assert.deepEqual(
    tauriEnvironment("/cache/pinned/bin/cargo-tauri", environment, "darwin"),
    {
      PATH: "/cache/pinned/bin:/cargo/bin:/usr/bin",
      TAURI_ENV_PLATFORM: "ios",
    },
  );
  assert.equal(environment.PATH, "/cargo/bin:/usr/bin");
  const windows = tauriEnvironment(
    String.raw`C:\cache\bin\cargo-tauri.exe`,
    { Path: String.raw`C:\cargo\bin`, PATH: "shadow", SystemRoot: "system" },
    "win32",
  );
  assert.equal(windows.Path, String.raw`C:\cache\bin;C:\cargo\bin`);
  assert.equal(windows.PATH, undefined);
  assert.equal(windows.SystemRoot, "system");
  assert.equal(
    tauriEnvironment("/cache/bin/cargo-tauri", {}, "linux").PATH,
    "/cache/bin",
  );
});
