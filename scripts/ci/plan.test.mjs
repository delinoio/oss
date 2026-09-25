import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { changedFiles, Event, jobPaths, matricesForEvent, nativeMatrices, planJobs, previousJobPaths } from "./plan.mjs";
import { validateResults } from "./result.mjs";

const native = Object.entries(jobPaths).filter(([, rule]) => rule.native).map(([id]) => id);
const devhudNative = ["devhud-desktop", "devhud-ios-simulator", "devhud-android-emulator"];
const selected = (event, paths) => Object.entries(planJobs(event, paths).jobs).filter(([, run]) => run).map(([id]) => id);

test("root Rust toolchain changes select Forge validation and rendering", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of ["rust-toolchain", "rust-toolchain.toml"]) {
      for (const id of ["forge-test", "forge-render"]) {
        assert.equal(planJobs(event, [path]).jobs[id], true, `${event}: ${path}: ${id}`);
      }
    }
  }
});

test("PR never allocates native package jobs, including changes to CI itself", () => {
  for (const path of ["apps/devhud/src/App.tsx", "apps/devhud/src-tauri/src/main.rs", "Cargo.lock", ".github/workflows/CI.yml", "scripts/ci/plan.mjs", ".github/actions/setup-ci-node/action.yml"]) {
    const jobs = selected(Event.PullRequest, [path]);
    assert.ok(jobs.includes("devhud-frontend"), path);
    for (const id of native) assert.ok(!jobs.includes(id), `${path}: ${id}`);
  }
  assert.deepEqual(native, ["linux-packages", "pnport-native", ...devhudNative]);
});

test("pnport installer changes select six-host native verification on main", () => {
  for (const installer of ["scripts/install/pnport.sh", "scripts/install/pnport.ps1"]) {
    assert.equal(planJobs(Event.Push, [installer]).jobs["pnport-native"], true, installer);
    assert.equal(planJobs(Event.PullRequest, [installer]).jobs["pnport-native"], false, installer);
  }
});

test("main selects affected non-Mac native jobs; manual selects every job", () => {
  for (const path of ["apps/devhud/src/App.tsx", "apps/devhud/src-tauri/src/main.rs", "protos/devhud/v1/settings.proto", "pnpm-lock.yaml", ".github/workflows/CI.yml"]) {
    for (const id of ["devhud-desktop", "devhud-android-emulator"]) assert.ok(selected(Event.Push, [path]).includes(id), `${path}: ${id}`);
    assert.ok(!selected(Event.Push, [path]).includes("devhud-ios-simulator"), path);
  }
  assert.deepEqual(selected(Event.Manual, []), Object.keys(jobPaths));
  assert.deepEqual(selected(Event.Push, ["docs/project-with-watch.md"]), []);
  assert.deepEqual(selected(Event.PullRequest, ["docs/project-with-watch.md"]), []);
  assert.deepEqual(selected(Event.Push, []), []);
});

test("Linux packages run on affected main pushes and manual dispatch, never on PRs", () => {
  for (const path of [
    "crates/binpm/src/main.rs", "crates/cargo-mono/src/main.rs",
    "crates/nodeup/src/main.rs", "crates/with-watch/src/main.rs",
    "Cargo.toml", "Cargo.lock", ".cargo/config.toml", "rust-toolchain", "rust-toolchain.toml",
    "packaging/linux/pins.json", "scripts/release/linux-packages.mjs",
    "scripts/release/linux-packages.test.mjs", "scripts/release/linux-packages/build-rust.sh",
    ".github/workflows/release-linux-packages.yml", "package.json", "pnpm-lock.yaml",
    ".github/workflows/CI.yml", ".github/actions/setup-ci-node/action.yml",
    "scripts/ci/plan.mjs", "scripts/ci/native-matrices.json",
  ]) {
    assert.equal(planJobs(Event.PullRequest, [path]).jobs["linux-packages"], false, path);
    assert.equal(planJobs(Event.Push, [path]).jobs["linux-packages"], true, path);
  }
  assert.equal(planJobs(Event.Manual, []).jobs["linux-packages"], true);
  for (const paths of [[], ["docs/project-with-watch.md"], ["apps/devhud/src/App.tsx"]]) {
    assert.equal(planJobs(Event.Push, paths).jobs["linux-packages"], false);
  }
  assert.deepEqual(
    selected(Event.PullRequest, [".github/workflows/CI.yml"]),
    Object.keys(jobPaths).filter((id) => !native.includes(id)),
  );
});

