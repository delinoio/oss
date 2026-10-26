import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { runInNewContext } from "node:vm";

import { load } from "js-yaml";
import {
  Event,
  Mode,
  jobPaths,
  matricesForEvent,
  nativeMatrices,
  planJobs,
} from "./plan.mjs";
import { jobCommands, jobTaskGraph } from "./task-graph.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const workflowSource = readFileSync(`${root}/.github/workflows/CI.yml`, "utf8");
const releaseWorkflowSource = readFileSync(
  `${root}/.github/workflows/release-async-commit-hook.yml`,
  "utf8",
);
const apiDockerfileSource = readFileSync(
  `${root}/servers/devhud-api/Dockerfile`,
  "utf8",
);
const workflow = load(workflowSource);
const releaseWorkflow = load(releaseWorkflowSource);
const packages = Object.fromEntries(
  [
    "package.json",
    "apps/devhud/package.json",
    "apps/devhud-admin/package.json",
    "apps/devhud-chrome-extension/package.json",
    "apps/public-docs/package.json",
    "packages/devhud-api-client/package.json",
    "servers/devhud-api/package.json",
  ].map((path) => [path, JSON.parse(readFileSync(`${root}/${path}`, "utf8"))]),
);
const turbo = JSON.parse(readFileSync(`${root}/turbo.json`, "utf8"));
const adminTurbo = JSON.parse(
  readFileSync(`${root}/apps/devhud-admin/turbo.json`, "utf8"),
);
const extensionTurbo = JSON.parse(
  readFileSync(`${root}/apps/devhud-chrome-extension/turbo.json`, "utf8"),
);
const devhudTauri = JSON.parse(
  readFileSync(`${root}/apps/devhud/src-tauri/tauri.conf.json`, "utf8"),
);

const legacyJobs = [
  "source-format",
  "go-quality",
  "go-test",
  "repository-environment",
  "rust-fmt",
  "rust-clippy",
  "rust-test",
  "forge-test",
  "forge-render",
  "linux-packages",
  "node-public-docs-test",
  "node-clibox-test",
  "node-pnport-test",
  "node-bundlewhy-test",
  "pnport-native",
];
const devhudJobs = [
  "devhud-frontend",
  "devhud-extension",
  "devhud-rust-conformance",
  "devhud-security",
  "devhud-desktop",
  "devhud-mobile-contracts",
  "devhud-android-emulator",
  "devhud-admin",
  "devhud-api",
  "devhud-oci",
  "devhud-supply-chain",
  "devhud-release-contracts",
];
const achJobs = ["async-commit-hook"];

function step(job, id) {
  return job.steps.find((candidate) => candidate.id === id);
}

function namedStep(job, name) {
  return job.steps.find((candidate) => candidate.name === name);
}

test("rustfmt runs the uncached real cache regression with the pinned formatter", () => {
  const job = workflow.jobs["rust-fmt"];
  const graph = jobTaskGraph(job);
  const fixture = graph.get("@delinoio/ci#ci:rust:fmt-fixture");
  assert.ok(fixture);
  assert.equal(fixture.task.cache, false);
  assert.deepEqual(fixture.task.dependsOn, []);
  assert.match(
    fixture.command,
    /node --test scripts\/ci\/rustfmt-cache\.fixture\.mjs/u,
  );
  const setup = job.steps.findIndex(
    ({ uses }) => uses === "dtolnay/rust-toolchain@v1",
  );
  assert.equal(
    job.steps[setup].with.toolchain,
    readFileSync(`${root}/rust-toolchain`, "utf8").trim(),
  );
  assert.equal(job.steps[setup].with.components, "rustfmt");
  assert.ok(
    setup <
      job.steps.findIndex(({ run }) => run?.includes("ci:rust:fmt-fixture")),
  );
});

test("protocol launcher fixtures run uncached on every native CI contract host", () => {
  const job = workflow.jobs["ci-contracts"];
  assert.equal(job["runs-on"], "${{ matrix.os }}");
  assert.equal(job.strategy["fail-fast"], false);
  assert.deepEqual(job.strategy.matrix.os, ["ubuntu-22.04", "windows-latest"]);
  for (const name of [
    "Setup Go",
    "Validate workflow syntax",
    "Validate CI contracts and affected behavior",
  ]) {
    assert.equal(namedStep(job, name).if, "${{ runner.os == 'Linux' }}");
  }
  assert.ok(
    namedStep(
      job,
      "Save Go cache after successful main validation",
    ).if.includes("steps.ci-go.outcome == 'success'"),
  );
  const native = namedStep(job, "Validate protocol launcher on native host");
  assert.equal(native.if, "${{ runner.os != 'Linux' }}");
  assert.equal(
    native.run,
    "node scripts/ci/run-affected.mjs @delinoio/ci ci:proto:launcher",
  );
  assert.deepEqual(native.env, { FORCE_RUN: "true" });
  const config = JSON.parse(
    readFileSync(`${root}/scripts/ci/turbo.json`, "utf8"),
  );
  assert.equal(config.tasks["ci:proto:launcher"].cache, false);
  assert.deepEqual(config.tasks["ci:proto:launcher"].dependsOn, []);
  const manifest = JSON.parse(
    readFileSync(`${root}/scripts/ci/package.json`, "utf8"),
  );
  assert.match(manifest.scripts["ci:contracts"], /scripts\/ci\/\*\.test\.mjs/u);
  assert.equal(
    manifest.scripts["ci:proto:launcher"],
    "node from-root.mjs node --test scripts/ci/protocol-launcher.test.mjs",
  );
});

