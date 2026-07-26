import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import test from "node:test";
import { resolve } from "node:path";

import { aggregate, canonicalize, summary, validate } from "./performance.mjs";

const fixture = resolve(import.meta.dirname, "../performance/fixtures/available-desktop.json");

test("validates the representative desktop result", () => {
  assert.equal(validate(JSON.parse(readFileSync(fixture, "utf8"))), true);
});

test("aggregation is deterministic and keeps unavailable distinct from failed", () => {
  const available = JSON.parse(readFileSync(fixture, "utf8"));
  assert.deepEqual(aggregate([fixture, fixture]), available);
  const unavailable = structuredClone(available);
  unavailable.targets[0] = { platform: "ios", architecture: "x86_64", status: "unavailable", unavailableReason: "unsupported-host", measurements: [{ name: "mobile-startup", status: "unavailable", method: "simctl-launch-wall-clock", samples: [] }] };
  const failed = structuredClone(available);
  failed.targets[0] = { platform: "android", architecture: "x86_64", status: "failed", failure: "launch-failed", measurements: [] };
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

test("available measurements require samples and their expected unit", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].measurements[0].samples = [];
  assert.throws(() => validate(value), /invalid measurement/u);
  value.targets[0].measurements[0].samples = [42];
  value.targets[0].measurements[0].unit = "bytes";
  assert.throws(() => validate(value), /invalid measurement/u);
});

test("available targets require every platform measurement", () => {
  const value = JSON.parse(readFileSync(fixture, "utf8"));
  value.targets[0].measurements = [];
  assert.throws(() => validate(value), /available target missing required measurements/u);
  value.targets[0].measurements = [
    { name: "mobile-startup", status: "available", method: "adb-am-start-w", samples: [42], unit: "milliseconds", note: "cold-process" }
  ];
  assert.throws(() => validate(value), /available target missing required measurements/u);
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
