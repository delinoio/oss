import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { GoTestShard, runGoTests, selectPackages } from "./go-test.mjs";

const root = "github.com/delinoio/oss";
const delidev = `${root}/cmds/delidev-cli`;
const fixtures = {
  [GoTestShard.Core]: [
    `${root}/cmds/async-commit-hook/internal/core`, `${root}/cmds/derun/internal/e2e`,
    `${root}/cmds/runmoor/internal/runmoor`, `${root}/servers/devhud-api/internal/server`,
    `${root}/protos/gen/go/delidev/v1`, `${root}/new-windows-only-package`, `${delidev}-tools`,
  ],
  [GoTestShard.Server]: [
    `${delidev}/internal/server`, `${delidev}/internal/store`, `${delidev}/internal/server/new-feature`,
  ],
  [GoTestShard.Harness]: [
    `${delidev}/internal/cli`, `${delidev}/internal/harness`, `${delidev}/internal/harness/codex`,
    `${delidev}/internal/harness/new-adapter`,
  ],
  [GoTestShard.Worker]: [
    delidev, `${delidev}/internal/worker`, `${delidev}/internal/workspace`,
    `${delidev}/internal/new-windows-feature`, `${delidev}/internal/serverless`,
    `${delidev}/internal/client`, `${delidev}/internal/harness-tools`,
  ],
};
const packages = Object.values(fixtures).flat();
const inventory = `${packages.toReversed().join("\r\n")}\r\n`;

test("Windows shards cover the entire discovered inventory once, including new packages and path boundaries", () => {
  const selected = Object.entries(fixtures).flatMap(([shard, expected]) => {
    const actual = selectPackages(inventory, shard);
    assert.deepEqual(actual, expected.toSorted());
    return actual;
  });
  assert.equal(new Set(selected).size, packages.length);
  assert.deepEqual(selected.toSorted(), packages.toSorted());
});

test("invalid shards, missing packages and invalid discovery cannot silently skip validation", () => {
  for (const shard of ["", "unknown", "all"]) assert.throws(() => selectPackages(inventory, shard));
  for (const output of ["", "\r\n", "bad package", "duplicate\nduplicate"]) {
    assert.throws(() => selectPackages(output, GoTestShard.Core));
  }
  assert.throws(() => selectPackages(`${delidev}/internal/server`, GoTestShard.Core), /no packages/u);
});

function runner(discovery, result = { status: 0 }, compilation = { status: 0 }) {
  const calls = [];
  const events = [];
  return {
    calls, events,
    options: {
      run(command, args, options) {
        calls.push({ command, args, options });
        return args[0] === "list" ? discovery : args.includes("-c") ? compilation : result;
      },
      log(line) { events.push(JSON.parse(line)); },
    },
  };
}

test("each Windows invocation discovers native packages and runs its whole shard without a shell", () => {
  for (const shard of Object.keys(fixtures)) {
    const fixture = runner({ status: 0, stdout: inventory });
    assert.equal(runGoTests(shard, fixture.options), 0);
    assert.equal(fixture.calls.length, 3);
    const [discovery, compilation, execution] = fixture.calls;
    assert.equal(discovery.command, "go");
    assert.deepEqual(discovery.args, ["list", "-f", "{{.ImportPath}}", "./..."]);
    assert.equal(discovery.options.shell, false);
    assert.deepEqual(discovery.options.stdio, ["ignore", "pipe", "inherit"]);
    assert.deepEqual(compilation, {
      command: "go", args: ["test", "-c", "-o", process.platform === "win32" ? "NUL" : "/dev/null", ...fixtures[shard].toSorted()],
      options: { shell: false, stdio: "inherit" },
    });
    assert.deepEqual(execution, {
      command: "go", args: ["test", "-count=1", "-p=1", shard === GoTestShard.Worker ? "-timeout=45m" : "-timeout=20m", ...fixtures[shard].toSorted()],
      options: { shell: false, stdio: "inherit" },
    });
    assert.equal(fixture.events[0].packageCount, fixtures[shard].length);
    assert.deepEqual(fixture.events[0].packages, fixtures[shard].toSorted());
    assert.equal(fixture.events[1].event, "ci_go_test_compile");
    assert.equal(fixture.events[1].exitCode, 0);
    assert.equal(fixture.events[2].event, "ci_go_test_complete");
    assert.equal(fixture.events[2].exitCode, 0);
    assert.ok(fixture.events[2].elapsedSeconds >= fixture.events[2].testSeconds);
  }
});

