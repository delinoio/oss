// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { runTestJson, testTimings, timingSummary, writeTestReport } from "./go-test-report.mjs";
import { GoTestShard, runGoTests } from "./go-test.mjs";

const pkg = "example.test/timings";
const event = (Action, fields = {}) => JSON.stringify({ Action, Package: pkg, ...fields });
const collect = () => {
  const logs = [], output = [];
  return { logs, output, collector: testTimings({ log: (line) => logs.push(JSON.parse(line)), output: (text) => output.push(text) }) };
};

test("timings retain top-level outcomes without double-counting subtests or persisting output", () => {
  const { collector, logs, output } = collect();
  for (const line of [
    event("start"), event("run", { Test: "TestParent" }),
    event("run", { Test: "TestParent/private-native-content" }),
    event("output", { Test: "TestParent/private-native-content", Output: "success-secret\n" }),
    event("pass", { Test: "TestParent/private-native-content", Elapsed: 8 }),
    event("pass", { Test: "TestParent", Elapsed: 9 }),
    event("skip", { Test: "TestSkipped", Elapsed: 0 }),
    event("output", { Output: `ok  \t${pkg}\t(cached)\n` }),
    event("pass", { Elapsed: 0.01 }),
  ]) collector.line(line);
  const result = collector.finish();
  assert.equal(result.invalid, false);
  assert.deepEqual(result.packages, [{ package: pkg, result: "pass", elapsedSeconds: 0.01, cached: true, tests: [
    { test: "TestParent", result: "pass", elapsedSeconds: 9 }, { test: "TestSkipped", result: "skip", elapsedSeconds: 0 },
  ] }]);
  assert.equal(logs.filter((line) => line.event === "ci_go_test_case_start").length, 1);
  assert.deepEqual(output, []);
  assert.doesNotMatch(JSON.stringify(result), /secret|private-native-content/u);
});

test("failed and interrupted tests retain bounded diagnostics while successful test logs stay suppressed", () => {
  const { collector, output } = collect();
  for (const line of [
    event("run", { Test: "TestGood" }), event("output", { Test: "TestGood", Output: "successful detail\n" }), event("pass", { Test: "TestGood", Elapsed: 1 }),
    event("run", { Test: "TestBad" }), event("output", { Test: "TestBad/child", Output: "assertion failed\n" }), event("fail", { Test: "TestBad", Elapsed: 2 }),
    event("run", { Test: "TestPanic" }), event("output", { Test: "TestPanic", Output: "panic detail\n" }), event("fail", { Elapsed: 3 }),
  ]) collector.line(line);
  assert.equal(output.join(""), "assertion failed\npanic detail\n");
  assert.equal(collector.finish().packages[0].tests.at(-1).result, "incomplete");
  const bounded = collect();
  bounded.collector.line(event("run", { Test: "TestHuge" }));
  bounded.collector.line(event("output", { Test: "TestHuge", Output: "x".repeat(2 * 1024 * 1024) }));
  bounded.collector.line(event("fail", { Test: "TestHuge", Elapsed: 1 }));
  bounded.collector.line(event("fail", { Elapsed: 1 }));
  assert.equal(bounded.output.join("").length, 1024 * 1024);
});

test("malformed output and invalid durations cannot produce valid timing data", () => {
  for (const line of ["not JSON", "null", event("pass", { Elapsed: -1 }), event("pass", { Elapsed: "1" })]) {
    const { collector } = collect();
    collector.line(line);
    assert.equal(collector.finish().invalid, true);
  }
});

test("metadata reports rank the twenty slowest top-level tests and label cached times", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "go-report-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const report = { sourceRevision: "a".repeat(40), shard: "workspace", exitCode: 1, compileSeconds: 2, testSeconds: 24, elapsedSeconds: 26,
    packages: [{ package: pkg, result: "fail", elapsedSeconds: 24, cached: false, tests: Array.from({ length: 25 }, (_, index) => ({ test: `TestCase${index}`, result: index === 24 ? "fail" : "pass", elapsedSeconds: index })) }] };
  const logs = [];
  const path = join(directory, "nested", "timings.json");
  writeTestReport(path, report, { log: (line) => logs.push(JSON.parse(line)) });
  assert.deepEqual(JSON.parse(readFileSync(path, "utf8")), report);
  assert.equal(logs[0].tests.length, 20);
  assert.equal(logs[0].tests[0].test, "TestCase24");
  const summary = timingSummary(report);
  assert.match(summary, /TestCase24 \| fail \| 24 \| false/u);
  assert.match(summary, /not current runner time/u);
  assert.ok(summary.indexOf("TestCase24") < summary.indexOf("TestCase23"));
});