test("cache setup receives workflow variables through composite inputs and retains fallback", () => {
  const source = readFileSync(
    `${root}/.github/actions/setup-turbo-cache/action.yml`,
    "utf8",
  );
  // The runner's action-manifest context excludes vars even though workflow
  // steps accept it. Keep variable lookup in the caller to avoid a load failure
  // that bypasses the action's authentication fallback.
  assert.doesNotMatch(source, /\$\{\{\s*vars\./u);
  const action = load(source);
  for (const name of ["team", "policy"])
    assert.equal(action.inputs[name].default, "");
  const auth = action.runs.steps.find(({ id }) => id === "auth");
  assert.equal(auth.with.team, "${{ inputs.team }}");
  assert.equal(auth.with.policy, "${{ inputs.policy }}");
  assert.equal(auth["continue-on-error"], true);
  assert.ok(auth.if.includes("inputs.team != ''"));
  assert.ok(auth.if.includes("github.repository == 'delinoio/oss'"));
  assert.ok(auth.if.includes("github.workflow == 'CI'"));
  assert.ok(
    auth.if.includes(
      "startsWith(github.workflow_ref, 'delinoio/oss/.github/workflows/CI.yml@')",
    ),
  );
  assert.ok(
    auth.if.includes(
      "github.event.pull_request.head.repo.full_name == github.repository",
    ),
  );
  for (const [id, job] of Object.entries(workflow.jobs)) {
    for (const setup of job.steps.filter(
      ({ uses }) => uses === "./.github/actions/setup-turbo-cache",
    )) {
      assert.deepEqual(
        setup.with,
        {
          team: "${{ vars.TURBO_TEAM }}",
          policy: "${{ vars.TURBO_OIDC_POLICY }}",
        },
        id,
      );
      assert.equal(job.permissions["id-token"], "write", id);
    }
    // Local manifests must exist before the runner attempts to load them.
    const checkout = job.steps.findIndex(({ uses }) =>
      uses?.startsWith("actions/checkout@"),
    );
    for (const [index, candidate] of job.steps.entries()) {
      if (candidate.uses?.startsWith("./.github/actions/"))
        assert.ok(
          checkout >= 0 && checkout < index,
          `${id}: ${candidate.uses} precedes checkout`,
        );
    }
  }
});

test("native CI prepares tool state before Turbo and serializes shared Rust suites", () => {
  const job = workflow.jobs["devhud-rust-conformance"];
  const preparation = job.steps.findIndex(
    ({ uses }) => uses === "dtolnay/rust-toolchain@v1",
  );
  const validation = job.steps.findIndex(({ run }) =>
    run?.includes("test:native:capture"),
  );
  assert.ok(preparation >= 0 && preparation < validation);
  assert.equal(
    job.steps[preparation].with.toolchain,
    readFileSync(`${root}/rust-toolchain`, "utf8").trim(),
  );
  const config = JSON.parse(
    readFileSync(`${root}/apps/devhud/turbo.json`, "utf8"),
  );
  const sequence = [
    "ci:build:frontend",
    "test:native:capture",
    "test:native:shortcuts",
    "test:native:ipc",
    "test:native:updater",
  ];
  for (let i = 1; i < sequence.length; i++)
    assert.deepEqual(config.tasks[sequence[i]].dependsOn, [sequence[i - 1]]);
  const go = readFileSync(
    `${root}/.github/actions/setup-ci-go/action.yml`,
    "utf8",
  );
  for (const variable of ["GOCACHE", "GOPATH"])
    assert.ok(
      go.includes(`echo "${variable}=$(go env ${variable})" >> "$GITHUB_ENV"`),
    );
  const goTasks = JSON.parse(
    readFileSync(`${root}/scripts/ci/turbo.json`, "utf8"),
  );
  for (const name of ["ci:go:vet", "ci:go:test"]) {
    for (const variable of ["GOCACHE", "GOPATH"])
      assert.ok(
        goTasks.tasks[name].passThroughEnv.includes(variable),
        `${name}: ${variable}`,
      );
  }
});

test("async-commit-hook retains runner, interface, protocol and unsigned archive validation", () => {
  const commands = jobCommands(workflow.jobs["async-commit-hook"]);
  for (const command of [
    "async-commit-hook#ci:embedded",
    "go test -race -count=1 ./cmds/async-commit-hook/...",
    "async-commit-hook#ci:check",
    "public-docs#ci:check",
    "@delinoio/async-commit-hook-api-client#test",
    "@delinoio/ci#ci:proto:check",
    "node --test scripts/release/async-commit-hook.test.mjs",
    'python3 scripts/release/build-async-commit-hook.py --output "$RUNNER_TEMP/ach-release"',
  ])
    assert.ok(commands.includes(command), command);
  assert.equal(
    commands.match(/pnpm install --frozen-lockfile --ignore-scripts/gu)?.length,
    1,
  );
  const graph = jobTaskGraph(workflow.jobs["async-commit-hook"]);
  assert.ok(graph.has("@delinoio/ci#ci:ach:native"));
  assert.ok(graph.has("@delinoio/ci#ci:ach:check"));
  const achTasks = JSON.parse(
    readFileSync(`${root}/scripts/ci/turbo.json`, "utf8"),
  ).tasks;
  assert.equal(achTasks["ci:ach:native"].cache, false);
  assert.deepEqual(achTasks["ci:ach:native"].dependsOn, [
    "async-commit-hook#ci:embedded",
  ]);
  assert.ok(achTasks["ci:ach:check"].dependsOn.includes("ci:ach:native"));
  assert.ok(achTasks["ci:ach:check"].dependsOn.includes("ci:ach:package"));
  assert.deepEqual(achTasks["ci:ach:package"].dependsOn, ["ci:ach:native"]);
});

test("async-commit-hook CI waits for frontend readers before both rebuilds", () => {
  const graph = jobTaskGraph(workflow.jobs["async-commit-hook"]);
  const app = "async-commit-hook";
  const ci = "@delinoio/ci";
  const embedded = graph.get(`${app}#ci:embedded`);
  const native = graph.get(`${ci}#ci:ach:native`);
  const archive = graph.get(`${ci}#ci:ach:package`);
  assert.equal(embedded.command, "pnpm build:embedded");
  assert.equal(embedded.task.cache, false);
  assert.deepEqual(embedded.task.dependsOn, ["ci:check"]);
  assert.deepEqual(native.task.dependsOn, [`${app}#ci:embedded`]);
  assert.deepEqual(archive.task.dependsOn, ["ci:ach:native"]);
  for (const task of [native, archive]) assert.equal(task.task.cache, false);
  assert.deepEqual(graph.get(`${app}#ci:check`).task.dependsOn, [
    "typecheck",
    "test:unit",
    "build:frontend",
    "ci:contracts",
  ]);
  assert.deepEqual(graph.get(`${app}#ci:contracts`).task.dependsOn, [
    "build:frontend",
  ]);
  assert.ok(graph.has(`${app}#ci:contracts`));
  assert.ok(graph.has(`${app}#build:frontend`));
  assert.ok(graph.has(`${ci}#ci:ach:check`));

  const direct = JSON.parse(
    readFileSync(`${root}/apps/async-commit-hook/package.json`, "utf8"),
  );
  assert.equal(
    direct.scripts["build:embedded"],
    "node scripts/embed.mjs --clean && pnpm --filter @delinoio/async-commit-hook-api-client build && pnpm build && node scripts/embed.mjs",
  );
  const directGraph = JSON.parse(
    readFileSync(`${root}/apps/async-commit-hook/turbo.json`, "utf8"),
  );
  assert.equal(directGraph.tasks["build:embedded"].cache, false);
});

test("every step reaching shared protocol breaking checks carries the event baseline", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    const plan = planJobs(event, ["protos/devhud/v1/common.proto"]);
    assert.equal(plan.jobs["async-commit-hook"], true);
    assert.ok(
      jobTaskGraph(workflow.jobs["async-commit-hook"]).has(
        "@delinoio/ci#ci:proto:breaking",
      ),
    );
  }
  const expression =
    "${{ github.event_name == 'push' && github.event.before || (github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main' && 'HEAD^') || 'origin/main' }}";
  const consumers = [];
  const workflows = [
    {
      name: "CI.yml",
      definition: workflow,
      baseline: expression,
      evaluateEvents: true,
    },
    {
      name: "release-async-commit-hook.yml",
      definition: releaseWorkflow,
      baseline: "HEAD^",
      evaluateEvents: false,
    },
  ];
  for (const { name, definition, baseline, evaluateEvents } of workflows) {
    for (const [id, job] of Object.entries(definition.jobs)) {
      for (const candidate of job.steps) {
        const reachesBreaking =
          jobTaskGraph({ steps: [candidate] }).has(
            "@delinoio/ci#ci:proto:breaking",
          ) || candidate.run?.includes("pnpm proto:check");
        if (!reachesBreaking) continue;
        const label = `${name} ${id}: ${candidate.name}`;
        consumers.push(label);
        const env = { ...definition.env, ...job.env, ...candidate.env };
        assert.equal(env.DEVHUD_PROTO_BASELINE, baseline, label);
        assert.equal(
          job.steps.find(({ uses }) => uses?.startsWith("actions/checkout@"))
            ?.with?.["fetch-depth"],
          0,
          label,
        );
        if (evaluateEvents) {
          // This closed expression uses equality, AND and OR with the same string
          // truthiness in JavaScript and Actions; evaluate the actual workflow value.
          for (const [event, ref, expected] of [
            ["push", "refs/heads/main", "pre-push-revision"],
            ["pull_request", "refs/pull/1450/merge", "origin/main"],
            ["workflow_dispatch", "refs/heads/main", "HEAD^"],
            ["workflow_dispatch", "refs/heads/feature", "origin/main"],
          ]) {
            assert.equal(
              runInNewContext(env.DEVHUD_PROTO_BASELINE.slice(3, -2), {
                github: {
                  event_name: event,
                  ref,
                  event: { before: "pre-push-revision" },
                },
              }),
              expected,
              `${label}: ${event} ${ref}`,
            );
          }
        }
      }
    }
  }
  assert.ok(
    consumers.some((label) =>
      label.startsWith("release-async-commit-hook.yml validate:"),
    ),
    "Inventory must include the async-commit-hook release protocol step",
  );
  assert.ok(
    consumers.length >= 2,
    "Inventory must include CI and release async-commit-hook protocol steps",
  );
  const config = JSON.parse(
    readFileSync(`${root}/scripts/ci/turbo.json`, "utf8"),
  );
  assert.ok(
    config.tasks["ci:proto:breaking"].passThroughEnv.includes(
      "DEVHUD_PROTO_BASELINE",
    ),
  );
});

test("DevHud generated-only plans reach the shared uncached freshness command", () => {
  for (const event of [Event.PullRequest, Event.Push]) {
    const plan = planJobs(event, [
      "packages/devhud-api-client/src/gen/devhud/v1/common_pb.ts",
    ]);
    assert.equal(plan.jobs["async-commit-hook"], true, event);
    const job = workflow.jobs["async-commit-hook"];
    const validation = namedStep(job, "Validate package tasks");
    assert.equal(validation.env.FORCE_RUN, "true");
    const graph = jobTaskGraph(job);
    assert.ok(graph.has("@delinoio/ci#ci:proto:check"), event);
    const fresh = graph.get("@delinoio/ci#ci:proto:fresh");
    assert.ok(fresh, event);
    assert.equal(fresh.task.cache, false);
    assert.equal(
      fresh.command,
      "node from-root.mjs node scripts/ci/protocol-fresh.mjs",
    );
  }
});