test("Linux and macOS execute the full Go suite without reusing successful test results", () => {
  const fixture = runner();
  assert.equal(runGoTests(GoTestShard.All, fixture.options), 0);
  assert.deepEqual(fixture.calls, [{
    command: "go", args: ["test", "-count=1", "-timeout=20m", "./..."], options: { shell: false, stdio: "inherit" },
  }]);
});

test("Windows compilation uses Go's literal NUL exception rather than Node's extended device path", () => {
  const fixture = runner({ status: 0, stdout: inventory });
  assert.equal(runGoTests(GoTestShard.Core, { ...fixture.options, platform: "win32" }), 0);
  assert.deepEqual(fixture.calls[1].args.slice(0, 4), ["test", "-c", "-o", "NUL"]);
  assert.deepEqual(fixture.calls[2].args.slice(0, 4), ["test", "-count=1", "-p=1", "-timeout=20m"]);
});

test("discovery failures never start tests, even with partial output", () => {
  for (const failure of [{ status: 23, stdout: inventory }, { status: null, signal: "SIGTERM", stdout: inventory }]) {
    const fixture = runner(failure);
    assert.equal(runGoTests(GoTestShard.Worker, fixture.options), failure.status ?? 1);
    assert.equal(fixture.calls.length, 1);
    assert.equal(fixture.events[0].event, "ci_go_test_discovery_failed");
  }
  const fixture = runner({ error: new Error("spawn failed") });
  assert.throws(() => runGoTests(GoTestShard.Core, fixture.options), /spawn failed/u);
  assert.equal(fixture.calls.length, 1);
});

test("test failures, signals and spawn errors cannot become success", () => {
  for (const failure of [{ status: 7 }, { status: null, signal: "SIGTERM" }]) {
    const fixture = runner({ status: 0, stdout: inventory }, failure);
    assert.equal(runGoTests(GoTestShard.Server, fixture.options), failure.status ?? 1);
    assert.equal(fixture.events.at(-1).exitCode, failure.status ?? 1);
  }
  const fixture = runner({ status: 0, stdout: inventory }, { error: new Error("spawn failed") });
  assert.throws(() => runGoTests(GoTestShard.Core, fixture.options), /spawn failed/u);
});

test("compilation failures stop before any test binary can run", () => {
  for (const failure of [{ status: 9 }, { status: null, signal: "SIGTERM" }]) {
    const fixture = runner({ status: 0, stdout: inventory }, { status: 0 }, failure);
    assert.equal(runGoTests(GoTestShard.Core, fixture.options), failure.status ?? 1);
    assert.equal(fixture.calls.length, 2);
    assert.equal(fixture.events.at(-1).event, "ci_go_test_compile");
    assert.equal(fixture.events.at(-1).exitCode, failure.status ?? 1);
  }
  const fixture = runner({ status: 0, stdout: inventory }, { status: 0 }, { error: new Error("compiler spawn failed") });
  assert.throws(() => runGoTests(GoTestShard.Core, fixture.options), /compiler spawn failed/u);
  assert.equal(fixture.calls.length, 2);
});

test("the CLI rejects malformed arguments before invoking Go", () => {
  const script = fileURLToPath(new URL("./go-test.mjs", import.meta.url));
  for (const args of [[], ["--shard", "unknown"], ["--shard", "core", "extra"], ["core"]]) {
    const result = spawnSync(process.execPath, [script, ...args], { encoding: "utf8" });
    assert.equal(result.status, 1);
    assert.equal(result.stdout, "");
    assert.equal(JSON.parse(result.stderr).event, "ci_go_test_failed");
  }
});
