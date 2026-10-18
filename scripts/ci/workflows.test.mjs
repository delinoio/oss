import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { lintWorkflows, prepareLintSource } from "./workflows.mjs";

const read = (file) =>
  readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const LINUX = "release-linux-packages.yml";
const linuxSource = () => read(`.github/workflows/${LINUX}`);
const LINUX_BLOCK =
  "    concurrency:\n      group: linux-package-repository\n      cancel-in-progress: false\n      queue: max\n";
const mutate = (source, from, to) => {
  assert.ok(source.includes(from), `fixture anchor missing: ${from}`);
  return source.replace(from, to);
};
const rejects = (name, source, message = /Unsupported concurrency queue/u) =>
  assert.throws(() => prepareLintSource(name, source), message);

test("the Linux publisher job-level declaration is still accepted and masked", () => {
  const original = linuxSource();
  const prepared = prepareLintSource(LINUX, original);
  assert.equal(prepared.masked, true);
  assert.equal(
    prepared.source,
    mutate(
      original,
      "      queue: max",
      "      # queue:max validated by repository contract",
    ),
  );
});

test("workflows without a queue are returned unchanged", () => {
  const source = "name: x\non: push\njobs: {}\n";
  assert.deepEqual(prepareLintSource("other.yml", source), {
    masked: false,
    source,
  });
  assert.deepEqual(
    prepareLintSource(LINUX, mutate(linuxSource(), "      queue: max\n", ""))
      .masked,
    false,
  );
});

test("queue declarations are rejected in every other workflow", () => {
  rejects(
    "CI.yml",
    "name: CI\non: push\nconcurrency:\n  group: ci\n  cancel-in-progress: false\n  queue: max\njobs: {}\n",
  );
  rejects(
    "other.yml",
    "name: x\non: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    concurrency:\n      group: g\n      queue: max\n    steps: []\n",
  );
  // Declarations allowed for one workflow are not transferable to another.
  rejects("other.yml", linuxSource());
});

test("the Linux publisher declaration is rejected when altered or relocated", () => {
  const source = linuxSource();
  rejects(LINUX, mutate(source, "      queue: max", "      queue: single"));
  rejects(
    LINUX,
    mutate(source, "group: linux-package-repository", "group: other"),
  );
  rejects(
    LINUX,
    mutate(
      source,
      "cancel-in-progress: false\n      queue",
      "cancel-in-progress: true\n      queue",
    ),
  );
  // A workflow-level queue is forbidden for the Linux publisher.
  rejects(
    LINUX,
    mutate(
      source,
      "\njobs:\n",
      `\n${LINUX_BLOCK.replace(/^    /gm, "")}jobs:\n`,
    ),
  );
  rejects(LINUX, mutate(source, "\n  publish:\n", "\n  publisher:\n"));
});

test("lintWorkflows masks only the validated queue line and still reports actionlint failures", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "workflows-lint-"));
  try {
    writeFileSync(path.join(directory, LINUX), linuxSource());
    writeFileSync(path.join(directory, "plain.yml"), "name: x\n");
    const stdinCalls = [];
    const fileCalls = [];
    const sink = { write() {} };
    const run = (status) =>
      lintWorkflows(directory, {
        runFiles: (paths) => {
          fileCalls.push(paths);
        },
        runStdin: (file, input) => {
          stdinCalls.push({ file, input });
          return { status, stdout: "", stderr: status ? "lint failure" : "" };
        },
        stdout: sink,
        stderr: sink,
      });
    assert.equal(run(0), 0);
    assert.equal(stdinCalls.length, 1);
    assert.equal(stdinCalls[0].file, path.join(directory, LINUX));
    assert.doesNotMatch(stdinCalls[0].input, /^\s*queue:/mu);
    assert.deepEqual(fileCalls, [[path.join(directory, "plain.yml")]]);
    // Ordinary actionlint failures on a masked input still fail the run.
    assert.equal(run(1), 1);
    // A failing batch lint of unmasked files fails the run too.
    assert.equal(
      lintWorkflows(directory, {
        runFiles: () => {
          throw new Error("actionlint failed");
        },
        runStdin: () => ({ status: 0 }),
        stdout: sink,
        stderr: sink,
      }),
      1,
    );
    // A spawn failure has no actionlint output, so the error must be logged.
    const logged = [];
    const spawnError = Object.assign(new Error("spawn go ENOENT"), {
      code: "ENOENT",
    });
    assert.equal(
      lintWorkflows(directory, {
        runFiles: () => {
          throw spawnError;
        },
        runStdin: () => ({ status: null, error: spawnError }),
        stdout: sink,
        stderr: { write: (text) => text && logged.push(text) },
      }),
      1,
    );
    assert.equal(logged.length, 2);
    for (const line of logged)
      assert.match(
        line,
        /^workflow lint failed: step=.* code=ENOENT message="spawn go ENOENT"\n$/u,
      );
    assert.match(
      logged[0],
      /step="actionlint for .*release-linux-packages\.yml"/u,
    );
    assert.match(logged[1], /step="actionlint batch"/u);
    // An unsupported declaration aborts before any lint input is produced.
    writeFileSync(
      path.join(directory, "bad.yml"),
      "name: x\non: push\nconcurrency:\n  group: g\n  queue: max\njobs: {}\n",
    );
    assert.throws(() => run(0), /Unsupported concurrency queue declaration/u);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
