// SPDX-License-Identifier: Apache-2.0
import { spawn } from "node:child_process";
import { appendFileSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const outcomes = new Set(["pass", "fail", "skip"]);
const diagnosticLimit = 1024 * 1024;
const packageName = /^[A-Za-z0-9._~+/-]+$/u;
const testName = /^(?:Test|Example|Fuzz)[\p{L}\p{Nd}_]*$/u;
const goHarnessOutput = /^(?:=== (?:RUN|PAUSE|CONT|NAME)\s+\S.*|--- (?:PASS|FAIL|SKIP):\s+\S+\s+\(\d+(?:\.\d+)?s\)|(?:ok|FAIL)\s+\S+\s+(?:\d+(?:\.\d+)?s|\(cached\))|(?:PASS|FAIL))\r?\n?$/u;

export function testTimings({ log = console.log, output = (text) => process.stdout.write(text) } = {}) {
  const packages = new Map();
  const diagnostics = new Map();
  let invalid = false;
  let diagnosticSequence = 0;
  const recordFor = (name) => {
    if (!packages.has(name)) packages.set(name, { package: name, result: "incomplete", elapsedSeconds: null, cached: false, tests: new Map() });
    return packages.get(name);
  };
  const diagnosticFor = (name, test) => {
    const packageDiagnostics = diagnostics.get(name) ?? { buckets: new Map() };
    const key = test ?? "";
    const bucket = packageDiagnostics.buckets.get(key) ?? { entries: [], bytes: 0, truncated: false };
    packageDiagnostics.buckets.set(key, bucket);
    diagnostics.set(name, packageDiagnostics);
    return { packageDiagnostics, bucket };
  };
  const discardDiagnostics = (name, test) => {
    const packageDiagnostics = diagnostics.get(name);
    if (!packageDiagnostics) return;
    packageDiagnostics.buckets.delete(test ?? "");
    if (packageDiagnostics.buckets.size === 0) diagnostics.delete(name);
  };
  const flushDiagnostics = (name) => {
    const packageDiagnostics = diagnostics.get(name);
    const record = packages.get(name);
    if (!packageDiagnostics || !record) return;
    const entries = [...packageDiagnostics.buckets.values()].flatMap((bucket) => bucket.entries)
      .filter((entry) => {
        const result = entry.test ? record.tests.get(entry.test)?.result : record.result;
        return result === "fail" || result === "incomplete";
      }).sort((a, b) => a.sequence - b.sequence);
    if (entries.some((entry) => packageDiagnostics.buckets.get(entry.test ?? "").truncated)) {
      log(JSON.stringify({ event: "ci_go_test_diagnostics_truncated", package: name, limitBytes: diagnosticLimit }));
    }
    for (const entry of entries) output(entry.text.toString("utf8"));
    diagnostics.delete(name);
  };
  return {
    line(line) {
      let event;
      try { event = JSON.parse(line); } catch { invalid = true; return; }
      if (!event || typeof event !== "object" || typeof event.Action !== "string") { invalid = true; return; }
      if (event.Action === "build-output") {
        if (typeof event.Output === "string") output(event.Output);
        return;
      }
      if (event.Action === "build-fail") return;
      if (typeof event.Package !== "string" || !packageName.test(event.Package)) { invalid = true; return; }
      const record = recordFor(event.Package);
      const topLevel = typeof event.Test === "string" ? event.Test.split("/")[0] : null;
      if (event.Action === "output") {
        if (typeof event.Output !== "string") { invalid = true; return; }
        if (!event.Test && /^ok\s+\S+\s+\(cached\)/u.test(event.Output)) record.cached = true;
        // JSON enables verbose success output. Keep bounded failure diagnostics
        // in memory, but never persist raw output or dynamic subtest names.
        if (goHarnessOutput.test(event.Output)) return;
        const current = topLevel ? record.tests.get(topLevel)?.result : null;
        if (current === "pass" || current === "skip") return;
        const { bucket } = diagnosticFor(event.Package, topLevel);
        const bytes = Buffer.from(event.Output);
        const text = bytes.subarray(Math.max(0, bytes.length - diagnosticLimit));
        bucket.truncated ||= text.length < bytes.length;
        bucket.entries.push({ test: topLevel, text, sequence: diagnosticSequence++ });
        bucket.bytes += text.length;
        while (bucket.bytes > diagnosticLimit) {
          bucket.bytes -= bucket.entries.shift().text.length;
          bucket.truncated = true;
        }
        return;
      }
      if (event.Test && (!testName.test(topLevel) || event.Test !== topLevel)) return;
      if (event.Action === "run" && topLevel) {
        record.tests.set(topLevel, { test: topLevel, result: "incomplete", elapsedSeconds: null });
        log(JSON.stringify({ event: "ci_go_test_case_start", package: event.Package, test: topLevel }));
      }
      if (!outcomes.has(event.Action)) return;
      if (event.Elapsed !== undefined && (!Number.isFinite(event.Elapsed) || event.Elapsed < 0)) { invalid = true; return; }
      const elapsedSeconds = event.Elapsed ?? 0;
      if (topLevel) {
        record.tests.set(topLevel, { test: topLevel, result: event.Action, elapsedSeconds });
        if (event.Action === "pass" || event.Action === "skip") discardDiagnostics(event.Package, topLevel);
      } else {
        record.result = event.Action;
        record.elapsedSeconds = elapsedSeconds;
        if (event.Action === "fail") {
          flushDiagnostics(event.Package);
        }
        log(JSON.stringify({ event: "ci_go_test_package_complete", package: event.Package, result: event.Action, elapsedSeconds, cached: record.cached }));
      }
    },
    finish({ flushIncompleteDiagnostics = false } = {}) {
      if (flushIncompleteDiagnostics) for (const name of diagnostics.keys()) flushDiagnostics(name);
      return { invalid, packages: [...packages.values()].map((record) => ({ ...record, tests: [...record.tests.values()].sort((a, b) => a.test.localeCompare(b.test)) })).sort((a, b) => a.package.localeCompare(b.package)) };
    },
  };
}

export async function runTestJson(command, args, options, { log = console.log, output } = {}) {
  const collector = testTimings({ log, output });
  const child = spawn(command, args, { ...options, stdio: ["ignore", "pipe", "inherit"] });
  const lines = createInterface({ input: child.stdout, crlfDelay: Infinity });
  lines.on("line", (line) => collector.line(line));
  const forward = (signal) => child.kill(signal);
  const onInterrupt = () => forward("SIGINT");
  const onTerminate = () => forward("SIGTERM");
  process.on("SIGINT", onInterrupt);
  process.on("SIGTERM", onTerminate);
  try {
    const result = await new Promise((resolveResult) => {
      let error;
      child.on("error", (value) => { error = value; });
      // close follows drained stdout, including its final unterminated line.
      child.on("close", (status, signal) => resolveResult({ status, signal, error }));
    });
    const timings = collector.finish({ flushIncompleteDiagnostics: result.status !== 0 || result.signal || result.error });
    const incomplete = timings.packages.some((record) => record.result === "incomplete" || record.tests.some((entry) => entry.result === "incomplete"));
    if (timings.invalid || result.status === 0 && (timings.packages.length === 0 || incomplete)) {
      log(JSON.stringify({ event: "ci_go_test_timing_invalid" }));
      result.status = result.status || 1;
    }
    return { ...result, packages: timings.packages };
  } finally {
    lines.close();
    process.off("SIGINT", onInterrupt);
    process.off("SIGTERM", onTerminate);
  }
}

export function writeTestReport(path, report, { log = console.log } = {}) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(`${path}.pending`, `${JSON.stringify(report, null, 2)}\n`);
  renameSync(`${path}.pending`, path);
  const slowest = report.packages.flatMap((record) => record.tests.map((entry) => ({ package: record.package, cached: record.cached, ...entry })))
    .filter((entry) => entry.elapsedSeconds !== null).sort((a, b) => b.elapsedSeconds - a.elapsedSeconds).slice(0, 20);
  log(JSON.stringify({ event: "ci_go_test_slowest", shard: report.shard, tests: slowest }));
}

