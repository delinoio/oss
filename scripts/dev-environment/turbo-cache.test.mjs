import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import { repositoryRoot } from "./contracts.mjs";
import { safeBaseEnvironment } from "./process.mjs";

const require = createRequire(import.meta.url);
const turbo = require.resolve("turbo/bin/turbo");
const owners = ["apps/devhud", "apps/devhud-admin", "servers/devhud-api"];
const task = "ci:environment:turbo";

test("environment checker invalidates a warm cache when its development graph changes", async (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "dev-environment-cache-"));
  // Keep generated cache metadata outside the fixture. The checker hashes broad
  // repository inputs, and platform-specific Turbo cache state must not change
  // the source-graph hash after a mutation is restored.
  const cacheDir = mkdtempSync(join(tmpdir(), "dev-environment-turbo-cache-"));
  t.after(() => {
    rmSync(cwd, { recursive: true, force: true });
    rmSync(cacheDir, { recursive: true, force: true });
  });
  const write = (path, content) => {
    const target = join(cwd, path);
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, content);
  };
  const sources = new Map();
  // Copy only committed graph definitions and checker code. Never copy local
  // configuration or invoke a development server, provider, or package install.
  for (const path of [
    "package.json",
    "pnpm-workspace.yaml",
    "pnpm-lock.yaml",
    "turbo.json",
    ".nvmrc",
    "scripts/ci/package.json",
    "scripts/ci/turbo.json",
    "scripts/ci/from-root.mjs",
    "scripts/dev-environment/verify-turbo.mjs",
    "scripts/dev-environment/process.mjs",
    "scripts/dev-environment/contracts.mjs",
    "scripts/spawn-dev-server.mjs",
    "packages/devhud-api-client/package.json",
    ...owners.flatMap((owner) => [`${owner}/package.json`, `${owner}/turbo.json`]),
  ]) {
    const content = readFileSync(join(repositoryRoot, path), "utf8");
    sources.set(path, content);
    write(path, content);
  }
  const clientConfig = "packages/devhud-api-client/turbo.json";
  if (existsSync(join(repositoryRoot, clientConfig))) {
    const content = readFileSync(join(repositoryRoot, clientConfig), "utf8");
    sources.set(clientConfig, content);
    write(clientConfig, content);
  }
  write(".gitignore", "node_modules\n.turbo\n.env\n.infisical.json\n.dev-environment\n");
  const git = (...args) => execFileSync("git", args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  git("init", "-b", "main");
  git("config", "user.name", "Environment Cache Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  git("add", "--all");
  git("commit", "-m", "fixture");
  symlinkSync(
    join(repositoryRoot, "node_modules"),
    join(cwd, "node_modules"),
    process.platform === "win32" ? "junction" : "dir",
  );

  const canary = "environment-cache-fixture-sensitive-value";
  const run = (...args) => {
    const result = spawnSync(process.execPath, [turbo, "run", task,
      "--filter=@delinoio/ci", "--cache=local:rw", "--cache-dir", cacheDir, ...args], {
      cwd,
      encoding: "utf8",
      shell: false,
      timeout: 30_000,
      env: {
        ...safeBaseEnvironment(),
        CI: "true",
        CI_CACHE_CONTEXT: "environment-cache-fixture",
        DEVHUD_DATABASE_URL: canary,
        TURBO_TELEMETRY_DISABLED: "1",
      },
    });
    assert.ifError(result.error);
    assert.doesNotMatch(result.stdout + result.stderr, new RegExp(canary, "u"));
    return result;
  };
  const output = (result) => result.stdout + result.stderr;
  const hash = () => {
    const result = run("--dry=json");
    assert.equal(result.status, 0, output(result));
    return JSON.parse(result.stdout).tasks.find(({ task: name }) => name === task).hash;
  };
  const cold = run();
  assert.equal(cold.status, 0, output(cold));
  assert.match(cold.stdout, /cache miss/u);
  const baselineHash = hash();
  const warm = run();
  assert.equal(warm.status, 0, output(warm));
  assert.match(warm.stdout, /cache hit/u);
  assert.equal(hash(), baselineHash);

  const mutateJson = (path, mutate) => {
    const config = JSON.parse(sources.get(path));
    mutate(config);
    write(path, JSON.stringify(config));
  };
  const checkChange = async (path, label, mutate, valid) => {
    await t.test(`${path}: ${label}`, () => {
      try {
        mutateJson(path, mutate);
        assert.notEqual(hash(), baselineHash, "graph changes must change the checker hash");
        const result = run();
        assert.match(result.stdout, /cache miss/u, output(result));
        if (valid) {
          assert.equal(result.status, 0, output(result));
        } else {
          assert.notEqual(result.status, 0, "a warm success must not hide an invalid graph");
          assert.match(output(result), /AssertionError/u);
        }
      } finally {
        write(path, sources.get(path));
      }
      assert.equal(hash(), baselineHash);
    });
  };
  for (const owner of owners) {
    for (const property of ["env", "passThroughEnv"]) {
      await checkChange(`${owner}/turbo.json`, `forbidden dev.${property}`, (config) => {
        config.tasks.dev = { [property]: ["DEVHUD_LOCAL_MODE", "DEVHUD_DATABASE_URL"] };
      }, false);
    }
    await checkChange(`${owner}/package.json`, "missing required dev task", (manifest) => {
      delete manifest.scripts.dev;
    }, false);
    await checkChange(`${owner}/package.json`, "valid dev script change", (manifest) => {
      manifest.scripts.dev += " --fixture-option";
    }, true);
  }
  for (const property of ["env", "passThroughEnv"]) {
    await checkChange("turbo.json", `forbidden root dev.${property}`, (config) => {
      config.tasks.dev[property] = ["DEVHUD_LOCAL_MODE", "DEVHUD_DATABASE_URL"];
    }, false);
  }
  await checkChange("packages/devhud-api-client/package.json", "dependency task change", (manifest) => {
    manifest.scripts["build:api"] = "node --version";
  }, true);
  await t.test("new dependency configuration invalidates and rejects forbidden environment", () => {
    const path = clientConfig;
    try {
      const config = JSON.parse(sources.get(path) ?? '{"extends":["//"],"tasks":{}}');
      config.tasks["build:api"] = { env: ["DEVHUD_DATABASE_URL"] };
      write(path, JSON.stringify(config));
      assert.notEqual(hash(), baselineHash);
      const result = run();
      assert.notEqual(result.status, 0, output(result));
      assert.match(result.stdout, /cache miss/u);
      assert.match(output(result), /AssertionError/u);
    } finally {
      if (sources.has(path)) write(path, sources.get(path));
      else rmSync(join(cwd, path), { force: true });
    }
  });
  await t.test("workspace inventory invalidates the checker", () => {
    const path = "pnpm-workspace.yaml";
    try {
      const source = sources.get(path);
      const newline = source.includes("\r\n") ? "\r\n" : "\n";
      write(path, source.replace(`  - apps/*${newline}`, `  - apps/unused-*${newline}`));
      assert.notEqual(hash(), baselineHash);
      const result = run();
      assert.notEqual(result.status, 0, output(result));
      assert.match(result.stdout, /cache miss/u);
      assert.match(output(result), /AssertionError/u);
    } finally {
      write(path, sources.get(path));
    }
  });
  assert.equal(hash(), baselineHash);
  const restored = run();
  assert.equal(restored.status, 0, output(restored));
  assert.match(restored.stdout, /cache hit/u);
});
