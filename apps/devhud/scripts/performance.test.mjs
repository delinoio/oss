import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import test from "node:test";
import { resolve } from "node:path";

import { aggregate, canonicalize, desktopBuildFailed, installedAndroidArchitecture, packageMeasurement, profileDesktop, recordPackageProvenance, summary, validate } from "./performance.mjs";

const fixture = resolve(import.meta.dirname, "../performance/fixtures/available-desktop.json");

test("validates the representative desktop result", () => {
  assert.equal(validate(JSON.parse(readFileSync(fixture, "utf8"))), true);
});

test("package size requires provenance created for the selected artifact", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const artifact = resolve(directory, "DevHud_0.1.0_amd64.deb");
    writeFileSync(artifact, "current package");
    assert.equal(packageMeasurement(artifact).unavailableReason, "build-provenance-unverified");
    recordPackageProvenance(artifact);
    assert.equal(packageMeasurement(artifact).measurement.status, "available");
    writeFileSync(artifact, "stale package");
    assert.equal(packageMeasurement(artifact).unavailableReason, "build-provenance-unverified");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("package provenance rejects artifacts from another application version", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const artifact = resolve(directory, "DevHud_0.0.9_amd64.deb");
    writeFileSync(artifact, "stale package");
    assert.throws(() => recordPackageProvenance(artifact), /current host-architecture/u);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("malformed performance markers fail without waiting for the startup timeout", async () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const executable = resolve(directory, "malformed-marker.mjs");
    writeFileSync(executable, "#!/usr/bin/env node\nconsole.log('DEVHUD_PERF {not-json}')\nsetInterval(() => {}, 1_000)\n");
    chmodSync(executable, 0o755);
    const profile = await profileDesktop(executable, "cold-process");
    assert.equal(profile.failure, "measurement-protocol-failed");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("malformed performance markers after ready invalidate the complete profile", async () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const executable = resolve(directory, "late-malformed-marker.mjs");
    writeFileSync(executable, "#!/usr/bin/env node\nconsole.log('DEVHUD_PERF {\\\"event\\\":\\\"ready\\\",\\\"application\\\":{\\\"version\\\":\\\"0.1.0\\\",\\\"tauriRevision\\\":\\\"f49ebda2fdba5755456b0f049e32593ca0ea331a\\\",\\\"cefRevision\\\":\\\"tauri-runtime-cef@f49ebda2fdba5755456b0f049e32593ca0ea331a\\\"}}')\nconsole.log('DEVHUD_PERF {\\\"event\\\":\\\"hud-shown\\\",\\\"durationMs\\\":1}')\nsetTimeout(() => console.log('DEVHUD_PERF {not-json}'), 20)\nsetInterval(() => {}, 1_000)\n");
    chmodSync(executable, 0o755);
    const profile = await profileDesktop(executable, "cold-process");
    assert.equal(profile.failure, "measurement-protocol-failed");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("build failures produce valid desktop evidence", () => {
  assert.equal(validate(desktopBuildFailed()), true);
  const packageSize = { name: "desktop-package-size", status: "available", method: "artifact-byte-count", samples: [42], unit: "bytes", note: "packaged-artifact" };
  const evidence = desktopBuildFailed(packageSize);
  assert.equal(validate(evidence), true);
  assert.deepEqual(evidence.targets[0].measurements, [packageSize]);
});

test("desktop performance command owns its cross-platform build fallback", () => {
  const script = readFileSync(resolve(import.meta.dirname, "performance.mjs"), "utf8");
  const packageManifest = JSON.parse(readFileSync(resolve(import.meta.dirname, "../package.json"), "utf8"));
  assert.equal(packageManifest.scripts["perf:desktop"], "node scripts/performance.mjs desktop --build");
  assert.match(script, /process\.platform === "win32" \? "pnpm\.cmd" : "pnpm"/u);
  assert.match(script, /buildTimeoutMs = 15 \* 60_000/u);
  assert.match(script, /"build:desktop:performance"\], buildTimeoutMs, \{ stdio: "inherit" \}/u);
  assert.match(script, /DevHud desktop performance build failed/u);
  assert.doesNotMatch(script, /console\.error\(build\.(?:stdout|stderr)/u);
});

test("Android architecture comes from the installed application ABI", () => {
  assert.equal(installedAndroidArchitecture("primaryCpuAbi=armeabi-v7a"), "armv7");
  assert.equal(installedAndroidArchitecture("primaryCpuAbi=arm64-v8a"), "arm64");
  assert.equal(installedAndroidArchitecture("ro.product.cpu.abi=arm64-v8a"), null);
});

test("aggregation is deterministic and keeps unavailable distinct from failed", () => {
  const available = JSON.parse(readFileSync(fixture, "utf8"));
  assert.deepEqual(aggregate([fixture, fixture]), available);
  const unavailable = structuredClone(available);
  unavailable.targets[0] = { platform: "ios", architecture: "x86_64", targetKind: "ios-simulator", status: "unavailable", unavailableReason: "unsupported-host", measurements: [{ name: "mobile-startup", status: "unavailable", method: "simctl-launch-wall-clock", samples: [] }] };
  const failed = structuredClone(available);
  failed.targets[0] = { platform: "android", architecture: "x86_64", targetKind: "android-emulator", status: "failed", failure: "launch-failed", measurements: [] };
  const directory = resolve(import.meta.dirname, "../performance/fixtures");
  // aggregate consumes files, so these existing equivalent fixture paths exercise ordering separately below.
  const merged = { ...available, targets: [...available.targets, unavailable.targets[0], failed.targets[0]].sort((a, b) => `${a.platform}/${a.architecture}`.localeCompare(`${b.platform}/${b.architecture}`)) };
  assert.equal(validate(merged), true);
  assert.match(summary(merged), /unsupported-host/u);
  assert.match(summary(merged), /launch-failed/u);
  assert.deepEqual(canonicalize(merged), canonicalize(JSON.parse(JSON.stringify(merged))));
  assert.ok(directory);
});

test("redaction contract rejects no data by construction", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  assert.doesNotMatch(JSON.stringify(value), /(?:\/home\/|token|password|shortcut|search|diagnostic)/iu);
});

test("schema rejects fields that could carry raw host or user data", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].path = "/private/user";
  assert.throws(() => validate(value), /invalid target/u);
});

test("validation rejects arbitrary units and notes", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].measurements[0].unit = "arbitrary | injected";
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0].measurements[0].unit = "milliseconds";
  value.targets[0].measurements[0].note = "/home/user/token";
  assert.throws(() => validate(value), /invalid measurement/u);
});

