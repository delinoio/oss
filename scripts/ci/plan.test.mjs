import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { changedFiles, Event, jobPaths, planJobs } from "./plan.mjs";
import { validateResults } from "./result.mjs";

const native = Object.entries(jobPaths).filter(([, rule]) => rule.native).map(([id]) => id);
const selected = (event, paths) => Object.entries(planJobs(event, paths).jobs).filter(([, run]) => run).map(([id]) => id);

test("PR never allocates native package jobs, including changes to CI itself", () => {
  for (const path of ["apps/devhud/src/App.tsx", "apps/devhud/src-tauri/src/main.rs", "Cargo.lock", ".github/workflows/CI.yml", "scripts/ci/plan.mjs", ".github/actions/setup-ci-node/action.yml"]) {
    const jobs = selected(Event.PullRequest, [path]);
    assert.ok(jobs.includes("devhud-frontend"), path);
    for (const id of native) assert.ok(!jobs.includes(id), `${path}: ${id}`);
  }
  assert.equal(native.length, 3);
});

test("main selects the existing full native matrix only when affected; manual selects every job", () => {
  for (const path of ["apps/devhud/src/App.tsx", "apps/devhud/src-tauri/src/main.rs", "protos/devhud/v1/settings.proto", "pnpm-lock.yaml", ".github/workflows/CI.yml"]) {
    for (const id of native) assert.ok(selected(Event.Push, [path]).includes(id), `${path}: ${id}`);
  }
  assert.deepEqual(selected(Event.Manual, []), Object.keys(jobPaths));
  assert.deepEqual(selected(Event.Push, ["docs/project-with-watch.md"]), []);
  assert.deepEqual(selected(Event.PullRequest, ["docs/project-with-watch.md"]), []);
  assert.deepEqual(selected(Event.Push, []), []);
});

test("Runmoor source and release scripts do not rebuild DevHud desktop/mobile", () => {
  for (const paths of [["cmds/runmoor/main.go"], ["scripts/release/runmoor.mjs", "scripts/release/runmoor.test.mjs"]]) {
    const jobs = selected(Event.Push, paths);
    for (const id of native) assert.ok(!jobs.includes(id), id);
    if (paths[0].startsWith("cmds/")) assert.ok(jobs.includes("go-test"));
    else assert.ok(jobs.includes("devhud-release-contracts"));
  }
  for (const path of ["scripts/release/finalize-devhud-deb.sh", "scripts/release/linux/prerm.in", "scripts/release/generate-checksums.sh"]) {
    assert.ok(selected(Event.Push, [path]).includes("devhud-desktop"), path);
  }
});

test("Runmoor docs select their checks and shared inputs force the workspace", () => {
  const id = "node-runmoor-docs-test";
  for (const event of [Event.PullRequest, Event.Push]) {
    const paths = ["apps/runmoor-docs/docs/install.md"];
    assert.deepEqual(selected(event, paths), ["repository-environment", id]);
    assert.equal(planJobs(event, paths).forced[id], false);
    for (const path of [".nvmrc", "pnpm-lock.yaml", "scripts/run-rspress-port.mjs"]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs[id], true, path);
      assert.equal(plan.forced[id], true, path);
    }
    for (const path of ["docs/apps-runmoor-docs-foundation.md", "docs/project-runmoor.md"]) {
      assert.deepEqual(selected(event, [path]), ["repository-environment"]);
    }
    const needs = results(event, paths);
    assert.equal(validateResults(needs), true);
    for (const result of ["failure", "cancelled", "skipped"]) {
      needs[id].result = result;
      assert.throws(() => validateResults(needs), new RegExp(id, "u"));
    }
  }
});

test("workspace, shared, runtime, and external contract inputs select their owners", () => {
  for (const [path, ids] of [
    ["apps/mpapp/App.tsx", ["node-mpapp-test", "node-mpapp-lint"]],
    ["scripts/install/binpm.sh", ["node-binpm-docs-test"]],
    ["scripts/install/nodeup.ps1", ["node-nodeup-docs-test"]],
    ["scripts/dev-environment/orchestrator.mjs", ["repository-environment"]],
    ["packages/devhud-api-client/src/client.ts", ["devhud-frontend", "devhud-protocol", "devhud-admin", "devhud-api", "rust-test"]],
    ["apps/devhud/src-tauri/src/updater.rs", ["rust-fmt", "rust-clippy", "rust-test", "devhud-rust-conformance", "devhud-frontend"]],
    ["protos/devhud/v1/account.proto", ["devhud-protocol", "devhud-api", "devhud-frontend"]],
    [".nvmrc", ["node-mpapp-test", "devhud-frontend", "devhud-api", "repository-environment"]],
    ["pnpm-lock.yaml", ["node-mpapp-test", "node-public-docs-test", "devhud-admin", "devhud-api", "devhud-frontend"]],
    [".cargo/config.toml", ["rust-fmt", "rust-clippy", "rust-test", "devhud-rust-conformance"]],
  ]) {
    for (const id of ids) assert.ok(selected(Event.PullRequest, [path]).includes(id), `${path}: ${id}`);
  }
  assert.equal(planJobs(Event.PullRequest, ["apps/devhud/src/App.tsx"]).forced["devhud-frontend"], false);
  assert.equal(planJobs(Event.PullRequest, ["packages/devhud-api-client/src/client.ts"]).forced["devhud-frontend"], false);
  for (const path of [".nvmrc", "Cargo.lock", "protos/devhud/v1/account.proto"]) {
    assert.equal(planJobs(Event.PullRequest, [path]).forced["devhud-frontend"], true, path);
  }
  assert.equal(planJobs(Event.PullRequest, ["scripts/install/binpm.sh"]).forced["node-binpm-docs-test"], true);
  assert.throws(() => planJobs("unknown", []), /Unsupported CI event/u);
});

function fixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), "ci-comparison-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  const write = (path, value) => { mkdirSync(dirname(join(cwd, path)), { recursive: true }); writeFileSync(join(cwd, path), value); };
  const commit = () => { git("add", "--all"); git("commit", "-m", "fixture"); return git("rev-parse", "HEAD"); };
  write("README.md", "initial\n");
  const initial = commit();
  return { cwd, git, write, commit, initial };
}

test("comparison uses the PR merge-base and the complete multi-commit push range", (t) => {
  const f = fixture(t);
  f.git("switch", "-c", "feature");
  f.write("apps/mpapp/first.ts", "first\n"); f.commit();
  f.write("cmds/runmoor/second.go", "second\n"); const head = f.commit();
  f.git("switch", "main");
  f.write("unrelated-main.txt", "main\n"); const base = f.commit();
  const pr = changedFiles(Event.PullRequest, { pull_request: { base: { sha: base }, head: { sha: head } } }, base, f.cwd);
  assert.equal(pr.base, f.initial);
  assert.equal(pr.head, head);
  assert.deepEqual(pr.paths, ["apps/mpapp/first.ts", "cmds/runmoor/second.go"]);
  const push = changedFiles(Event.Push, { before: f.initial }, head, f.cwd);
  assert.deepEqual(push, pr);
  // A force push must compare tree endpoints, not their common ancestor.
  assert.ok(changedFiles(Event.Push, { before: base }, head, f.cwd).paths.includes("unrelated-main.txt"));
});

test("deleted and renamed paths retain both owners, including unusual filenames", (t) => {
  const f = fixture(t);
  f.write("apps/mpapp/old name.ts", "original\n"); const base = f.commit();
  mkdirSync(join(f.cwd, "apps/devhud"), { recursive: true });
  renameSync(join(f.cwd, "apps/mpapp/old name.ts"), join(f.cwd, "apps/devhud/new\nname.ts"));
  rmSync(join(f.cwd, "README.md")); const head = f.commit();
  const { paths } = changedFiles(Event.Push, { before: base }, head, f.cwd);
  assert.deepEqual(paths, ["README.md", "apps/devhud/new\nname.ts", "apps/mpapp/old name.ts"]);
  const jobs = selected(Event.Push, paths);
  assert.ok(jobs.includes("node-mpapp-test"));
  assert.ok(jobs.includes("devhud-desktop"));
});

test("missing, malformed, and unavailable revisions fail rather than skip checks", (t) => {
  const f = fixture(t);
  for (const before of [undefined, "", "0".repeat(40), "--help", "f".repeat(40)]) {
    assert.throws(() => changedFiles(Event.Push, { before }, f.initial, f.cwd));
  }
  assert.throws(() => changedFiles(Event.PullRequest, {}, f.initial, f.cwd));
  assert.deepEqual(changedFiles(Event.Manual, {}, f.initial, f.cwd), { base: f.initial, head: f.initial, paths: [] });
});

function results(event, paths) {
  const { jobs } = planJobs(event, paths);
  return {
    changes: { result: "success", outputs: { jobs: JSON.stringify(jobs) } },
    "ci-contracts": { result: "success" },
    ...Object.fromEntries(Object.entries(jobs).map(([id, run]) => [id, { result: run ? "success" : "skipped" }])),
  };
}

test("TaskFlow conformance follows the central plan and rejects incomplete results", () => {
  const ids = ["taskflow-conformance", "taskflow-docker"];
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of ["crates/taskflow/src/runner.rs", "docs/crates-taskflow-conformance.md", "Cargo.lock", ".cargo/config.toml"]) {
      for (const id of ids) assert.ok(selected(event, [path]).includes(id), `${path}: ${id}`);
    }
    for (const id of ids) assert.ok(!selected(event, ["cmds/runmoor/main.go"]).includes(id), id);
    assert.ok(selected(event, ["go.mod"]).includes("taskflow-conformance"));
    for (const id of ids) {
      const needs = results(event, ["crates/taskflow/src/runner.rs"]);
      assert.equal(validateResults(needs), true);
      for (const result of ["failure", "cancelled", "skipped"]) {
        needs[id].result = result;
        assert.throws(() => validateResults(needs), new RegExp(id, "u"));
      }
    }
  }
});

test("aggregate accepts only success and skips explicitly authorized by the plan", () => {
  for (const event of Object.values(Event)) assert.equal(validateResults(results(event, ["apps/devhud/src/App.tsx"])), true);
  assert.equal(validateResults(results(Event.PullRequest, [])), true);
  for (const result of ["failure", "cancelled", "skipped", undefined]) {
    for (const id of ["changes", "ci-contracts", "devhud-frontend"]) {
      const needs = results(Event.PullRequest, ["apps/devhud/src/App.tsx"]);
      needs[id].result = result;
      assert.throws(() => validateResults(needs), new RegExp(id, "u"));
    }
  }
  for (const id of ["devhud-frontend", "devhud-desktop"]) {
    const needs = results(Event.PullRequest, ["apps/devhud/src/App.tsx"]);
    delete needs[id];
    assert.throws(() => validateResults(needs), /inventory/u);
  }
  for (const value of ["", "{}", "{", JSON.stringify({ ...planJobs(Event.Manual, []).jobs, unexpected: true })]) {
    const needs = results(Event.Manual, []);
    needs.changes.outputs.jobs = value;
    assert.throws(() => validateResults(needs));
  }
  const unexpected = results(Event.PullRequest, []);
  unexpected["devhud-desktop"].result = "success";
  assert.throws(() => validateResults(unexpected), /expected skipped/u);
});
