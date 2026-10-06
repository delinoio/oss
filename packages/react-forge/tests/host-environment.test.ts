import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const require = createRequire(import.meta.url);

test("React Forge host validation preserves Cargo target selection through strict Turbo", async (t) => {
  const cwd = realpathSync(mkdtempSync(join(tmpdir(), "react-forge-host-")));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const write = (path: string, body: string) => {
    const file = join(cwd, path);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, body);
  };
  const manifest = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  const config = JSON.parse(readFileSync(join(root, "packages/react-forge/turbo.json"), "utf8"));
  // Exercise the real host allowlist without building native dependencies. The
  // complete host dependency graph and gates stay covered by ci-contract.test.mjs.
  const task = { ...config.tasks["ci:host"], dependsOn: [] };
  write("package.json", JSON.stringify({ name: "react-forge-host-fixture", private: true, packageManager: manifest.packageManager }));
  write("pnpm-workspace.yaml", "packages:\n  - packages/*\n");
  write("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  packages/react-forge: {}\n");
  write("turbo.json", readFileSync(join(root, "turbo.json"), "utf8"));
  write("rust-toolchain", readFileSync(join(root, "rust-toolchain"), "utf8"));
  write("packages/react-forge/turbo.json", JSON.stringify({ extends: config.extends, tasks: { "ci:host": task } }));
  write("packages/react-forge/package.json", JSON.stringify({ name: "@delino/react-forge", private: true, scripts: { "ci:host": "node probe.mjs" } }));
  write("Cargo.toml", '[package]\nname = "host-fixture"\nversion = "0.0.0"\nedition = "2021"\n[workspace]\nmembers = ["."]\n');
  write("src/lib.rs", "");
  write("packages/react-forge/probe.mjs", `
    import assert from "node:assert/strict";
    import { spawnSync } from "node:child_process";
    import { writeFileSync } from "node:fs";
    const result = spawnSync("cargo", ["metadata", "--offline", "--no-deps", "--format-version", "1", "--manifest-path", "../../Cargo.toml"], { encoding: "utf8", timeout: 30_000 });
    assert.equal(result.status, 0, result.stdout + result.stderr);
    writeFileSync("probe.json", JSON.stringify({
      cargoTargetDir: process.env.CARGO_TARGET_DIR ?? null,
      targetDirectory: JSON.parse(result.stdout).target_directory,
      unlisted: process.env.REACT_FORGE_UNLISTED_PROBE ?? null,
    }));
  `);

  for (const configured of [true, false]) {
    await t.test(configured ? "configured absolute directory reaches Cargo unchanged" : "unset variable retains Cargo's default directory", () => {
      const env: NodeJS.ProcessEnv = { ...process.env, CARGO_HOME: join(cwd, "cargo-home"), TURBO_TELEMETRY_DISABLED: "1", REACT_FORGE_UNLISTED_PROBE: "filtered" };
      delete env.CARGO_TARGET_DIR;
      delete env.CARGO_BUILD_TARGET_DIR;
      const selected = join(cwd, "target", "React Forge Cache");
      if (configured) env.CARGO_TARGET_DIR = selected;
      const output = join(cwd, "packages/react-forge/probe.json");
      rmSync(output, { force: true });
      const result = spawnSync(process.execPath, [require.resolve("turbo/bin/turbo"), "run", "ci:host", "--filter=@delino/react-forge", "--env-mode=strict", "--cache=local:rw"], { cwd, env, encoding: "utf8", timeout: 60_000 });
      assert.equal(result.status, 0, result.stdout + result.stderr);
      const probe = JSON.parse(readFileSync(output, "utf8"));
      assert.equal(probe.cargoTargetDir, configured ? selected : null);
      assert.equal(probe.targetDirectory, configured ? selected : join(cwd, "target"));
      assert.equal(probe.unlisted, null, "unlisted variables must remain filtered");
    });
  }
});