test("validation rejects targetless evidence and mismatched measurement notes", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets = [];
  assert.throws(() => validate(value), /invalid performance result envelope/u);
  value.targets = JSON.parse(readFileSync(fixture, "utf8")).targets;
  value.targets[0].measurements[0].note = "warm-process";
  assert.throws(() => validate(value), /invalid measurement/u);
});

test("available measurements require samples and their expected unit", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].measurements[0].samples = [];
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0].measurements[0].samples = [42];
  value.targets[0].measurements[0].unit = "bytes";
  assert.throws(() => validate(value), /invalid measurement/u);
});

test("unavailable and failed measurements cannot retain availability data", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].status = "unavailable";
  value.targets[0].unavailableReason = "artifact-not-found";
  const measurement = value.targets[0].measurements[0];
  measurement.status = "unavailable";
  assert.throws(() => validate(value), /invalid measurement/u);
  measurement.samples = [];
  delete measurement.unit;
  delete measurement.note;
  assert.equal(validate(value), true);
});

test("validation rejects incompatible platform, status, and measurement combinations", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].architecture = "armv7";
  assert.throws(() => validate(value), /invalid target/u);
  value.targets[0].architecture = "x86_64";
  value.targets[0].failure = "launch-failed";
  assert.throws(() => validate(value), /status-specific fields/u);
  delete value.targets[0].failure;
  value.targets[0].measurements[0].method = "process-hud-marker";
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0] = { platform: "android", architecture: "armv7", targetKind: "android-device", status: "available", measurements: [{ name: "mobile-startup", status: "available", method: "simctl-launch-wall-clock", samples: [42], unit: "milliseconds", note: "cold-process" }] };
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0].measurements[0].method = "adb-am-start-w";
  value.targets[0].measurements[0].status = "bad | injected";
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0].measurements[0].status = "available";
  value.targets[0].measurements.push({ name: "desktop-package-size", status: "available", method: "artifact-byte-count", samples: [42], unit: "bytes", note: "packaged-artifact" });
  assert.throws(() => validate(value), /invalid measurement/u);
});

test("available targets require every platform measurement", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].measurements = [];
  assert.throws(() => validate(value), /available target missing required measurements/u);
  value.targets[0].measurements = [
    { name: "desktop-cold-startup", status: "available", method: "process-ready-marker", samples: [42], unit: "milliseconds", note: "cold-process" }
  ];
  assert.throws(() => validate(value), /available target missing required measurements/u);
});

test("validation binds iOS launch methods to their target kinds", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0] = { platform: "ios", architecture: "arm64", targetKind: "ios-simulator", status: "available", measurements: [{ name: "mobile-startup", status: "available", method: "devicectl-launch-wall-clock", samples: [42], unit: "milliseconds", note: "cold-process" }] };
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0].targetKind = "ios-device";
  assert.equal(validate(value), true);
});

