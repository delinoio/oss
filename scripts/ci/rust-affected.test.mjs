import assert from "node:assert/strict";
import { renameSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { Event } from "./plan.mjs";
import { excludedRustPackages, runRustCheck, selectRustPackages } from "./rust-affected.mjs";
import { consumers, fixtureNames, rustFixture } from "./rust-affected-fixture.mjs";
const all = fixtureNames.filter(name => !excludedRustPackages.includes(name));
const select = (f, extra = {}) => selectRustPackages({ ...f, event: Event.Push, range: f.range(), ...extra });

test("independent edits select one package across a multi-commit push", t => {
  const f = rustFixture(t);
  f.write("unrelated/src/lib.rs", "pub fn value() -> u32 { 2 }\n"); f.commit();
  f.write("unrelated/fixture.txt", "second commit"); f.commit();
  const result = select(f, { run: (...args) => { assert.equal(args[3].RUST_LOG, "off"); return f.run(...args); } });
  assert.deepEqual(result.packages, ["unrelated"]); assert.equal(result.mode, "affected");
  assert.equal(f.git("worktree", "list", "--porcelain").split("worktree ").length, 2);
});
test("an initial empty Rust plan never prepares or invokes the selector", t => {
  const f = rustFixture(t);
  const result = select(f, { binary: undefined, plan: { jobs: { "rust-test": false, "rust-clippy": false }, forced: {} }, run: () => { throw new Error("unexpected invocation"); } });
  assert.deepEqual(result.packages, []); assert.equal(result.reason, "no-rust-jobs");
});
test("shared changes retain transitive, normal, optional, build, dev and target dependents", t => {
  const f = rustFixture(t); f.write("core-lib/fixture.txt", "change"); f.commit();
  assert.deepEqual(select(f).packages, [...consumers, "core-lib"].sort());
});
test("metadata lockfile mutation fails planning and cleans the worktree", t => {
  const f = rustFixture(t); f.write("unrelated/fixture.txt", "change"); f.commit();
  const run = (...args) => {
    const result = f.run(...args);
    if (args[1].includes("list")) writeFileSync(join(args[2], "Cargo.lock"), "mutated lockfile\n");
    return result;
  };
  assert.throws(() => select(f, { run }));
  assert.equal(f.git("worktree", "list", "--porcelain").split("worktree ").length, 2);
});
test("default AGENTS exclusion and scene exclusion produce authorized empty jobs", t => {
  for (const path of ["unrelated/AGENTS.md", "forge-scene/fixture.txt"]) {
    const f = rustFixture(t); f.write(path, "change"); f.commit();
    const result = select(f);
    assert.deepEqual(result.packages, []); assert.equal(result.jobs["rust-test"], false); assert.equal(result.jobs["rust-clippy"], false);
  }
});
test("deleted and renamed files select both owners with rename detection disabled", t => {
  const f = rustFixture(t); f.write("core-lib/old name.txt", "rename"); const base = f.commit();
  renameSync(join(f.cwd, "core-lib/old name.txt"), join(f.cwd, "unrelated/new name.txt")); f.commit();
  assert.deepEqual(select(f, { range: f.range(base) }).packages, [...consumers, "core-lib", "unrelated"].sort());
});
test("newline filenames cannot become an empty or incomplete affected set", t => {
  const f = rustFixture(t); f.write("unrelated/new\nname.txt", "change"); f.commit();
  const result = select(f); assert.equal(result.reason, "path-comparison"); assert.deepEqual(result.packages, all);
});
test("non-linear pushes select the full validation workspace", t => {
  const f = rustFixture(t); f.git("switch", "-c", "feature"); f.write("unrelated/feature.txt", "feature"); const head = f.commit();
  f.git("switch", "main"); f.write("core-lib/main.txt", "main"); const base = f.commit(); f.git("switch", "feature");
  const result = select(f, { range: f.range(base, head) }); assert.equal(result.reason, "non-linear-range"); assert.deepEqual(result.packages, all);
});
test("PR merge manifest differences select the merged workspace baseline", t => {
  const f = rustFixture(t); f.git("switch", "-c", "feature"); f.write("unrelated/feature.txt", "feature"); const head = f.commit();
  f.git("switch", "main"); f.write("core-lib/Cargo.toml", f.read("core-lib/Cargo.toml") + "\n# changed on main\n"); f.commit();
  f.git("merge", "--no-ff", "feature", "-m", "fixture merge");
  const result = select(f, { event: Event.PullRequest, range: f.range(f.base, head) }); assert.equal(result.reason, "merge-graph"); assert.deepEqual(result.packages, all);
});
for (const [path, reason] of [["Cargo.toml", "shared-input"], [".cargo/config.toml", "shared-input"], ["rust-toolchain.toml", "shared-input"], ["packages/devhud-api-client/fixture.txt", "external-input"]]) test(`${path} retains the full Rust baseline`, t => {
  const f = rustFixture(t); f.write(path, path === "Cargo.toml" ? f.read(path) + "\n# fixture\n" : "fixture"); f.commit();
  const result = select(f); assert.equal(result.reason, reason); assert.deepEqual(result.packages, all);
});
test("manual and forced CI retain full selection", t => {
  const f = rustFixture(t);
  assert.deepEqual(select(f, { event: Event.Manual }).packages, all);
  assert.equal(select(f, { plan: { ...f.plan, forced: { "rust-test": true } } }).reason, "forced");
});
test("pnport preload changes include the runtime test owner", t => {
  const f = rustFixture(t); f.write("pnport-preload/fixture.txt", "change"); f.commit();
  assert.deepEqual(select(f).packages, ["pnport", "pnport-preload"]);
});
for (const [name, update] of [
  ["unknown package", value => ({ ...value, packages: ["unknown"] })],
  ["invalid files", value => ({ ...value, files: "invalid" })],
  ["invalid base", value => ({ ...value, base_ref: "invalid" })],
  ["invalid JSON", () => { throw new SyntaxError("Invalid JSON"); }],
  ["command failure", () => { throw new Error("cargo failed"); }],
]) test(`${name} fails instead of skipping and cleans the worktree`, t => {
  const f = rustFixture(t); f.write("unrelated/fixture.txt", "change"); f.commit();
  const run = (...args) => args[1].includes("list") ? f.run(...args) : update(f.run(...args));
  assert.throws(() => select(f, { run }));
  assert.equal(f.git("worktree", "list", "--porcelain").split("worktree ").length, 2);
});
test("Rust checks preserve flags, literal arguments and Cargo failures", t => {
  const f = rustFixture(t);
  const stepSummary = join(f.cwd, "check-summary.md");
  for (const command of ["test", "clippy"]) {
    let invocation;
    const status = runRustCheck(command, ["app", "core-lib"], { cwd: f.cwd, stepSummary, run: (...args) => { invocation = args; return { status: 42 }; } });
    assert.equal(status, 42); assert.equal(invocation[0], "cargo"); assert.equal(invocation[2].shell, false);
    assert.deepEqual(invocation[1], [command, "--locked", "-p", "app", "-p", "core-lib", "--all-targets", ...(command === "clippy" ? ["--all-features", "--", "-D", "warnings"] : [])]);
    assert.match(f.read("check-summary.md"), new RegExp(`Command: .*cargo ${command} --locked -p app -p core-lib --all-targets`));
    assert.match(f.read("check-summary.md"), /Result: failure; exit status: 42/u);
  }
  assert.throws(() => runRustCheck("test", ["app"], { cwd: f.cwd, stepSummary, run: () => ({ error: new Error("spawn failed"), status: null }) }), /spawn failed/u);
  for (const packages of [[], ["--workspace"], ["forge-scene"], ["app", "app"]]) assert.throws(() => runRustCheck("test", packages));
});
