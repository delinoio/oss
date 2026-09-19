import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const script = join(root, "scripts/ci/run-affected.mjs");

test("committed Turbo honors the exact comparison, empty scopes, external forcing, and task failures", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "ci-turbo-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const write = (path, body) => { mkdirSync(dirname(join(cwd, path)), { recursive: true }); writeFileSync(join(cwd, path), body); };
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  const commit = () => { git("add", "--all"); git("commit", "-m", "fixture"); return git("rev-parse", "HEAD"); };
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  write("package.json", JSON.stringify({ name: "ci-fixture", private: true, packageManager: "pnpm@10.26.2" }));
  write("pnpm-workspace.yaml", "packages:\n  - apps/*\n");
  write("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  apps/first: {}\n  apps/second: {}\n");
  write(".gitignore", "node_modules\n.turbo\nran\n");
  write("turbo.json", JSON.stringify({ tasks: { test: { cache: false }, fail: { cache: false } } }));
  for (const name of ["first", "second"]) {
    write(`apps/${name}/package.json`, JSON.stringify({ name, scripts: { test: "node test.cjs", fail: "node -e 'process.exit(7)'" } }));
    write(`apps/${name}/test.cjs`, "require('node:fs').writeFileSync('ran', 'ok');\n");
  }
  const base = commit();
  write("apps/first/source.js", "// changed workspace\n");
  const head = commit();
  symlinkSync(join(root, "node_modules"), join(cwd, "node_modules"), "dir");
  const run = (workspace, task, forced = "false", extra = {}) => spawnSync(process.execPath, [script, workspace, task], {
    cwd, encoding: "utf8", env: { ...process.env, FORCE_RUN: forced, TURBO_SCM_BASE: base, TURBO_SCM_HEAD: head, TURBO_TELEMETRY_DISABLED: "1", ...extra },
  });
  let result = run("second", "test");
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.equal(existsSync(join(cwd, "apps/second/ran")), false);
  result = run("first", "test");
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.equal(existsSync(join(cwd, "apps/first/ran")), true);
  result = run("second", "test", "true");
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.equal(existsSync(join(cwd, "apps/second/ran")), true);
  assert.notEqual(run("first", "fail").status, 0);
  assert.notEqual(run("first", "test", "false", { TURBO_SCM_BASE: "" }).status, 0);
});
