import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import yaml from "js-yaml";

for (const project of ["binpm", "cargo-mono", "nodeup", "with-watch", "derun"]) {
  const workflow = yaml.load(readFileSync(new URL(`../../.github/workflows/release-${project}.yml`, import.meta.url), "utf8"));
  test(`${project} pins every source checkout and keeps dry-run candidates unsigned and read-only`, () => {
    assert.deepEqual(workflow.permissions, { contents: "read" });
    assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, "true");
    assert.deepEqual(workflow.on.push.tags, [`${project}@v*`]);
    const jobs = workflow.jobs;
    assert.equal(jobs.prepare.outputs.revision, "${{ steps.meta.outputs.revision }}");
    for (const name of ["prepare", "build", "package", "publish"]) {
      const checkout = jobs[name].steps.find((step) => step.uses?.startsWith("actions/checkout@"));
      assert.equal(checkout.with.ref, name === "prepare" ? "${{ github.sha }}" : "${{ needs.prepare.outputs.revision }}");
      assert.equal(checkout.with["persist-credentials"], false);
    }
    const meta = jobs.prepare.steps.find((step) => step.id === "meta");
    assert.equal(meta.run, "node scripts/release/project.mjs legacy-source");
    assert.equal(meta.env.GH_TOKEN, "${{ github.token }}");
    assert.equal(meta.env.RELEASE_PROJECT, project);
    assert.equal(meta.env.REQUESTED_DRY_RUN, "${{ inputs.dry_run }}");
    assert.deepEqual(jobs.package.needs, ["prepare", "build"]);
    for (const name of ["prepare", "build", "package"]) assert.equal(jobs[name].permissions, undefined);
    const candidate = jobs.package.steps;
    assert.equal(candidate.find((step) => step.name === "Generate unsigned checksums").run, "bash scripts/release/generate-checksums.sh --artifacts-dir dist --skip-signing");
    assert.doesNotMatch(JSON.stringify(candidate), /cosign-installer|action-gh-release|create-github-app-token|secrets\./u);
    const upload = candidate.find((step) => step.name === "Upload unsigned candidate");
    const download = jobs.publish.steps.find((step) => step.name === "Download unsigned candidate");
    assert.equal(upload.with.name, `${project}-release-candidate`);
    assert.equal(download.with.name, upload.with.name);
    assert.equal(download.with.path, "dist");
    if (project !== "cargo-mono") {
      const render = candidate.find((step) => step.name === "Render Homebrew update");
      assert.equal(render.if, "env.DRY_RUN == 'true'");
      assert.match(render.run, /--dry-run/u);
    }
  });

  test(`${project} rejects tag conflicts before signing, upload and tap authority`, () => {
    const publish = workflow.jobs.publish;
    assert.deepEqual(publish.needs, ["prepare", "package"]);
    assert.equal(publish.if, "needs.prepare.outputs.dry_run == 'false'");
    assert.deepEqual(publish.permissions, { contents: "write", "id-token": "write" });
    const steps = publish.steps;
    const index = (name) => { const i = steps.findIndex((step) => step.name === name); assert.ok(i >= 0, name); return i; };
    const checks = ["Verify release tag before signing", "Create and verify release tag before publication"];
    assert.ok(index(checks[0]) < index("Install cosign"));
    assert.ok(index(checks[0]) < index("Generate checksums and signatures"));
    assert.ok(index("Generate checksums and signatures") < index(checks[1]));
    assert.ok(index(checks[1]) < index("Create GitHub release"));
    const release = steps[index("Create GitHub release")];
    assert.equal(steps[index(checks[1])].id, "release-source");
    assert.equal(release.with.target_commitish, "${{ steps.release-source.outputs.target_commitish }}");
    assert.equal(release.with.tag_name, "${{ env.RELEASE_TAG }}");
    if (project !== "cargo-mono") {
      checks.push("Verify release tag before Homebrew writes");
      assert.ok(index("Create GitHub release") < index(checks[2]));
      assert.ok(index(checks[2]) < index("Create Homebrew repository token"));
      assert.ok(index(checks[2]) < index("Submit Homebrew update"));
    }
    for (const name of checks) {
      const check = steps[index(name)];
      assert.equal(check.if, undefined);
      assert.equal(check["continue-on-error"], undefined);
      assert.equal(check.run, `node scripts/release/project.mjs ${name === checks[1] ? "legacy-tag" : "legacy-source"}`);
      assert.deepEqual(check.env, { GH_TOKEN: "${{ github.token }}", RELEASE_PROJECT: project, REQUESTED_VERSION: "${{ needs.prepare.outputs.version }}", REQUESTED_DRY_RUN: "false" });
    }
    assert.deepEqual(workflow.jobs["linux-packages"].needs, ["prepare", "publish"]);
  });
}
