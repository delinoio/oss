// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const helper = fileURLToPath(new URL("./check-go-format.mjs", import.meta.url));
const formatted = "package fixture\n\nfunc good() {}\n";
const unformatted = "package fixture\nfunc bad(){println(\"fixture\")}\n";

function fixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), "go format paths "));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8" });
  git("init", "--quiet");
  const files = new Map();
  const track = (name, body) => {
    mkdirSync(dirname(join(cwd, name)), { recursive: true });
    writeFileSync(join(cwd, name), body);
    git("add", "--", name);
    files.set(name, body);
  };
  const run = (scopes = ["selected"], imports = []) => {
    const result = spawnSync(process.execPath, [...imports, helper, ...scopes], {
      cwd, encoding: "utf8", timeout: 30_000,
    });
    for (const [name, body] of files) assert.equal(readFileSync(join(cwd, name), "utf8"), body);
    assert.equal(result.error, undefined);
    return result;
  };
  return { cwd, git, track, run };
}

for (const quote of ["true", "false"]) {
  test(`checks non-ASCII tracked paths with core.quotePath=${quote}`, (t) => {
    const f = fixture(t);
    f.git("config", "core.quotePath", quote);
    f.track("selected/plain.go", formatted);
    f.track("selected/환영.go", unformatted);
    f.track("outside/ignored.go", unformatted);
    const failed = f.run();
    assert.equal(failed.status, 1);
    assert.ok(failed.stderr.includes("selected/환영.go"));
    assert.ok(!failed.stderr.includes("ignored.go"));
    f.track("selected/환영.go", formatted);
    const passed = f.run();
    assert.equal(passed.status, 0, passed.stderr);
    assert.match(passed.stderr, /verified 2 tracked Go files/);
  });
}

test("passes a newline filename intact to the real formatter", { skip: process.platform === "win32" }, (t) => {
  const f = fixture(t);
  const name = "selected/line\nbreak.go";
  f.track(name, unformatted);
  assert.equal(f.run().status, 1);
  f.track(name, formatted);
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stderr, /verified 1 tracked Go files/);
});

test("ordinary formatting failures and multiple scopes remain source preserving", (t) => {
  const f = fixture(t);
  f.track("selected/plain.go", unformatted);
  f.track("second/good.go", formatted);
  f.track("outside/ignored.go", unformatted);
  assert.equal(f.run(["selected", "second"]).status, 1);
  f.track("selected/plain.go", formatted);
  const result = f.run(["selected", "second"]);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stderr, /verified 2 tracked Go files/);
});

test("retains missing-scope and empty-inventory failures", (t) => {
  const f = fixture(t);
  assert.match(f.run([]).stderr, /at least one repository-relative Go scope/);
  const empty = f.run();
  assert.equal(empty.status, 1);
  assert.match(empty.stderr, /no tracked Go files/);
});

test("propagates formatter syntax errors", (t) => {
  const f = fixture(t);
  f.track("selected/broken.go", "package fixture\nfunc (\n");
  const result = f.run();
  assert.equal(result.status, 1);
  assert.match(result.stderr, /broken\.go/);
});

test("propagates Git discovery errors", (t) => {
  const f = fixture(t);
  rmSync(join(f.cwd, ".git"), { recursive: true, force: true });
  const result = f.run();
  assert.equal(result.status, 1);
  assert.match(result.stderr, /not a git repository/);
});

test("retains formatter startup failures and literal argv on every platform", (t) => {
  const f = fixture(t);
  f.track("selected/good.go", formatted);
  const preload = join(f.cwd, "capture.mjs");
  const output = join(f.cwd, "argv.json");
  const names = ["selected/환영.go", "selected/line\nbreak.go"];
  writeFileSync(preload, `
    import cp from "node:child_process";
    import { syncBuiltinESMExports } from "node:module";
    import { writeFileSync } from "node:fs";
    const original = cp.execFileSync;
    cp.execFileSync = (command, args, options) => args[0] === "ls-files"
      ? ${JSON.stringify(names.join("\0") + "\0")}
      : original(command, args, options);
    cp.spawnSync = (command, args) => {
      writeFileSync(${JSON.stringify(output)}, JSON.stringify({ command, args }));
      return { error: new Error("synthetic formatter unavailable") };
    };
    syncBuiltinESMExports();
  `);
  const result = f.run(["selected"], ["--import", preload]);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /synthetic formatter unavailable/);
  assert.deepEqual(JSON.parse(readFileSync(output, "utf8")), { command: "gofmt", args: ["-l", ...names] });
});
