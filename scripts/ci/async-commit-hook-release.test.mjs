import assert from "node:assert/strict";
import {
  readFileSync,
  writeFileSync,
  mkdtempSync,
  mkdirSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { load } from "js-yaml";
const root = new URL("../../", import.meta.url);
const workflow = load(
  readFileSync(
    new URL(".github/workflows/release-async-commit-hook.yml", root),
    "utf8",
  ),
);
test("manual release fails closed with an unsigned nonpublishing dry run", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  for (const job of ["publish", "homebrew"])
    assert.equal(workflow.jobs[job].if, "${{ !inputs.dry_run }}");
  assert.equal(workflow.jobs.deploy, undefined);
  assert.equal(workflow.jobs.publish.environment, "async-commit-hook-release");
  assert.match(JSON.stringify(workflow.jobs.validate), /refs\/heads\/main/);
  assert.doesNotMatch(
    JSON.stringify(workflow.jobs.validate),
    /gh release create|git push|pages deploy|sign-blob/,
  );
  assert.match(JSON.stringify(workflow.jobs.publish), /REQUIRE_COSIGN=1/);
  for (const step of workflow.jobs.validate.steps.filter((step) =>
    step.uses?.startsWith("actions/upload-artifact@"),
  )) {
    assert.equal(step.if, "${{ !inputs.dry_run }}");
  }
});
test("CI and release commands force fresh Go tests while preserving their scopes", () => {
  const native = JSON.parse(
    readFileSync(new URL("scripts/ci/package.json", root), "utf8"),
  ).scripts["ci:ach:native"];
  assert.equal(
    native,
    "node from-root.mjs go test -race -count=1 ./cmds/async-commit-hook/...",
  );
  const validation = workflow.jobs.validate.steps.find(
    (step) => step.name === "Install and validate",
  ).run;
  const goCommands = validation
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.startsWith("go test "));
  assert.deepEqual(goCommands, [
    "go test -count=1 ./...",
    "go test -race -count=1 ./cmds/async-commit-hook/...",
  ]);
  assert.ok(
    validation.indexOf("pnpm --filter async-commit-hook build:embedded") <
      validation.indexOf(goCommands[0]),
  );
});