test("CI keeps every legacy check and aggregates every required job", () => {
  const jobs = Object.keys(workflow.jobs);
  for (const id of [
    "ci-contracts",
    ...legacyJobs,
    ...devhudJobs,
    ...achJobs,
    "ci-result",
  ])
    assert.ok(jobs.includes(id), id);
  const required = jobs.filter((id) => id !== "ci-result").sort();
  assert.deepEqual([...workflow.jobs["ci-result"].needs].sort(), required);
  assert.equal(workflow.jobs["ci-result"].if, "always()");
  const result = namedStep(
    workflow.jobs["ci-result"],
    "Validate every planned result",
  );
  assert.equal(result.run, "node scripts/ci/result.mjs");
  assert.equal(result.env.CI_NEEDS, "${{ toJSON(needs) }}");
});

test("one change plan gates every domain job before runner allocation", () => {
  assert.equal(
    workflow.jobs.changes.steps.find(({ id }) => id === "plan").run,
    "node scripts/ci/plan.mjs",
  );
  assert.deepEqual(
    Object.keys(jobPaths).sort(),
    [...legacyJobs, ...devhudJobs, ...achJobs].sort(),
  );
  for (const id of [...legacyJobs, ...devhudJobs, ...achJobs]) {
    const job = workflow.jobs[id];
    assert.deepEqual(job.needs, "changes", id);
    assert.equal(
      job.if,
      "${{ needs.changes.result == 'success' && fromJSON(needs.changes.outputs.jobs)['" +
        id +
        "'] }}",
      id,
    );
    assert.equal(step(job, "filter"), undefined, id);
    assert.equal(step(job, "gate"), undefined, id);
  }
  const filters = JSON.stringify(jobPaths);
  for (const path of [
    "servers/**",
    "protos/**",
    "packages/devhud-api-client/**",
    "apps/devhud/**",
    "apps/devhud-admin/**",
    "apps/devhud-chrome-extension/**",
    "crates/devhud-native-messaging-host/**",
    "packaging/devhud/**",
    "apps/public-docs/**",
    ".github/workflows/package-devhud-private.yml",
    ".github/workflows/release-devhud.yml",
    ".github/workflows/devhud-cef-security-review.yml",
    "scripts/ci/check-go-format.mjs",
    ".dockerignore",
  ])
    assert.ok(filters.includes(path), path);
  for (const id of [
    "devhud-frontend",
    "devhud-extension",
    "devhud-security",
    "devhud-admin",
    "devhud-api",
    "repository-environment",
  ]) {
    for (const path of [
      ".nvmrc",
      "package.json",
      "pnpm-lock.yaml",
      "pnpm-workspace.yaml",
      "turbo.json",
    ]) {
      assert.ok(jobPaths[id].paths.includes(path), `${id}: ${path}`);
    }
  }
});

test("Node jobs use one frozen install and the planner's exact comparison through committed Turbo", () => {
  assert.doesNotMatch(workflowSource, /pnpm\s+dlx\s+turbo/iu);
  assert.match(packages["package.json"].scripts["ci:affected"], /^turbo run$/u);
  for (const [id, rule] of Object.entries(jobPaths).filter(
    ([, rule]) => rule.workspace,
  )) {
    const job = workflow.jobs[id];
    const run = job.steps.find(({ run }) =>
      run?.includes(`scripts/ci/run-affected.mjs ${rule.workspace} `),
    );
    assert.equal(run.env.TURBO_SCM_BASE, "${{ needs.changes.outputs.base }}");
    assert.equal(run.env.TURBO_SCM_HEAD, "${{ needs.changes.outputs.head }}");
    assert.equal(
      run.env.FORCE_RUN,
      "${{ fromJSON(needs.changes.outputs.forced)['" + id + "'] }}",
    );
    assert.ok(run.run.includes(rule.workspace));
    assert.equal(
      job.steps.filter(({ run }) => run?.includes("pnpm install")).length,
      1,
      id,
    );
  }
  for (const job of Object.values(workflow.jobs)) {
    for (const { run } of job.steps) {
      if (run?.includes("pnpm install"))
        assert.doesNotMatch(
          run,
          /pnpm install --frozen-lockfile(?! --ignore-scripts)/u,
        );
    }
  }
  const frontend = workflow.jobs["devhud-frontend"];
  assert.equal(
    namedStep(frontend, "Run affected frontend contracts").run,
    "node scripts/ci/run-affected.mjs devhud ci:check",
  );
  assert.match(
    namedStep(frontend, "Verify immutable desktop and mobile pins").run,
    /verify:pins/u,
  );
  const testScript = packages["apps/devhud/package.json"].scripts.test;
  for (const fixture of [
    "verify-frontend-output.mjs",
    "run-tauri.test.mjs",
    "run-mobile.test.mjs",
    "verify-pins-policy.test.mjs",
    "mobile:check",
  ])
    assert.ok(testScript.includes(fixture), fixture);
});

test("DevHud client test-only changes require semantic type checking and runtime tests", () => {
  const frontend = workflow.jobs["devhud-frontend"];
  const graph = jobTaskGraph(frontend);
  assert.equal(
    graph.get("@delinoio/devhud-api-client#typecheck")?.command,
    "tsc -p tsconfig.json --noEmit",
  );
  const client = frontend.steps.find(({ run }) =>
    run?.startsWith(
      "node scripts/ci/run-affected.mjs @delinoio/devhud-api-client ",
    ),
  );
  assert.equal(
    client?.run,
    "node scripts/ci/run-affected.mjs @delinoio/devhud-api-client typecheck test",
  );
  assert.equal(client.env.FORCE_RUN, "true");
  assert.equal(client.if, undefined);
  assert.equal(
    graph.get("@delinoio/devhud-api-client#test")?.command,
    "vitest run",
  );
  assert.equal(
    graph.get("@delinoio/devhud-api-client#build")?.command,
    "tsc -p tsconfig.build.json",
  );

  for (const event of [Event.PullRequest, Event.Push]) {
    const plan = planJobs(event, [
      "packages/devhud-api-client/tests/validation.test.ts",
    ]);
    assert.equal(plan.jobs["devhud-frontend"], true, event);
    const unrelated = planJobs(event, ["packages/example/src/session.ts"]);
    assert.equal(unrelated.jobs["devhud-frontend"], false, event);
  }
});

test("caches restore on PRs and save only after successful main validation", () => {
  for (const path of ["CI.yml", "runmoor.yml"]) {
    const { jobs } = load(
      readFileSync(`${root}/.github/workflows/${path}`, "utf8"),
    );
    for (const [id, job] of Object.entries(jobs)) {
      for (const kind of ["node", "go"]) {
        const setup = job.steps.find(
          ({ uses }) => uses === `./.github/actions/setup-ci-${kind}`,
        );
        if (!setup) continue;
        const save = job.steps.find(
          ({ with: inputs, uses }) =>
            uses?.startsWith("actions/cache/save@") &&
            inputs.key.includes(`ci-${kind}`),
        );
        assert.ok(save, `${id}: ${kind}`);
        assert.match(
          save.if,
          /success\(\) && github.ref == 'refs\/heads\/main'/u,
        );
        assert.ok(job.steps.indexOf(save) > job.steps.indexOf(setup));
      }
      for (const candidate of job.steps.filter(
        ({ uses }) =>
          uses ===
          "Swatinem/rust-cache@6323deb102c322ba6fcbdcafc7e3dddab59af2b6",
      )) {
        assert.equal(
          candidate.with["save-if"],
          "${{ github.ref == 'refs/heads/main' }}",
        );
        assert.equal(candidate.with["cache-on-failure"], false);
      }
    }
  }
  assert.ok(
    !workflow.jobs["rust-fmt"].steps.some(
      ({ uses }) =>
        uses === "Swatinem/rust-cache@6323deb102c322ba6fcbdcafc7e3dddab59af2b6",
    ),
  );
  for (const kind of ["node", "go"]) {
    const action = load(
      readFileSync(
        `${root}/.github/actions/setup-ci-${kind}/action.yml`,
        "utf8",
      ),
    );
    assert.ok(
      !action.runs.steps.some(({ uses }) =>
        uses?.startsWith("actions/cache/save@"),
      ),
    );
    const cache = action.runs.steps.find(
      ({ uses }) =>
        uses ===
        "actions/cache/restore@caa296126883cff596d87d8935842f9db880ef25",
    );
    for (const marker of [
      "runner.os",
      "runner.arch",
      "hashFiles",
      kind === "node" ? "node-version" : "go-version",
    ])
      assert.ok(cache.with.key.includes(marker), marker);
    const setup = action.runs.steps.find(({ uses }) =>
      uses?.startsWith(`actions/setup-${kind}@`),
    );
    assert.equal(
      setup.with[kind === "node" ? "package-manager-cache" : "cache"],
      false,
    );
  }
});

