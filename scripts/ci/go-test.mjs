import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { affectedGoPackages, GoMode, selectionOptions } from "./go-affected.mjs";
import { runTestJson, writeTestReport } from "./go-test-report.mjs";

export const GoTestShard = Object.freeze({
  All: "all",
  Core: "core",
  Server: "server",
  Harness: "harness",
  Worker: "worker",
  Workspace: "workspace",
});

const delidev = "github.com/delinoio/oss/cmds/delidev-cli";
const within = (value, prefix) => value === prefix || value.startsWith(`${prefix}/`);

export function shardForPackage(importPath) {
  if (!within(importPath, delidev)) return GoTestShard.Core;
  if (["server", "store"].some((name) => within(importPath, `${delidev}/internal/${name}`))) return GoTestShard.Server;
  if (["cli", "harness"].some((name) => within(importPath, `${delidev}/internal/${name}`))) return GoTestShard.Harness;
  if (within(importPath, `${delidev}/internal/workspace`)) return GoTestShard.Workspace;
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

export async function runGoTests(shard, { run = spawnSync, runTests = runTestJson, log = console.log, platform = process.platform, reportPath, saveReport = writeTestReport, ...options } = selectionOptions()) {
  if (!Object.values(GoTestShard).includes(shard)) throw new Error("Unknown Go test shard");
  const started = performance.now();
  const report = { sourceRevision: /^[0-9a-f]{40}$/u.test(process.env.GITHUB_SHA ?? "") ? process.env.GITHUB_SHA : null, shard, mode: options.mode ?? GoMode.Full, commands: [], compileSeconds: 0, testSeconds: 0, elapsedSeconds: 0, exitCode: null, signal: null, packages: [] };
  const finish = (exitCode, signal = null) => {
    Object.assign(report, { exitCode, signal, elapsedSeconds: Math.round((performance.now() - started) / 1000) });
    saveReport(reportPath ?? resolve(options.cwd ?? process.cwd(), ".turbo", "go-test", `${shard}.json`), report, { log });
    return exitCode;
  };
  const commandOptions = { shell: false, stdio: "inherit", ...(options.cwd ? { cwd: options.cwd } : {}) };
  let packages = ["./..."];
  const mode = options.mode ?? GoMode.Full;
  if (!Object.values(GoMode).includes(mode)) throw new Error("Unknown Go validation mode");
  try {
    if (mode === GoMode.Affected) {
      const selection = affectedGoPackages({ ...options, run(command, args, commandOptions) {
        report.commands.push([command, ...args]);
        return run(command, args, commandOptions);
      }, log });
      packages = selection.packages.filter((name) => shard === GoTestShard.All || shardForPackage(name) === shard);
      if (packages.length === 0) {
        log(JSON.stringify({ event: "ci_go_test_empty", shard, mode, packageCount: 0 }));
        return finish(0);
      }
    } else if (shard !== GoTestShard.All) {
      report.commands.push(["go", "list", "-f", "{{.ImportPath}}", "./..."]);
      const discovery = run("go", ["list", "-f", "{{.ImportPath}}", "./..."], {
        shell: false, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], maxBuffer: 8 * 1024 * 1024,
      });
      if (discovery.error) throw discovery.error;
      if (discovery.status !== 0) {
        log(JSON.stringify({ event: "ci_go_test_discovery_failed", shard, exitCode: discovery.status, signal: discovery.signal }));
        return finish(discovery.status ?? 1, discovery.signal);
      }
      packages = selectPackages(discovery.stdout, shard);
    }
  } catch (error) {
    log(JSON.stringify({ event: "ci_go_test_selection_failed", shard, mode, message: error.message }));
    finish(1);
    throw error;
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
    const compileArgs = ["test", "-c", "-o", nullOutput, ...packages];
    report.commands.push(["go", ...compileArgs]);
    const compilation = run("go", compileArgs, commandOptions);
    if (compilation.error) { finish(1); throw compilation.error; }
    const exitCode = compilation.status ?? 1;
    report.compileSeconds = Math.round((performance.now() - compileStarted) / 1000);
    log(JSON.stringify({ event: "ci_go_test_compile", shard, elapsedSeconds: report.compileSeconds, exitCode, signal: compilation.signal }));
    if (exitCode !== 0) return finish(exitCode, compilation.signal);
  }
  // Hosted Windows Git, shell and SQLite fixtures can starve bounded protocols
  // when package binaries share a runner. Separate runners provide parallelism;
  // retain -p=1 until full native Windows evidence permits concurrent packages.
  // Maximum-inventory workspace regressions perform thousands of durable
  // claims across fresh recovery owners. Their aggregate Windows package time
  // exceeds 20 minutes; this watchdog does not extend any product deadline.
  // Reassess the larger budget after native fixture timings permit reduction.
  const timeout = [GoTestShard.Worker, GoTestShard.Workspace].includes(shard) ? "45m" : "20m";
  // Subprocess command changes can leave a consumer's test binary unchanged.
  // Disable result reuse so TestMain runs; compiled objects remain cacheable.
  const args = ["test", "-count=1", "-json", ...(shard === GoTestShard.All ? [] : ["-p=1"]), `-timeout=${timeout}`, ...packages];
  report.commands.push(["go", ...args]);
  const testStarted = performance.now();
  const result = await runTests("go", args, commandOptions, { log });
  report.testSeconds = Math.round((performance.now() - testStarted) / 1000);
  report.packages = result.packages ?? [];
  if (result.error) { finish(1, result.signal); throw result.error; }
  const exitCode = result.status ?? 1;
  log(JSON.stringify({ event: "ci_go_test_complete", shard, elapsedSeconds: Math.round((performance.now() - started) / 1000), testSeconds: report.testSeconds, exitCode, signal: result.signal }));
  return finish(exitCode, result.signal);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.length === 2 && process.env.CI_GO_TEST_SHARD ? ["--shard", process.env.CI_GO_TEST_SHARD] : process.argv.slice(2);
    if (args.length !== 2 || args[0] !== "--shard") throw new Error("Expected --shard all|core|server|harness|worker|workspace");
    process.exitCode = await runGoTests(args[1]);
  } catch (error) {
    console.error(JSON.stringify({ event: "ci_go_test_failed", message: error.message }));
    process.exitCode = 1;
  }
}
