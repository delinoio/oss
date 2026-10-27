import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, posix } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  changedFiles,
  Event,
  Mode,
  executionMode,
  jobPaths,
  matchesPath,
  matricesForEvent,
  nativeMatrices,
  planJobs,
  previousJobPaths,
} from "./plan.mjs";
import { validateResults } from "./result.mjs";

const native = Object.entries(jobPaths)
  .filter(([, rule]) => rule.native)
  .map(([id]) => id);
const selected = (event, paths, mode = Mode.Affected) =>
  Object.entries(planJobs(event, paths, jobPaths, mode).jobs)
    .filter(([, run]) => run)
    .map(([id]) => id);

test("rustfmt configuration selects only formatting on PRs and main pushes", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const directory of [
      "",
      "crates/binpm/",
      "crates/binpm/src/",
      "crates/binpm/src/.hidden/",
      "apps/delidev/src-tauri/src/",
      "packages/example/",
      ".cargo/",
      ".github/actions/example/",
    ]) {
      for (const filename of [".rustfmt.toml", "rustfmt.toml"]) {
        const path = directory + filename;
        assert.deepEqual(
          selected(event, [path]),
          ["rust-fmt"],
          `${event}: ${path}`,
        );
      }
    }
    assert.deepEqual(
      selected(event, [".rustfmt.toml", "docs/project-with-watch.md"]),
      ["rust-fmt", "source-format"],
    );
    const source = "crates/binpm/src/main.rs";
    assert.deepEqual(
      selected(event, [source, "crates/binpm/.rustfmt.toml"]),
      selected(event, [source]),
    );
    assert.deepEqual(selected(event, ["docs/project-with-watch.md"]), [
      "source-format",
    ]);
  }
});

test("shared path matching includes every hidden segment and preserves glob boundaries", () => {
  for (const [pattern, path, expected] of [
    ["apps/example/**", "apps/example/.env.example", true],
    ["apps/example/**", "apps/example/.x/.y/.file", true],
    ["apps/example/**", "apps/example-extra/.file", false],
    ["apps/example/*.ts", "apps/example/.file.ts", true],
    ["apps/example/*.ts", "apps/example/.x/.file.ts", false],
    ["**/*.rs", ".file.rs", true],
    ["**/*.rs", ".x/.y/.file.rs", true],
    ["**/Cargo.toml", ".x/.y/Cargo.toml", true],
    ["**/Cargo.toml", ".x/.y/.Cargo.toml", false],
    [".cargo/**", ".cargo/.x/.file", true],
    [".cargo/**", "cargo/.file", false],
    ["apps/example/.*", "apps/example/.env.example", true],
    ["apps/example/.*", "apps/example/env.example", false],
    ["apps/example/.env.example", "apps/example/.env.example", true],
    ["apps/example/.env.example", "apps/example/env.example", false],
    ["apps/example/.env.example", "apps/example/x.env.example", false],
    ["scripts/release/*example*", "scripts/release/.example-fixture", true],
    ["scripts/release/*example*", "scripts/release/.x/example-fixture", false],
    [
      "packages/example/examples/**/*.tsx",
      "packages/example/examples/.file.tsx",
      true,
    ],
    [
      "packages/example/examples/**/*.tsx",
      "packages/example/examples/.x/.y/.file.tsx",
      true,
    ],
    ["apps/example/**", "apps/example/.space name.ts", true],
    ["apps/example/**", "apps/example/.line\nname.ts", true],
    ["apps/example/**", "apps/example/.back\\slash.ts", true],
    ["apps/example/**", "apps/example/.한글[1].ts", true],
    ["apps/example/.file", "apps/example/.file\n", false],
    ["apps/example/.line\nname.ts", "apps/example/.line\nname.ts", true],
    ["apps/example/.back/slash.ts", "apps/example/.back\\slash.ts", false],
    ["apps/example/**", "apps/example/**/.file", true],
  ]) {
    assert.equal(
      matchesPath(path, pattern),
      expected,
      `${JSON.stringify(path)}: ${pattern}`,
    );
  }
});

test("ordinary paths retain the existing ownership pattern semantics", () => {
  const patterns = [
    ...new Set(
      Object.values(jobPaths).flatMap((rule) => [
        ...rule.paths,
        ...(rule.workspacePaths ?? []),
      ]),
    ),
  ];
  const paths = patterns
    .flatMap((pattern) => [
      pattern
        .replaceAll("**/", "")
        .replaceAll("**", "file")
        .replaceAll("*", "file"),
      pattern.replaceAll("**", "x/y").replaceAll("*", "file"),
    ])
    .concat([
      "README.md",
      "unrelated/file.ts",

      "crates/fixture-test/src/file.rs",
    ]);
  for (const pattern of patterns) {
    for (const path of paths.filter(
      (value) => !value.split("/").some((segment) => segment.startsWith(".")),
    ))
      assert.equal(
        matchesPath(path, pattern),
        posix.matchesGlob(path, pattern),
        `${path}: ${pattern}`,
      );
  }
});

test("hidden shared action descendants force every event-eligible job", () => {
  for (const event of [Event.PullRequest, Event.Push, Event.Manual]) {
    for (const path of [
      ".github/actions/.action.yml",
      ".github/actions/.setup/action.yml",
      ".github/actions/setup/.x/.file",
    ]) {
      assert.deepEqual(
        planJobs(event, [path]),
        planJobs(event, [".github/workflows/CI.yml"]),
        `${event}: ${path}`,
      );
    }
  }
});