test("DevHud desktop retries its locked Rust fetch before offline contract verification", () => {
  const desktop = workflow.jobs["devhud-desktop"];
  const cache = namedStep(desktop, "Cache Rust dependencies");
  const fetch = namedStep(desktop, "Fetch locked Rust dependencies");
  const verify = namedStep(desktop, "Verify frontend and immutable pins");

  assert.equal(
    cache.uses,
    "Swatinem/rust-cache@6323deb102c322ba6fcbdcafc7e3dddab59af2b6",
  );
  assert.equal(cache.with["save-if"], "${{ github.ref == 'refs/heads/main' }}");
  assert.equal(cache.with["cache-on-failure"], false);
  assert.equal(cache.with["cache-targets"], false);
  for (const expected of [
    "for attempt in 1 2 3",
    "cargo fetch --locked",
    "attempt * 15",
    "Cargo dependency fetch failed after three attempts",
  ]) {
    assert.ok(fetch.run.includes(expected), expected);
  }
  assert.equal(verify.env.CARGO_NET_OFFLINE, "true");
  assert.ok(desktop.steps.indexOf(cache) < desktop.steps.indexOf(fetch));
  assert.ok(desktop.steps.indexOf(fetch) < desktop.steps.indexOf(verify));
});

test("DevHud API PostgreSQL runs only inside its selected domain job", () => {
  const job = workflow.jobs["devhud-api"];
  assert.equal(job.services, undefined);
  const startIndex = job.steps.findIndex(
    (candidate) => candidate.name === "Start PostgreSQL",
  );
  const integrationIndex = job.steps.findIndex(
    (candidate) =>
      candidate.name === "Run PostgreSQL migration and integration tests",
  );
  const stopIndex = job.steps.findIndex(
    (candidate) => candidate.name === "Stop PostgreSQL",
  );
  assert.ok(
    startIndex >= 0 &&
      startIndex < integrationIndex &&
      integrationIndex < stopIndex,
  );
  const startPostgreSQL = job.steps[startIndex];
  assert.equal(startPostgreSQL.if, undefined);
  for (const expected of [
    "docker run --detach",
    "postgres:15-bookworm",
    "--publish 5432:5432",
    "pg_isready",
    "docker inspect",
    "State.Health.Status",
    "docker logs devhud-postgres",
  ])
    assert.ok(startPostgreSQL.run.includes(expected), expected);
  const stopPostgreSQL = job.steps[stopIndex];
  assert.equal(stopPostgreSQL.if, "${{ always() }}");
  assert.match(stopPostgreSQL.run, /docker rm --force devhud-postgres/u);
});

test("desktop and mobile matrices match the committed architecture contracts", () => {
  assert.equal(
    workflow.jobs["devhud-desktop"].strategy.matrix,
    "${{ fromJSON(needs.changes.outputs.desktop_matrix) }}",
  );
  const desktop = nativeMatrices["devhud-desktop"].map(({ id }) => id);
  assert.deepEqual(desktop, [
    "macos-x64",
    "macos-arm64",
    "windows-x64-nsis",
    "windows-x64-msi",
    "windows-arm64-nsis",
    "windows-arm64-msi",
    "ubuntu-x64-deb",
    "ubuntu-x64-appimage",
    "ubuntu-arm64-deb",
    "ubuntu-arm64-appimage",
  ]);
  const platforms = JSON.parse(
    readFileSync(`${root}/apps/devhud/platforms.json`, "utf8"),
  );
  for (const target of platforms.targets)
    assert.ok(
      desktop.some((id) => id === target.id || id.startsWith(`${target.id}-`)),
      target.id,
    );
  assert.equal(workflow.jobs["devhud-ios-simulator"], undefined);
  const android =
    workflow.jobs["devhud-android-emulator"].strategy.matrix.include;
  assert.deepEqual(
    android.map(({ target }) => target),
    ["aarch64", "armv7", "x86_64", "aarch64-armv7"],
  );
  assert.deepEqual(
    android.find(({ target }) => target === "aarch64-armv7"),
    {
      target: "aarch64-armv7",
      artifacts: "--aab",
      production: true,
      combined: true,
    },
  );
  const combinedAndroid = namedStep(
    workflow.jobs["devhud-android-emulator"],
    "Build combined production Android App Bundle",
  );
  assert.match(
    combinedAndroid.run,
    /android build --target aarch64 --target armv7 --aab/u,
  );
  const combinedAndroidVerification = namedStep(
    workflow.jobs["devhud-android-emulator"],
    "Verify combined production Android App Bundle contracts",
  );
  assert.match(
    combinedAndroidVerification.run,
    /--android-abi arm64-v8a --android-abi armeabi-v7a/u,
  );
  assert.deepEqual(workflow.jobs["devhud-oci"].strategy.matrix.target, [
    "api",
    "sweeper",
  ]);
  assert.deepEqual(devhudTauri.bundle.icon, [
    "icons/icon.png",
    "icons/icon.ico",
  ]);
  for (const icon of devhudTauri.bundle.icon) {
    assert.ok(
      readFileSync(`${root}/apps/devhud/src-tauri/${icon}`).length > 0,
      icon,
    );
  }
});

test("DevHud signature fixtures run on Windows without release authority", () => {
  const job = workflow.jobs["devhud-supply-chain"];
  assert.equal(job["runs-on"], "${{ matrix.os }}");
  assert.deepEqual(job.strategy.matrix.os, ["ubuntu-22.04", "windows-latest"]);
  assert.equal(job.strategy["fail-fast"], false);
  const setup = namedStep(job, "Setup Go for native signature fixture");
  const native = namedStep(
    job,
    "Validate Windows signature failure and success fixtures",
  );
  assert.equal(setup.if, "${{ runner.os == 'Windows' }}");
  assert.equal(setup.uses, "./.github/actions/setup-ci-go");
  assert.ok(job.steps.indexOf(setup) < job.steps.indexOf(native));
  assert.equal(native.if, "${{ runner.os == 'Windows' }}");
  assert.equal(native.env.FORCE_RUN, "true");
  assert.equal(
    namedStep(
      job,
      "Validate installer, SPDX, provenance, updater, and key-rotation fixtures",
    ).if,
    "${{ runner.os != 'Windows' }}",
  );
  const task = jobTaskGraph(job).get("@delinoio/ci#ci:windows-signature");
  assert.equal(
    task.command,
    "node from-root.mjs node --test scripts/release/devhud-private-workflow.test.mjs",
  );
  assert.equal(task.task.cache, false);
  assert.equal(job.environment, undefined);
  assert.equal(job.permissions.contents, "read");
  assert.equal(job.permissions["id-token"], "write");
  assert.doesNotMatch(JSON.stringify(job), /secrets\./u);
});

