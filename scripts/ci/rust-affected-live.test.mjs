import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import test from "node:test";
import { Event } from "./plan.mjs";
import { runRustCheck, selectRustPackages } from "./rust-affected.mjs";
import { consumers, rustFixture } from "./rust-affected-fixture.mjs";

// Ordinary contract tests are offline. The changes job supplies the verified
// public executable and must execute these fixtures before package selection.
const binary = process.env.CARGO_MONO_TEST_BINARY;
test("published CLI selects every manifest dependency kind and runs only those targets", { skip: !binary }, t => {
  const f = rustFixture(t);
  assert.equal(execFileSync(binary, ["--version"], { encoding: "utf8" }).trim(), "cargo mono 0.6.9");
  f.write("core-lib/src/lib.rs", "pub fn value() -> u32 { 2 }\n"); f.commit();
  const result = selectRustPackages({ cwd: f.cwd, binary, event: Event.Push, range: f.range(), plan: f.plan });
  assert.deepEqual(result.packages, [...consumers, "core-lib"].sort());
  for (const command of ["test", "clippy"]) assert.equal(runRustCheck(command, result.packages, { cwd: f.cwd }), 0);
});
test("published CLI detects both rename owners, preload runtime ownership and empty selection", { skip: !binary }, t => {
  const f = rustFixture(t);
  const select = base => selectRustPackages({ cwd: f.cwd, binary, event: Event.Push, range: f.range(base), plan: f.plan });
  f.write("core-lib/old.txt", "move"); let base = f.commit();
  f.git("mv", "core-lib/old.txt", "unrelated/new.txt"); f.commit();
  assert.deepEqual(select(base).packages, [...consumers, "core-lib", "unrelated"].sort());
  base = f.git("rev-parse", "HEAD"); f.write("pnport-preload/fixture.txt", "runtime"); f.commit();
  assert.deepEqual(select(base).packages, ["pnport", "pnport-preload"]);
  base = f.git("rev-parse", "HEAD"); f.write("unrelated/AGENTS.md", "fixture"); f.commit();
  const empty = select(base); assert.deepEqual(empty.packages, []); assert.equal(empty.jobs["rust-test"], false);
});