test("Runmoor source and release scripts do not rebuild DevHud desktop/mobile", () => {
  for (const paths of [["cmds/runmoor/main.go"], ["scripts/release/runmoor.mjs"], ["scripts/release/runmoor.test.mjs"], ["scripts/release/runmoor.mjs", "scripts/release/runmoor.test.mjs"]]) {
    const jobs = selected(Event.Push, paths);
    for (const id of devhudNative) assert.ok(!jobs.includes(id), id);
    if (paths[0].startsWith("cmds/")) assert.ok(jobs.includes("go-test"));
    else if (paths.some((path) => path.endsWith(".test.mjs"))) assert.ok(jobs.includes("devhud-release-contracts"));
    else assert.ok(!jobs.includes("devhud-release-contracts"));
  }
  for (const path of ["scripts/release/finalize-devhud-deb.sh", "scripts/release/linux/prerm.in", "scripts/release/generate-checksums.sh"]) {
    assert.ok(selected(Event.Push, [path]).includes("devhud-desktop"), path);
  }
});

test("every shared release fixture selects its executing CI job", () => {
  const fixtures = readdirSync(new URL("../release/", import.meta.url)).filter((name) => name.endsWith(".test.mjs"));
  assert.ok(fixtures.length > 0);
  for (const name of fixtures) {
    const path = `scripts/release/${name}`;
    for (const event of [Event.PullRequest, Event.Push]) {
      const needs = results(event, [path]);
      assert.equal(planJobs(event, [path]).jobs["devhud-release-contracts"], true, `${event}: ${path}`);
      assert.equal(validateResults(needs), true);
      needs["devhud-release-contracts"].result = "skipped";
      assert.throws(() => validateResults(needs), /devhud-release-contracts/u);
    }
  }
});

test("integrated project docs select the public-docs workspace and shared inputs force it", () => {
  const id = "node-public-docs-test";
  for (const event of [Event.PullRequest, Event.Push]) {
    const paths = ["apps/public-docs/docs/runmoor/install.md"];
    assert.deepEqual(selected(event, paths), ["repository-environment", id, "devhud-release-contracts"]);
    assert.equal(planJobs(event, paths).forced[id], false);
    for (const path of [".nvmrc", "pnpm-lock.yaml", "scripts/run-rspress-port.mjs"]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs[id], true, path);
      assert.equal(plan.forced[id], true, path);
    }
    for (const path of ["docs/project-runmoor.md"]) {
      assert.deepEqual(selected(event, [path]), ["repository-environment"]);
    }
    const reactForgeContract = planJobs(event, ["docs/apps-react-forge-docs-foundation.md"]);
    assert.equal(reactForgeContract.jobs[id], true);
    assert.equal(reactForgeContract.forced[id], true);
    const needs = results(event, paths);
    assert.equal(validateResults(needs), true);
    for (const result of ["failure", "cancelled", "skipped"]) {
      needs[id].result = result;
      assert.throws(() => validateResults(needs), new RegExp(id, "u"));
    }
  }
});

test("async-commit-hook source and shared validation inputs select its complete validation job", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "cmds/async-commit-hook/main.go", "apps/async-commit-hook/src/App.tsx",
      "scripts/install/async-commit-hook.ps1", "packages/async-commit-hook-api-client/src/client.ts",
      "protos/async_commit_hook/v1/service.proto", "packaging/async-commit-hook/release-metadata.json",
      "scripts/release/build-async-commit-hook.py", "scripts/release/async-commit-hook.test.mjs",
      "scripts/release/publish-async-commit-hook.py", "scripts/release/async-commit-hook-publish-fixtures.py",
      ".github/workflows/release-async-commit-hook.yml", "docs/project-async-commit-hook.md",
      "buf.yaml", "buf.gen.yaml", "go.mod", "go.sum", "package.json", "pnpm-lock.yaml",
      "pnpm-workspace.yaml", ".nvmrc", ".npmrc", "turbo.json",
      "scripts/check-proto-breaking.sh", "scripts/run-rsbuild-dev.mjs", "scripts/spawn-dev-server.mjs",
      "scripts/dev-environment/process.mjs",
    ]) assert.ok(selected(event, [path]).includes("async-commit-hook"), `${event}: ${path}`);
    for (const path of ["cmds/runmoor/main.go", "apps/public-docs/docs/projects-overview.md", "docs/project-with-watch.md"]) {
      assert.ok(!selected(event, [path]).includes("async-commit-hook"), `${event}: ${path}`);
    }
  }
});

