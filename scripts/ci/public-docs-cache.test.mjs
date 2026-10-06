import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const turbo = createRequire(import.meta.url).resolve("turbo/bin/turbo");
const installers = ["nodeup", "binpm", "async-commit-hook", "pnport"].flatMap((slug) =>
  ["sh", "ps1"].map((extension) => ({
    source: `scripts/install/${slug}.${extension}`,
    output: `apps/public-docs/doc_build/${slug}/install.${extension}`,
  })),
);

function docsFixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), "public docs cache "));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const write = (path, contents) => {
    mkdirSync(dirname(join(cwd, path)), { recursive: true });
    writeFileSync(join(cwd, path), contents);
  };
  const read = (path) => readFileSync(join(cwd, path), "utf8");
  const files = execFileSync("git", ["ls-files", "-z", "--",
    ".gitignore", ".nvmrc", "package.json", "pnpm-lock.yaml", "turbo.json",
    "rust-toolchain", "go.mod", "go.sum", "docs/apps-*-docs-foundation.md",
    "apps/public-docs", "packages/docs-site-switcher", ...installers.map(({ source }) => source),
  ], { cwd: root, encoding: "utf8" }).split("\0").filter(Boolean);
  for (const file of files) {
    mkdirSync(dirname(join(cwd, file)), { recursive: true });
    cpSync(join(root, file), join(cwd, file));
  }
  write("pnpm-workspace.yaml", "packages:\n  - apps/public-docs\n  - packages/docs-site-switcher\n");
  // Rspress enables Node's module cache by default. Keep this fixture from
  // writing that shared host cache; only the disposable Turbo cache is tested.
  const rootConfig = JSON.parse(read("turbo.json"));
  rootConfig.globalPassThroughEnv = [...(rootConfig.globalPassThroughEnv ?? []), "NODE_DISABLE_COMPILE_CACHE"];
  write("turbo.json", JSON.stringify(rootConfig));
  write(".gitignore", `${read(".gitignore")}\n/task-runs.log\n`);
  write("apps/public-docs/scripts/cache-fixture-run.mjs", `
    import { appendFileSync } from "node:fs";
    appendFileSync("../../task-runs.log", process.argv[2] + "\\n");
  `);
  const manifest = JSON.parse(read("apps/public-docs/package.json"));
  // The package test runs the existing content/security mutation suite. Here,
  // run both complete-site validators on each cache miss without repeating it.
  manifest.scripts["ci:routes"] = "node scripts/validate-clean-urls.mjs && node scripts/validate-public-docs.mjs";
  for (const task of ["build:frontend", "ci:routes"]) {
    manifest.scripts[task] = `node scripts/cache-fixture-run.mjs ${task} && ${manifest.scripts[task]}`;
  }
  write("apps/public-docs/package.json", JSON.stringify(manifest));
  const git = (...args) => execFileSync("git", args, { cwd, stdio: ["ignore", "pipe", "pipe"] });
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  git("add", "--all");
  git("commit", "-m", "fixture");

  const link = (source, destination) => symlinkSync(source, destination, process.platform === "win32" ? "junction" : "dir");
  link(join(root, "node_modules"), join(cwd, "node_modules"));
  for (const workspace of ["apps/public-docs", "packages/docs-site-switcher"]) {
    const modules = `${workspace}/node_modules`;
    mkdirSync(join(cwd, modules));
    for (const entry of readdirSync(join(root, modules))) {
      if (entry === ".bin") {
        cpSync(join(root, modules, entry), join(cwd, modules, entry), { recursive: true });
      } else if (entry.startsWith("@")) {
        mkdirSync(join(cwd, modules, entry));
        for (const name of readdirSync(join(root, modules, entry))) {
          const source = `${entry}/${name}` === "@delinoio/docs-site-switcher"
            ? join(cwd, "packages/docs-site-switcher") : join(root, modules, entry, name);
          link(source, join(cwd, modules, entry, name));
        }
      } else if (!entry.startsWith(".")) {
        link(join(root, modules, entry), join(cwd, modules, entry));
      }
    }
  }
  const run = (...tasks) => spawnSync(process.execPath, [turbo, "run", ...tasks,
    "--filter=public-docs", "--cache=local:rw", "--cache-dir=.turbo/cache", "--no-daemon",
  ], {
    cwd, encoding: "utf8", timeout: 180_000, maxBuffer: 20 * 1024 * 1024,
    env: { ...process.env, CI: "true", NODE_DISABLE_COMPILE_CACHE: "1", TURBO_TELEMETRY_DISABLED: "1", TURBO_TOKEN: "", TURBO_TEAM: "" },
  });
  const pass = (...tasks) => {
    const result = run(...tasks);
    assert.equal(result.error, undefined, String(result.error));
    assert.equal(result.status, 0, result.stdout + result.stderr);
    return result;
  };
  const dry = (...tasks) => new Map(JSON.parse(pass(...tasks, "--dry=json").stdout).tasks.map((task) => [task.taskId, task]));
  const count = (task) => read("task-runs.log").split("\n").filter((line) => line === task).length;
  const checkInstallers = () => {
    for (const { source, output } of installers) {
      assert.deepEqual(readFileSync(join(cwd, output)), readFileSync(join(cwd, source)), output);
    }
  };
  return { cwd, write, read, run, pass, dry, count, checkInstallers };
}

