import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const read = (path) => readFileSync(join(root, path), "utf8");
const manifest = JSON.parse(read("scripts/ci/package.json"));
const task = JSON.parse(read("scripts/ci/turbo.json")).tasks["ci:rust:fmt"];
const taskId = "@delinoio/ci#ci:rust:fmt";

// This live fixture runs in rust-fmt after tool installation, outside the
// ordinary offline contract suite. Never download a compiler during the test.
for (const directory of ["", "crates/example/", "crates/example/src/", "crates/example/src/.hidden/"]) {
  for (const filename of [".rustfmt.toml", "rustfmt.toml"]) {
    const config = directory + filename;
    test(`real formatting cache invalidates for ${config}`, (t) => {
      const cwd = mkdtempSync(join(tmpdir(), "ci-rustfmt-cache-"));
      t.after(() => rmSync(cwd, { recursive: true, force: true }));
      const write = (path, value) => {
        mkdirSync(dirname(join(cwd, path)), { recursive: true });
        writeFileSync(join(cwd, path), value);
      };
      const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
      const pin = read("rust-toolchain").trim();
      const rustfmt = spawnSync("rustup", ["run", pin, "rustfmt", "--version"], { encoding: "utf8" });
      assert.equal(rustfmt.status, 0, `Install the pinned formatter before this fixture: ${rustfmt.stderr}`);
      git("init", "-b", "main");
      git("config", "user.name", "CI Fixture");
      git("config", "user.email", "ci@example.invalid");
      git("config", "commit.gpgsign", "false");
      git("config", "core.hooksPath", join(cwd, "empty-hooks"));
      write(".gitignore", "node_modules\n.turbo\ntarget\n");
      write("package.json", JSON.stringify({ name: "rustfmt-cache-fixture", private: true, packageManager: JSON.parse(read("package.json")).packageManager }));
      write("pnpm-workspace.yaml", "packages:\n  - scripts/ci\n");
      write("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  scripts/ci: {}\n");
      write("turbo.json", JSON.stringify({ tasks: {} }));
      write("scripts/ci/package.json", JSON.stringify({ name: manifest.name, scripts: { "ci:rust:fmt": manifest.scripts["ci:rust:fmt"] } }));
      write("scripts/ci/turbo.json", JSON.stringify({ extends: ["//"], tasks: { "ci:rust:fmt": task } }));
      write("scripts/ci/from-root.mjs", read("scripts/ci/from-root.mjs"));
      write("rust-toolchain", read("rust-toolchain"));
      write(".nvmrc", read(".nvmrc"));
      write("Cargo.toml", '[workspace]\nmembers = ["crates/example"]\nresolver = "3"\n');
      const sourcePath = `crates/example/src/${directory.includes(".hidden") ? ".hidden/" : ""}lib.rs`;
      write("crates/example/Cargo.toml", `[package]\nname = "example"\nversion = "0.0.0"\nedition = "2024"\n[lib]\npath = "${sourcePath.slice("crates/example/".length)}"\n`);
      const source = "pub fn literal() -> u32 {\n    0xabcdef\n}\n";
      write(sourcePath, source);
      // Nested overrides must supersede a real parent configuration.
      if (directory) write(".rustfmt.toml", 'hex_literal_case = "Lower"\n');
      write(config, 'hex_literal_case = "Lower"\n');
      git("add", "--all");
      git("commit", "-m", "fixture");
      symlinkSync(join(root, "node_modules"), join(cwd, "node_modules"), process.platform === "win32" ? "junction" : "dir");

      const runs = join(cwd, ".turbo/runs");
      let previousSummaries = new Set();
      let previousCheck;
      const run = (success, cache) => {
        const result = spawnSync(process.execPath, [join(root, "scripts/ci/run-affected.mjs"), manifest.name, "ci:rust:fmt"], {
          cwd, encoding: "utf8", env: { ...process.env, CI: "true", FORCE_RUN: "true", CARGO_NET_OFFLINE: "true", TURBO_REMOTE_CACHE_AUTH: "false", TURBO_TELEMETRY_DISABLED: "1" },
        });
        const output = result.stdout + result.stderr;
        assert.equal(result.error, undefined, output);
        if (success) assert.equal(result.status, 0, output);
        else {
          assert.notEqual(result.status, 0, output);
          assert.match(output, /0xABCDEF/u);
        }
        const summaries = readdirSync(runs).filter((path) => path.endsWith(".json"));
        const added = summaries.filter((path) => !previousSummaries.has(path));
        assert.equal(added.length, 1, output);
        previousSummaries = new Set(summaries);
        const summary = JSON.parse(readFileSync(join(runs, added[0]), "utf8"));
        assert.equal(summary.tasks.length, 1);
        const check = summary.tasks[0];
        assert.equal(check.taskId, taskId);
        assert.ok(Object.keys(check.inputs).every((path) => !path.split(/[\\/]/u).includes(".turbo")), "Generated Turbo logs must not change the formatting hash");
        const inputChanges = [...new Set([...Object.keys(previousCheck?.inputs ?? {}), ...Object.keys(check.inputs)])]
          .filter((path) => previousCheck?.inputs[path] !== check.inputs[path]);
        assert.equal(check.cache.status, cache, `${output}\n${JSON.stringify({ inputChanges, previousHash: previousCheck?.hash, hash: check.hash })}`);
        previousCheck = check;
        assert.equal(readFileSync(join(cwd, sourcePath), "utf8"), source);
        return check;
      };

      const cold = run(true, "MISS");
      const warm = run(true, "HIT");
      assert.equal(warm.hash, cold.hash);
      write(config, 'hex_literal_case = "Upper"\n');
      assert.deepEqual(git("diff", "--name-only").split("\n"), [config]);
      const changed = run(false, "MISS");
      assert.notEqual(changed.hash, cold.hash);
      assert.equal(run(false, "MISS").hash, changed.hash);
      write(config, 'hex_literal_case = "Lower"\n');
      assert.equal(run(true, "HIT").hash, cold.hash);
      rmSync(join(cwd, config));
      const removed = run(true, "MISS");
      assert.notEqual(removed.hash, cold.hash);
      write(config, 'hex_literal_case = "Upper"\n');
      assert.equal(run(false, "MISS").hash, changed.hash);
      t.diagnostic(JSON.stringify({ config, formatter: rustfmt.stdout.trim(), baselineHash: cold.hash, changedHash: changed.hash, removedHash: removed.hash }));
    });
  }
}
