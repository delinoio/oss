// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import yaml from "js-yaml";
import { planJobs, Event } from "./plan.mjs";
const root = new URL("../../", import.meta.url);
const workflow = yaml.load(
  readFileSync(
    new URL(".github/workflows/release-delitools-mobile.yml", root),
    "utf8",
  ),
);

test("DeliTools release is main-only, serialized and uses protected test-channel uploads", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.publish.default, false);
  assert.equal(
    workflow.jobs.preflight.if,
    "${{ github.ref == 'refs/heads/main' }}",
  );
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  for (const platform of ["ios", "android"]) {
    const job = workflow.jobs[platform];
    assert.equal(job.needs, "preflight");
    assert.equal(job.environment, "delitools-mobile");
    const publish = job.steps.find((step) =>
      step.name?.startsWith("Upload to"),
    );
    assert.equal(publish.if, "${{ inputs.publish }}");
    const cleanup = job.steps.find(
      (step) => step.name === "Remove signing material",
    );
    assert.equal(cleanup.if, "${{ always() }}");
    const upload = job.steps.find((step) =>
      step.uses?.startsWith("actions/upload-artifact@"),
    );
    assert.ok(job.steps.indexOf(upload) < job.steps.indexOf(publish));
    assert.equal(upload.with.path, "mobile-artifacts/");
    assert.equal(upload.with["if-no-files-found"], "error");
  }
  assert.equal(workflow.jobs.ios["runs-on"], "macos-15");
  assert.equal(workflow.jobs.android["runs-on"], "ubuntu-22.04");
});

test("DeliTools app and release changes select affected validation without unrelated DevHud native jobs", () => {
  for (const event of Object.values(Event)) {
    for (const path of [
      "apps/delitools/src/main.tsx",
      ".github/workflows/release-delitools-mobile.yml",
      "apps/delitools/scripts/publish-android.mjs",
    ]) {
      const plan = planJobs(event, [path]);
      assert.equal(plan.jobs["delitools-frontend"], true, `${event}: ${path}`);
      assert.equal(plan.jobs["delidev-frontend"], false);
      assert.equal(plan.jobs["devhud-desktop"], false);
    }
  }
});

for (const [platform, stepName, filename] of [
  ["android", "Restore upload keystore", "delitools-upload.jks"],
  ["ios", "Restore App Store Connect API key", "delitools-auth.p8"],
]) {
  test(`DeliTools ${platform} signing restore keeps Node expressions literal and writes private files`, () => {
    const directory = mkdtempSync(join(tmpdir(), "delitools-signing-"));
    try {
      const step = workflow.jobs[platform].steps.find(
        (step) => step.name === stepName,
      );
      const content = "fixture signing material";
      const result = spawnSync("bash", ["-e", "-c", step.run], {
        encoding: "utf8",
        env: {
          PATH: process.env.PATH,
          RUNNER_TEMP: directory,
          GITHUB_ENV: join(directory, "github-env"),
          KEYSTORE_B64: Buffer.from(content).toString("base64"),
          PRIVATE_KEY_B64: Buffer.from(content).toString("base64"),
          TAURI_ANDROID_KEYSTORE_PASSWORD: "fixture-password",
          TAURI_ANDROID_KEY_ALIAS: "fixture-alias",
          TAURI_ANDROID_KEY_PASSWORD: "fixture-password",
          APPLE_DEVELOPMENT_TEAM: "0000000000",
          APPLE_API_ISSUER: "fixture-issuer",
          APPLE_API_KEY: "fixture-key",
        },
      });
      assert.equal(result.status, 0, result.stderr);
      assert.equal(readFileSync(join(directory, filename), "utf8"), content);
      assert.equal(statSync(join(directory, filename)).mode & 0o777, 0o600);
      assert.ok(
        readFileSync(join(directory, "github-env"), "utf8").includes(
          join(directory, filename),
        ),
      );
      assert.equal(result.stdout.includes(content), false);
      assert.equal(result.stderr.includes(content), false);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
}

test("DeliTools verifies the protected Android upload identity before retaining candidates", () => {
  const job = workflow.jobs.android;
  assert.equal(
    job.env.DELITOOLS_ANDROID_CERTIFICATE_SHA256,
    "${{ vars.DELITOOLS_ANDROID_CERTIFICATE_SHA256 }}",
  );
  const verify = job.steps.find(
    (step) => step.name === "Collect and verify signed bundle",
  );
  assert.equal(verify.if, undefined);
  assert.match(
    verify.run,
    /node apps\/delitools\/scripts\/verify-android-signing.mjs/,
  );
  const archive = job.steps.find((step) =>
    step.uses?.startsWith("actions/upload-artifact@"),
  );
  assert.ok(job.steps.indexOf(verify) < job.steps.indexOf(archive));
});

test("DeliTools pins an upload-compatible Xcode before iOS project generation", () => {
  const job = workflow.jobs.ios;
  assert.equal(
    job.env.DEVELOPER_DIR,
    "/Applications/Xcode_26.3.app/Contents/Developer",
  );
  const verify = job.steps.find(
    (step) => step.name === "Verify selected Xcode and iOS SDK",
  );
  assert.match(verify.run, /xcodebuild -version/);
  assert.match(verify.run, /xcrun --sdk iphoneos --show-sdk-version/);
  assert.match(verify.run, /26\.\*/);
  const init = job.steps.find((step) => step.run?.includes("mobile:ios:init"));
  assert.ok(job.steps.indexOf(verify) < job.steps.indexOf(init));
});