test("implemented DevHud conformance commands are wired to their owning jobs", () => {
  const commands = new Map([
    [
      "devhud-api",
      [
        "ci:format",
        "ci:vet",
        "ci:build",
        "ci:unit",
        "ci:migrations",
        "ci:integration",
        "ci:api",
        "ci:sweeper",
      ],
    ],
    [
      "devhud-rust-conformance",
      [
        "test:native:capture",
        "test:native:shortcuts",
        "test:native:ipc",
        "test:native:updater",
      ],
    ],
    ["devhud-frontend", ["run-affected.mjs devhud ci:check", "verify:pins"]],
    ["devhud-admin", ["devhud-admin ci:check"]],
    [
      "devhud-extension",
      ["test:unit", "vitest run", "ci:policy", "test:package", "ci:zip"],
    ],
    [
      "devhud-security",
      [
        "test:security",
        "test:adapters",
        "diagnostics-policy.test.mjs",
        "native-bridge.test.mjs",
        "mobile-policy.test.mjs",
      ],
    ],
    [
      "devhud-desktop",
      [
        "verify:pins",
        "smoke:platform",
        "xvfb-run",
        "io.delino.devhud.native_messaging",
      ],
    ],
    ["devhud-mobile-contracts", ["mobile:check", "test:components"]],
    [
      "devhud-android-emulator",
      ["mobile:generate", "run-mobile.mjs android build", "--bundletool-jar"],
    ],
    [
      "devhud-supply-chain",
      [
        "finalize-devhud-deb.test.mjs",
        "generate-devhud-supply-chain.test.mjs",
        "generate-devhud-updater.test.mjs",
        "validate-devhud-private-build.test.mjs",
      ],
    ],
    ["devhud-release-contracts", ["scripts/release/*.test.mjs"]],
  ]);
  for (const [id, expected] of commands) {
    const source =
      JSON.stringify(workflow.jobs[id]) + jobCommands(workflow.jobs[id]);
    for (const command of expected)
      assert.ok(source.includes(command), `${id}: ${command}`);
  }
  const apiGraph = jobTaskGraph(workflow.jobs["devhud-api"]);
  for (const name of [
    "ci:format",
    "ci:vet",
    "ci:build",
    "ci:unit",
    "ci:migrations",
    "ci:integration",
    "ci:api",
    "ci:sweeper",
  ]) {
    assert.ok(apiGraph.get(`@delinoio/devhud-api#${name}`)?.command, name);
    assert.equal(
      apiGraph.get(`@delinoio/devhud-api#${name}`).task.cache,
      false,
      name,
    );
  }
  assert.match(
    packages["apps/devhud/package.json"].scripts.test,
    /^pnpm lint && pnpm test:unit && pnpm test:components/u,
  );
  assert.ok(
    jobTaskGraph(workflow.jobs["devhud-security"]).has("devhud#test:security"),
  );
  assert.ok(
    jobTaskGraph(workflow.jobs["devhud-security"]).has("devhud#test:adapters"),
  );
});

test("OCI validation is multi-architecture, non-root, migration-bearing, and local-only", () => {
  const job = workflow.jobs["devhud-oci"];
  const ociSource = readFileSync(
    `${root}/servers/devhud-api/scripts/ci-oci.sh`,
    "utf8",
  );
  const source = JSON.stringify(job) + ociSource + jobCommands(job);
  for (const expected of [
    "linux/amd64,linux/arm64",
    "type=oci",
    "65532",
    "io.delino.devhud.migrations",
    "io.delino.devhud.administrator-assets",
    "spdx-json",
    "packages | length > 0",
    "go test -count=1 -tags=integration ./servers/devhud-api/internal/postgres",
    "--load",
    "--platform linux/amd64",
    "docker:$image",
    "--user 65532:65532",
    "devhud-api migrate",
    "migrate",
    "--once",
  ])
    assert.ok(source.includes(expected), expected);
  assert.equal(job.services, undefined);
  const startPostgreSQL = namedStep(job, "Start PostgreSQL");
  assert.equal(startPostgreSQL.if, undefined);
  for (const expected of [
    "docker run --detach",
    "postgres:15-bookworm",
    "--publish 5432:5432",
    "pg_isready",
    "docker inspect",
    "State.Health.Status",
    "docker logs devhud-postgres",
  ])
    assert.ok(startPostgreSQL.run.includes(expected), expected);
  const setupNode = namedStep(job, "Setup Node.js");
  assert.equal(setupNode.if, undefined);
  assert.equal(setupNode.uses, "./.github/actions/setup-ci-node");
  assert.ok(jobTaskGraph(job).has("devhud-admin#build:embedded"));
  const stopPostgreSQL = namedStep(job, "Stop PostgreSQL");
  assert.equal(stopPostgreSQL.if, "${{ always() }}");
  assert.match(stopPostgreSQL.run, /docker rm --force devhud-postgres/u);
  const buildAndInspect = ociSource;
  assert.match(
    buildAndInspect,
    /if \[ "\$OCI_TARGET" = api \]; then\s+docker run "\$\{docker_args\[@\]\}" "\$image" migrate\s+else\s+DEVHUD_DATABASE_URL="\$DEVHUD_TEST_DATABASE_URL" go run \.\/servers\/devhud-api\/cmd\/devhud-api migrate/u,
  );
  assert.doesNotMatch(source, /(?:docker|skopeo) push/iu);
  assert.doesNotMatch(source, /docker-daemon:/u);
  assert.match(
    apiDockerfileSource,
    /^FROM --platform=\$BUILDPLATFORM golang:/mu,
  );
  const moduleGoVersion = readFileSync(`${root}/go.mod`, "utf8").match(
    /^go (\S+)$/mu,
  )?.[1];
  const imageGoVersion = apiDockerfileSource.match(
    /^FROM --platform=\$BUILDPLATFORM golang:([\d.]+)-bookworm AS build$/mu,
  )?.[1];
  assert.ok(moduleGoVersion);
  assert.equal(
    imageGoVersion,
    moduleGoVersion,
    "OCI builder must match the module toolchain floor",
  );
});

test("Debian desktop validation installs, launches, unregisters, and removes the package", () => {
  const source = JSON.stringify(workflow.jobs["devhud-desktop"]);
  for (const expected of [
    "finalize-devhud-deb.sh",
    "sudo dpkg -i",
    "/usr/bin/devhud",
    "gnome-keyring-daemon",
    "/run/user/$(id -u)",
    "XDG_RUNTIME_DIR",
    "DBUS_SESSION_BUS_ADDRESS#unix:path=",
    "sudo dpkg -r",
    "test ! -e /usr/bin/devhud",
    "test ! -e /etc/opt/chrome/native-messaging-hosts/io.delino.devhud.native_messaging.json",
  ])
    assert.ok(source.includes(expected), expected);
  const lifecycle = namedStep(
    workflow.jobs["devhud-desktop"],
    "Verify Ubuntu Debian Native Messaging install and uninstall lifecycle",
  ).run;
  for (const expected of [
    'if [ -S "$runtime/bus" ]',
    'export DBUS_SESSION_BUS_ADDRESS="unix:path=$runtime/bus"',
    'sudo ln -s "$session_socket" "$runtime/bus"',
    "created_bus=true",
    'if [ "$created_bus" = true ]',
  ])
    assert.ok(lifecycle.includes(expected), expected);
  const register = lifecycle.indexOf(
    '"$host" register "$host" "$user_manifest"',
  );
  const registered = lifecycle.indexOf('test -e "$user_manifest"', register);
  const uninstall = lifecycle.indexOf('sudo dpkg -r "$package"');
  const removed = lifecycle.indexOf('test ! -e "$user_manifest"');
  assert.ok(
    register >= 0 &&
      register < registered &&
      registered < uninstall &&
      uninstall < removed,
  );
});

test("Windows desktop validation removes Native Messaging and executable payloads", () => {
  const lifecycle = namedStep(
    workflow.jobs["devhud-desktop"],
    "Verify Windows Native Messaging install and uninstall lifecycle",
  ).run;
  const manifestDeclared = lifecycle.indexOf(
    "$manifestPath = $registration.'(default)'",
  );
  const manifestPresent = lifecycle.indexOf(
    "if (-not (Test-Path -LiteralPath $manifestPath))",
    manifestDeclared,
  );
  const uninstall = lifecycle.indexOf(
    'if ($env:DEVHUD_BUNDLE -eq "msi")',
    manifestPresent,
  );
  const registryRemoved = lifecycle.indexOf(
    'if (Test-Path "HKCU:\\Software\\Google\\Chrome\\NativeMessagingHosts\\io.delino.devhud.native_messaging")',
    uninstall,
  );
  const manifestRemoved = lifecycle.indexOf(
    "if (Test-Path -LiteralPath $manifestPath)",
    registryRemoved,
  );
  const executableRemoved = lifecycle.indexOf(
    "if (Test-Path -LiteralPath $env:DEVHUD_SMOKE_ARTIFACT)",
    manifestRemoved,
  );
  assert.ok(
    manifestDeclared >= 0 &&
      manifestDeclared < manifestPresent &&
      manifestPresent < uninstall &&
      uninstall < registryRemoved &&
      registryRemoved < manifestRemoved &&
      manifestRemoved < executableRemoved,
  );
});