test("async-commit-hook failures, missing results and unauthorized skips fail the aggregate", () => {
  const paths = ["cmds/async-commit-hook/main.go"];
  assert.equal(validateResults(results(Event.PullRequest, paths)), true);
  for (const result of ["failure", "cancelled", "skipped", undefined]) {
    const needs = results(Event.PullRequest, paths);
    needs["async-commit-hook"].result = result;
    assert.throws(() => validateResults(needs), /async-commit-hook/u);
  }
  const needs = results(Event.PullRequest, paths);
  delete needs["async-commit-hook"];
  assert.throws(() => validateResults(needs), /inventory/u);
});

test("workspace, shared, runtime, and external contract inputs select their owners", () => {
  for (const [path, ids] of [
    ["apps/public-docs/docs/projects-overview.md", ["node-public-docs-test"]],
    ["scripts/install/binpm.sh", ["node-public-docs-test"]],
    ["scripts/install/nodeup.ps1", ["node-public-docs-test"]],
    ["scripts/install/async-commit-hook.sh", ["async-commit-hook", "node-public-docs-test"]],
    ["packages/docs-site-switcher/src/index.tsx", ["node-public-docs-test"]],
    ["apps/public-docs/theme/index.tsx", ["repository-environment", "node-public-docs-test"]],
    ["scripts/dev-environment/orchestrator.mjs", ["repository-environment"]],
    ["packages/devhud-api-client/src/client.ts", ["devhud-frontend", "devhud-protocol", "devhud-admin", "devhud-api", "rust-test"]],
    ["apps/devhud/src-tauri/src/updater.rs", ["rust-fmt", "rust-clippy", "rust-test", "devhud-rust-conformance", "devhud-frontend"]],
    ["protos/devhud/v1/account.proto", ["devhud-protocol", "devhud-api", "devhud-frontend"]],
    [".nvmrc", ["node-public-docs-test", "devhud-frontend", "devhud-api", "repository-environment"]],
    ["pnpm-lock.yaml", ["node-public-docs-test", "devhud-admin", "devhud-api", "devhud-frontend"]],
    [".cargo/config.toml", ["rust-fmt", "rust-clippy", "rust-test", "devhud-rust-conformance"]],
    ["crates/binpm/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/cargo-mono/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/nodeup/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/with-watch/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["rust-toolchain.toml", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["Cargo.lock", ["rust-fmt", "rust-clippy", "rust-test"]],
  ]) {
    for (const id of ids) assert.ok(selected(Event.PullRequest, [path]).includes(id), `${path}: ${id}`);
  }
  for (const path of ["apps/public-docs/theme/index.tsx", "apps/public-docs/docs/runmoor/install.md"]) {
    assert.equal(planJobs(Event.PullRequest, [path]).forced["node-public-docs-test"], false, path);
  }
  assert.equal(planJobs(Event.PullRequest, ["apps/devhud/src/App.tsx"]).forced["devhud-frontend"], false);
  assert.equal(planJobs(Event.PullRequest, ["packages/devhud-api-client/src/client.ts"]).forced["devhud-frontend"], false);
  for (const path of [".nvmrc", "Cargo.lock", "protos/devhud/v1/account.proto"]) {
    assert.equal(planJobs(Event.PullRequest, [path]).forced["devhud-frontend"], true, path);
  }
  assert.equal(planJobs(Event.PullRequest, ["scripts/install/binpm.sh"]).forced["node-public-docs-test"], true);
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
  f.write("apps/public-docs/first.ts", "first\n"); f.commit();
  f.write("cmds/runmoor/second.go", "second\n"); const head = f.commit();
  f.git("switch", "main");
  f.write("unrelated-main.txt", "main\n"); const base = f.commit();
  const pr = changedFiles(Event.PullRequest, { pull_request: { base: { sha: base }, head: { sha: head } } }, base, f.cwd);
  assert.equal(pr.base, f.initial);
  assert.equal(pr.head, head);
  assert.deepEqual(pr.paths, ["apps/public-docs/first.ts", "cmds/runmoor/second.go"]);
  const push = changedFiles(Event.Push, { before: f.initial }, head, f.cwd);
  assert.deepEqual(push, pr);
  // A force push must compare tree endpoints, not their common ancestor.
  assert.ok(changedFiles(Event.Push, { before: base }, head, f.cwd).paths.includes("unrelated-main.txt"));
});

test("deleted and renamed paths retain both owners, including unusual filenames", (t) => {
  const f = fixture(t);
  f.write("apps/public-docs/old name.ts", "original\n"); const base = f.commit();
  mkdirSync(join(f.cwd, "apps/devhud"), { recursive: true });
  renameSync(join(f.cwd, "apps/public-docs/old name.ts"), join(f.cwd, "apps/devhud/new\nname.ts"));
  rmSync(join(f.cwd, "README.md")); const head = f.commit();
  const { paths } = changedFiles(Event.Push, { before: base }, head, f.cwd);
  assert.deepEqual(paths, ["README.md", "apps/devhud/new\nname.ts", "apps/public-docs/old name.ts"]);
  const jobs = selected(Event.Push, paths);
  assert.ok(jobs.includes("node-public-docs-test"));
  assert.ok(jobs.includes("devhud-desktop"));
});

test("missing, malformed, and unavailable revisions fail rather than skip checks", (t) => {
  const f = fixture(t);
  for (const before of [undefined, "", "0".repeat(40), "--help", "f".repeat(40)]) {
    assert.throws(() => changedFiles(Event.Push, { before }, f.initial, f.cwd));
  }
  assert.throws(() => changedFiles(Event.PullRequest, {}, f.initial, f.cwd));
  assert.deepEqual(changedFiles(Event.Manual, {}, f.initial, f.cwd), { base: f.initial, head: f.initial, paths: [] });
  assert.throws(() => previousJobPaths("bad", f.cwd), /40-character commit SHA/u);
  assert.throws(() => previousJobPaths("f".repeat(40), f.cwd));
});

test("historical path rules are read from the exact comparison base", (t) => {
  const f = fixture(t);
  assert.deepEqual(previousJobPaths(f.initial, f.cwd), {});
  f.write("scripts/ci/job-paths.json", JSON.stringify({ "go-test": { paths: ["cmds/**"] } }));
  const base = f.commit();
  f.write("scripts/ci/job-paths.json", JSON.stringify({ "go-test": { paths: ["cmds/**", "go.mod"] } }));
  f.commit();
  assert.deepEqual(previousJobPaths(base, f.cwd), { "go-test": { paths: ["cmds/**"] } });
  f.write("scripts/ci/job-paths.json", "{");
  const bad = f.commit();
  assert.throws(() => previousJobPaths(bad, f.cwd), SyntaxError);
});

function results(event, paths) {
  const { jobs } = planJobs(event, paths);
  const matrices = matricesForEvent(event);
  return {
    changes: { result: "success", outputs: { jobs: JSON.stringify(jobs), event, desktop_matrix: JSON.stringify(matrices.desktopMatrix), react_forge_matrix: JSON.stringify(matrices.reactForgeMatrix) } },
    "ci-contracts": { result: "success" },
    ...Object.fromEntries(Object.entries(jobs).map(([id, run]) => [id, { result: run ? "success" : "skipped" }])),
  };
}

test("aggregate requires Linux package skips on PRs and success when selected on main or manually", () => {
  const id = "linux-packages";
  for (const path of ["Cargo.lock", ".github/workflows/CI.yml"]) {
    for (const event of Object.values(Event)) {
      const needs = results(event, [path]);
      assert.equal(needs[id].result, event === Event.PullRequest ? "skipped" : "success");
      assert.equal(validateResults(needs), true);
      const unexpected = event === Event.PullRequest ? "success" : "skipped";
      for (const result of ["failure", "cancelled", unexpected, undefined]) {
        needs[id].result = result;
        assert.throws(() => validateResults(needs), /linux-packages/u);
      }
      delete needs[id];
      assert.throws(() => validateResults(needs), /inventory/u);
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
  const wrongMatrix = results(Event.Push, ["apps/devhud/src/App.tsx"]);
  wrongMatrix.changes.outputs.desktop_matrix = JSON.stringify({ include: nativeMatrices["devhud-desktop"] });
  assert.throws(() => validateResults(wrongMatrix), /desktop_matrix/u);
});


test("React Forge runs on affected PRs and main, with shared package regression coverage", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of ["packages/react-forge/src/session.ts", "packages/react-forge/tests/session.test.tsx", "packages/react-forge/bin/react-forge.mjs", "packages/react-forge/examples/travel-ir.tsx", "packages/react-forge/examples/travel-ir-assets/coast.png", "packages/react-forge/scripts/validate-host.sh", "crates/react-forge-node/src/lib.rs", "crates/forge-pdf/src/lib.rs", "crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", "rust-toolchain", "pnpm-lock.yaml"]) {
      assert.equal(planJobs(event, [path]).jobs["react-forge"], true, path);
    }
    for (const id of ["react-forge", "forge-test", "forge-render"]) assert.equal(planJobs(event, ["crates/forge-package/src/lib.rs"]).jobs[id], true, id);
    assert.equal(planJobs(event, ["docs/project-with-watch.md"]).jobs["react-forge"], false);
    for (const path of ["packages/react-forge/README.md", "packages/react-forge/AGENTS.md", "packages/react-forge/examples/README.md", "packages/react-forge/examples/travel-ir-assets/README.md", "crates/forge-pdf/README.md", "crates/react-forge-node/AGENTS.md", "docs/packages-react-forge-contract.md", "docs/project-react-forge.md"]) {
      assert.equal(planJobs(event, [path]).jobs["react-forge"], false, path);
    }
  }
});

