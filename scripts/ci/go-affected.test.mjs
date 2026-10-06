import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { affectedGoPackages, commandConsumers, parseInventory, selectAffected, selectionOptions } from "./go-affected.mjs";
import { GoTestShard, runGoTests } from "./go-test.mjs";
import { runGoQuality } from "./go-quality.mjs";

const modulePath = "github.com/delinoio/oss";
const root = process.cwd();
const path = (directory) => `${modulePath}/${directory}`;
const pkg = (directory, imports = []) => ({ path: path(directory), directory, imports: imports.map(path) });
const inventory = [
  pkg("cmds/delidev-cli", ["cmds/delidev-cli/internal/worker"]),
  pkg("cmds/delidev-cli/internal/apiproxy"),
  pkg("cmds/delidev-cli/internal/worker", ["cmds/delidev-cli/internal/apiproxy", "cmds/delidev-cli/internal/workspace"]),
  pkg("cmds/delidev-cli/internal/workspace"),
  pkg("cmds/delidev-cli/internal/cli"), pkg("cmds/delidev-cli/internal/server"),
  pkg("servers/devhud-api"), pkg("cmds/runmoor/internal/runmoor"),
];
const changes = (name, status = "M") => [{ path: name, status }];
const affected = (name, status) => selectAffected(inventory, changes(name, status)).packages;

test("source changes include reverse and subprocess consumers without unrelated durable workspace tests", () => {
  assert.deepEqual(affected("cmds/delidev-cli/internal/apiproxy/proxy.go"), inventory.slice(0, 6).filter((item) => !item.directory.endsWith("/workspace")).map((item) => item.path).sort());
  assert.deepEqual(affected("cmds/delidev-cli/internal/workspace/state.go"), inventory.slice(0, 6).filter((item) => !item.directory.endsWith("/apiproxy")).map((item) => item.path).sort());
  assert.ok(commandConsumers.some((item) => item.command === "cmds/runmoor"));
});

test("test-only changes and testdata select their owner without propagating production dependencies", () => {
  for (const name of ["cmds/delidev-cli/internal/apiproxy/proxy_test.go", "cmds/delidev-cli/internal/apiproxy/testdata/nested/request.json"]) {
    assert.deepEqual(affected(name), [path("cmds/delidev-cli/internal/apiproxy")]);
  }
  assert.deepEqual(affected("apps/delidev/src/App.tsx"), []);
});

test("frontend source changes include their real Go embed owners and callers", () => {
  const embed = "servers/devhud-api/internal/adminassets";
  const packages = [pkg(embed), pkg("servers/devhud-api", [embed]), pkg("cmds/delidev-cli")];
  for (const file of ["apps/devhud-admin/src/App.tsx", "packages/devhud-api-client/src/client.ts"]) {
    assert.deepEqual(selectAffected(packages, changes(file)).packages, packages.slice(0, 2).map((item) => item.path).sort());
    assert.throws(() => selectAffected(inventory, changes(file)), /embed owner/u);
  }
});

test("deletions, moves and unknown domain resources expand conservatively", () => {
  for (const [name, status] of [["cmds/delidev-cli/removed/source.go", "D"], ["cmds/delidev-cli/internal/worker/state.go", "T"], ["cmds/delidev-cli/internal/worker/schema.json", "M"]]) {
    assert.deepEqual(affected(name, status), inventory.slice(0, 6).map((item) => item.path).sort());
  }
  assert.deepEqual(affected("unknown/package/removed.go", "D"), inventory.map((item) => item.path).sort());
  assert.deepEqual(affected("cmds/new-command/new.go", "A"), inventory.map((item) => item.path).sort());
  assert.deepEqual(affected("go.mod"), inventory.map((item) => item.path).sort());
});

test("native inventory includes production, internal-test and external-test imports", () => {
  const records = [
    { ImportPath: path("leaf"), Dir: join(root, "leaf") },
    { ImportPath: path("internal"), Dir: join(root, "internal"), TestImports: [path("leaf")] },
    { ImportPath: path("external"), Dir: join(root, "external"), XTestImports: [path("leaf")] },
  ];
  const parsed = parseInventory(records.map(JSON.stringify).join("\n"), root);
  assert.deepEqual(selectAffected(parsed, changes("leaf/file.go")).packages, records.map((item) => item.ImportPath).sort());
  for (const output of ["", "{", "noise\n{}", "{}", JSON.stringify({ ...records[0], Error: { Err: "missing embed" } }), records.map(JSON.stringify).join("\n") + "false", JSON.stringify({ ...records[0], Dir: tmpdir() })]) {
    assert.throws(() => parseInventory(output, root));
  }
  assert.throws(() => parseInventory([records[0], records[0]].map(JSON.stringify).join("\n"), root));
});

