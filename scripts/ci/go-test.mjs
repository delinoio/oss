import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

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

export function runGoTests(shard, { run = spawnSync, log = console.log } = {}) {
  if (!Object.values(GoTestShard).includes(shard)) throw new Error("Unknown Go test shard");
  const started = performance.now();
  let packages = ["./..."];
  if (shard !== GoTestShard.All) {
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
  // Hosted Windows Git, shell and SQLite fixtures can starve bounded protocols
  // when package binaries share a runner. Separate runners provide parallelism;
  // retain -p=1 until full native Windows evidence permits concurrent packages.
  const args = ["test", ...(shard === GoTestShard.All ? [] : ["-p=1"]), "-timeout=20m", ...packages];
  const result = run("go", args, { shell: false, stdio: "inherit" });
  if (result.error) throw result.error;
  const exitCode = result.status ?? 1;
  log(JSON.stringify({ event: "ci_go_test_complete", shard, elapsedSeconds: Math.round((performance.now() - started) / 1000), exitCode, signal: result.signal }));
  return exitCode;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.slice(2);
    if (args.length !== 2 || args[0] !== "--shard") throw new Error("Expected --shard all|core|server|harness|worker");
    process.exitCode = runGoTests(args[1]);
  } catch (error) {
    console.error(JSON.stringify({ event: "ci_go_test_failed", message: error.message }));
    process.exitCode = 1;
  }
}
