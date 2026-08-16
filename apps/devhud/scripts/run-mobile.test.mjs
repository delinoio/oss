import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { mobileCargoArguments, mobileExecution, preserveAndroidArtifacts } from "./run-mobile.mjs";

test("builds only contracted mobile commands and architectures", () => {
  assert.deepEqual(mobileCargoArguments(["ios", "build", "--target", "x86_64"]).slice(-4), ["ios", "build", "--target", "x86_64"]);
  assert.deepEqual(mobileCargoArguments(["android", "build", "--target", "armv7"]).slice(-4), ["android", "build", "--target", "armv7"]);
  assert.deepEqual(mobileCargoArguments(["android", "build", "--target=x86_64"]).slice(-3), ["android", "build", "--target=x86_64"]);
  assert.throws(() => mobileCargoArguments(["android", "build", "--target", "i686"]), /unsupported android target/u);
  assert.throws(() => mobileCargoArguments(["android", "build", "--target=i686"]), /unsupported android target/u);
  assert.throws(() => mobileCargoArguments(["desktop", "build"]), /Usage/u);
});

test("does not permit callers to replace pinned platform configuration", () => {
  assert.throws(() => mobileCargoArguments(["ios", "build", "--config", "other.json"]), /overrides are not allowed/u);
});

test("builds the Intel iOS simulator through the generated simulator workspace", () => {
  const execution = mobileExecution(["ios", "build", "--target", "x86_64", "--ci", "--no-sign"]);
  assert.equal(execution.command, "xcodebuild");
  assert.deepEqual(execution.prerequisites, [{ command: "pnpm", arguments: ["build:frontend"] }]);
  assert.deepEqual(execution.arguments, [
    "-workspace", "src-tauri/gen/apple/devhud.xcworkspace",
    "-scheme", "devhud_iOS",
    "-sdk", "iphonesimulator",
    "-configuration", "release",
    "-destination", "generic/platform=iOS Simulator",
    "ARCHS=x86_64",
    "CODE_SIGNING_ALLOWED=NO",
    "build",
  ]);
  assert.equal(mobileExecution(["ios", "build", "--target", "aarch64-sim", "--ci", "--no-sign"]).command, "cargo");
});

test("preserves each Android target's requested artifacts outside generated output", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-mobile-artifacts-"));
  const appRoot = join(root, "apps/devhud");
  const apk = join(appRoot, "src-tauri/gen/android/app/build/outputs/apk/universal/release/app.apk");
  const aab = join(appRoot, "src-tauri/gen/android/app/build/outputs/bundle/universalRelease/app.aab");
  mkdirSync(join(apk, ".."), { recursive: true });
  mkdirSync(join(aab, ".."), { recursive: true });
  writeFileSync(apk, "apk-arm64");
  writeFileSync(aab, "aab-arm64");
  try {
    preserveAndroidArtifacts("aarch64", ["--apk", "--aab"], { appRoot, repoRoot: root });
    assert.equal(readFileSync(join(root, "target/devhud-mobile/android/aarch64/aarch64-app.apk"), "utf8"), "apk-arm64");
    assert.equal(readFileSync(join(root, "target/devhud-mobile/android/aarch64/aarch64-app.aab"), "utf8"), "aab-arm64");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