test("Go runner changes select validation without adding native Go runners to PRs", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "scripts/ci/go-test.mjs",
      "scripts/ci/go-test.test.mjs",
      "scripts/ci/go-affected.mjs",
      "scripts/ci/go-quality.mjs",
    ]) {
      assert.deepEqual(selected(event, [path]), [
        "go-quality",
        "go-test",
        "source-format",
      ]);
      assert.deepEqual(
        matricesForEvent(event).goTestMatrix.include,
        nativeMatrices[
          event === Event.PullRequest ? "go-test-pr" : "go-test"
        ].filter((row) => !row.os.startsWith("macos")),
      );
    }
  }
});

test("Go report runner changes select native Go tests without widening other jobs", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "scripts/ci/go-test-report.mjs",
      "scripts/ci/go-test-report.test.mjs",
    ]) {
      const plan = planJobs(event, [path]);
      assert.deepEqual(selected(event, [path]), ["go-test", "source-format"]);
      assert.ok(Object.values(plan.forced).every((forced) => !forced));
      assert.deepEqual(
        matricesForEvent(event).goTestMatrix.include,
        nativeMatrices[
          event === Event.PullRequest ? "go-test-pr" : "go-test"
        ].filter((row) => !row.os.startsWith("macos")),
      );
    }
    assert.deepEqual(selected(event, ["docs/project-with-watch.md"]), [
      "source-format",
    ]);
    assert.equal(
      planJobs(event, ["scripts/ci/go-test.mjs"]).jobs["go-test"],
      true,
    );
  }
});

test("root Turbo inputs select native checks without global forcing or PR packaging", () => {
  const ids = ["rust-fmt"];
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "turbo.json",
      "package.json",
      "pnpm-lock.yaml",
      "pnpm-workspace.yaml",
    ]) {
      const plan = planJobs(event, [path]);
      for (const id of ids) {
        assert.equal(plan.jobs[id], true, `${event}: ${path}: ${id}`);
        assert.equal(plan.forced[id], false, `${event}: ${path}: ${id}`);
      }
      if (event === Event.PullRequest) {
        for (const id of native)
          assert.equal(plan.jobs[id], false, `${path}: ${id}`);
      }
    }
    for (const path of [
      "docs/project-with-watch.md",
      "unrelated/turbo.json",
      "unrelated/package.json",
      "unrelated/pnpm-lock.yaml",
      "unrelated/pnpm-workspace.yaml",
    ]) {
      for (const id of ids)
        assert.equal(
          planJobs(event, [path]).jobs[id],
          false,
          `${event}: ${path}: ${id}`,
        );
    }
  }
});

test("root Turbo input native checks must succeed in CI Result", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "turbo.json",
      "package.json",
      "pnpm-lock.yaml",
      "pnpm-workspace.yaml",
    ]) {
      assert.equal(validateResults(results(event, [path])), true);
      for (const id of ["rust-fmt"]) {
        for (const result of ["failure", "cancelled", "skipped", undefined]) {
          const needs = results(event, [path]);
          needs[id].result = result;
          assert.throws(
            () => validateResults(needs),
            new RegExp(id, "u"),
            `${event}: ${path}: ${id}: ${result}`,
          );
        }
      }
    }
  }
});

test("PR never allocates native package jobs, including changes to CI itself", () => {
  for (const path of [
    "Cargo.lock",
    ".gitattributes",
    ".github/workflows/CI.yml",
    "scripts/ci/plan.mjs",
    ".github/actions/setup-ci-node/action.yml",
  ]) {
    const jobs = selected(Event.PullRequest, [path]);

    for (const id of native) assert.ok(!jobs.includes(id), `${path}: ${id}`);
  }
  assert.deepEqual(native, ["linux-packages", "pnport-native"]);
});

test("shared CI execution inputs exercise every eligible workspace job", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "scripts/ci/run-affected.mjs",
      "scripts/ci/buf-entry.mjs",
    ]) {
      const plan = planJobs(event, [path]);
      const expected = Object.keys(jobPaths).filter(
        (id) => event !== Event.PullRequest || !native.includes(id),
      );
      assert.deepEqual(
        Object.entries(plan.jobs)
          .filter(([, run]) => run)
          .map(([id]) => id),
        expected,
        `${event}: ${path}`,
      );
      for (const id of expected)
        assert.equal(plan.forced[id], true, `${event}: ${path}: ${id}`);
    }
  }
});

test("Git LFS attribute changes force every eligible job", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    const plan = planJobs(event, [".gitattributes"]);
    const expected = Object.keys(jobPaths).filter(
      (id) => event !== Event.PullRequest || !native.includes(id),
    );
    assert.deepEqual(
      Object.entries(plan.jobs)
        .filter(([, run]) => run)
        .map(([id]) => id),
      expected,
    );
    for (const id of expected)
      assert.equal(plan.forced[id], true, `${event}: ${id}`);
  }
});

test("pnport installer changes select Linux native verification on main", () => {
  for (const installer of [
    "scripts/install/pnport.sh",
    "scripts/install/pnport.ps1",
  ]) {
    assert.equal(
      planJobs(Event.Push, [installer]).jobs["pnport-native"],
      true,
      installer,
    );
    assert.equal(
      planJobs(Event.PullRequest, [installer]).jobs["pnport-native"],
      false,
      installer,
    );
  }
});