test("warm Go caches cannot hide async-commit-hook command regressions", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "ach-go-cache-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const checkout = join(directory, "checkout");
  const cache = join(directory, "go-cache");
  const counter = join(directory, "executions");
  mkdirSync(join(checkout, "cmds", "async-commit-hook", "integration"), {
    recursive: true,
  });
  writeFileSync(
    join(checkout, "go.mod"),
    "module github.com/delinoio/oss\n\ngo 1.22\n",
  );
  const commandSource = join(checkout, "cmds", "async-commit-hook", "main.go");
  const command = (value) =>
    `package main\nimport "fmt"\nfunc main() { fmt.Println("${value}") }\n`;
  writeFileSync(commandSource, command("good"));
  writeFileSync(
    join(
      checkout,
      "cmds",
      "async-commit-hook",
      "integration",
      "integration_test.go",
    ),
    `package integration
import (
  "fmt"
  "os"
  "os/exec"
  "path/filepath"
  "runtime"
  "strings"
  "testing"
)
var helper string
func TestMain(m *testing.M) {
  directory, err := os.MkdirTemp("", "ach-go-helper-")
  if err != nil { panic(err) }
  helper = filepath.Join(directory, "ach")
  if runtime.GOOS == "windows" { helper += ".exe" }
  build := exec.Command("go", "build", "-o", helper, "..")
  build.Stdout, build.Stderr = os.Stdout, os.Stderr
  if err := build.Run(); err != nil { panic(err) }
  counter, err := os.OpenFile(os.Getenv("ACH_TEST_EXECUTIONS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { panic(err) }
  if _, err := fmt.Fprintln(counter, "executed"); err != nil { panic(err) }
  if err := counter.Close(); err != nil { panic(err) }
  code := m.Run()
  if err := os.RemoveAll(directory); err != nil { panic(err) }
  os.Exit(code)
}
func TestCommand(t *testing.T) {
  output, err := exec.Command(helper).Output()
  if err != nil { t.Fatal(err) }
  if actual := strings.TrimSpace(string(output)); actual != "good" {
    t.Fatalf("expected good, got %s", actual)
  }
}
`,
  );
  const env = {
    ...process.env,
    GOCACHE: cache,
    GOWORK: "off",
    GOFLAGS: "",
    GOTOOLCHAIN: "local",
    ACH_TEST_EXECUTIONS: counter,
  };
  const go = (args) =>
    spawnSync("go", args, {
      cwd: checkout,
      env,
      shell: false,
      encoding: "utf8",
      timeout: 180_000,
    });
  const executions = () =>
    readFileSync(counter, "utf8").trim().split("\n").length;
  const validation = workflow.jobs.validate.steps.find(
    (step) => step.name === "Install and validate",
  ).run;
  const releaseArgs = validation
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.startsWith("go test "))
    .map((line) => line.slice("go ".length).split(" "));
  const native = JSON.parse(
    readFileSync(new URL("scripts/ci/package.json", root), "utf8"),
  ).scripts["ci:ach:native"];
  const nativeArgs = native.slice("node from-root.mjs go ".length).split(" ");
  assert.deepEqual(nativeArgs, releaseArgs[1]);
  assert.equal(releaseArgs.length, 2);

  for (const args of releaseArgs) {
    for (let attempt = 1; attempt <= 2; attempt++) {
      const result = go(args);
      assert.equal(result.status, 0, result.stdout + result.stderr);
      assert.doesNotMatch(result.stdout, /\(cached\)/u);
      assert.equal(executions(), releaseArgs.indexOf(args) * 2 + attempt);
    }
  }
  // Forced runs do not populate Go's test-result cache. Seed each default
  // invocation separately, then prove its successful result is reusable.
  for (const args of releaseArgs) {
    const unforced = args.filter((arg) => arg !== "-count=1");
    const seeded = go(unforced);
    assert.equal(seeded.status, 0, seeded.stdout + seeded.stderr);
    const beforeCached = executions();
    const cached = go(unforced);
    assert.equal(cached.status, 0, cached.stdout + cached.stderr);
    assert.match(cached.stdout, /integration\s+\(cached\)/u);
    assert.equal(executions(), beforeCached);
  }
  const beforeChange = executions();
  writeFileSync(commandSource, command("bad"));
  for (const args of releaseArgs) {
    const stale = go(args.filter((arg) => arg !== "-count=1"));
    assert.equal(stale.status, 0, stale.stdout + stale.stderr);
    assert.match(stale.stdout, /integration\s+\(cached\)/u);
    assert.equal(executions(), beforeChange);
  }
  for (const args of [nativeArgs, ...releaseArgs]) {
    const broken = go(args);
    assert.notEqual(broken.status, 0, broken.stdout + broken.stderr);
    assert.match(broken.stdout, /expected good, got bad/u);
    assert.doesNotMatch(broken.stdout, /\(cached\)/u);
  }
  assert.equal(executions(), beforeChange + 3);

  // The test-result bypass keeps Go's compiled-object cache available.
  const compiled = [
    "test",
    "-c",
    "-o",
    process.platform === "win32" ? "NUL" : "/dev/null",
    "./cmds/async-commit-hook/integration",
  ];
  const seed = go(compiled);
  assert.equal(seed.status, 0, seed.stdout + seed.stderr);
  const reused = go(["test", "-x", ...compiled.slice(1)]);
  assert.equal(reused.status, 0, reused.stdout + reused.stderr);
  assert.doesNotMatch(reused.stderr, /[/\\]compile(?:\.exe)?(?:"|\s)/u);
});

