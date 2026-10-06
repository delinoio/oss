import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { cachePolicy } from "./cache-context.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));

test("remote writes belong to authenticated main runs, while PRs read and missing auth stays local", () => {
  const trusted = { CI: "true", TURBO_TOKEN: "fixture", TURBO_TEAM: "delino", TURBO_REMOTE_CACHE_AUTH: "true" };
  for (const event of ["push", "workflow_dispatch"]) assert.equal(cachePolicy({ ...trusted, GITHUB_REF: "refs/heads/main", GITHUB_EVENT_NAME: event }), "local:rw,remote:rw");
  for (const event of ["pull_request", "workflow_dispatch", "push"]) assert.equal(cachePolicy({ ...trusted, GITHUB_REF: "refs/heads/feature", GITHUB_EVENT_NAME: event }), "local:rw,remote:r");
  assert.equal(cachePolicy({ ...trusted, TURBO_REMOTE_CACHE_AUTH: "false" }), "local:rw");
  assert.equal(cachePolicy({ ...trusted, TURBO_TOKEN: "" }), "local:rw");
  assert.equal(cachePolicy({}), undefined);
});

test("cold/warm runs restore output, invalidate owned inputs/options/tools/dependencies, and still execute native/freshness failures", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "ci-cache-contract-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const write = (path, data) => { mkdirSync(dirname(join(cwd, path)), { recursive: true }); writeFileSync(join(cwd, path), data); };
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  git("init", "-b", "main"); git("config", "user.name", "CI Fixture"); git("config", "user.email", "ci@example.invalid"); git("config", "commit.gpgsign", "false"); git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  write("package.json", JSON.stringify({ name: "fixture", private: true, packageManager: "pnpm@10.26.2" }));
  write("pnpm-workspace.yaml", "packages:\n  - packages/*\n");
  write("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  packages/data: {}\n");
  write(".nvmrc", "24\n");
  write(".gitignore", "node_modules\n.turbo\n**/dist\n**/ran\n**/native-ran\n**/fresh-ran\n**/fresh-input\n");
  write("turbo.json", JSON.stringify({ tasks: {
    build: { outputs: ["dist/**"], inputs: ["$TURBO_DEFAULT$", "$TURBO_ROOT$/.nvmrc"], env: ["CI_CACHE_CONTEXT", "BUILD_OPTION"] },
    unit: { dependsOn: ["build"], env: ["CI_CACHE_CONTEXT", "BUILD_OPTION"] },
    native: { dependsOn: ["unit"], cache: false, env: ["CHECK_FAIL"] },
    fresh: { dependsOn: ["native"], cache: false },
  } }));
  write("packages/data/package.json", JSON.stringify({ name: "@fixture/data", scripts: { build: "node build.cjs", unit: "node unit.cjs", native: "node native.cjs", fresh: "node fresh.cjs" } }));
  write("packages/data/source", "source-v1");
  write("packages/data/dependency", "dependency-v1");
  write("packages/data/build.cjs", "const fs=require('node:fs');fs.mkdirSync('dist',{recursive:true});fs.writeFileSync('dist/output',fs.readFileSync('source')+':'+fs.readFileSync('dependency')+':'+(process.env.BUILD_OPTION||'debug'));\n");
  write("packages/data/unit.cjs", "require('node:fs').appendFileSync('ran','unit\\n');\n");
  write("packages/data/native.cjs", "require('node:fs').appendFileSync('native-ran','native\\n');if(process.env.CHECK_FAIL==='true')process.exit(7);\n");
  write("packages/data/fresh-input", "up-to-date");
  write("packages/data/fresh.cjs", "const fs=require('node:fs');fs.appendFileSync('fresh-ran','fresh\\n');if(fs.readFileSync('fresh-input','utf8')!=='up-to-date')process.exit(8);\n");
  git("add", "--all"); git("commit", "-m", "fixture");
  symlinkSync(join(root, "node_modules"), join(cwd, "node_modules"), process.platform === "win32" ? "junction" : "dir");
  const run = (env = {}) => spawnSync(process.execPath, [join(root, "scripts/ci/run-affected.mjs"), "@fixture/data", "fresh"], { cwd, encoding: "utf8", env: { ...process.env, CI: "true", TURBO_REMOTE_CACHE_AUTH: "false", FORCE_RUN: "true", TURBO_TELEMETRY_DISABLED: "1", ...env } });
  const pass = (env) => { const result = run(env); assert.equal(result.status, 0, result.stdout + result.stderr); return result; };
  const count = (path) => readFileSync(join(cwd, `packages/data/${path}`), "utf8").trim().split("\n").length;
  pass(); assert.equal(count("ran"), 1);
  rmSync(join(cwd, "packages/data/dist"), { recursive: true });
  const warm = pass(); assert.match(warm.stdout, /cache hit/u); assert.ok(existsSync(join(cwd, "packages/data/dist/output"))); assert.equal(count("ran"), 1); assert.equal(count("native-ran"), 2); assert.equal(count("fresh-ran"), 2);
  for (const [path, value] of [["packages/data/source", "source-v2"], ["packages/data/dependency", "dependency-v2"], [".nvmrc", "24.20.0\n"]]) {
    const before = count("ran"); write(path, value); pass(); assert.equal(count("ran"), before + 1, path);
  }
  const beforeDependency = count("ran");
  const manifest = JSON.parse(readFileSync(join(cwd, "packages/data/package.json"), "utf8"));
  manifest.devDependencies = { "fixture-tool": "2.0.0" };
  write("packages/data/package.json", JSON.stringify(manifest));
  write("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  packages/data:\n    devDependencies:\n      fixture-tool: {specifier: 2.0.0, version: 2.0.0}\npackages:\n  fixture-tool@2.0.0: {}\nsnapshots:\n  fixture-tool@2.0.0: {}\n");
  pass(); assert.equal(count("ran"), beforeDependency + 1);
  const beforeOption = count("ran"); pass({ BUILD_OPTION: "release" }); assert.equal(count("ran"), beforeOption + 1);
  assert.match(readFileSync(join(cwd, "packages/data/dist/output"), "utf8"), /:release$/u);
  const failed = run({ BUILD_OPTION: "release", CHECK_FAIL: "true" }); assert.notEqual(failed.status, 0); assert.match(failed.stdout + failed.stderr, /cache hit/u);
  write("packages/data/fresh-input", "stale generated contract");
  const stale = run({ BUILD_OPTION: "release" }); assert.notEqual(stale.status, 0, stale.stdout + stale.stderr); assert.match(stale.stdout + stale.stderr, /cache hit/u);
});
