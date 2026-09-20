import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import yaml from "js-yaml";

test("Workflow keeps credentials, OIDC and actual signing out of every dry-run job", () => {
  const workflow = yaml.load(readFileSync(new URL("../../.github/workflows/release-runmoor.yml", import.meta.url), "utf8"));
  assert.deepEqual(workflow.permissions, { contents: "read" });
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  assert.deepEqual(workflow.on.push.tags, ["runmoor@v*"]);
  for (const [name, job] of Object.entries(workflow.jobs)) {
    if (name === "publish") continue;
    assert.equal(job.permissions?.["id-token"], undefined);
    assert.doesNotMatch(JSON.stringify(job), /secrets\.|cosign|sign-blob|action-gh-release/u);
  }
  const publish = workflow.jobs.publish;
  assert.equal(publish.if, "needs.plan.outputs.mode == 'publish'");
  assert.deepEqual(publish.permissions, { contents: "write", "id-token": "write" });
  const sign = publish.steps.find((step) => step.name === "Sign exact artifacts with Sigstore").run;
  assert.match(sign, /--signed true --identity "https:\/\/github.com\/\$\{GITHUB_WORKFLOW_REF\}"/u);
  const verifier = readFileSync(new URL("../release/runmoor.mjs", import.meta.url), "utf8");
  assert.match(verifier, /"verify-blob", "--bundle"/u);
  assert.match(verifier, /"--certificate-oidc-issuer", "https:\/\/token.actions.githubusercontent.com"/u);
  const release = publish.steps.find((step) => step.uses?.startsWith("softprops/"));
  assert.equal(release.with.draft, true);
  assert.equal(release.with.prerelease, false);
  assert.equal(release.with.overwrite_files, false);
  const publishStable = publish.steps.find((step) => step.name === "Publish stable release");
  assert.match(publishStable.run, /gh release edit "\$RELEASE_TAG" --draft=false --prerelease=false/u);
  assert.equal(publishStable.env.GH_TOKEN, "${{ github.token }}");
  assert.ok(publish.steps.indexOf(release) < publish.steps.indexOf(publishStable));
});
