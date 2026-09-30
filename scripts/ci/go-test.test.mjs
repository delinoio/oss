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

function runner(discovery, result = { status: 0 }) {
  const calls = [];
  const events = [];
  return {
    calls, events,
    options: {
      run(command, args, options) {
        calls.push({ command, args, options });
        return args[0] === "list" ? discovery : result;
      },
      log(line) { events.push(JSON.parse(line)); },
    },
  };
}

test("each Windows invocation discovers native packages and runs its whole shard without a shell", () => {
  for (const shard of Object.keys(fixtures)) {
    const fixture = runner({ status: 0, stdout: inventory });
    assert.equal(runGoTests(shard, fixture.options), 0);
    assert.equal(fixture.calls.length, 2);
    const [discovery, execution] = fixture.calls;
    assert.equal(discovery.command, "go");
    assert.deepEqual(discovery.args, ["list", "-f", "{{.ImportPath}}", "./..."]);
    assert.equal(discovery.options.shell, false);
    assert.deepEqual(discovery.options.stdio, ["ignore", "pipe", "inherit"]);
    assert.deepEqual(execution, {
      command: "go", args: ["test", "-p=1", "-timeout=20m", ...fixtures[shard].toSorted()],
      options: { shell: false, stdio: "inherit" },
    });
    assert.equal(fixture.events[0].packageCount, fixtures[shard].length);
    assert.deepEqual(fixture.events[0].packages, fixtures[shard].toSorted());
    assert.equal(fixture.events[1].event, "ci_go_test_complete");
    assert.equal(fixture.events[1].exitCode, 0);
    assert.ok(fixture.events[1].elapsedSeconds >= 0);
  }
});

test("Linux and macOS retain their original full-suite Go invocation", () => {
  const fixture = runner();
  assert.equal(runGoTests(GoTestShard.All, fixture.options), 0);
  assert.deepEqual(fixture.calls, [{
    command: "go", args: ["test", "-timeout=20m", "./..."], options: { shell: false, stdio: "inherit" },
  }]);
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

test("the CLI rejects malformed arguments before invoking Go", () => {
  const script = fileURLToPath(new URL("./go-test.mjs", import.meta.url));
  for (const args of [[], ["--shard", "unknown"], ["--shard", "core", "extra"], ["core"]]) {
    const result = spawnSync(process.execPath, [script, ...args], { encoding: "utf8" });
    assert.equal(result.status, 1);
    assert.equal(result.stdout, "");
    assert.equal(JSON.parse(result.stderr).event, "ci_go_test_failed");
  }
});