test("macOS and AppImage desktop validation exercises packaged Native Messaging lifecycles", () => {
  const macOS = namedStep(
    workflow.jobs["devhud-desktop"],
    "Verify macOS Native Messaging register and unregister lifecycle",
  ).run;
  for (const expected of [
    "DEVHUD_SMOKE_NATIVE_HOST",
    "devhud-native-home",
    "Library/Application Support/Google/Chrome/NativeMessagingHosts",
    "jq -e",
    "register",
    "unregister",
  ])
    assert.ok(macOS.includes(expected), `macOS: ${expected}`);
  const macRegister = macOS.indexOf(
    '"$DEVHUD_SMOKE_NATIVE_HOST" register "$DEVHUD_SMOKE_NATIVE_HOST" "$user_manifest"',
  );
  const macRegistered = macOS.indexOf('test -e "$user_manifest"', macRegister);
  const macUnregister = macOS.indexOf(
    '"$DEVHUD_SMOKE_NATIVE_HOST" unregister "$user_manifest"',
    macRegistered,
  );
  const macRemoved = macOS.indexOf('test ! -e "$user_manifest"', macUnregister);
  assert.ok(
    macRegister >= 0 &&
      macRegister < macRegistered &&
      macRegistered < macUnregister &&
      macUnregister < macRemoved,
  );

  const appImage = namedStep(
    workflow.jobs["devhud-desktop"],
    "Verify Ubuntu AppImage Native Messaging register and unregister lifecycle",
  ).run;
  const appImagePreparation = namedStep(
    workflow.jobs["devhud-desktop"],
    "Prepare Ubuntu installed layout",
  ).run;
  for (const expected of [
    "appdir=$(realpath squashfs-root)",
    'sandbox=$(realpath "$appdir/shared/bin/chrome-sandbox")',
    'artifact="$appdir/bin/devhud"',
    'host="$appdir/bin/devhud-native-messaging-host"',
  ])
    assert.ok(
      appImagePreparation.includes(expected),
      `AppImage preparation: ${expected}`,
    );
  for (const expected of [
    "DEVHUD_SMOKE_NATIVE_HOST",
    "devhud-native-home",
    ".config/google-chrome/NativeMessagingHosts",
    "dbus-run-session",
    "gnome-keyring-daemon",
    "jq -e",
  ])
    assert.ok(appImage.includes(expected), `AppImage: ${expected}`);
  const appImageRegister = appImage.indexOf('"$host" register "$host"');
  const appImageRegistered = appImage.indexOf(
    'test -e "$user_manifest"',
    appImageRegister,
  );
  const appImageUnregister = appImage.indexOf(
    '"$host" unregister',
    appImageRegistered,
  );
  const appImageRemoved = appImage.indexOf(
    'test ! -e "$user_manifest"',
    appImageUnregister,
  );
  assert.ok(
    appImageRegister >= 0 &&
      appImageRegister < appImageRegistered &&
      appImageRegistered < appImageUnregister &&
      appImageUnregister < appImageRemoved,
  );
});

test("package-local CI commands and deterministic cache boundaries are explicit", () => {
  const requiredScripts = new Map([
    [
      "apps/devhud/package.json",
      [
        "typecheck",
        "lint",
        "test:unit",
        "test:components",
        "test:accessibility",
        "test:security",
        "test:adapters",
        "build:frontend",
        "test:native:capture",
        "test:native:shortcuts",
        "test:native:ipc",
        "test:native:updater",
      ],
    ],
    [
      "apps/devhud-admin/package.json",
      [
        "typecheck",
        "lint",
        "test:unit",
        "test:components",
        "test:accessibility",
        "build:frontend",
        "verify:embedded",
      ],
    ],
    [
      "apps/devhud-chrome-extension/package.json",
      [
        "typecheck",
        "lint",
        "test:unit",
        "test:components",
        "test:accessibility",
        "test:package",
        "build:frontend",
      ],
    ],
    [
      "servers/devhud-api/package.json",
      [
        "ci:format",
        "ci:vet",
        "ci:build",
        "ci:unit",
        "ci:migrations",
        "ci:integration",
        "ci:api",
        "ci:sweeper",
      ],
    ],
    ["apps/public-docs/package.json", ["build:frontend", "test:routes"]],
  ]);
  for (const [path, names] of requiredScripts) {
    for (const name of names)
      assert.equal(
        typeof packages[path].scripts[name],
        "string",
        `${path}#${name}`,
      );
  }
  for (const output of ["dist/**", "build/**", "artifacts/**", "doc_build/**"])
    assert.ok(turbo.tasks["build:frontend"].outputs.includes(output), output);
  assert.ok(
    turbo.tasks.build.inputs.includes("$TURBO_DEFAULT$"),
    "build must hash package-default tracked inputs",
  );
  for (const output of [
    "protos/gen/**",
    "packages/devhud-api-client/src/gen/**",
  ])
    assert.ok(
      turbo.tasks["//#proto:generate"].outputs.includes(output),
      output,
    );

  assert.equal(
    packages["package.json"].scripts["proto:generate:cached"],
    "turbo run //#proto:generate",
  );
  assert.equal(
    packages["package.json"].scripts["proto:fresh"],
    "turbo run ci:proto:fresh --filter=@delinoio/ci",
  );
  const ciTasks = JSON.parse(
    readFileSync(`${root}/scripts/ci/turbo.json`, "utf8"),
  ).tasks;
  assert.equal(ciTasks["ci:proto:fresh"].cache, false);
  assert.match(
    readFileSync(`${root}/scripts/ci/protocol-fresh.mjs`, "utf8"),
    /run\("node", \[resolveBufEntry\(cwd\), "generate"\]\)/u,
  );
  const adminScripts = packages["apps/devhud-admin/package.json"].scripts;
  for (const task of ["build:embedded", "verify:embedded"]) {
    assert.match(
      adminScripts[task],
      /^pnpm --filter @delinoio\/devhud-api-client build && pnpm build &&/u,
      task,
    );
  }
  for (const task of [
    "build",
    "build:frontend",
    "build:embedded",
    "verify:embedded",
  ]) {
    assert.equal(adminTurbo.tasks[task].cache, false, task);
  }
  assert.deepEqual(extensionTurbo.extends, ["//"]);
  // Both build:test and the clean-build regression replace the same outputs.
  // Finish the regression before the final build or cached output restoration.
  assert.deepEqual(extensionTurbo.tasks["test:package"].dependsOn, []);
  assert.equal(extensionTurbo.tasks["test:package"].cache, false);
  assert.deepEqual(extensionTurbo.tasks["build:test"].dependsOn, [
    "test:package",
  ]);
  assert.deepEqual(extensionTurbo.tasks["ci:zip"].dependsOn, ["build:test"]);
  for (const output of ["dist/**", "build/**", "artifacts/**"]) {
    assert.ok(
      extensionTurbo.tasks["build:test"].outputs.includes(output),
      output,
    );
  }
  for (const task of ["build", "build:frontend"]) {
    assert.deepEqual(
      extensionTurbo.tasks[task].inputs,
      ["$TURBO_DEFAULT$", "$TURBO_ROOT$/apps/devhud/src-tauri/icons/icon.png"],
      task,
    );
  }
  const nativeTurbo = JSON.parse(
    readFileSync(`${root}/apps/devhud/turbo.json`, "utf8"),
  );
  for (const task of [
    "test:unit",
    "test:components",
    "test:security",
    "test:adapters",
  ])
    assert.deepEqual(nativeTurbo.tasks[task].dependsOn, ["^build"], task);
  for (const task of [
    "test",
    "build",
    "test:native:capture",
    "test:native:shortcuts",
    "test:native:ipc",
    "test:native:updater",
    "mobile:generate",
    "build:ios",
    "build:android",
    "smoke:platform",
  ])
    assert.equal(nativeTurbo.tasks[task].cache, false, task);

  const devhudScripts = packages["apps/devhud/package.json"].scripts;
  const devhudTestFiles = readdirSync(`${root}/apps/devhud/src`).filter(
    (path) => /\.test\.tsx?$/u.test(path),
  );
  for (const path of devhudTestFiles) {
    const script = path.endsWith(".tsx")
      ? devhudScripts["test:components"]
      : devhudScripts["test:unit"];
    assert.ok(script.includes(`src/${path}`), path);
  }
  assert.doesNotMatch(
    `${devhudScripts["test:unit"]} ${devhudScripts["test:components"]}`,
    /[*?]/u,
  );
});

