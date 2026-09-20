import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { load } from "js-yaml";
const root = fileURLToPath(new URL("../../", import.meta.url));
const read = (path) => readFileSync(`${root}/${path}`, "utf8");
const native = load(read(".github/workflows/runlens.yml"));
const release = load(read(".github/workflows/release-runlens.yml"));
test("Runlens production documentation requires an explicit main-branch dispatch", () => {
  const docs = load(read(".github/workflows/runlens-docs.yml"));
  assert.equal(docs.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.equal(docs.jobs.deploy.if, "${{ !inputs.dry_run && github.ref == 'refs/heads/main' }}");
  assert.equal(docs.jobs.deploy.environment, "runlens-docs-production");
  assert.equal(docs.jobs.deploy.needs, "build");
  assert.doesNotMatch(JSON.stringify(docs.jobs.build), /secrets\.|pages deploy/u);
  assert.deepEqual(docs.concurrency, {
    group: "runlens-docs-${{ !inputs.dry_run && github.ref == 'refs/heads/main' && 'production' || github.run_id }}",
    "cancel-in-progress": false,
  });
  const [download, deploy] = docs.jobs.deploy.steps;
  assert.equal(docs.jobs.deploy.steps.length, 2);
  assert.equal(download.uses, "actions/download-artifact@v8");
  assert.equal(deploy.uses, "cloudflare/wrangler-action@v3");
  assert.equal(deploy.env.GH_TOKEN, "${{ github.token }}");
  assert.match(deploy.with.preCommands, /git\/ref\/heads\/main/u);
  assert.match(deploy.with.preCommands, /GITHUB_SHA/u);
  assert.doesNotMatch(deploy.with.preCommands, /\n/u);
  const directory = mkdtempSync(join(tmpdir(), "runlens-docs-guard-"));
  try {
    writeFileSync(join(directory, "gh"), '#!/bin/sh\nprintf "%s\\n" "$TEST_HEAD"\nexit "$TEST_STATUS"\n', { mode: 0o700 });
    for (const [head, status, success] of [["current", "0", true], ["stale", "0", false], ["", "1", false]]) {
      const result = spawnSync("sh", ["-c", deploy.with.preCommands], {
        env: { ...process.env, PATH: `${directory}:${process.env.PATH}`, GITHUB_REPOSITORY: "owner/repo", GITHUB_SHA: "current", TEST_HEAD: head, TEST_STATUS: status },
      });
      assert.equal(result.status === 0, success, `head=${head} status=${status}`);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
test("Runlens uses native six-platform execution and separate minimum OS evidence", () => {
  assert.equal(native.jobs.native.strategy.matrix.include.length, 6);
  assert.equal(new Set(native.jobs.native.strategy.matrix.include.map((row) => row.target)).size, 6);
  const commands = JSON.stringify(native.jobs.native);
  for (const marker of ["cargo test --locked --features test-support", "runlens-test.py", "static.c", "--no-deps", "minimum_os", "runlens.py evidence"]) assert.ok(commands.includes(marker), marker);
  assert.deepEqual(native.permissions, { contents: "read" });
  assert.doesNotMatch(commands, /secrets\.|sign-blob|gh release/u);
});
test("Windows native validation exercises real Detours setup errors", () => {
  const step = native.jobs.native.steps.find(step => step.name === "Windows Detours setup failure regression");
  assert.equal(step.if, "runner.os == 'Windows'");
  assert.equal(step["working-directory"], "crates/runlens");
  assert.equal(step.run, "cargo test --locked --package fspy_preload_windows detours_long_results_preserve_setup_failures");
});
test("Runlens native validation watches its prebuilt Homebrew release inputs", () => {
  for (const event of ["pull_request", "push"]) {
    for (const path of ["packaging/homebrew/templates/runlens.rb.tmpl", "scripts/release/update-homebrew.sh"]) {
      assert.ok(native.on[event].paths.includes(path), `${event}: ${path}`);
    }
  }
});
test("Runlens dry run cannot reach publication credentials or OIDC", () => {
  assert.deepEqual(release.permissions, { contents: "read" });
  assert.equal(release.on.workflow_dispatch.inputs.dry_run.default, true);
  for (const name of ["sign", "install", "publish"]) assert.equal(release.jobs[name].if, "!inputs.dry_run", name);
  for (const name of ["build", "validate"]) assert.doesNotMatch(JSON.stringify(release.jobs[name]), /secrets\.|id-token|sign-blob/u);
  assert.deepEqual(release.jobs.sign.permissions, { contents: "read", "id-token": "write" });
  assert.deepEqual(release.jobs.validate.permissions, { contents: "read", actions: "read" });
  assert.deepEqual(release.jobs.publish.permissions, { contents: "write" });
  assert.deepEqual(release.jobs.publish.needs, ["validate", "sign", "install"]);
  assert.equal(release.jobs.install.strategy.matrix.include.length, 6);
  const validation = JSON.stringify(release.jobs.validate);
  for (const marker of ["run.head_sha !== context.sha", "run.conclusion !== 'success'", "context.ref", "getReleaseByTag", "readiness"]) assert.ok(validation.includes(marker), marker);
});
test("Runlens keeps the verified release private until the Homebrew update succeeds", () => {
  const steps = release.jobs.publish.steps;
  const draft = steps.findIndex((step) => step.id === "draft");
  const tap = steps.findIndex((step) => step.run?.includes("runlens.py homebrew"));
  const publish = steps.findIndex((step) => step.with?.script?.includes("publishDraft"));
  assert.ok(draft >= 0 && draft < tap && tap < publish);
  assert.match(steps[draft].with.script, /prepareDraft/u);
  assert.match(steps[draft].with.script, /core.setOutput\('release_id', release.id\)/u);
  assert.doesNotMatch(steps[draft].with.script, /updateRelease|draft: false/u);
  for (const step of [steps[tap], steps[publish]]) {
    assert.equal(step.if, undefined, "publication must retain implicit success() gating");
    assert.equal(step["continue-on-error"], undefined);
  }
  assert.equal(steps[publish].env.RELEASE_ID, "${{ steps.draft.outputs.release_id }}");
  assert.match(steps[publish].with.script, /publishDraft\(\{github, context, releaseId: Number\(process.env.RELEASE_ID\), tag: process.env.RELEASE_TAG, directory: 'runlens-artifacts'\}\)/u);
  assert.doesNotMatch(steps[publish].with.script, /updateRelease/u);
});
test("production installers require version-bound authenticated checksums and archives", () => {
  for (const file of ["runlens.sh", "runlens.ps1"]) {
    const source = read(`scripts/install/${file}`);
    for (const marker of ["verify-blob", "--certificate-identity", "https://token.actions.githubusercontent.com", "SHA256SUMS.sigstore.json", "release-runlens.yml@refs/tags/", "archive"]) assert.ok(source.toLowerCase().includes(marker.toLowerCase()), `${file}: ${marker}`);
    assert.doesNotMatch(source, /skip.verify|allow.unsigned|latest\/download/iu);
  }
});
test("Runlens lockfile and toolchain remain outside the root workspace", () => {
  assert.match(read("Cargo.toml"), /exclude = \["crates\/runlens"\]/u);
  assert.match(read("crates/runlens/rust-toolchain.toml"), /nightly-2026-08-02/u);
  assert.match(read("lefthook.yml"), /root: "crates\/runlens\/"/u);
  assert.match(read("packaging/homebrew/templates/runlens.rb.tmpl"), /bin.install "runlens"/u);
  assert.doesNotMatch(read("packaging/homebrew/templates/runlens.rb.tmpl"), /cargo build|git clone/u);
});

test("Windows native validation checks malformed process attribute memory", () => {
  const step = native.jobs.native.steps.find((step) => step.name === "Windows malformed process attributes regression");
  assert.equal(step.if, "runner.os == 'Windows'");
  assert.equal(step.run, "cargo test --locked --package fspy_preload_windows process_image_attributes_reject_malformed_memory_without_dereferencing");
});

test("native archives and evidence use the checked-out source version", () => {
  const steps = native.jobs.native.steps;
  const source = steps.find(step => step.id === "source");
  assert.match(source.run, /runlens\.py source-version/u);
  for (const command of ["package", "evidence"]) {
    const step = steps.find(step => step.run?.includes(`runlens.py ${command}`));
    assert.equal(step.env.RUNLENS_VERSION, "${{ steps.source.outputs.version }}");
    assert.match(step.run, /--version "\$RUNLENS_VERSION"/u);
    assert.doesNotMatch(step.run, /0\.1\.0/u);
  }
});

test("Windows native validation checks malformed file attribute memory", () => {
  const step = native.jobs.native.steps.find((step) => step.name === "Windows malformed file attributes regression");
  assert.equal(step.if, "runner.os == 'Windows'");
  assert.equal(step.run, "cargo test --locked --package fspy_preload_windows object_attributes_reject_malformed_memory_without_dereferencing");
});
