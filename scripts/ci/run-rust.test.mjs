// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { rustTask } from "./run-rust.mjs";

const manifest = JSON.parse(readFileSync(new URL("./package.json", import.meta.url), "utf8"));
const config = JSON.parse(readFileSync(new URL("./turbo.json", import.meta.url), "utf8"));

test("selected Rust owners have exactly their required Turbo prerequisite graph", () => {
  const owners = ["devhud", "delidev-desktop", "pnport"];
  for (const command of ["test", "clippy"]) {
    for (let mask = 0; mask < 8; mask++) {
      const packages = ["cargo-mono", ...owners.filter((_, index) => mask & (1 << index))];
      const name = rustTask(command, packages);
      const task = config.tasks[name];
      assert.equal(manifest.scripts[name], `node from-root.mjs node scripts/ci/rust-affected.mjs ${command}`);
      assert.equal(task.cache, false);
      assert.ok(task.passThroughEnv.includes("RUST_PACKAGES"));
      assert.ok(task.passThroughEnv.includes("GITHUB_STEP_SUMMARY"));
      const expected = [
        ...(packages.includes("devhud") ? ["devhud#ci:build:frontend"] : []),
        ...(packages.includes("delidev-desktop") ? ["delidev-desktop#build:frontend", "delidev-desktop#prepare:sidecar"] : []),
        ...(command === "test" && packages.includes("pnport") ? ["ci:pnport:companion"] : []),
      ];
      assert.deepEqual(task.dependsOn, expected, name);
    }
  }
});

test("invalid, excluded and empty Rust selections cannot start a task", () => {
  for (const packages of [[], ["forge-scene"], ["pnport", "pnport"], ["--workspace"]]) {
    assert.throws(() => rustTask("test", packages));
  }
  assert.throws(() => rustTask("fmt", ["cargo-mono"]));
});