test("CI is read-only and contains no publication or secret injection path", () => {
  assert.deepEqual(workflow.permissions, {
    contents: "read",
    "pull-requests": "read",
  });
  assert.doesNotMatch(workflowSource, /\$\{\{\s*secrets\./u);
  for (const forbidden of [
    /\bgit push\b/iu,
    /\bgh release (?:create|edit|upload|delete)/iu,
    /\bdocker push\b/iu,
    /\bcosign sign\b/iu,
    /wrangler\s+pages\s+deploy/iu,
    /devhud-store-release\.mjs\s+(?:submit|publish|withdraw)/iu,
    /devhud-release-controller\.mjs\s+(?:prepare|promote|rollback)/iu,
  ])
    assert.doesNotMatch(workflowSource, forbidden);
});

test("local CI commands are documented by repository contracts", () => {
  const contract = readFileSync(
    `${root}/docs/repository-workflow-contract.md`,
    "utf8",
  );
  const project = readFileSync(`${root}/docs/project-devhud.md`, "utf8");
  for (const command of [
    "pnpm ci:workflows",
    "pnpm ci:contracts",
    "pnpm ci:release-fixtures",
  ])
    assert.ok(contract.includes(command), command);
  for (const command of [
    "test:native:capture",
    "test:native:shortcuts",
    "test:native:ipc",
    "test:security",
    "test:adapters",
  ])
    assert.ok(project.includes(command), command);
});

test("Go validation consumes the planned event matrix and retains the native Windows core shard on main", () => {
  const job = workflow.jobs["go-test"];
  assert.equal(job.strategy["fail-fast"], false);
  assert.equal(
    workflow.jobs.changes.outputs.go_test_matrix,
    "${{ steps.plan.outputs.go_test_matrix }}",
  );
  assert.equal(
    job.strategy.matrix,
    "${{ fromJSON(needs.changes.outputs.go_test_matrix) }}",
  );
  assert.deepEqual(matricesForEvent(Event.Push).goTestMatrix, {
    include: [
      { os: "ubuntu-latest", shard: "all", label: "ubuntu-latest" },
      ...["core"].map((shard) => ({
        os: "windows-latest",
        shard,
        label: `windows-latest, ${shard}`,
      })),
    ],
  });
  assert.equal(job.name, "Go Test (${{ matrix.label }})");
  assert.equal(job["runs-on"], "${{ matrix.os }}");
  assert.equal(
    namedStep(job, "Run go test").run,
    "node scripts/ci/run-affected.mjs @delinoio/ci ci:go:test",
  );
  assert.equal(
    namedStep(job, "Run go test").env.CI_GO_TEST_SHARD,
    "${{ matrix.shard }}",
  );
  assert.equal(namedStep(job, "Checkout").with.lfs, true);
  const embeds = namedStep(
    job,
    "Generate and verify embedded administrator assets",
  );
  for (const name of ["devhud-admin", "async-commit-hook"])
    assert.ok(jobTaskGraph(job).has(`${name}#build:embedded`));
  assert.ok(
    job.steps.indexOf(embeds) <
      job.steps.indexOf(namedStep(job, "Run go test")),
  );
  assert.equal(
    step(job, "ci-go").with["cache-scope"],
    "${{ matrix.os == 'ubuntu-latest' && 'go-test-all' || format('go-test-{0}', matrix.shard) }}",
  );
  assert.equal(
    step(job, "ci-go").with["cache-fallback-scope"],
    "${{ matrix.os == 'windows-latest' && matrix.shard == 'workspace' && 'go-test-worker' || '' }}",
  );
  const summary = namedStep(job, "Summarize Go test timings");
  assert.ok(summary.if.includes("always()"));
  assert.ok(summary.run.includes("go-test-report.mjs --summary"));
  const report = namedStep(job, "Preserve Go test timings");
  assert.equal(report.if, "${{ always() }}");
  assert.equal(report.with.path, ".turbo/go-test/*.json");
  assert.equal(report.with["include-hidden-files"], true);
  assert.equal(report.with["retention-days"], 7);
  for (const field of [
    "runner.os",
    "matrix.shard",
    "github.sha",
    "github.run_attempt",
  ])
    assert.ok(report.with.name.includes(field));
});

test("scoped Go caches preserve default keys and restore shared main caches on first use", () => {
  const action = load(
    readFileSync(`${root}/.github/actions/setup-ci-go/action.yml`, "utf8"),
  );
  assert.equal(action.inputs["cache-scope"].default, "");
  assert.equal(action.inputs["cache-scope"].required, false);
  assert.equal(action.inputs["cache-fallback-scope"].default, "");
  assert.equal(action.inputs["cache-fallback-scope"].required, false);
  const cache = step({ steps: action.runs.steps }, "restore");
  const sharedPrefix =
    "ci-go-v1-${{ runner.os }}-${{ runner.arch }}-${{ steps.go.outputs.go-version }}-";
  const scopedPrefix =
    "ci-go-v1${{ inputs.cache-scope != '' && format('-{0}', inputs.cache-scope) || '' }}-${{ runner.os }}-${{ runner.arch }}-${{ steps.go.outputs.go-version }}-";
  assert.equal(
    cache.with.key,
    `${scopedPrefix}\${{ hashFiles('go.mod', 'go.sum') }}`,
  );
  const previousScope =
    "${{ inputs.cache-fallback-scope != '' && format('ci-go-v1-{0}-{1}-{2}-{3}-', inputs.cache-fallback-scope, runner.os, runner.arch, steps.go.outputs.go-version) || '' }}";
  assert.deepEqual(cache.with["restore-keys"].trim().split("\n"), [
    scopedPrefix,
    previousScope,
    sharedPrefix,
  ]);
  assert.equal(
    action.outputs["cache-key"].value,
    "${{ steps.restore.outputs.cache-primary-key }}",
  );
});

test("Forge retains three-platform interoperability and mandatory Linux rendering", () => {
  assert.equal(
    workflow.jobs["forge-test"].strategy.matrix,
    "${{ fromJSON(needs.changes.outputs.forge_matrix) }}",
  );
  const testCommands = jobCommands(workflow.jobs["forge-test"]);
  assert.match(
    testCommands,
    /cargo test -p forge-tree-doc -p forge-pptx -p delino-forge/u,
  );
  const renderCommands = jobCommands(workflow.jobs["forge-render"]);
  assert.match(renderCommands, /libreoffice-impress poppler-utils/u);
  assert.match(renderCommands, /--test render -- --ignored/u);
});

test("Rust CI consumes one verified prebuilt selection and gates native preparation", () => {
  const changes = workflow.jobs.changes;
  assert.equal(changes["runs-on"], "ubuntu-24.04");
  assert.equal(
    changes.outputs.jobs,
    "${{ steps.rust.outputs.jobs || steps.plan.outputs.jobs }}",
  );
  assert.equal(
    changes.outputs.rust_packages,
    "${{ steps.rust.outputs.rust_packages || '[]' }}",
  );
  const prepare = namedStep(changes, "Prepare verified prebuilt cargo-mono");
  const live = namedStep(
    changes,
    "Verify published cargo-mono selection behavior",
  );
  const selection = namedStep(changes, "Select final Rust packages and jobs");
  assert.equal(prepare.run, "node scripts/ci/cargo-mono-prebuilt.mjs");
  assert.equal(
    live.run,
    "node scripts/ci/run-affected.mjs @delinoio/ci ci:rust:selection-fixture",
  );
  assert.equal(selection.run, "node scripts/ci/rust-affected.mjs plan");
  assert.equal(prepare.if, selection.if);
  assert.ok(changes.steps.indexOf(prepare) < changes.steps.indexOf(live));
  assert.ok(changes.steps.indexOf(live) < changes.steps.indexOf(selection));
  assert.equal(selection.env.CI_BASE, "${{ steps.plan.outputs.base }}");
  assert.equal(selection.env.CI_HEAD, "${{ steps.plan.outputs.head }}");
  const selected = (name) =>
    "${{ contains(fromJSON(needs.changes.outputs.rust_packages), '" +
    name +
    "') }}";
  for (const [id, name, command] of [
    ["rust-test", "Run cargo test", "test"],
    ["rust-clippy", "Run clippy", "clippy"],
  ]) {
    const job = workflow.jobs[id];
    const check = namedStep(job, name);
    assert.equal(check.run, `node scripts/ci/run-rust.mjs ${command}`);
    assert.equal(check.env.FORCE_RUN, "true");
    assert.equal(
      check.env.RUST_PACKAGES,
      "${{ needs.changes.outputs.rust_packages }}",
    );

    if (id === "rust-test") {
      const go = namedStep(job, "Setup Go for Rust test prerequisites");
      for (const owner of ["pnport"]) assert.ok(go.if.includes(owner));
    }
    for (const owner of ["devhud"]) {
      assert.ok(
        namedStep(
          job,
          id === "rust-clippy"
            ? "Install Linux desktop prerequisites"
            : "Install DevHUD Linux prerequisites",
        ).if.includes(
          `contains(fromJSON(needs.changes.outputs.rust_packages), '${owner}')`,
        ),
      );
    }
    if (id === "rust-test") {
      assert.ok(
        namedStep(
          job,
          "Save Go cache after successful main validation",
        ).if.includes("steps.ci-go.outcome == 'success'"),
      );
    } else {
      assert.equal(
        namedStep(job, "Save Go cache after successful main validation"),
        undefined,
      );
    }
  }

  assert.ok(
    jobCommands(workflow.jobs["rust-fmt"]).includes("cargo fmt --all --check"),
  );
});

test("Go selection preserves exact comparisons and keeps quality validation unchanged", () => {
  for (const id of ["go-test", "go-quality"]) {
    const job = workflow.jobs[id];
    const execution = job.steps.find(({ run }) =>
      run?.includes(id === "go-test" ? "ci:go:test" : "ci:go:quality"),
    );
    if (id === "go-quality")
      assert.equal(
        execution.env.CI_GO_MODE,
        "${{ (github.event_name == 'pull_request' || (github.event_name == 'workflow_dispatch' && needs.changes.outputs.mode == 'affected')) && !fromJSON(needs.changes.outputs.forced)['go-quality'] && 'affected' || 'full' }}",
      );
    assert.equal(execution.env.CI_GO_BASE, "${{ needs.changes.outputs.base }}");
    assert.equal(execution.env.CI_GO_HEAD, "${{ needs.changes.outputs.head }}");
  }
  for (const id of [
    "ci-contracts",
    "go-quality",
    "async-commit-hook",
    "devhud-api",
  ])
    assert.equal(step(workflow.jobs[id], "ci-go").with["cache-scope"], id);
  assert.equal(
    namedStep(workflow.jobs["go-test"], "Verify native affected Go discovery")
      .run,
    "node scripts/ci/run-affected.mjs @delinoio/ci ci:go:discovery",
  );
});

test("Go workflow selects affected native main packages and full Ubuntu main, manual and forced runs", () => {
  const expression = namedStep(workflow.jobs["go-test"], "Run go test").env
    .CI_GO_MODE;
  for (const event of Object.values(Event)) {
    for (const row of matricesForEvent(event).goTestMatrix.include) {
      for (const forced of [false, true]) {
        const mode = runInNewContext(expression.slice(3, -2), {
          github: { event_name: event },
          matrix: row,
          needs: {
            changes: {
              outputs: {
                mode: Mode.Affected,
                forced: JSON.stringify({ "go-test": forced }),
              },
            },
          },
          fromJSON: JSON.parse,
        });
        const expected =
          forced || (event === Event.Push && row.os === "ubuntu-latest")
            ? "full"
            : "affected";
        assert.equal(
          mode,
          expected,
          `${event}: ${row.label}: forced=${forced}`,
        );
      }
    }
  }
});

test("the separate Runmoor PR workflow retains full native validation through Turbo", () => {
  const runmoor = load(
    readFileSync(`${root}/.github/workflows/runmoor.yml`, "utf8"),
  );
  const job = runmoor.jobs.docker;
  assert.equal(job["timeout-minutes"], 20);
  assert.deepEqual(runmoor.permissions, { contents: "read" });
  const commands = jobCommands(job);
  assert.ok(commands.includes("go test -race -count=1 ./cmds/runmoor/..."));
  assert.ok(
    commands.includes(
      "go test -count=1 -run TestDockerIntegration -v -timeout 10m ./cmds/runmoor/internal/runmoor",
    ),
  );
  assert.equal(
    namedStep(job, "Local Docker integration without GitHub job assignment").env
      .RUNMOOR_DOCKER_TEST,
    "1",
  );
  assert.ok(
    job.steps.some(({ uses }) => uses === "./.github/actions/setup-ci-node"),
  );
  for (const [id, { task }] of jobTaskGraph(job))
    assert.equal(task.cache, false, id);
  for (const candidate of job.steps) {
    if (candidate.run?.includes("ci:runmoor:"))
      assert.equal(candidate.env.FORCE_RUN, "true");
    if (candidate.uses?.startsWith("actions/cache/save@"))
      assert.ok(
        candidate.if.includes("success() && github.ref == 'refs/heads/main'"),
      );
  }
});

test("PR Ubuntu shards reuse the main all cache and Windows alone restores the workspace fallback", () => {
  const inputs = step(workflow.jobs["go-test"], "ci-go").with;
  for (const event of Object.values(Event)) {
    for (const matrix of matricesForEvent(event).goTestMatrix.include) {
      const evaluate = (value) =>
        runInNewContext(value.slice(3, -2), {
          matrix,
          format: (pattern, value) => pattern.replace("{0}", value),
        });
      assert.equal(
        evaluate(inputs["cache-scope"]),
        matrix.os === "ubuntu-latest"
          ? "go-test-all"
          : `go-test-${matrix.shard}`,
      );
      assert.equal(
        evaluate(inputs["cache-fallback-scope"]),
        matrix.os === "windows-latest" && matrix.shard === "workspace"
          ? "go-test-worker"
          : "",
      );
    }
  }
});

test("CI exposes no full manual input and allocates no macOS runner on any event", () => {
  assert.equal(workflow.on.workflow_dispatch, null);
  for (const event of Object.values(Event)) {
    const matrices = matricesForEvent(event);
    const outputs = Object.fromEntries(
      Object.entries({
        go_test_matrix: matrices.goTestMatrix,
        environment_matrix: matrices.environmentMatrix,
        forge_matrix: matrices.forgeMatrix,
        clibox_matrix: matrices.cliboxMatrix,
        desktop_matrix: matrices.desktopMatrix,
        pnport_matrix: matrices.pnportMatrix,
      }).map(([name, value]) => [name, JSON.stringify(value)]),
    );
    const selected = planJobs(event, [".github/workflows/CI.yml"]).jobs;
    for (const [id, job] of Object.entries(workflow.jobs)) {
      if (id in selected && !selected[id]) continue;
      const context = { needs: { changes: { outputs } }, fromJSON: JSON.parse };
      let matrix = job.strategy?.matrix;
      if (typeof matrix === "string")
        matrix = runInNewContext(matrix.slice(3, -2), context);
      const rows = matrix?.include ?? matrix?.os?.map((os) => ({ os })) ?? [{}];
      for (const row of rows) {
        const runner = job["runs-on"].startsWith("${{")
          ? runInNewContext(job["runs-on"].slice(3, -2), {
              ...context,
              matrix: row,
            })
          : job["runs-on"];
        assert.doesNotMatch(runner, /^macos/u, `${event}: ${id}`);
      }
    }
  }
});

test("Rstack formatting uses the exact comparison and never caches results", () => {
  const job = workflow.jobs["source-format"];
  assert.equal(job["runs-on"], "ubuntu-latest");
  const run = namedStep(job, "Check changed files with Rstack");
  assert.equal(run.env.TURBO_SCM_BASE, "${{ needs.changes.outputs.base }}");
  assert.equal(run.env.TURBO_SCM_HEAD, "${{ needs.changes.outputs.head }}");
  assert.equal(run.env.FORCE_RUN, "true");
  const leaf = jobTaskGraph(job).get("@delinoio/ci#ci:format");
  assert.equal(leaf.task.cache, false);
  assert.deepEqual(leaf.task.dependsOn, []);
  assert.ok(leaf.task.passThroughEnv.includes("TURBO_SCM_BASE"));
  assert.ok(leaf.task.passThroughEnv.includes("TURBO_SCM_HEAD"));
  assert.equal(packages["package.json"].devDependencies.rstack, "0.8.3");
});

test("selected DevHud Clippy installs native prerequisites before Cargo", () => {
  const job = workflow.jobs["rust-clippy"];
  const preparation = namedStep(job, "Install Linux desktop prerequisites");
  const evaluate = (packages) =>
    runInNewContext(preparation.if.slice(3, -2), {
      needs: {
        changes: { outputs: { rust_packages: JSON.stringify(packages) } },
      },
      fromJSON: JSON.parse,
      contains: (list, value) => list.includes(value),
    });
  assert.equal(evaluate(["devhud"]), true);
  assert.equal(evaluate(["cargo-mono"]), false);
  for (const dependency of [
    "libgtk-3-dev",
    "libwebkit2gtk-4.1-dev",
    "libayatana-appindicator3-dev",
  ]) {
    assert.ok(preparation.run.includes(dependency), dependency);
  }
  assert.ok(
    job.steps.indexOf(preparation) <
      job.steps.indexOf(namedStep(job, "Run clippy")),
  );
});