test("downstream publication retries preserve the successful immutable GitHub release", () => {
  assert.equal(workflow.jobs.homebrew.needs, "publish");
  assert.deepEqual(workflow.jobs.homebrew.permissions, { contents: "read" });
  assert.doesNotMatch(
    JSON.stringify(workflow.jobs.publish),
    /HOMEBREW|git push origin HEAD:main|pages deploy/,
  );
  assert.doesNotMatch(
    JSON.stringify(workflow.jobs.homebrew),
    /gh release create|sign-blob|id-token/,
  );
  const directory = mkdtempSync(join(tmpdir(), "ach-homebrew-"));
  try {
    const bin = join(directory, "tools"),
      remote = join(directory, "tap.git"),
      seed = join(directory, "seed"),
      release = join(directory, "release");
    mkdirSync(bin);
    mkdirSync(release);
    const env = {
      ...process.env,
      HOME: directory,
      GIT_CONFIG_NOSYSTEM: "1",
      GIT_CONFIG_GLOBAL: "/dev/null",
      GIT_AUTHOR_NAME: "Fixture",
      GIT_AUTHOR_EMAIL: "fixture@example.invalid",
      GIT_COMMITTER_NAME: "Fixture",
      GIT_COMMITTER_EMAIL: "fixture@example.invalid",
      GH_TOKEN: "fixture",
      ACH_TEST_TAP: remote,
    };
    const git = (...args) => {
      const r = spawnSync("git", args, { env, encoding: "utf8" });
      assert.equal(r.status, 0, r.stderr);
      return r.stdout.trim();
    };
    git("init", "--bare", "--initial-branch=main", remote);
    git("clone", remote, seed);
    git(
      "-C",
      seed,
      "-c",
      "commit.gpgsign=false",
      "commit",
      "--allow-empty",
      "-m",
      "initial",
    );
    git("-C", seed, "push", "origin", "main");
    writeFileSync(join(release, "async-commit-hook.rb"), "fixture formula\n");
    writeFileSync(
      join(bin, "gh"),
      '#!/bin/sh\nif [ "$1 $2" = "repo clone" ]; then exec git clone "$ACH_TEST_TAP" "$4"; fi\nif [ "$1 $2" = "auth setup-git" ]; then exit 0; fi\nexit 99\n',
      { mode: 0o755 },
    );
    const script = workflow.jobs.homebrew.steps.find(
      (step) => step.name === "Update Homebrew formula",
    ).run;
    let first;
    for (const attempt of [1, 2]) {
      const temp = join(directory, `attempt-${attempt}`);
      mkdirSync(temp);
      const result = spawnSync("sh", ["-eu", "-c", script], {
        cwd: directory,
        env: { ...env, PATH: bin + ":" + process.env.PATH, RUNNER_TEMP: temp },
        encoding: "utf8",
      });
      assert.equal(result.status, 0, result.stderr);
      const head = git("--git-dir", remote, "rev-parse", "main");
      if (first)
        assert.equal(head, first, "retry rewrote an already published formula");
      first = head;
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("release tag identity is checked before signing and again before creation", () => {
  const steps = workflow.jobs.publish.steps;
  const check = steps.findIndex(
    (step) => step.name === "Verify existing release tag before signing",
  );
  const sign = steps.findIndex(
    (step) => step.name === "Sign every artifact and checksum manifest",
  );
  const command =
    'python3 scripts/release/build-async-commit-hook.py --verify-tag "$GITHUB_SHA"';
  assert.ok(
    check >= 0 && check < sign,
    "tag verification must precede all artifact signing",
  );
  assert.equal(steps[check].run, command);
  const publish = steps.find(
    (step) => step.name === "Publish exact version",
  ).run;
  assert.equal(
    publish.trim(),
    'python3 scripts/release/publish-async-commit-hook.py --assets release --version "$VERSION" --revision "$GITHUB_SHA" --run-id "$GITHUB_RUN_ID"',
  );
  const publisher = readFileSync(
    new URL("scripts/release/publish-async-commit-hook.py", root),
    "utf8",
  );
  assert.ok(
    publisher.lastIndexOf("build.verify_tag(version, revision)") <
      publisher.indexOf('published = api(endpoint, {"draft": False})'),
  );
  assert.doesNotMatch(
    JSON.stringify(workflow.jobs.validate),
    /publish-async-commit-hook\.py --assets/,
  );
});

test("release embeds the UI before Go validation and leaves docs publication to public-docs", () => {
  const validate = workflow.jobs.validate.steps
    .map((step) => step.run ?? "")
    .join("\n");
  assert.ok(
    validate.indexOf("pnpm --filter async-commit-hook build:embedded") <
      validate.indexOf("go test -count=1 ./..."),
  );
  assert.match(validate, /pnpm --filter public-docs test/);
  assert.doesNotMatch(
    JSON.stringify(workflow.jobs.validate),
    /ach-site|ACH_PAGES_PROJECT|pages deploy/,
  );
  const builder = readFileSync(
    new URL("scripts/release/build-async-commit-hook.py", root),
    "utf8",
  );
  assert.ok(
    builder.indexOf('"build:embedded"') <
      builder.indexOf('for target in m["targets"]'),
  );
});