function mock(discovery = {}, selectedPath = "cmds/delidev-cli/internal/apiproxy/proxy.go", failure = null) {
  const calls = [], events = [];
  const run = (command, args, options) => {
    calls.push({ command, args, options });
    if (failure && command === failure.command && args[0] === failure.action) return failure.result;
    if (command === "git") return { status: 0, stdout: args[0] === "rev-parse" ? root : args[0] === "diff" ? `M\0${selectedPath}\0` : "" };
    if (command === "go" && args[0] === "list") return { status: 0, stdout: inventory.map((item) => JSON.stringify({ ImportPath: item.path, Dir: join(root, item.directory), Imports: item.imports })).join("\n"), ...discovery };
    if (command === "gofmt") return { status: 0, stdout: "" };
    return { status: 0 };
  };
  return { calls, events, options: { mode: "affected", base: "a".repeat(40), head: "b".repeat(40), run, log: (line) => events.push(JSON.parse(line)) } };
}

test("empty affected Windows shards succeed without compiling or running fixtures", () => {
  const fixture = mock();
  assert.equal(runGoTests(GoTestShard.Core, fixture.options), 0);
  assert.equal(fixture.events.at(-1).event, "ci_go_test_empty");
  assert.ok(!fixture.calls.some((call) => call.args[0] === "test"));
  const worker = mock();
  assert.equal(runGoTests(GoTestShard.Worker, { ...worker.options, platform: "win32" }), 0);
  assert.deepEqual(worker.calls.at(-2).args.slice(0, 4), ["test", "-c", "-o", "NUL"]);
  assert.deepEqual(worker.calls.at(-1).args.slice(0, 3), ["test", "-p=1", "-timeout=45m"]);
  const unix = mock();
  assert.equal(runGoTests(GoTestShard.All, unix.options), 0);
  assert.deepEqual(unix.calls.at(-1).args.slice(0, 2), ["test", "-timeout=20m"]);
});

test("bad comparisons, discovery errors and partial inventories cannot silently pass", () => {
  for (const base of [undefined, "", "--help", "0".repeat(40)]) assert.throws(() => affectedGoPackages({ ...mock().options, base }));
  for (const result of [{ status: 2, stdout: "partial" }, { status: null, signal: "SIGTERM" }, { error: new Error("spawn failed") }]) {
    assert.throws(() => runGoTests(GoTestShard.All, mock({}, undefined, { command: "go", action: "list", result }).options));
  }
  assert.throws(() => selectionOptions({ CI_GO_MODE: "unknown" }));
  assert.equal(selectionOptions({}).mode, "full");
});

test("Go quality preserves empty selection and propagates formatting/vet failures", () => {
  const empty = mock({}, "apps/delidev/src/App.tsx");
  assert.equal(runGoQuality(empty.options), 0);
  assert.ok(!empty.calls.some((call) => call.command === "gofmt"));
  const calls = [];
  for (const failed of ["gofmt", "go"]) {
    assert.equal(runGoQuality({ mode: "full", log() {}, run(command, args, options) {
      calls.push({ command, args, options });
      if (command === "git") return { status: 0, stdout: "cmds/source.go\0" };
      return { status: command === failed ? 3 : 0, stdout: "" };
    } }), 3);
  }
  assert.ok(calls.every((call) => call.options.shell === false));
});

test("real native Go discovery respects OS files, test imports and exact Git changes", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "ci-go-affected-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  const write = (name, contents) => { const file = join(cwd, name); mkdirSync(dirname(file), { recursive: true }); writeFileSync(file, contents); };
  git("init", "--quiet"); git("config", "user.email", "fixture@example.invalid"); git("config", "user.name", "CI fixture");
  write("go.mod", `module ${modulePath}\n\ngo 1.22\n`);
  write("cmds/sample/leaf/leaf.go", "package leaf\nconst Value = 1\n");
  write("cmds/sample/consumer/consumer.go", "package consumer\n");
  write("cmds/sample/consumer/consumer_test.go", `package consumer_test\nimport ("testing"; "${modulePath}/cmds/sample/leaf")\nfunc TestValue(t *testing.T) { if leaf.Value < 1 { t.Fatal("value") } }\n`);
  write("cmds/sample/native/native_windows.go", "package native\nconst Value = 1\n");
  write("cmds/sample/native/native_unix.go", "//go:build !windows\n\npackage native\nconst Value = 2\n");
  git("add", "."); git("commit", "--quiet", "-m", "fixture baseline"); const base = git("rev-parse", "HEAD");
  write("cmds/sample/leaf/leaf.go", "package leaf\nconst Value = 2\n");
  git("add", "."); git("commit", "--quiet", "-m", "fixture change"); const head = git("rev-parse", "HEAD");
  const result = affectedGoPackages({ base, head, cwd, log() {} });
  assert.deepEqual(result.packages, [path("cmds/sample/consumer"), path("cmds/sample/leaf")]);
  assert.equal(runGoTests(GoTestShard.All, { mode: "affected", base, head, cwd, log() {} }), 0);
  const native = spawnSync("go", ["list", "-mod=readonly", "-f", "{{join .GoFiles \",\"}}", "./cmds/sample/native"], { cwd, encoding: "utf8" });
  assert.equal(native.status, 0, native.stderr);
  assert.equal(native.stdout.trim(), process.platform === "win32" ? "native_windows.go" : "native_unix.go");
  assert.throws(() => affectedGoPackages({ base: "f".repeat(40), head, cwd, log() {} }));
});
