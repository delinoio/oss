import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { affectedGoPackages, GoMode, selectionOptions } from "./go-affected.mjs";

export const GoTestShard = Object.freeze({
  All: "all",
  Core: "core",
  Server: "server",
  Harness: "harness",
  Worker: "worker",
});

const delidev = "github.com/delinoio/oss/cmds/delidev-cli";
const within = (value, prefix) => value === prefix || value.startsWith(`${prefix}/`);

export function shardForPackage(importPath) {
  if (!within(importPath, delidev)) return GoTestShard.Core;
  if (["server", "store"].some((name) => within(importPath, `${delidev}/internal/${name}`))) return GoTestShard.Server;
  if (["cli", "harness"].some((name) => within(importPath, `${delidev}/internal/${name}`))) return GoTestShard.Harness;
  return GoTestShard.Worker;
}

export function selectPackages(output, shard) {
  if (!Object.values(GoTestShard).includes(shard) || shard === GoTestShard.All) throw new Error("Expected a Windows Go test shard");
  const packages = output.trim().split(/\r?\n/u).map((line) => line.trim()).filter(Boolean);
  if (packages.length === 0 || new Set(packages).size !== packages.length || packages.some((name) => /\s/u.test(name))) {
    throw new Error("Go package discovery returned an empty or invalid inventory");
  }
  const selected = packages.filter((name) => shardForPackage(name) === shard).sort();
  if (selected.length === 0) throw new Error(`Go test shard ${shard} has no packages`);
  return selected;
}

export function runGoTests(shard, { run = spawnSync, log = console.log, platform = process.platform, ...options } = selectionOptions()) {
  if (!Object.values(GoTestShard).includes(shard)) throw new Error("Unknown Go test shard");
  const started = performance.now();
  const commandOptions = { shell: false, stdio: "inherit", ...(options.cwd ? { cwd: options.cwd } : {}) };
  let packages = ["./..."];
  const mode = options.mode ?? GoMode.Full;
  if (!Object.values(GoMode).includes(mode)) throw new Error("Unknown Go validation mode");
  if (mode === GoMode.Affected) {
    const selection = affectedGoPackages({ ...options, run, log });
    packages = selection.packages.filter((name) => shard === GoTestShard.All || shardForPackage(name) === shard);
    if (packages.length === 0) {
      log(JSON.stringify({ event: "ci_go_test_empty", shard, mode, packageCount: 0 }));
      return 0;
    }
  } else if (shard !== GoTestShard.All) {
    const discovery = run("go", ["list", "-f", "{{.ImportPath}}", "./..."], {
      shell: false, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], maxBuffer: 8 * 1024 * 1024,
    });
    if (discovery.error) throw discovery.error;
    if (discovery.status !== 0) {
      log(JSON.stringify({ event: "ci_go_test_discovery_failed", shard, exitCode: discovery.status, signal: discovery.signal }));
      return discovery.status ?? 1;
    }
    packages = selectPackages(discovery.stdout, shard);
  }
  log(JSON.stringify({ event: "ci_go_test_start", shard, packageCount: shard === GoTestShard.All ? null : packages.length, packages }));
  if (shard !== GoTestShard.All) {
    // -p=1 also serializes compilation. Populate the build cache at Go's default
    // compiler parallelism before any fixture runs, so cold caches do not extend
    // the serial test critical path. -c never executes tests or TestMain; the null
    // output supports packages with identical names without retaining binaries.
    // Remove this phase if native cold-cache measurements show no net saving.
    const compileStarted = performance.now();
    // Go recognizes literal NUL on Windows, not Node's extended device path
    // from os.devNull. That spelling is required for its multi-package exception.
    const nullOutput = platform === "win32" ? "NUL" : "/dev/null";
    const compilation = run("go", ["test", "-c", "-o", nullOutput, ...packages], commandOptions);
    if (compilation.error) throw compilation.error;
    const exitCode = compilation.status ?? 1;
    log(JSON.stringify({ event: "ci_go_test_compile", shard, elapsedSeconds: Math.round((performance.now() - compileStarted) / 1000), exitCode, signal: compilation.signal }));
    if (exitCode !== 0) return exitCode;
  }
  // Hosted Windows Git, shell and SQLite fixtures can starve bounded protocols
  // when package binaries share a runner. Separate runners provide parallelism;
  // retain -p=1 until full native Windows evidence permits concurrent packages.
  // Maximum-inventory workspace regressions perform thousands of durable
  // claims across fresh recovery owners. Their aggregate Windows package time
  // exceeds 20 minutes; this watchdog does not extend any product deadline.
  // Reassess the larger budget after native fixture timings permit reduction.
  const timeout = shard === GoTestShard.Worker ? "45m" : "20m";
  // Subprocess command changes can leave a consumer's test binary unchanged.
  // Disable result reuse so TestMain runs; compiled objects remain cacheable.
  const args = ["test", "-count=1", ...(shard === GoTestShard.All ? [] : ["-p=1"]), `-timeout=${timeout}`, ...packages];
  const testStarted = performance.now();
  const result = run("go", args, commandOptions);
  if (result.error) throw result.error;
  const exitCode = result.status ?? 1;
  log(JSON.stringify({ event: "ci_go_test_complete", shard, elapsedSeconds: Math.round((performance.now() - started) / 1000), testSeconds: Math.round((performance.now() - testStarted) / 1000), exitCode, signal: result.signal }));
  return exitCode;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.length === 2 && process.env.CI_GO_TEST_SHARD ? ["--shard", process.env.CI_GO_TEST_SHARD] : process.argv.slice(2);
    if (args.length !== 2 || args[0] !== "--shard") throw new Error("Expected --shard all|core|server|harness|worker");
    process.exitCode = runGoTests(args[1]);
  } catch (error) {
    console.error(JSON.stringify({ event: "ci_go_test_failed", message: error.message }));
    process.exitCode = 1;
  }
}
