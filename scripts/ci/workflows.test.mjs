import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { lintWorkflows, prepareLintSource } from "./workflows.mjs";

const read = (file) =>
  readFileSync(new URL(`../../${file}`, import.meta.url), "utf8");
const MOBILE = "delidev-mobile-beta.yml";
const LINUX = "release-linux-packages.yml";
const mobileSource = () => read(`.github/workflows/${MOBILE}`);
const linuxSource = () => read(`.github/workflows/${LINUX}`);
const MOBILE_BLOCK =
  "concurrency:\n  group: delidev-mobile-beta\n  cancel-in-progress: false\n  queue: max\n";
const LINUX_BLOCK =
  "    concurrency:\n      group: linux-package-repository\n      cancel-in-progress: false\n      queue: max\n";
const mutate = (source, from, to) => {
  assert.ok(source.includes(from), `fixture anchor missing: ${from}`);
  return source.replace(from, to);
};
const rejects = (name, source, message = /Unsupported concurrency queue/u) =>
  assert.throws(() => prepareLintSource(name, source), message);

test("committed mobile beta workflow declares the complete global queue", () => {
  assert.ok(mobileSource().includes(MOBILE_BLOCK));
});

test("the mobile workflow-level declaration is accepted and only its queue line is masked", () => {
  const original = mobileSource();
  const prepared = prepareLintSource(MOBILE, original);
  assert.equal(prepared.masked, true);
  const before = original.split("\n");
  const after = prepared.source.split("\n");
  assert.equal(after.length, before.length);
  const changed = before.flatMap((line, index) =>
    line === after[index] ? [] : [index],
  );
  assert.deepEqual(changed.length, 1);
  assert.equal(before[changed[0]], "  queue: max");
  assert.equal(
    after[changed[0]],
    "  # queue:max validated by repository contract",
  );
  assert.doesNotMatch(prepared.source, /^\s*queue:/mu);
});

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
    prepareLintSource(
      MOBILE,
      mutate(mobileSource(), "  queue: max\n", ""),
    ).masked,
    false,
  );
});

test("mobile queue declaration rejects any altered shape", () => {
  const source = mobileSource();
  rejects(MOBILE, mutate(source, "  queue: max", "  queue: single"));
  rejects(
    MOBILE,
    mutate(source, "group: delidev-mobile-beta", "group: delidev-mobile"),
  );
  rejects(
    MOBILE,
    mutate(source, "cancel-in-progress: false", "cancel-in-progress: true"),
  );
  rejects(
    MOBILE,
    mutate(source, "cancel-in-progress: false\n", "cancel-in-progress: false\n  extra: 1\n"),
  );
});

test("mobile queue declaration rejects malformed or multiple queue lines", () => {
  const source = mobileSource();
  rejects(
    MOBILE,
    mutate(
      source,
      MOBILE_BLOCK,
      "concurrency: { group: delidev-mobile-beta, cancel-in-progress: false, queue: max }\n",
    ),
    /Unexpected queue syntax/u,
  );
  rejects(
    MOBILE,
    mutate(source, "  queue: max", "  queue:  max"),
    /Unexpected queue syntax/u,
  );
  rejects(
    MOBILE,
    mutate(source, "  queue: max", "    queue: max"),
    /Unexpected queue syntax|bad indentation|duplicated|YAMLException/iu,
  );
  rejects(
    MOBILE,
    mutate(source, "  queue: max\n", "  queue: max\n  queue: max\n"),
    /duplicated mapping key|YAMLException/iu,
  );
  // A second queue-looking line in another mapping is not a masked line.
  rejects(
    MOBILE,
    mutate(source, "env:\n", "env:\n  queue: max\n"),
    /Unexpected queue syntax/u,
  );
});

test("mobile queue line must sit inside the workflow concurrency block", () => {
  const moved = mutate(mobileSource(), "  queue: max\n", "").replace(
    "env:\n",
    "env:\n  queue: max\n",
  );
  // No workflow-level queue is parsed, so the file lints normally and the
  // unrelated env key is not masked.
  const prepared = prepareLintSource(MOBILE, moved);
  assert.equal(prepared.masked, false);
  assert.equal(prepared.source, moved);
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
  rejects("other.yml", mobileSource());
  rejects("other.yml", linuxSource());
  rejects(LINUX, mobileSource());
  rejects(MOBILE, linuxSource());
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
    mutate(source, "cancel-in-progress: false\n      queue", "cancel-in-progress: true\n      queue"),
  );
  // A workflow-level queue is forbidden for the Linux publisher.
  rejects(
    LINUX,
    mutate(source, "\njobs:\n", `\n${MOBILE_BLOCK}jobs:\n`),
  );
  rejects(LINUX, mutate(source, "\n  publish:\n", "\n  publisher:\n"));
});

test("lintWorkflows masks only the validated queue line and still reports actionlint failures", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "workflows-lint-"));
  try {
    writeFileSync(path.join(directory, MOBILE), mobileSource());
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
    assert.equal(stdinCalls[0].file, path.join(directory, MOBILE));
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
    // An unsupported declaration aborts before any lint input is produced.
    writeFileSync(
      path.join(directory, "bad.yml"),
      "name: x\non: push\nconcurrency:\n  group: g\n  queue: max\njobs: {}\n",
    );
    assert.throws(
      () => run(0),
      /Unsupported concurrency queue declaration/u,
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