test("unavailable targets cannot retain every required measurement", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].status = "unavailable";
  value.targets[0].unavailableReason = "artifact-not-found";
  assert.throws(() => validate(value), /unavailable target has complete measurements/u);
});

test("aggregation validates provenance and retains available duplicate targets", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const available = JSON.parse(readFileSync(fixture, "utf8"));
    const unavailable = structuredClone(available);
    unavailable.targets[0].status = "unavailable";
    unavailable.targets[0].unavailableReason = "artifact-not-found";
    unavailable.targets[0].measurements = [];
    const stale = structuredClone(available);
    stale.application.version = "9.9.9";
    const availableFile = resolve(directory, "available.json");
    const unavailableFile = resolve(directory, "unavailable.json");
    const staleFile = resolve(directory, "stale.json");
    writeFileSync(availableFile, JSON.stringify(available));
    writeFileSync(unavailableFile, JSON.stringify(unavailable));
    writeFileSync(staleFile, JSON.stringify(stale));
    assert.equal(aggregate([unavailableFile, availableFile]).targets[0].status, "available");
    assert.throws(() => aggregate([staleFile]), /invalid application provenance/u);
    assert.throws(() => aggregate([]), /at least one performance result/u);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation retains partial evidence when a target later fails", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const packaged = JSON.parse(readFileSync(fixture, "utf8"));
    packaged.targets[0].status = "unavailable";
    packaged.targets[0].unavailableReason = "no-display-server";
    packaged.targets[0].measurements = [packaged.targets[0].measurements.find((measurement) => measurement.name === "desktop-package-size")];
    const failed = structuredClone(packaged);
    failed.targets[0] = { platform: "linux", architecture: "x86_64", status: "failed", failure: "startup-timeout", measurements: [] };
    const packagedFile = resolve(directory, "packaged.json");
    const failedFile = resolve(directory, "failed.json");
    writeFileSync(packagedFile, JSON.stringify(packaged));
    writeFileSync(failedFile, JSON.stringify(failed));
    const merged = aggregate([packagedFile, failedFile]);
    assert.equal(merged.targets[0].status, "failed");
    assert.equal(merged.targets[0].measurements[0].name, "desktop-package-size");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation merges equal-status available targets independently of input order", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const first = JSON.parse(readFileSync(fixture, "utf8"));
    const second = structuredClone(first);
    second.targets[0].measurements[0].samples = [99];
    const firstFile = resolve(directory, "first.json");
    const secondFile = resolve(directory, "second.json");
    writeFileSync(firstFile, JSON.stringify(first));
    writeFileSync(secondFile, JSON.stringify(second));
    const forward = aggregate([firstFile, secondFile]);
    const reverse = aggregate([secondFile, firstFile]);
    assert.deepEqual(forward, reverse);
    assert.deepEqual(forward.targets[0].measurements.find((measurement) => measurement.name === "desktop-cold-startup").samples, [99, 120]);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation combines complementary unavailable evidence into an available target", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const runtime = JSON.parse(readFileSync(fixture, "utf8"));
    const packageOnly = structuredClone(runtime);
    for (const measurement of runtime.targets[0].measurements) if (measurement.name === "desktop-package-size") { measurement.status = "unavailable"; measurement.samples = []; delete measurement.unit; delete measurement.note; }
    for (const measurement of packageOnly.targets[0].measurements) if (measurement.name !== "desktop-package-size") { measurement.status = "unavailable"; measurement.samples = []; delete measurement.unit; delete measurement.note; }
    for (const value of [runtime, packageOnly]) {
      value.targets[0].status = "unavailable";
      value.targets[0].unavailableReason = "artifact-not-found";
    }
    const runtimeFile = resolve(directory, "runtime.json");
    const packageFile = resolve(directory, "package.json");
    writeFileSync(runtimeFile, JSON.stringify(runtime));
    writeFileSync(packageFile, JSON.stringify(packageOnly));
    const merged = aggregate([runtimeFile, packageFile]);
    assert.equal(merged.targets[0].status, "available");
    assert.equal(validate(merged), true);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation resolves mixed target status after combining all evidence", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const runtime = JSON.parse(readFileSync(fixture, "utf8"));
    const packageOnly = structuredClone(runtime);
    const failed = structuredClone(runtime);
    for (const measurement of runtime.targets[0].measurements) if (measurement.name === "desktop-package-size") { measurement.status = "unavailable"; measurement.samples = []; delete measurement.unit; delete measurement.note; }
    for (const measurement of packageOnly.targets[0].measurements) if (measurement.name !== "desktop-package-size") { measurement.status = "unavailable"; measurement.samples = []; delete measurement.unit; delete measurement.note; }
    for (const value of [runtime, packageOnly]) { value.targets[0].status = "unavailable"; value.targets[0].unavailableReason = "artifact-not-found"; }
    failed.targets[0] = { platform: "linux", architecture: "x86_64", status: "failed", failure: "startup-timeout", measurements: [] };
    const runtimeFile = resolve(directory, "runtime.json");
    const packageFile = resolve(directory, "package.json");
    const failedFile = resolve(directory, "failed.json");
    writeFileSync(runtimeFile, JSON.stringify(runtime));
    writeFileSync(packageFile, JSON.stringify(packageOnly));
    writeFileSync(failedFile, JSON.stringify(failed));
    const forward = aggregate([runtimeFile, failedFile, packageFile]);
    const reverse = aggregate([packageFile, failedFile, runtimeFile]);
    assert.deepEqual(forward, reverse);
    assert.equal(forward.targets[0].status, "available");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation combines complementary failed evidence into an available target", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const cold = JSON.parse(readFileSync(fixture, "utf8"));
    const warm = structuredClone(cold);
    cold.targets[0].status = "failed";
    cold.targets[0].failure = "startup-timeout";
    cold.targets[0].measurements = cold.targets[0].measurements.filter((measurement) => measurement.name !== "desktop-warm-startup");
    warm.targets[0].status = "failed";
    warm.targets[0].failure = "startup-exited";
    warm.targets[0].measurements = warm.targets[0].measurements.filter((measurement) => measurement.name !== "desktop-cold-startup");
    const coldFile = resolve(directory, "cold.json");
    const warmFile = resolve(directory, "warm.json");
    writeFileSync(coldFile, JSON.stringify(cold));
    writeFileSync(warmFile, JSON.stringify(warm));
    const merged = aggregate([coldFile, warmFile]);
    assert.equal(merged.targets[0].status, "available");
    assert.equal(validate(merged), true);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation keeps mobile target kinds separate", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const simulator = JSON.parse(readFileSync(fixture, "utf8"));
    simulator.targets[0] = { platform: "android", architecture: "arm64", targetKind: "android-emulator", status: "available", measurements: [{ name: "mobile-startup", status: "available", method: "adb-am-start-w", samples: [42], unit: "milliseconds", note: "cold-process" }] };
    const device = structuredClone(simulator);
    device.targets[0].targetKind = "android-device";
    device.targets[0].measurements[0] = { ...device.targets[0].measurements[0], samples: [24] };
    const simulatorFile = resolve(directory, "simulator.json");
    const deviceFile = resolve(directory, "device.json");
    writeFileSync(simulatorFile, JSON.stringify(simulator));
    writeFileSync(deviceFile, JSON.stringify(device));
    const forward = aggregate([simulatorFile, deviceFile]);
    const reverse = aggregate([deviceFile, simulatorFile]);
    assert.deepEqual(forward, reverse);
    assert.equal(validate(forward), true);
    assert.equal(forward.targets.length, 2);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("aggregation preserves independent repeated samples and reports even medians", () => {
  const directory = mkdtempSync(resolve(tmpdir(), "devhud-performance-"));
  try {
    const first = JSON.parse(readFileSync(fixture, "utf8"));
    const second = structuredClone(first);
    first.targets[0].measurements[0].samples = [80, 120];
    second.targets[0].measurements[0].samples = [120];
    const firstFile = resolve(directory, "first.json");
    const secondFile = resolve(directory, "second.json");
    writeFileSync(firstFile, JSON.stringify(first));
    writeFileSync(secondFile, JSON.stringify(second));
    const merged = aggregate([firstFile, secondFile]);
    assert.deepEqual(merged.targets[0].measurements[0].samples, [80, 120, 120]);
    const onlyFirst = aggregate([firstFile, firstFile]);
    assert.deepEqual(onlyFirst, first);
    assert.match(summary(first), /desktop-cold-startup: 100 milliseconds/u);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("validation rejects repeated metrics and only permits unknown architectures for unavailable evidence", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].measurements.push(structuredClone(value.targets[0].measurements[0]));
  assert.throws(() => validate(value), /duplicate measurement name/u);
  value.targets[0].measurements.pop();
  value.targets[0] = { platform: "ios", architecture: "unknown", targetKind: "ios-simulator", status: "unavailable", unavailableReason: "tool-not-installed", measurements: [{ name: "mobile-startup", status: "unavailable", method: "simctl-launch-wall-clock", samples: [] }] };
  assert.equal(validate(value), true);
  value.targets[0].status = "failed";
  delete value.targets[0].unavailableReason;
  value.targets[0].failure = "launch-failed";
  assert.throws(() => validate(value), /unknown architecture must be unavailable/u);
});

test("validation rejects duplicate platform and architecture target identities", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets.push(structuredClone(value.targets[0]));
  assert.throws(() => validate(value), /duplicate target identity/u);
});