test("pnport native checks include Cargo and Node configuration on affected main pushes", () => {
  for (const event of Object.values(Event)) {
    const matrix = matricesForEvent(event).pnportMatrix.include;
    assert.deepEqual(
      matrix.map(({ suffix }) => suffix),
      [
        "linux-x64-gnu",
        "linux-arm64-gnu",
        "win32-x64-msvc",
        "win32-arm64-msvc",
      ],
      event,
    );
    for (const path of [".cargo/config.toml", ".nvmrc"]) {
      const needs = results(event, [path]);
      const expected = event === Event.Push;
      assert.equal(
        planJobs(event, [path]).jobs["pnport-native"],
        expected,
        `${event}: ${path}`,
      );
      assert.equal(
        needs["pnport-native"].result,
        expected ? "success" : "skipped",
        `${event}: ${path}`,
      );
      assert.equal(validateResults(needs), true, `${event}: ${path}`);
      for (const result of expected
        ? ["failure", "cancelled", "skipped", undefined]
        : ["success", "failure", "cancelled", undefined]) {
        needs["pnport-native"].result = result;
        assert.throws(
          () => validateResults(needs),
          /pnport-native/u,
          `${event}: ${path}: ${result}`,
        );
      }
    }
  }
  assert.equal(
    planJobs(Event.Push, ["docs/project-with-watch.md"]).jobs["pnport-native"],
    false,
  );
  assert.equal(
    planJobs(Event.Push, ["crates/pnport/src/main.rs"]).jobs["pnport-native"],
    true,
  );
});

test("main selects affected non-Mac native jobs; manual skips native jobs", () => {
  for (const path of [
    "pnpm-lock.yaml",
    ".gitattributes",
    ".github/workflows/CI.yml",
  ]) {
    assert.ok(!selected(Event.Push, [path]).includes("ios-simulator"), path);
  }
  assert.deepEqual(selected(Event.Manual, []), []);
  assert.deepEqual(selected(Event.Push, ["docs/project-with-watch.md"]), [
    "source-format",
  ]);
  assert.deepEqual(
    selected(Event.PullRequest, ["docs/project-with-watch.md"]),
    ["source-format"],
  );
  assert.deepEqual(selected(Event.Push, []), []);
});

test("Linux packages run on affected main pushes, never on PRs or manual CI", () => {
  for (const path of [
    "crates/binpm/src/main.rs",
    "crates/cargo-mono/src/main.rs",
    "crates/nodeup/src/main.rs",
    "crates/with-watch/src/main.rs",
    "Cargo.toml",
    "Cargo.lock",
    ".cargo/config.toml",
    "rust-toolchain",
    "rust-toolchain.toml",
    "packaging/linux/pins.json",
    "scripts/release/linux-packages.mjs",
    "scripts/release/linux-packages.test.mjs",
    "scripts/release/linux-packages/build-rust.sh",
    ".github/workflows/release-linux-packages.yml",
    "package.json",
    "pnpm-lock.yaml",
    ".github/workflows/CI.yml",
    ".github/actions/setup-ci-node/action.yml",
    "scripts/ci/plan.mjs",
    "scripts/ci/native-matrices.json",
  ]) {
    assert.equal(
      planJobs(Event.PullRequest, [path]).jobs["linux-packages"],
      false,
      path,
    );
    assert.equal(
      planJobs(Event.Push, [path]).jobs["linux-packages"],
      true,
      path,
    );
  }
  assert.equal(planJobs(Event.Manual, []).jobs["linux-packages"], false);
  for (const paths of [[], ["docs/project-with-watch.md"], []]) {
    assert.equal(planJobs(Event.Push, paths).jobs["linux-packages"], false);
  }
  assert.deepEqual(
    selected(Event.PullRequest, [".github/workflows/CI.yml"]),
    Object.keys(jobPaths).filter((id) => !native.includes(id)),
  );
});

test("shared release implementations select the fixture job that exercises them", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "scripts/release/project.mjs",
      "scripts/release/legacy-cli-release.mjs",
      "scripts/release/runmoor.mjs",
      "scripts/release/runmoor-homebrew.mjs",
      "packaging/homebrew/templates/runmoor.rb.tmpl",
      "scripts/release/update-homebrew.sh",
    ]) {
      assert.equal(
        planJobs(event, [path]).jobs["release-contracts"],
        true,
        `${event}: ${path}`,
      );
    }
  }
});

test("Linux publication entry point, helpers and configuration select portable fixtures", () => {
  const helpers = readdirSync(
    new URL("../release/linux-packages/", import.meta.url),
    { recursive: true },
  );
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "scripts/release/linux-packages.mjs",
      "packaging/linux/pins.json",
      ...helpers.map(
        (name) =>
          `scripts/release/linux-packages/${name.replaceAll("\\", "/")}`,
      ),
    ]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs["release-contracts"], true, `${event}: ${path}`);
      assert.equal(
        plan.jobs["linux-packages"],
        event === Event.Push,
        `${event}: ${path}`,
      );
      if (path !== "scripts/release/linux-packages.mjs") {
        assert.equal(plan.jobs["node-clibox-test"], true, `${event}: ${path}`);
      }
      const needs = results(event, [path]);
      assert.equal(validateResults(needs), true);
      for (const result of ["failure", "cancelled", "skipped"]) {
        needs["release-contracts"].result = result;
        assert.throws(() => validateResults(needs), /release-contracts/u);
      }
    }
  }
});