test("public-docs hashes installers, restores current assets, and executes stale-asset failures", async (t) => {
  const f = docsFixture(t);
  const tasks = ["build", "build:frontend", "ci:routes"];
  const originals = new Map(installers.map(({ source }) => [source, f.read(source)]));

  await t.test("all eight independent installer changes invalidate all three consumers only", () => {
    const baseline = f.dry(...tasks);
    const rootTasks = JSON.parse(f.read("turbo.json")).tasks;
    for (const task of tasks) {
      const resolved = baseline.get(`public-docs#${task}`);
      assert.equal(resolved.resolvedTaskDefinition.cache, true, task);
      for (const { source } of installers) assert.ok(resolved.inputs[`../../${source}`], `${task}: ${source}`);
      if (task !== "ci:routes") {
        assert.deepEqual(resolved.resolvedTaskDefinition.outputs, [...rootTasks[task].outputs].sort(), task);
        for (const input of rootTasks[task].inputs) {
          assert.ok(resolved.resolvedTaskDefinition.inputs.includes(input.replace("$TURBO_ROOT$/", "../../")), `${task}: ${input}`);
        }
      }
    }
    assert.deepEqual([...baseline.get("public-docs#ci:routes").resolvedTaskDefinition.dependsOn].sort(),
      ["@delinoio/docs-site-switcher#test", "build:frontend"]);
    for (const { source } of installers) {
      try {
        f.write(source, `${originals.get(source)}\n# cache invalidation fixture\n`);
        const changed = f.dry(...tasks);
        for (const task of tasks) {
          assert.notEqual(changed.get(`public-docs#${task}`).hash, baseline.get(`public-docs#${task}`).hash, `${source}: ${task}`);
        }
        for (const [id, task] of baseline) {
          if (!id.startsWith("public-docs#")) assert.equal(changed.get(id).hash, task.hash, `${source}: unrelated ${id}`);
        }
      } finally {
        f.write(source, originals.get(source));
      }
      assert.deepEqual(f.dry(...tasks), baseline, `${source}: restored hashes`);
    }
  });

  await t.test("a warm complete build regenerates changed assets and unchanged runs restore output", () => {
    f.pass("ci:routes");
    assert.equal(f.count("build:frontend"), 1);
    assert.equal(f.count("ci:routes"), 1);
    f.checkInstallers();
    rmSync(join(f.cwd, "apps/public-docs/doc_build"), { recursive: true });
    f.pass("ci:routes");
    f.checkInstallers();
    assert.equal(f.count("build:frontend"), 1);
    assert.equal(f.count("ci:routes"), 1);
    for (const { source } of installers) f.write(source, `${originals.get(source)}\n# warm rebuild fixture\n`);
    rmSync(join(f.cwd, "apps/public-docs/doc_build"), { recursive: true });
    f.pass("ci:routes");
    f.checkInstallers();
    assert.equal(f.count("build:frontend"), 2);
    assert.equal(f.count("ci:routes"), 2);
    for (const [source, original] of originals) f.write(source, original);
    rmSync(join(f.cwd, "apps/public-docs/doc_build"), { recursive: true });
    f.pass("ci:routes");
    f.checkInstallers();
    assert.equal(f.count("build:frontend"), 2);
    assert.equal(f.count("ci:routes"), 2);
  });

  await t.test("each stale installer fails the real validator even with a cached build stub", () => {
    // Freeze the producer before priming success to isolate the validator's own
    // external inputs. No task configuration changes occur during source mutations.
    const manifest = JSON.parse(f.read("apps/public-docs/package.json"));
    manifest.scripts["build:frontend"] = "node scripts/cache-fixture-run.mjs build:frontend";
    manifest.scripts["ci:routes"] = "node scripts/cache-fixture-run.mjs ci:routes && node scripts/validate-public-docs.mjs";
    f.write("apps/public-docs/package.json", JSON.stringify(manifest));
    const config = JSON.parse(f.read("apps/public-docs/turbo.json"));
    config.tasks["build:frontend"].inputs = ["$TURBO_EXTENDS$"];
    f.write("apps/public-docs/turbo.json", JSON.stringify(config));
    f.pass("ci:routes");
    const buildCount = f.count("build:frontend");
    let routeCount = f.count("ci:routes");
    const baseline = f.dry("ci:routes");
    f.pass("ci:routes");
    assert.equal(f.count("ci:routes"), routeCount);
    for (const { source, output } of installers) {
      try {
        f.write(source, `${originals.get(source)}\n# stale asset fixture\n`);
        const changed = f.dry("ci:routes");
        assert.equal(changed.get("public-docs#build:frontend").hash, baseline.get("public-docs#build:frontend").hash);
        assert.notEqual(changed.get("public-docs#ci:routes").hash, baseline.get("public-docs#ci:routes").hash);
        const failed = f.run("ci:routes");
        assert.notEqual(failed.status, 0, failed.stdout + failed.stderr);
        assert.ok((failed.stdout + failed.stderr).includes(`${output.replace("apps/public-docs/doc_build", "")} differs from ${source}`));
        assert.equal(f.count("build:frontend"), buildCount);
        assert.equal(f.count("ci:routes"), ++routeCount);
      } finally {
        f.write(source, originals.get(source));
      }
      f.pass("ci:routes");
      assert.equal(f.count("build:frontend"), buildCount);
      assert.equal(f.count("ci:routes"), routeCount);
    }
  });
});
