// Prepare network dependencies separately; execute with Yarn networking disabled.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { delimiter, dirname, join, resolve } from "node:path";
import { npm, event } from "./common.mjs";

const [mode, suppliedDirectory, suppliedBinary] = process.argv.slice(2);
assert(["prepare", "run"].includes(mode) && suppliedDirectory,
  "Usage: cache-conformance.mjs prepare|run <new-fixture-directory> [pnport-binary]");
const directory = resolve(suppliedDirectory);
const yarn = "4.18.0";
const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}`,
  YARN_ENABLE_TELEMETRY: "0", YARN_ENABLE_SCRIPTS: "0" };
delete env.NODE_OPTIONS;
const yarnArgs = ["exec", "--yes", "--package", `@yarnpkg/cli-dist@${yarn}`, "--", "yarn"];
const formats = ["inline", "split"];
function execute(program, args, cwd) {
  const result = spawnSync(program, args, { cwd, env, encoding: "utf8", timeout: 60_000 });
  assert.ifError(result.error);
  assert.equal(result.status, 0, `${program}: ${result.stdout}\n${result.stderr}`);
  return result.stdout;
}
function cacheSnapshot(directory) {
  const files = readdirSync(directory, { recursive: true })
    .filter((name) => statSync(join(directory, name)).isFile()).sort();
  assert(files.length > 0, "Default Vitest cache must contain results.");
  const hash = createHash("sha256");
  for (const name of files) hash.update(name).update("\0").update(readFileSync(join(directory, name))).update("\0");
  return hash.digest("hex");
}
if (mode === "prepare") {
  assert(!existsSync(directory), "Preparation requires a new fixture directory.");
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  for (const format of formats) {
    const project = join(directory, format);
    mkdirSync(project);
    writeFileSync(join(project, "package.json"), JSON.stringify({ name: "pnport-cache-conformance", private: true,
      packageManager: `yarn@${yarn}`, devDependencies: { vitest: "5.0.1", vite: "8.3.0" } }));
    writeFileSync(join(project, ".yarnrc.yml"), `nodeLinker: pnp\nenableGlobalCache: false\nenableScripts: false\npnpEnableInlining: ${format === "inline"}\n`);
    writeFileSync(join(project, "cache.test.js"), "import { test, expect } from 'vitest';\ntest('cache', () => expect(1 + 1).toBe(2));\n");
    // These fresh temporary projects need their first lockfile even under CI.
    // Confine the immutable-install override to fixture preparation.
    npm([...yarnArgs, "install"], { cwd: project, env: { ...env, YARN_ENABLE_IMMUTABLE_INSTALLS: "false" } });
    assert(!existsSync(join(project, "node_modules")));
    writeFileSync(join(project, "probe.c"), `#include <fcntl.h>
#include <stdio.h>
#include <sys/stat.h>
#include <unistd.h>
int main(void) {
  int dependency = open("node_modules/vitest/package.json", O_RDONLY);
  char bytes[32];
  if (dependency < 0 || read(dependency, bytes, sizeof(bytes)) <= 0) return 1;
  close(dependency);
  if (mkdir("node_modules/.native-cache", 0700) && access("node_modules/.native-cache", F_OK)) return 2;
  int cache = open("node_modules/.native-cache/results.json", O_CREAT | O_WRONLY | O_TRUNC, 0600);
  if (cache < 0 || write(cache, "cache", 5) != 5) return 3;
  close(cache);
  return 0;
}
`);
    execute("cc", ["-Wall", "-Wextra", "-Werror", "probe.c", "-o", "probe"], project);
  }
  event("cache_fixture_prepared", { yarn, vitest: "5.0.1", vite: "8.3.0", formats });
} else {
  assert(suppliedBinary, "Offline execution requires the matching pnport binary and companion.");
  const binary = resolve(suppliedBinary);
  env.YARN_ENABLE_NETWORK = "0";
  env.npm_config_offline = "true";
  for (const format of formats) {
    const project = join(directory, format);
    const base = ["--cache-dir", join(directory, "native-cache")];
    const vitestCache = join(project, "node_modules/.vite/vitest");
    let retained;
    for (let iteration = 0; iteration < 2; iteration++) {
      const doctor = () => JSON.parse(execute(binary, [...base, "doctor", "--json"], project));
      assert.equal(doctor().ready, true);
      if (retained) assert.equal(cacheSnapshot(vitestCache), retained);
      // Resolve the prepared package bin through pnport so Vitest and its
      // workers share the matching native view without an npm/Yarn wrapper.
      execute(binary, [...base, "run", "--", "vitest", "run"], project);
      assert(existsSync(vitestCache), "Default Vitest cache must exist.");
      retained = cacheSnapshot(vitestCache);
      assert.equal(doctor().ready, true);
      execute(binary, [...base, "run", "--", join(project, "probe")], project);
      assert.equal(readFileSync(join(project, "node_modules/.native-cache/results.json"), "utf8"), "cache");
      assert.equal(doctor().ready, true);
      assert.equal(cacheSnapshot(vitestCache), retained, "pnport must preserve existing Vitest cache bytes.");
    }
    event("cache_conformance", { format, iterations: 2, defaultVitestCache: true,
      nativeDependencyRead: true, nativeCacheWrite: true, vitestCachePreserved: true, ready: true });
  }
}