test("unrelated release helpers retain their narrower owners", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    for (const path of [
      "scripts/release/runmoor.mjs",
      "scripts/release/runmoor-homebrew.mjs",
    ]) {
      assert.deepEqual(selected(event, [path]), [
        "source-format",
        "release-contracts",
      ]);
    }
    assert.deepEqual(
      selected(event, ["scripts/release/generate-delidev-updater.test.mjs"]),
      ["source-format", "release-contracts"],
    );
  }
});

test("a readback regression fails the portable fixture command and CI aggregate", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "linux-publication-ci-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  for (const path of [
    "scripts/ci/from-root.mjs",
    "scripts/ci/buf-entry.mjs",
    "scripts/release/linux-packages.test.mjs",
    "scripts/release/linux-packages",
    "scripts/release/runmoor.mjs",
    "packaging/linux/pins.json",
    "LICENSE",
    "NOTICE",
  ]) {
    const target = join(cwd, path);
    mkdirSync(dirname(target), { recursive: true });
    cpSync(new URL(`../../${path}`, import.meta.url), target, {
      recursive: true,
    });
  }
  // Keep this fault injection offline and independent of native package tools.
  // A new test runner must not inherit the parent runner's child context.
  const env = { ...process.env };
  delete env.NODE_TEST_CONTEXT;
  const run = () =>
    spawnSync(
      process.execPath,
      [
        join(cwd, "scripts/ci/from-root.mjs"),
        "node",
        "--test",
        "--test-name-pattern=public readback never accepts mismatched bytes",
        "scripts/release/*.test.mjs",
      ],
      { cwd, env, encoding: "utf8", timeout: 30000 },
    );
  const baseline = run();
  assert.ifError(baseline.error);
  assert.equal(baseline.status, 0, baseline.stdout + baseline.stderr);
  const path = "scripts/release/linux-packages/public-readback.mjs";
  writeFileSync(
    join(cwd, path),
    "export async function verifyPublicObject() {}\n",
  );
  const failing = run();
  assert.ifError(failing.error);
  assert.equal(failing.status, 1, failing.stdout + failing.stderr);
  assert.match(failing.stdout, /Missing expected rejection/u);
  assert.match(
    failing.stdout,
    /"event":"ci_task_exit","command":"node","code":1/u,
  );
  for (const event of [Event.PullRequest, Event.Push]) {
    const needs = results(event, [path]);
    assert.equal(planJobs(event, [path]).jobs["release-contracts"], true);
    needs["release-contracts"].result =
      failing.status === 0 ? "success" : "failure";
    assert.throws(
      () => validateResults(needs),
      /release-contracts: expected success, got failure/u,
    );
  }
});

test("every shared release fixture selects its executing CI job", () => {
  const fixtures = readdirSync(new URL("../release/", import.meta.url)).filter(
    (name) => name.endsWith(".test.mjs"),
  );
  assert.ok(fixtures.length > 0);
  for (const name of fixtures) {
    const path = `scripts/release/${name}`;
    for (const event of [Event.PullRequest, Event.Push]) {
      const needs = results(event, [path]);
      assert.equal(
        planJobs(event, [path]).jobs["release-contracts"],
        true,
        `${event}: ${path}`,
      );
      assert.equal(validateResults(needs), true);
      needs["release-contracts"].result = "skipped";
      assert.throws(() => validateResults(needs), /release-contracts/u);
    }
  }
});