test("event matrices retain only the authorized native hosts", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    const { desktopMatrix, reactForgeMatrix } = matricesForEvent(event);
    assert.deepEqual(desktopMatrix.include.map(({ id }) => id), nativeMatrices["devhud-desktop"].filter(({ os }) => os !== "macos").map(({ id }) => id));
    assert.deepEqual(reactForgeMatrix.include.map(({ id }) => id), nativeMatrices["react-forge"].filter(({ platform }) => platform !== "darwin").map(({ id }) => id));
    assert.equal(planJobs(event, [".github/workflows/CI.yml"]).jobs["devhud-ios-simulator"], false);
  }
  assert.deepEqual(matricesForEvent(Event.Manual).desktopMatrix.include, nativeMatrices["devhud-desktop"]);
  assert.deepEqual(matricesForEvent(Event.Manual).reactForgeMatrix.include, nativeMatrices["react-forge"]);
  assert.equal(planJobs(Event.Manual, []).jobs["devhud-ios-simulator"], true);
});

test("path-rule edits force only changed eligible jobs", () => {
  const previous = structuredClone(jobPaths);
  previous["go-test"].paths = previous["go-test"].paths.filter((path) => path !== "cmds/**");
  previous["devhud-frontend"].paths.push("packages/unrelated/**");
  for (const event of [Event.PullRequest, Event.Push]) {
    const plan = planJobs(event, ["scripts/ci/job-paths.json"], previous);
    assert.equal(plan.jobs["go-test"], true);
    assert.equal(plan.jobs["devhud-frontend"], true);
    assert.equal(plan.jobs["react-forge"], false);
    assert.equal(plan.jobs["devhud-desktop"], false);
    assert.equal(plan.forced["go-test"], true);
  }
  assert.deepEqual(selected(Event.Push, ["scripts/ci/job-paths.json"]), []);
  for (const path of [".github/workflows/CI.yml", "scripts/ci/plan.mjs", "scripts/ci/native-matrices.json"]) {
    const plan = planJobs(Event.Push, [path]);
    assert.equal(plan.jobs["react-forge"], true, path);
    assert.equal(plan.jobs["devhud-ios-simulator"], false, path);
  }
});

test("unrelated packages and protocols do not select DevHud jobs", () => {
  for (const path of ["packages/react-forge/src/session.ts", "packages/docs-site-switcher/src/index.ts", "protos/async_commit_hook/v1/ach.proto", "servers/unrelated/main.go"]) {
    const plan = planJobs(Event.Push, [path]);
    for (const id of Object.keys(jobPaths).filter((name) => name.startsWith("devhud-"))) assert.equal(plan.jobs[id], false, `${path}: ${id}`);
  }
  for (const path of ["packages/devhud-api-client/src/index.ts", "protos/devhud/v1/settings.proto", "protos/gen/go/devhud/v1/settings.pb.go", "servers/devhud-api/internal/rpc/settings.go"]) {
    assert.ok(Object.entries(planJobs(Event.Push, [path]).jobs).some(([id, run]) => id.startsWith("devhud-") && run), path);
  }
});