export function timingSummary(report) {
  const rows = report.packages.flatMap((record) => record.tests.map((entry) => ({ package: record.package, ...entry })))
    .filter((entry) => entry.elapsedSeconds !== null).sort((a, b) => b.elapsedSeconds - a.elapsedSeconds).slice(0, 20);
  return [
    `### Go test timings (${report.shard})`, "",
    `Source: \`${report.sourceRevision ?? "local"}\`. Exit code: ${report.exitCode ?? "pending"}.`, "",
    `Compile: ${report.compileSeconds}s. Test wall time: ${report.testSeconds}s. Total: ${report.elapsedSeconds}s.`, "",
    "Cached packages retain the original test timings; their elapsed values are not current runner time.", "",
    "| Package | Result | Seconds | Cached |", "| --- | --- | ---: | --- |",
    ...report.packages.map((record) => `| ${record.package} | ${record.result} | ${record.elapsedSeconds ?? "incomplete"} | ${record.cached} |`), "",
    "Slowest completed top-level tests (subtests are included in their parent's time):", "",
    "| Package | Test | Result | Seconds | Cached |", "| --- | --- | --- | ---: | --- |",
    ...rows.map((entry) => `| ${entry.package} | ${entry.test} | ${entry.result} | ${entry.elapsedSeconds} | ${report.packages.find((record) => record.package === entry.package).cached} |`), "",
  ].join("\n");
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length !== 4 || process.argv[2] !== "--summary") throw new Error("Expected --summary <report.json>");
  const report = JSON.parse(readFileSync(process.argv[3], "utf8"));
  const summary = timingSummary(report);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
  else process.stdout.write(summary);
}