test("integrated project docs select the public-docs workspace and shared inputs force it", () => {
  const id = "node-public-docs-test";
  for (const event of [Event.PullRequest, Event.Push]) {
    const paths = ["apps/public-docs/docs/runmoor/install.md"];
    assert.deepEqual(selected(event, paths), [
      id,
      "source-format",
      "release-contracts",
    ]);
    assert.equal(planJobs(event, paths).forced[id], false);
    for (const path of [
      ".nvmrc",
      "pnpm-lock.yaml",
      "scripts/run-rspress-port.mjs",
    ]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs[id], true, path);
      assert.equal(plan.forced[id], true, path);
    }
    for (const path of ["docs/project-runmoor.md"]) {
      assert.deepEqual(selected(event, [path]), ["source-format"]);
    }
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
      "cmds/async-commit-hook/main.go",
      "apps/async-commit-hook/src/App.tsx",
      "scripts/install/async-commit-hook.ps1",
      "packages/async-commit-hook-api-client/src/client.ts",
      "protos/async_commit_hook/v1/service.proto",
      "packaging/async-commit-hook/release-metadata.json",
      "scripts/release/build-async-commit-hook.py",
      "scripts/release/async-commit-hook.test.mjs",
      "scripts/release/publish-async-commit-hook.py",
      "scripts/release/async-commit-hook-publish-fixtures.py",
      ".github/workflows/release-async-commit-hook.yml",
      "docs/project-async-commit-hook.md",
      "buf.yaml",
      "buf.gen.yaml",
      "go.mod",
      "go.sum",
      "package.json",
      "pnpm-lock.yaml",
      "pnpm-workspace.yaml",
      ".nvmrc",
      ".npmrc",
      "turbo.json",
      "scripts/check-proto-breaking.sh",
      "scripts/run-rsbuild-dev.mjs",
      "scripts/spawn-dev-server.mjs",
      "scripts/dev-environment/process.mjs",
    ])
      assert.ok(
        selected(event, [path]).includes("async-commit-hook"),
        `${event}: ${path}`,
      );
    for (const path of [
      "cmds/runmoor/main.go",
      "apps/public-docs/docs/projects-overview.md",
      "docs/project-with-watch.md",
    ]) {
      assert.ok(
        !selected(event, [path]).includes("async-commit-hook"),
        `${event}: ${path}`,
      );
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
    [
      "scripts/install/async-commit-hook.sh",
      ["async-commit-hook", "node-public-docs-test"],
    ],
    ["packages/docs-site-switcher/src/index.tsx", ["node-public-docs-test"]],
    ["apps/public-docs/theme/index.tsx", ["node-public-docs-test"]],
    ["scripts/dev-environment/orchestrator.mjs", []],

    ["apps/delidev/src/App.tsx", []],
    [".nvmrc", ["node-public-docs-test"]],
    ["pnpm-lock.yaml", ["node-public-docs-test"]],
    [".cargo/config.toml", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/binpm/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/cargo-mono/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/nodeup/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["crates/with-watch/src/main.rs", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["rust-toolchain.toml", ["rust-fmt", "rust-clippy", "rust-test"]],
    ["Cargo.lock", ["rust-fmt", "rust-clippy", "rust-test"]],
  ]) {
    for (const id of ids)
      assert.ok(
        selected(Event.PullRequest, [path]).includes(id),
        `${path}: ${id}`,
      );
  }
  for (const path of [
    "apps/public-docs/theme/index.tsx",
    "apps/public-docs/docs/runmoor/install.md",
  ]) {
    assert.equal(
      planJobs(Event.PullRequest, [path]).forced["node-public-docs-test"],
      false,
      path,
    );
  }
  assert.equal(
    planJobs(Event.PullRequest, []).forced["node-public-docs-test"],
    false,
  );
  assert.equal(
    planJobs(Event.PullRequest, []).forced["node-public-docs-test"],
    false,
  );
  assert.equal(
    planJobs(Event.PullRequest, ["scripts/install/binpm.sh"]).forced[
      "node-public-docs-test"
    ],
    true,
  );
  assert.throws(() => planJobs("unknown", []), /Unsupported CI event/u);
});

function fixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), "ci-comparison-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) =>
    execFileSync("git", args, {
      cwd,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  const write = (path, value) => {
    mkdirSync(dirname(join(cwd, path)), { recursive: true });
    writeFileSync(join(cwd, path), value);
  };
  const commit = () => {
    git("add", "--all");
    git("commit", "-m", "fixture");
    return git("rev-parse", "HEAD");
  };
  write("README.md", "initial\n");
  const initial = commit();
  return { cwd, git, write, commit, initial };
}

test("comparison uses the PR merge-base and the complete multi-commit push range", (t) => {
  const f = fixture(t);
  f.git("switch", "-c", "feature");
  f.write("apps/public-docs/first.ts", "first\n");
  f.commit();
  f.write("cmds/runmoor/second.go", "second\n");
  const head = f.commit();
  f.git("switch", "main");
  f.write("unrelated-main.txt", "main\n");
  const base = f.commit();
  const pr = changedFiles(
    Event.PullRequest,
    { pull_request: { base: { sha: base }, head: { sha: head } } },
    base,
    f.cwd,
  );
  assert.equal(pr.base, f.initial);
  assert.equal(pr.head, head);
  assert.deepEqual(pr.paths, [
    "apps/public-docs/first.ts",
    "cmds/runmoor/second.go",
  ]);
  const push = changedFiles(Event.Push, { before: f.initial }, head, f.cwd);
  assert.deepEqual(push, pr);
  // A force push must compare tree endpoints, not their common ancestor.
  assert.ok(
    changedFiles(Event.Push, { before: base }, head, f.cwd).paths.includes(
      "unrelated-main.txt",
    ),
  );
});

test("deleted and renamed paths retain both owners, including unusual filenames", (t) => {
  const f = fixture(t);
  f.write("apps/public-docs/old name.ts", "original\n");
  const base = f.commit();
  mkdirSync(join(f.cwd, "apps/async-commit-hook"), { recursive: true });
  renameSync(
    join(f.cwd, "apps/public-docs/old name.ts"),
    join(f.cwd, "apps/async-commit-hook/new\nname.ts"),
  );
  rmSync(join(f.cwd, "README.md"));
  const head = f.commit();
  const { paths } = changedFiles(Event.Push, { before: base }, head, f.cwd);
  assert.deepEqual(paths, [
    "README.md",
    "apps/async-commit-hook/new\nname.ts",
    "apps/public-docs/old name.ts",
  ]);
  const jobs = selected(Event.Push, paths);
  assert.ok(jobs.includes("node-public-docs-test"));
  assert.ok(jobs.includes("async-commit-hook"));
});

test("missing, malformed, and unavailable revisions fail rather than skip checks", (t) => {
  const f = fixture(t);
  for (const before of [
    undefined,
    "",
    "0".repeat(40),
    "--help",
    "f".repeat(40),
  ]) {
    assert.throws(() => changedFiles(Event.Push, { before }, f.initial, f.cwd));
  }
  assert.throws(() => changedFiles(Event.PullRequest, {}, f.initial, f.cwd));
  assert.throws(() => changedFiles(Event.Manual, {}, f.initial, f.cwd));
  assert.throws(
    () => previousJobPaths("bad", f.cwd),
    /40-character commit SHA/u,
  );
  assert.throws(() => previousJobPaths("f".repeat(40), f.cwd));
});

test("historical path rules are read from the exact comparison base", (t) => {
  const f = fixture(t);
  assert.deepEqual(previousJobPaths(f.initial, f.cwd), {});
  f.write(
    "scripts/ci/job-paths.json",
    JSON.stringify({ "go-test": { paths: ["cmds/**"] } }),
  );
  const base = f.commit();
  f.write(
    "scripts/ci/job-paths.json",
    JSON.stringify({ "go-test": { paths: ["cmds/**", "go.mod"] } }),
  );
  f.commit();
  assert.deepEqual(previousJobPaths(base, f.cwd), {
    "go-test": { paths: ["cmds/**"] },
  });
  f.write("scripts/ci/job-paths.json", "{");
  const bad = f.commit();
  assert.throws(() => previousJobPaths(bad, f.cwd), SyntaxError);
});

function results(event, paths, mode = Mode.Affected) {
  const { jobs } = planJobs(event, paths, jobPaths, mode);
  const matrices = matricesForEvent(event, mode);
  return {
    changes: {
      result: "success",
      outputs: {
        mode,
        clibox_matrix: JSON.stringify(matrices.cliboxMatrix),
        pnport_matrix: JSON.stringify(matrices.pnportMatrix),
        jobs: JSON.stringify(jobs),
        rust_packages: JSON.stringify(
          jobs["rust-test"] || jobs["rust-clippy"] ? ["fixture-package"] : [],
        ),
        event,
        go_test_matrix: JSON.stringify(matrices.goTestMatrix),
      },
    },
    "ci-contracts": { result: "success" },
    ...Object.fromEntries(
      Object.entries(jobs).map(([id, run]) => [
        id,
        { result: run ? "success" : "skipped" },
      ]),
    ),
  };
}

test("aggregate requires Linux package skips on PRs/manual CI and success when selected on main", () => {
  const id = "linux-packages";
  for (const path of ["Cargo.lock", ".github/workflows/CI.yml"]) {
    for (const event of Object.values(Event)) {
      const needs = results(event, [path]);
      assert.equal(
        needs[id].result,
        event !== Event.Push ? "skipped" : "success",
      );
      assert.equal(validateResults(needs), true);
      const unexpected = event !== Event.Push ? "success" : "skipped";
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
  for (const event of Object.values(Event))
    assert.equal(
      validateResults(results(event, ["apps/public-docs/docs/index.md"])),
      true,
    );
  assert.equal(validateResults(results(Event.PullRequest, [])), true);
  for (const result of ["failure", "cancelled", "skipped", undefined]) {
    for (const id of ["changes", "ci-contracts", "node-public-docs-test"]) {
      const needs = results(Event.PullRequest, [
        "apps/public-docs/docs/index.md",
      ]);
      needs[id].result = result;
      assert.throws(() => validateResults(needs), new RegExp(id, "u"));
    }
  }

  for (const id of ["node-public-docs-test", "pnport-native"]) {
    const needs = results(Event.PullRequest, [
      "apps/public-docs/docs/index.md",
    ]);
    delete needs[id];
    assert.throws(() => validateResults(needs), /inventory/u);
  }
  for (const value of [
    "",
    "{}",
    "{",
    JSON.stringify({ ...planJobs(Event.Manual, []).jobs, unexpected: true }),
  ]) {
    const needs = results(Event.Manual, []);
    needs.changes.outputs.jobs = value;
    assert.throws(() => validateResults(needs));
  }
  const unexpected = results(Event.PullRequest, []);
  unexpected["pnport-native"].result = "success";
  assert.throws(() => validateResults(unexpected), /expected skipped/u);
});

test("Go matrix contains only Ubuntu on PRs, including dependency and forced configuration changes", () => {
  const ubuntu = { os: "ubuntu-latest", shard: "all", label: "ubuntu-latest" };
  for (const path of [
    "cmds/runmoor/main.go",
    "go.mod",
    "go.sum",
    ".github/workflows/CI.yml",
    "scripts/ci/native-matrices.json",
  ]) {
    assert.equal(
      planJobs(Event.PullRequest, [path]).jobs["go-test"],
      true,
      path,
    );
    assert.deepEqual(
      matricesForEvent(Event.PullRequest).goTestMatrix,
      {
        include: ["core"].map((shard) => ({
          os: "ubuntu-latest",
          shard,
          label: `ubuntu-latest, ${shard}`,
        })),
      },
      path,
    );
  }
  const complete = [
    ubuntu,
    ...["core"].map((shard) => ({
      os: "windows-latest",
      shard,
      label: `windows-latest, ${shard}`,
    })),
  ];
  assert.deepEqual(matricesForEvent(Event.Push).goTestMatrix, {
    include: complete.filter((row) => !row.os.startsWith("macos")),
  });
  assert.deepEqual(
    matricesForEvent(Event.Manual).goTestMatrix,
    matricesForEvent(Event.PullRequest).goTestMatrix,
  );
});

test("planner publishes the event Go matrix from real Git comparisons", (t) => {
  const f = fixture(t);
  const script = fileURLToPath(new URL("./plan.mjs", import.meta.url));
  const eventPath = join(f.cwd, ".git", "go-plan-event.json");
  const outputPath = join(f.cwd, ".git", "go-plan-output");
  for (const path of [
    "cmds/runmoor/example.go",
    "go.mod",
    ".github/workflows/CI.yml",
  ]) {
    f.write(path, "fixture\n");
    const head = f.commit();
    f.git("update-ref", "refs/remotes/origin/main", f.initial);
    for (const event of Object.values(Event)) {
      writeFileSync(
        eventPath,
        JSON.stringify(
          event === Event.PullRequest
            ? {
                pull_request: { base: { sha: f.initial }, head: { sha: head } },
              }
            : { before: f.initial },
        ),
      );
      writeFileSync(outputPath, "");
      execFileSync(process.execPath, [script], {
        cwd: f.cwd,
        env: {
          ...process.env,
          GITHUB_EVENT_NAME: event,
          GITHUB_EVENT_PATH: eventPath,
          GITHUB_REF: "refs/heads/feature",
          GITHUB_SHA: head,
          GITHUB_OUTPUT: outputPath,
          GITHUB_STEP_SUMMARY: "",
        },
      });
      const outputs = Object.fromEntries(
        readFileSync(outputPath, "utf8")
          .trim()
          .split("\n")
          .map((line) => {
            const index = line.indexOf("=");
            return [line.slice(0, index), line.slice(index + 1)];
          }),
      );
      assert.equal(
        JSON.parse(outputs.jobs)["go-test"],
        true,
        `${event}: ${path}`,
      );
      assert.deepEqual(
        JSON.parse(outputs.go_test_matrix),
        matricesForEvent(event).goTestMatrix,
      );
      assert.equal(outputs.base, f.initial);
      assert.equal(outputs.head, head);
    }
  }
});

test("CI Result rejects altered or missing Go matrices and every unsuccessful selected Go result", () => {
  for (const event of Object.values(Event)) {
    for (const value of [
      undefined,
      "{",
      "{}",
      "null",
      JSON.stringify({ include: [] }),
      JSON.stringify({
        include: matricesForEvent(event).goTestMatrix.include.slice(1),
      }),
      JSON.stringify({
        include: [
          ...matricesForEvent(event).goTestMatrix.include,
          matricesForEvent(event).goTestMatrix.include[0],
        ],
      }),
      JSON.stringify(
        matricesForEvent(event === Event.Push ? Event.PullRequest : Event.Push)
          .goTestMatrix,
      ),
      JSON.stringify({
        include: [
          ...matricesForEvent(event).goTestMatrix.include,
          nativeMatrices["go-test"][0],
        ],
      }),
    ]) {
      const needs = results(event, ["cmds/runmoor/example.go"]);
      needs.changes.outputs.go_test_matrix = value;
      assert.throws(() => validateResults(needs), value);
    }
    for (const result of ["failure", "cancelled", "skipped", undefined]) {
      const needs = results(event, ["cmds/runmoor/example.go"]);
      needs["go-test"].result = result;
      assert.throws(() => validateResults(needs), /go-test/u);
    }
  }
});

test("path-rule edits force only changed eligible jobs", () => {
  const previous = structuredClone(jobPaths);
  previous["go-test"].paths = previous["go-test"].paths.filter(
    (path) => path !== "cmds/**",
  );
  previous["node-public-docs-test"].paths.push("packages/unrelated/**");
  for (const event of [Event.PullRequest, Event.Push]) {
    const plan = planJobs(event, ["scripts/ci/job-paths.json"], previous);
    assert.equal(plan.jobs["go-test"], true);
    assert.equal(plan.jobs["node-public-docs-test"], true);
    assert.equal(plan.jobs["pnport-native"], false);
    assert.equal(plan.forced["go-test"], true);
  }
  assert.deepEqual(selected(Event.Push, ["scripts/ci/job-paths.json"]), [
    "source-format",
  ]);
  for (const path of [
    ".github/workflows/CI.yml",
    "scripts/ci/plan.mjs",
    "scripts/ci/native-matrices.json",
  ]) {
    const plan = planJobs(Event.Push, [path]);
    assert.equal(plan.jobs["rust-test"], true, path);
  }
});

test("aggregate rejects missing and inconsistent Rust package selections", () => {
  for (const value of [
    undefined,
    "{}",
    '["fixture-package","fixture-package"]',
    "[]",
  ]) {
    const needs = results(Event.PullRequest, ["crates/binpm/src/main.rs"]);
    needs.changes.outputs.rust_packages = value;
    assert.throws(() => validateResults(needs));
  }
  const needs = results(Event.PullRequest, ["docs/project-with-watch.md"]);
  needs.changes.outputs.rust_packages = '["fixture-package"]';
  assert.throws(() => validateResults(needs), /Rust package selection/u);
});

test("manual selection matches PR scope without macOS or native packaging", () => {
  for (const paths of [
    [],
    ["cmds/runmoor/example.go"],
    ["crates/binpm/src/main.rs"],
    [".github/workflows/CI.yml"],
    ["go.mod"],
    ["docs/project-with-watch.md"],
  ]) {
    assert.deepEqual(
      planJobs(Event.Manual, paths),
      planJobs(Event.PullRequest, paths),
    );
    assert.deepEqual(
      matricesForEvent(Event.Manual),
      matricesForEvent(Event.PullRequest),
    );
    assert.equal(validateResults(results(Event.Manual, paths)), true);
  }
  for (const event of Object.values(Event)) {
    assert.doesNotMatch(
      JSON.stringify(matricesForEvent(event)),
      /macos|darwin|apple/u,
    );
  }
});

test("manual affected comparison pins main and uses the complete branch merge-base range", (t) => {
  const f = fixture(t);
  f.git("switch", "-c", "feature");
  f.write("cmds/runmoor/first.go", "first");
  f.commit();
  f.write("crates/binpm/second.rs", "second");
  const head = f.commit();
  f.git("switch", "main");
  f.write("main-only.txt", "main");
  const mainSha = f.commit();
  const range = changedFiles(Event.Manual, {}, head, f.cwd, {
    ref: "refs/heads/feature",
    mainSha,
  });
  assert.deepEqual(range, {
    base: f.initial,
    head,
    paths: ["cmds/runmoor/first.go", "crates/binpm/second.rs"],
  });
  f.write("later-main.txt", "later");
  f.commit();
  assert.deepEqual(
    changedFiles(Event.Manual, {}, head, f.cwd, {
      ref: "refs/heads/feature",
      mainSha,
    }),
    range,
  );
  assert.deepEqual(
    changedFiles(Event.Manual, {}, mainSha, f.cwd, { ref: "refs/heads/main" }),
    { base: f.initial, head: mainSha, paths: ["main-only.txt"] },
  );
});

test("manual affected comparison and mode errors fail closed", (t) => {
  const f = fixture(t);
  assert.throws(() =>
    changedFiles(Event.Manual, {}, f.initial, f.cwd, {
      ref: "refs/heads/main",
    }),
  );
  for (const mainSha of [
    undefined,
    "",
    "0".repeat(40),
    "--help",
    "f".repeat(40),
  ]) {
    assert.throws(() =>
      changedFiles(Event.Manual, {}, f.initial, f.cwd, {
        ref: "refs/heads/feature",
        mainSha,
      }),
    );
  }
  assert.throws(() =>
    changedFiles(Event.Manual, {}, f.initial, f.cwd, {
      ref: "refs/tags/v1",
      mainSha: f.initial,
    }),
  );
  for (const mode of ["", "invalid", "full", null, true])
    assert.throws(() => executionMode(Event.Manual, mode));
  for (const event of [Event.PullRequest, Event.Push])
    assert.throws(() => executionMode(event, "full"));
});

test("CI Result rejects missing modes, native execution on affected manual CI, and every altered platform matrix", () => {
  for (const mode of [undefined, "", "invalid", null]) {
    const needs = results(Event.Manual, []);
    needs.changes.outputs.mode = mode;
    assert.throws(() => validateResults(needs));
  }
  for (const output of ["clibox_matrix", "pnport_matrix"]) {
    for (const value of [undefined, "{", "{}", "null", "[]"]) {
      const needs = results(Event.Manual, []);
      needs.changes.outputs[output] = value;
      assert.throws(() => validateResults(needs));
    }
    const needs = results(Event.Manual, []);
    const matrix = JSON.parse(needs.changes.outputs[output]);
    const rows = matrix.include ?? matrix.os;
    rows.push(rows[0]);
    needs.changes.outputs[output] = JSON.stringify(matrix);
    assert.throws(() => validateResults(needs), new RegExp(output, "u"));
  }
  const needs = results(Event.Manual, []);
  const jobs = JSON.parse(needs.changes.outputs.jobs);
  jobs["pnport-native"] = true;
  needs.changes.outputs.jobs = JSON.stringify(jobs);
  needs["pnport-native"].result = "success";
  assert.throws(() => validateResults(needs), /pnport-native/u);
});

test("CI Result requires every selected formatting job to succeed", () => {
  for (const event of Object.values(Event)) {
    for (const result of ["failure", "cancelled", "skipped", undefined]) {
      const needs = results(event, ["docs/guide.md"]);
      needs["source-format"].result = result;
      assert.throws(() => validateResults(needs), /source-format/);
    }
  }
});

test("AIPRouter shared Go libraries and fixtures select Go validation", () => {
  for (const event of Object.values(Event)) {
    for (const path of [
      "packages/aiprouter/core/ids.go",
      "scripts/aiprouter/fixture/main.go",
    ]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs["go-test"], true, `${event}: ${path}`);
      assert.equal(plan.jobs["go-quality"], true, `${event}: ${path}`);
    }
  }
});

test("Labs docs own their app and force external build inputs on all CI events", () => {
  const id = "node-labs-docs-test";
  for (const event of Object.values(Event)) {
    const local = planJobs(event, ["apps/labs-docs/docs/en/index.md"]);
    assert.equal(local.jobs[id], true);
    assert.equal(local.forced[id], false);
    for (const path of [
      "docs/apps-labs-docs-foundation.md",
      "scripts/run-rspress-port.mjs",
      "pnpm-lock.yaml",
    ]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs[id], true);
      assert.equal(plan.forced[id], true);
    }
    assert.equal(
      planJobs(event, ["apps/public-docs/docs/index.md"]).jobs[id],
      false,
    );
    assert.equal(planJobs(event, ["docs/project-rustia.md"]).jobs[id], false);
  }
});

test("shared Labs localization inputs force both app checks on every eligible event", () => {
  for (const event of Object.values(Event)) {
    for (const path of [
      "scripts/localization/chatgpt.mjs",
      "scripts/localization/catalog.mjs",
      "docs/apps-labs-localization-contract.md",
    ]) {
      const plan = planJobs(event, [path]);
      for (const app of ["labs-docs", "labs-site"]) {
        assert.equal(plan.jobs[`node-${app}-test`], true);
        assert.equal(plan.forced[`node-${app}-test`], true);
      }
    }
  }
});
