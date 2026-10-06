import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { copyFileSync, existsSync, globSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { safeBaseEnvironment } from "../dev-environment/process.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const require = createRequire(import.meta.url);
const taskId = "@delinoio/ci#ci:environment:turbo";

test("environment verification reuses valid cache and rejects each changed development graph", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "ci-environment-cache-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const env = { ...safeBaseEnvironment(), CI: "true", TURBO_TELEMETRY_DISABLED: "1", NO_COLOR: "1" };
  const write = (path, data) => writeFileSync(join(cwd, path), data);

  // Copy the real task, verifier and workspace graph. No app, provider or
  // development server executes, and no checkout-local state is copied.
  const files = [
    "package.json", "pnpm-workspace.yaml", "pnpm-lock.yaml", "turbo.json",
    ".gitignore", ".nvmrc", "rust-toolchain", "go.mod", "go.sum",
    "scripts/ci/package.json", "scripts/ci/turbo.json", "scripts/ci/from-root.mjs",
    "scripts/dev-environment/verify-turbo.mjs", "scripts/dev-environment/contracts.mjs",
    "scripts/dev-environment/process.mjs", "scripts/spawn-dev-server.mjs",
    ...globSync(["apps/*/package.json", "apps/*/turbo.json", "packages/*/package.json", "packages/*/turbo.json", "servers/*/package.json", "servers/*/turbo.json"], { cwd: root }),
  ];
  for (const path of files) {
    mkdirSync(dirname(join(cwd, path)), { recursive: true });
    copyFileSync(join(root, path), join(cwd, path));
  }
  const git = (...args) => execFileSync("git", args, { cwd, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  git("add", "--all");
  git("commit", "-m", "fixture");
  symlinkSync(join(root, "node_modules"), join(cwd, "node_modules"), process.platform === "win32" ? "junction" : "dir");

  const runs = join(cwd, ".turbo/runs");
  const summaries = () => existsSync(runs) ? readdirSync(runs).filter((name) => name.endsWith(".json")) : [];
  const run = (scenario) => {
    // Each CI job starts from fresh source. The explicit scripts/** input also
    // matches package-local Turbo logs, so discard those generated logs between
    // runs while retaining the root cache and summaries for cache verification.
    // Remove this cleanup if Turbo excludes generated logs from explicit inputs.
    rmSync(join(cwd, "scripts/ci/.turbo"), { recursive: true, force: true });
    const before = new Set(summaries());
    const result = spawnSync(process.execPath, [
      require.resolve("turbo/bin/turbo"), "run", "ci:environment:turbo",
      "--filter=@delinoio/ci", "--cache=local:rw", "--summarize",
    ], { cwd, env, encoding: "utf8", timeout: 60_000 });
    assert.ifError(result.error);
    const output = result.stdout + result.stderr;
    const created = summaries().filter((name) => !before.has(name));
    assert.equal(created.length, 1, output);
    const summary = JSON.parse(readFileSync(join(runs, created[0]), "utf8"));
    assert.deepEqual(summary.tasks.map((task) => task.taskId), [taskId], output);
    const task = summary.tasks[0];
    t.diagnostic(JSON.stringify({ event: "environment_cache_fixture", scenario, hash: task.hash, cache: task.cache.status, exitCode: result.status }));
    return { ...result, output, task };
  };

  const cold = run("cold");
  assert.equal(cold.status, 0, cold.output);
  assert.equal(cold.task.cache.status, "MISS", cold.output);
  assert.match(cold.output, /Resolved Turbo environment contains only/u);
  const warm = run("warm");
  assert.equal(warm.status, 0, warm.output);
  assert.equal(warm.task.cache.status, "HIT", warm.output);
  assert.equal(warm.task.hash, cold.task.hash);

  const paths = ["apps/devhud/turbo.json", "apps/devhud-admin/turbo.json", "servers/devhud-api/turbo.json", "pnpm-workspace.yaml"];
  for (const path of paths) {
    const original = readFileSync(join(cwd, path), "utf8");
    try {
      if (path === "pnpm-workspace.yaml") {
        assert.match(original, /^  - servers\/\*\r?\n/mu);
        write(path, original.replace(/^  - servers\/\*\r?\n/mu, ""));
      } else {
        const config = JSON.parse(original);
        config.tasks.dev = { ...config.tasks.dev, env: ["INFISICAL_TOKEN"] };
        write(path, JSON.stringify(config));
      }
      const invalid = run(path);
      assert.notEqual(invalid.task.hash, cold.task.hash, `${path} must invalidate the cached graph`);
      assert.equal(invalid.task.cache.status, "MISS", `${path}: ${invalid.output}`);
      assert.notEqual(invalid.status, 0, `${path}: ${invalid.output}`);
      assert.match(invalid.output, /AssertionError/u, `${path} must execute the original verifier`);
      assert.doesNotMatch(invalid.output, /Resolved Turbo environment contains only/u);
    } finally {
      write(path, original);
    }
    const restored = run(`restored ${path}`);
    assert.equal(restored.status, 0, restored.output);
    assert.equal(restored.task.hash, cold.task.hash, path);
    assert.equal(restored.task.cache.status, "HIT", restored.output);
  }
});
