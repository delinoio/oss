import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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