test("streamed child output drains partial final lines and preserves failures, signals and spawn errors", async () => {
  const payload = `${event("start")}\n${event("pass", { Elapsed: 0.1 })}`;
  for (const code of [0, 7]) {
    const source = `process.stdout.write(${JSON.stringify(payload)}); process.exitCode = ${code};`;
    const result = await runTestJson(process.execPath, ["-e", source], { shell: false }, { log() {}, output() {} });
    assert.equal(result.status, code);
    assert.equal(result.packages[0].result, "pass");
  }
  for (const payload of ["bad JSON\n", `${event("run", { Test: "TestUnfinished" })}\n`, ""]) {
    const result = await runTestJson(process.execPath, ["-e", `process.stdout.write(${JSON.stringify(payload)})`], { shell: false }, { log() {}, output() {} });
    assert.equal(result.status, 1);
  }
  const missing = await runTestJson("missing-go-fixture-executable", [], { shell: false }, { log() {}, output() {} });
  assert.equal(missing.error.code, "ENOENT");
  const signaled = await runTestJson(process.execPath, ["-e", "process.kill(process.pid, 'SIGTERM')"], { shell: false }, { log() {}, output() {} });
  assert.notEqual(signaled.status, 0);
});

test("real Go execution records passing, skipped, failed and incomplete tests with original exit status", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "go-timing-native-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  writeFileSync(join(directory, "go.mod"), `module ${pkg}\n\ngo 1.24\n`);
  writeFileSync(join(directory, "timings_test.go"), `package timings
import "testing"
func TestPass(t *testing.T) { t.Log("success-only-output"); t.Run("private-subtest", func(t *testing.T) {}) }
func TestSkip(t *testing.T) { t.Skip("skip detail") }
func TestFail(t *testing.T) { t.Fatal("expected-fixture-failure") }
func TestPanic(t *testing.T) { panic("expected-fixture-panic") }
`);
  const logs = [], output = [];
  const options = { log: (line) => logs.push(JSON.parse(line)), output: (text) => output.push(text) };
  const passArgs = ["test", "-json", "-run=TestPass|TestSkip", "."];
  const passing = await runTestJson("go", passArgs, { cwd: directory, shell: false }, options);
  assert.equal(passing.status, 0);
  assert.deepEqual(passing.packages[0].tests.map((entry) => [entry.test, entry.result]), [["TestPass", "pass"], ["TestSkip", "skip"]]);
  assert.deepEqual(output, []);
  const cached = await runTestJson("go", passArgs, { cwd: directory, shell: false }, options);
  assert.equal(cached.status, 0);
  assert.equal(cached.packages[0].cached, true);
  assert.deepEqual(cached.packages[0].tests.map((entry) => [entry.test, entry.result]), [["TestPass", "pass"], ["TestSkip", "skip"]]);
  const path = join(directory, "reports", "all.json");
  const failed = await runGoTests(GoTestShard.All, { cwd: directory, mode: "full", reportPath: path, log: options.log,
    runTests: (command, args, commandOptions) => runTestJson(command, args, commandOptions, options) });
  assert.equal(failed, 1);
  const report = JSON.parse(readFileSync(path, "utf8"));
  assert.equal(report.exitCode, 1);
  assert.equal(report.packages[0].result, "fail");
  assert.equal(report.packages[0].tests.find((entry) => entry.test === "TestFail").result, "fail");
  assert.equal(report.packages[0].tests.find((entry) => entry.test === "TestPanic").result, "fail");
  assert.match(output.join(""), /expected-fixture-failure/u);
  assert.match(output.join(""), /expected-fixture-panic/u);
  assert.doesNotMatch(JSON.stringify(report), /private-subtest|success-only-output|expected-fixture/u);
});
